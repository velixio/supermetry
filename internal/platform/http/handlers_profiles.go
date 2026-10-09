package platformhttp

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"panel/internal/api"
	"panel/internal/platform/audit"
	"panel/internal/platform/authz"
	"panel/internal/replay"
	coresites "panel/internal/sites"
)

type profileCursor struct {
	LastSeenAt time.Time `json:"last_seen_at"`
	ID         int64     `json:"id"`
}

func (h *handler) listProfiles(w http.ResponseWriter, r *http.Request) {
	_, site, ok := h.sitePrincipal(w, r, authz.ActionStatsRead, h.resourceRoles(r)...)
	if !ok {
		return
	}
	// Profiles stored while the setting was on stay listed after it is turned
	// off, so they can still be found and erased.
	if h.deps.Store == nil {
		unavailable(w, "profile")
		return
	}
	limit, valid := parseLimit(r)
	if !valid {
		writeProblem(w, http.StatusBadRequest, "Bad request", "limit must be between 1 and 100")
		return
	}
	search := strings.TrimSpace(r.URL.Query().Get("search"))
	if len(search) > 128 {
		writeProblem(w, http.StatusBadRequest, "Bad request", "search must not exceed 128 characters")
		return
	}
	cursor, hasCursor, err := decodeProfileCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", "cursor is invalid")
		return
	}
	rows, err := h.deps.Store.Pool.Query(r.Context(), `
		select p.id,p.site_id,p.external_id,p.traits,p.first_seen_at,p.last_seen_at,
		       count(distinct e.session_id)::int8,count(e.profile_id)::int8,
		       exists(select 1 from replay_sessions rr where rr.site_id=p.site_id and rr.profile_id=p.id)
		from profiles p
		left join events e on e.site_id=p.site_id and e.profile_id=p.id
		where p.site_id=$1
		  and ($2='' or position(lower($2) in lower(p.external_id))>0)
		  and ($3::boolean=false or (p.last_seen_at,p.id)<($4,$5))
		group by p.id
		order by p.last_seen_at desc,p.id desc
		limit $6`, site.ID, search, hasCursor, cursor.LastSeenAt, cursor.ID, limit+1)
	if err != nil {
		serviceError(w, err)
		return
	}
	defer rows.Close()
	items := make([]api.Profile, 0, limit+1)
	for rows.Next() {
		profile, scanErr := scanProfile(rows)
		if scanErr != nil {
			serviceError(w, scanErr)
			return
		}
		items = append(items, profile)
	}
	if err := rows.Err(); err != nil {
		serviceError(w, err)
		return
	}
	var next *string
	if len(items) > limit {
		last := items[limit-1]
		value := encodeProfileCursor(profileCursor{LastSeenAt: last.LastSeenAt, ID: last.Id})
		next = &value
		items = items[:limit]
	}
	writeJSON(w, http.StatusOK, api.ProfileList{ProfilesEnabled: site.ProfilesEnabled, Data: items, NextCursor: next})
}

type profileScanner interface{ Scan(...any) error }

func scanProfile(row profileScanner) (api.Profile, error) {
	var profile api.Profile
	var traits []byte
	if err := row.Scan(&profile.Id, &profile.SiteId, &profile.ExternalId, &traits, &profile.FirstSeenAt, &profile.LastSeenAt, &profile.Sessions, &profile.Events, &profile.HasReplay); err != nil {
		return api.Profile{}, err
	}
	profile.Traits = make(map[string]string)
	if len(traits) != 0 {
		if err := json.Unmarshal(traits, &profile.Traits); err != nil {
			return api.Profile{}, err
		}
	}
	return profile, nil
}

func (h *handler) getProfile(w http.ResponseWriter, r *http.Request) {
	_, site, profile, ok := h.profilePrincipal(w, r, authz.ActionStatsRead, h.resourceRoles(r)...)
	if !ok {
		return
	}
	limit, valid := parseLimit(r)
	if !valid {
		writeProblem(w, http.StatusBadRequest, "Bad request", "limit must be between 1 and 100")
		return
	}
	offset, err := decodeTimelineCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", "cursor is invalid")
		return
	}
	rows, err := h.deps.Store.Pool.Query(r.Context(), `
		select e.ts,e.kind,coalesce(n.value,''),coalesce(p.value,''),e.props,e.session_id::text,
		       exists (select 1 from replay_sessions rr where rr.site_id=e.site_id and rr.session_id=e.session_id)
		from events e
		left join dict n on n.kind=7 and n.id=e.name_id
		left join dict p on p.kind=0 and p.id=e.path_id
		where e.site_id=$1 and e.profile_id=$2
		order by e.ts desc,e.session_id desc,e.name_id desc,e.path_id desc
		offset $3 limit $4`, site.ID, profile.Id, offset, limit+1)
	if err != nil {
		serviceError(w, err)
		return
	}
	defer rows.Close()
	timeline := make([]api.ProfileEvent, 0, limit+1)
	for rows.Next() {
		var event api.ProfileEvent
		var props []byte
		if err := rows.Scan(&event.Ts, &event.Kind, &event.Name, &event.Path, &props, &event.SessionId, &event.HasReplay); err != nil {
			serviceError(w, err)
			return
		}
		if len(props) != 0 {
			decoded := make(map[string]any)
			if err := json.Unmarshal(props, &decoded); err != nil {
				serviceError(w, err)
				return
			}
			event.Props = &decoded
		}
		timeline = append(timeline, event)
	}
	if err := rows.Err(); err != nil {
		serviceError(w, err)
		return
	}
	var next *string
	if len(timeline) > limit {
		value := base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(offset + limit)))
		next = &value
		timeline = timeline[:limit]
	}
	detail := api.ProfileDetail{
		Id: profile.Id, SiteId: profile.SiteId, ExternalId: profile.ExternalId, Traits: profile.Traits,
		FirstSeenAt: profile.FirstSeenAt, LastSeenAt: profile.LastSeenAt, Sessions: profile.Sessions,
		Events: profile.Events, HasReplay: profile.HasReplay, Timeline: timeline, NextCursor: next,
	}
	writeJSON(w, http.StatusOK, detail)
}

func (h *handler) deleteProfile(w http.ResponseWriter, r *http.Request) {
	principal, site, profile, ok := h.profilePrincipal(w, r, authz.ActionStatsRead, authz.RoleOwner, authz.RoleAdmin)
	if !ok {
		return
	}
	// Erasure works whether or not profiles are currently enabled. It removes
	// the profile's events and every row of the sessions and visitor-days it
	// was identified in, in both tiers: the anonymous events of those
	// sessions, the hot session rows, sessions_closed and custom_closed.
	// Aggregates (cube_day, visitor_day, rollups) hold counts, not rows, and
	// are not adjusted; the audit entry records that.
	if h.deps.ProfileTombstones == nil {
		unavailable(w, "profile erasure")
		return
	}
	var replaySessionIDs []int64
	err := h.deps.Store.Tx(r.Context(), func(tx pgx.Tx) error {
		ctx := r.Context()
		if err := h.deps.ProfileTombstones.RememberTx(ctx, tx, site.ID, profile.ExternalId); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			create temporary table profile_erasure on commit drop as
			select session_id, visitor_id, local_day, min(ts) as first_ts, max(ts) as last_ts
			from events where site_id=$1 and profile_id=$2
			group by session_id, visitor_id, local_day`, site.ID, profile.Id); err != nil {
			return err
		}
		if h.deps.ReplayStore != nil {
			rows, err := tx.Query(ctx, `
				select session_id from profile_erasure
				union
				select session_id from replay_sessions where site_id=$1 and profile_id=$2
				order by session_id`, site.ID, profile.Id)
			if err != nil {
				return err
			}
			for rows.Next() {
				var sessionID int64
				if err := rows.Scan(&sessionID); err != nil {
					rows.Close()
					return err
				}
				replaySessionIDs = append(replaySessionIDs, sessionID)
			}
			rowsErr := rows.Err()
			rows.Close()
			if rowsErr != nil {
				return rowsErr
			}
		}
		for _, sql := range []string{
			`delete from events where site_id=$1 and profile_id=$2`,
			`delete from events e using profile_erasure k
			 where e.site_id=$1 and e.session_id=k.session_id
			   and e.ts between k.first_ts - interval '1 day' and k.last_ts + interval '1 day'`,
			`delete from sessions s using profile_erasure k
			 where s.site_id=$1 and s.session_id=k.session_id
			   and s.start between k.first_ts - interval '1 day' and k.last_ts`,
			`delete from sessions_closed s using profile_erasure k
			 where s.site_id=$1 and s.session_id=k.session_id
			   and s.day between k.local_day - 1 and k.local_day`,
			`delete from custom_closed c using profile_erasure k
			 where c.site_id=$1 and c.visitor_id=k.visitor_id and c.day=k.local_day`,
		} {
			args := []any{site.ID}
			if strings.Contains(sql, "$2") {
				args = append(args, profile.Id)
			}
			if _, err := tx.Exec(ctx, sql, args...); err != nil {
				return err
			}
		}
		tag, err := tx.Exec(r.Context(), `delete from profiles where id=$1 and site_id=$2`, profile.Id, site.ID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return pgx.ErrNoRows
		}
		return audit.Write(r.Context(), tx, principal, "profile.erased", "profile", profile.Id, map[string]any{
			"site_id": site.ID, "aggregates_adjusted": false,
		})
	})
	if err != nil {
		h.deps.ProfileTombstones.ForgetMemory(site.ID, profile.ExternalId)
		serviceError(w, err)
		return
	}
	if h.deps.ReplayStore != nil {
		if eraseErr := replay.EraseSessions(r.Context(), h.deps.Store.Pool, h.deps.ReplayStore, site.ID, replaySessionIDs); eraseErr != nil {
			serviceError(w, eraseErr)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) profilePrincipal(w http.ResponseWriter, r *http.Request, action authz.Action, roles ...authz.Role) (authz.Principal, coresites.Site, api.Profile, bool) {
	principal, ok := h.principal(w, r)
	if !ok {
		return authz.Principal{}, coresites.Site{}, api.Profile{}, false
	}
	id, err := parseInt64(r.PathValue("profile"))
	if err != nil || h.deps.Store == nil {
		writeProblem(w, http.StatusNotFound, "Not found", "profile not found")
		return authz.Principal{}, coresites.Site{}, api.Profile{}, false
	}
	row := h.deps.Store.Pool.QueryRow(r.Context(), `
		select p.id,p.site_id,p.external_id,p.traits,p.first_seen_at,p.last_seen_at,
		       count(distinct e.session_id)::int8,count(e.profile_id)::int8,
		       exists(select 1 from replay_sessions rr where rr.site_id=p.site_id and rr.profile_id=p.id)
		from profiles p left join events e on e.site_id=p.site_id and e.profile_id=p.id
		where p.id=$1 group by p.id`, id)
	profile, err := scanProfile(row)
	if errors.Is(err, pgx.ErrNoRows) {
		writeProblem(w, http.StatusNotFound, "Not found", "profile not found")
		return authz.Principal{}, coresites.Site{}, api.Profile{}, false
	}
	if err != nil {
		serviceError(w, err)
		return authz.Principal{}, coresites.Site{}, api.Profile{}, false
	}
	principal, site, ok := h.authorizeSite(w, principal, profile.SiteId, action)
	if !ok {
		return authz.Principal{}, coresites.Site{}, api.Profile{}, false
	}
	if !roleAllowed(principal.Role, roles) {
		writeProblem(w, http.StatusForbidden, "Forbidden", "the principal cannot perform this action")
		return authz.Principal{}, coresites.Site{}, api.Profile{}, false
	}
	return principal, site, profile, true
}

func encodeProfileCursor(cursor profileCursor) string {
	encoded, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func decodeProfileCursor(value string) (profileCursor, bool, error) {
	if value == "" {
		return profileCursor{}, false, nil
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(value)
	if err != nil {
		return profileCursor{}, false, err
	}
	var cursor profileCursor
	if err := json.Unmarshal(decoded, &cursor); err != nil || cursor.LastSeenAt.IsZero() || cursor.ID <= 0 {
		return profileCursor{}, false, errors.New("invalid profile cursor")
	}
	return cursor, true, nil
}

func decodeTimelineCursor(value string) (int, error) {
	if value == "" {
		return 0, nil
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(value)
	if err != nil {
		return 0, err
	}
	offset, err := strconv.Atoi(string(decoded))
	if err != nil || offset < 0 || offset > 1_000_000 {
		return 0, errors.New("invalid timeline cursor")
	}
	return offset, nil
}
