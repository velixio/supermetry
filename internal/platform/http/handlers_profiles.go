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

	"supermetry/internal/api"
	"supermetry/internal/platform/audit"
	"supermetry/internal/platform/authz"
	"supermetry/internal/profileerasure"
	coresites "supermetry/internal/sites"
)

type profileCursor struct {
	LastSeenAt time.Time `json:"last_seen_at"`
	ID         int64     `json:"id"`
}

func (h *handler) listProfiles(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.sitePrincipal(w, r, authz.ActionProfilesRead, h.resourceRoles(r)...)
	if !ok {
		return
	}
	if !authorizeSensitiveExport(w, r, principal, site.ID, authz.ActionProfilesRead) {
		return
	}
	if !h.allowHistoricalIdentity(w) {
		return
	}
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
	whereSQL := profileListWhereSQL(false)
	var count int
	err = h.deps.Store.Pool.QueryRow(r.Context(), `select count(*) from (
		select 1 from profiles p
		`+whereSQL+`
		limit $3
	) t`, site.ID, search, listResultTotalCap+1).Scan(&count)
	if err != nil {
		serviceError(w, err)
		return
	}
	total, totalCapped := capListTotal(count)
	rows, err := h.deps.Store.Pool.Query(r.Context(), `
		select p.id,p.site_id,p.external_id,p.traits,p.first_seen_at,p.last_seen_at,
		       count(distinct e.session_id)::int8,count(e.profile_id)::int8,
		       exists(select 1 from replay_sessions rr where rr.site_id=p.site_id and rr.profile_id=p.id)
		from profiles p
		left join events e on e.site_id=p.site_id and e.profile_id=p.id
		`+profileListWhereSQL(true)+`
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
		if !authz.Can(principal, authz.ActionReplayRead, site.ID) {
			profile.HasReplay = false
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
	writeJSON(w, http.StatusOK, api.ProfileList{
		ProfilesEnabled: site.ProfilesEnabled, Data: items, NextCursor: next,
		Total: total, TotalCapped: totalCapped,
	})
}

func profileListWhereSQL(includeCursor bool) string {
	where := `where p.site_id=$1
		  and ($2='' or position(lower($2) in lower(p.external_id))>0)`
	if includeCursor {
		where += `
		  and ($3::boolean=false or (p.last_seen_at,p.id)<($4,$5))`
	}
	return where
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
	principal, site, profile, ok := h.profilePrincipal(w, r, authz.ActionProfilesRead, h.resourceRoles(r)...)
	if !ok {
		return
	}
	if !authorizeSensitiveExport(w, r, principal, site.ID, authz.ActionProfilesRead) {
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
	whereSQL := profileTimelineWhereSQL()
	var count int
	err = h.deps.Store.Pool.QueryRow(r.Context(), `select count(*) from (
		select 1 from events e `+whereSQL+` limit $3
	) t`, site.ID, profile.Id, listResultTotalCap+1).Scan(&count)
	if err != nil {
		serviceError(w, err)
		return
	}
	total, totalCapped := capListTotal(count)
	rows, err := h.deps.Store.Pool.Query(r.Context(), `
		select e.ts,e.kind,coalesce(n.value,''),coalesce(p.value,''),e.props,e.session_id::text,
		       exists (select 1 from replay_sessions rr where rr.site_id=e.site_id and rr.session_id=e.session_id)
		from events e
		left join dict n on n.kind=7 and n.id=e.name_id
		left join dict p on p.kind=0 and p.id=e.path_id
		`+profileTimelineWhereSQL()+`
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
		if !authz.Can(principal, authz.ActionReplayRead, site.ID) {
			event.HasReplay = false
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
	if !authz.Can(principal, authz.ActionReplayRead, site.ID) {
		profile.HasReplay = false
	}
	detail := api.ProfileDetail{
		Id: profile.Id, SiteId: profile.SiteId, ExternalId: profile.ExternalId, Traits: profile.Traits,
		FirstSeenAt: profile.FirstSeenAt, LastSeenAt: profile.LastSeenAt, Sessions: profile.Sessions,
		Events: profile.Events, HasReplay: profile.HasReplay, Timeline: timeline, NextCursor: next,
		Total: total, TotalCapped: totalCapped,
	}
	writeJSON(w, http.StatusOK, detail)
}

func profileTimelineWhereSQL() string {
	return `where e.site_id=$1 and e.profile_id=$2`
}

func (h *handler) deleteProfile(w http.ResponseWriter, r *http.Request) {
	principal, site, profile, ok := h.profilePrincipal(w, r, authz.ActionStatsRead, authz.RoleOwner, authz.RoleAdmin)
	if !ok {
		return
	}
	if h.deps.ProfileTombstones == nil || h.deps.ProfileErasures == nil || h.deps.Store == nil {
		unavailable(w, "profile erasure")
		return
	}
	var request profileerasure.Request
	err := h.deps.Store.Tx(r.Context(), func(tx pgx.Tx) error {
		if err := h.deps.ProfileTombstones.RememberTx(r.Context(), tx, site.ID, profile.ExternalId); err != nil {
			return err
		}
		var err error
		request, err = h.deps.ProfileErasures.CreateOrGetTx(r.Context(), tx, site.ID, profile.Id, profile.ExternalId, erasureRequester(principal))
		if err != nil {
			return err
		}
		return audit.Write(r.Context(), tx, principal, "profile.erasure_requested", "profile", profile.Id, map[string]any{
			"site_id": site.ID, "erasure_id": request.ID,
		})
	})
	if err != nil {
		serviceError(w, err)
		return
	}
	w.Header().Set("Location", "/api/private/erasures/"+strconv.FormatInt(request.ID, 10))
	writeJSON(w, http.StatusAccepted, map[string]any{"erasure_id": request.ID, "status": request.Status})
}

func erasureRequester(principal authz.Principal) string {
	if principal.KeyID > 0 {
		return profileerasure.RequestedByAPIKey(principal.KeyID)
	}
	return profileerasure.RequestedByUser(principal.UserID)
}

type erasureResponse struct {
	ID          int64                 `json:"id"`
	SiteID      int32                 `json:"site_id"`
	ProfileID   int64                 `json:"profile_id"`
	Status      profileerasure.Status `json:"status"`
	Attempts    int                   `json:"attempts"`
	LastError   *string               `json:"last_error"`
	CreatedAt   time.Time             `json:"created_at"`
	UpdatedAt   time.Time             `json:"updated_at"`
	SettleAfter *time.Time            `json:"settle_after"`
	CompletedAt *time.Time            `json:"completed_at"`
}

func newErasureResponse(request profileerasure.Request) erasureResponse {
	return erasureResponse{
		ID: request.ID, SiteID: request.SiteID, ProfileID: request.ProfileID, Status: request.Status,
		Attempts: request.Attempts, LastError: request.LastError, CreatedAt: request.CreatedAt,
		UpdatedAt: request.UpdatedAt, SettleAfter: request.SettleAfter, CompletedAt: request.CompletedAt,
	}
}

func (h *handler) getPrivateErasure(w http.ResponseWriter, r *http.Request) {
	h.getErasure(w, r, true)
}

func (h *handler) getPublicErasure(w http.ResponseWriter, r *http.Request) {
	h.getErasure(w, r, false)
}

func (h *handler) getErasure(w http.ResponseWriter, r *http.Request, ownersOnly bool) {
	_, request, ok := h.erasurePrincipal(w, r, ownersOnly)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, newErasureResponse(request))
}

func (h *handler) retryErasure(w http.ResponseWriter, r *http.Request) {
	_, request, ok := h.erasurePrincipal(w, r, true)
	if !ok {
		return
	}
	if err := h.deps.ProfileErasures.Retry(r.Context(), request.ID); err != nil {
		if errors.Is(err, profileerasure.ErrInvalidState) {
			writeProblem(w, http.StatusBadRequest, "Bad request", "only failed erasures can be retried")
			return
		}
		serviceError(w, err)
		return
	}
	request, err := h.deps.ProfileErasures.Get(r.Context(), request.ID)
	if err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newErasureResponse(request))
}

func (h *handler) erasurePrincipal(w http.ResponseWriter, r *http.Request, ownersOnly bool) (authz.Principal, profileerasure.Request, bool) {
	principal, ok := h.principal(w, r)
	if !ok {
		return authz.Principal{}, profileerasure.Request{}, false
	}
	if h.deps.ProfileErasures == nil {
		unavailable(w, "profile erasure")
		return authz.Principal{}, profileerasure.Request{}, false
	}
	id, err := parseInt64(r.PathValue("erasure"))
	if err != nil {
		writeProblem(w, http.StatusNotFound, "Not found", "erasure request not found")
		return authz.Principal{}, profileerasure.Request{}, false
	}
	request, err := h.deps.ProfileErasures.Get(r.Context(), id)
	if errors.Is(err, profileerasure.ErrNotFound) {
		writeProblem(w, http.StatusNotFound, "Not found", "erasure request not found")
		return authz.Principal{}, profileerasure.Request{}, false
	}
	if err != nil {
		serviceError(w, err)
		return authz.Principal{}, profileerasure.Request{}, false
	}
	principal, _, ok = h.authorizeSite(w, principal, request.SiteID, authz.ActionProfilesRead)
	if !ok {
		return authz.Principal{}, profileerasure.Request{}, false
	}
	if ownersOnly && principal.Role != authz.RoleOwner && principal.Role != authz.RoleAdmin {
		writeProblem(w, http.StatusForbidden, "Forbidden", "the principal cannot perform this action")
		return authz.Principal{}, profileerasure.Request{}, false
	}
	return principal, request, true
}

func (h *handler) profilePrincipal(w http.ResponseWriter, r *http.Request, action authz.Action, roles ...authz.Role) (authz.Principal, coresites.Site, api.Profile, bool) {
	principal, ok := h.principal(w, r)
	if !ok {
		return authz.Principal{}, coresites.Site{}, api.Profile{}, false
	}
	if action == authz.ActionProfilesRead && !h.allowHistoricalIdentity(w) {
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
