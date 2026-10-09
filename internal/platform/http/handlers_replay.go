package platformhttp

import (
	"compress/gzip"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"panel/internal/dict"
	"panel/internal/platform/authz"
	"panel/internal/platform/store"
	"panel/internal/replay"
	coresites "panel/internal/sites"
)

type replayListItem struct {
	SessionID int64     `json:"session_id,string"` // string: JS numbers lose precision above 2^53
	StartedAt time.Time `json:"started_at"`
	DurationS int64     `json:"duration_s"`
	Pages     int32     `json:"pages"`
	Bytes     int64     `json:"bytes"`
	Trigger   string    `json:"trigger"`
	ProfileID *int64    `json:"profile_id"`
	Browser   string    `json:"browser"`
	OS        string    `json:"os"`
	Device    string    `json:"device"`
	Country   string    `json:"country"`
	City      string    `json:"city"`
	Actions   int64     `json:"actions"`
}

type replayCursor struct {
	StartedAt time.Time `json:"started_at"`
	SessionID int64     `json:"session_id"`
}

func (h *handler) listReplays(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.replaySitePrincipal(w, r)
	if !ok {
		return
	}
	if h.deps.Store == nil {
		unavailable(w, "replay")
		return
	}
	from, fromErr := time.Parse(time.DateOnly, r.URL.Query().Get("from"))
	to, toErr := time.Parse(time.DateOnly, r.URL.Query().Get("to"))
	if fromErr != nil || toErr != nil || from.After(to) {
		writeProblem(w, http.StatusBadRequest, "Bad request", "from and to must be valid dates in ascending order")
		return
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			writeProblem(w, http.StatusBadRequest, "Bad request", "limit must be between 1 and 100")
			return
		}
		limit = parsed
	}
	cursor, hasCursor, err := decodeReplayCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", "cursor is invalid")
		return
	}
	var count int
	err = h.deps.Store.Pool.QueryRow(r.Context(), `select count(*) from (
		select 1 from replay_sessions r
		`+replayListWhereSQL(false)+`
		limit $4
	) t`, site.ID, from, to, listResultTotalCap+1).Scan(&count)
	if err != nil {
		serviceError(w, err)
		return
	}
	total, totalCapped := capListTotal(count)
	rows, err := h.deps.Store.Pool.Query(r.Context(), `
		select r.session_id,r.started_at,greatest(0,extract(epoch from (r.last_seen_at-r.started_at)))::bigint,
		       r.pages,r.bytes,coalesce(r.trigger,''),r.profile_id,
		       coalesce(browser.value,''),coalesce(os.value,''),
		       case details.device when 0 then 'desktop' when 1 then 'mobile' when 2 then 'tablet' else '' end,
		       coalesce(trim(details.country),''),coalesce(city.value,''),coalesce(details.actions,0)
		from replay_sessions r
		left join lateral (
		  select candidate.country,candidate.city_id,candidate.device,candidate.browser_id,candidate.os_id,candidate.actions
		  from (
		    select s.country,s.city_id,s.device,s.browser_id,s.os_id,
		           s.pageviews::bigint+s.custom::bigint as actions,0 as priority
		    from sessions_closed s
		    where s.site_id=r.site_id and s.session_id=r.session_id
		      and s.day between r.day-1 and r.day+1
		      and s.started_at>=r.started_at-interval '1 day' and s.started_at<r.started_at+interval '1 day'
		      and exists (select 1 from day_close dc where dc.site_id=s.site_id and dc.day=s.day and dc.status='done')
		    union all
		    select s.country,s.city_id,s.device,s.browser_id,s.os_id,
		           s.pageviews::bigint+s.events::bigint as actions,1 as priority
		    from sessions s
		    where s.site_id=r.site_id and s.retention_class=$8 and s.session_id=r.session_id
		      and s.start>=r.started_at-interval '1 day' and s.start<r.started_at+interval '1 day'
		      and not exists (select 1 from day_close dc where dc.site_id=s.site_id and dc.day=s.local_day and dc.status='done')
		  ) candidate
		  order by candidate.priority
		  limit 1
		) details on true
		left join dict browser on browser.kind=$9 and browser.id=details.browser_id
		left join dict os on os.kind=$10 and os.id=details.os_id
		left join dict city on city.kind=$11 and city.id=details.city_id
		`+replayListWhereSQL(true)+`
		order by r.started_at desc,r.session_id desc limit $7`, site.ID, from, to, hasCursor, cursor.StartedAt, cursor.SessionID, limit+1,
		site.Retention, dict.Browser, dict.OS, dict.City)
	if err != nil {
		serviceError(w, err)
		return
	}
	defer rows.Close()
	items := make([]replayListItem, 0, limit+1)
	for rows.Next() {
		var item replayListItem
		if err := rows.Scan(&item.SessionID, &item.StartedAt, &item.DurationS, &item.Pages, &item.Bytes, &item.Trigger, &item.ProfileID,
			&item.Browser, &item.OS, &item.Device, &item.Country, &item.City, &item.Actions); err != nil {
			serviceError(w, err)
			return
		}
		if !authz.Can(principal, authz.ActionProfilesRead, site.ID) {
			item.ProfileID = nil
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		serviceError(w, err)
		return
	}
	var next *string
	if len(items) > limit {
		last := items[limit-1]
		value := encodeReplayCursor(replayCursor{StartedAt: last.StartedAt, SessionID: last.SessionID})
		next = &value
		items = items[:limit]
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": items, "next_cursor": next, "total": total, "total_capped": totalCapped,
	})
}

func replayListWhereSQL(includeCursor bool) string {
	where := `where r.site_id=$1 and r.day between $2::date and $3::date
		  and r.started_at>=$2::date-interval '1 day' and r.started_at<$3::date+interval '2 days'`
	if includeCursor {
		where += `
		  and ($4::boolean=false or (r.started_at,r.session_id)<($5,$6))`
	}
	return where
}

func (h *handler) replayAsset(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.PathValue("replay_path"), "/"), "/")
	switch {
	case len(parts) == 2 && parts[0] == "css":
		r.SetPathValue("hash", parts[1])
		h.replayCSS(w, r)
	case len(parts) == 2 && parts[1] == "data":
		r.SetPathValue("session", parts[0])
		h.replayData(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (h *handler) replayData(w http.ResponseWriter, r *http.Request) {
	_, site, ok := h.replaySitePrincipal(w, r)
	if !ok {
		return
	}
	if h.deps.Store == nil || h.deps.ReplayStore == nil {
		unavailable(w, "replay")
		return
	}
	sessionID, err := parseInt64(r.PathValue("session"))
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", "session must be a positive integer")
		return
	}
	var key string
	var day time.Time
	err = h.deps.Store.Pool.QueryRow(r.Context(), `select blob_key,day from replay_sessions where site_id=$1 and session_id=$2 order by started_at desc limit 1`, site.ID, sessionID).Scan(&key, &day)
	if errors.Is(err, pgx.ErrNoRows) {
		writeProblem(w, http.StatusNotFound, "Not found", "replay not found")
		return
	}
	if err != nil {
		serviceError(w, err)
		return
	}
	if key != replay.SessionKey(site.ID, day, sessionID) {
		writeProblem(w, http.StatusNotFound, "Not found", "replay not found")
		return
	}
	body, err := h.deps.ReplayStore.Open(r.Context(), key)
	if err != nil {
		writeProblem(w, http.StatusNotFound, "Not found", "replay data not found")
		return
	}
	defer body.Close()
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Content-Encoding", "gzip")
	w.Header().Set("Cache-Control", "private, no-store")
	zw := gzip.NewWriter(w)
	_, copyErr := io.Copy(zw, replay.NewConcatReader(body))
	closeErr := zw.Close()
	if copyErr != nil || closeErr != nil {
		return
	}
}

func (h *handler) replayCSS(w http.ResponseWriter, r *http.Request) {
	_, site, ok := h.replaySitePrincipal(w, r)
	if !ok {
		return
	}
	if h.deps.ReplayStore == nil {
		unavailable(w, "replay")
		return
	}
	hash := r.PathValue("hash")
	if len(hash) != sha256.Size*2 {
		http.NotFound(w, r)
		return
	}
	if _, err := hex.DecodeString(hash); err != nil {
		http.NotFound(w, r)
		return
	}
	body, err := h.deps.ReplayStore.Open(r.Context(), replay.CSSKey(site.ID, hash))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer body.Close()
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Content-Encoding", "gzip")
	w.Header().Set("Cache-Control", "private, no-store")
	_, _ = io.Copy(w, body)
}

func (h *handler) getReplaySettings(w http.ResponseWriter, r *http.Request) {
	_, site, ok := h.sitePrincipal(w, r, authz.ActionStatsRead, authz.RoleOwner, authz.RoleAdmin, authz.RoleEditor, authz.RoleViewer)
	if !ok {
		return
	}
	settings, ok := h.loadReplaySiteSettings(w, r, site)
	if ok {
		writeJSON(w, http.StatusOK, settings)
	}
}

func (h *handler) putReplaySettings(w http.ResponseWriter, r *http.Request) {
	// Changing replay settings can start recording visitors, so only site
	// managers may do it; viewers and editors keep read access.
	_, site, ok := h.sitePrincipal(w, r, authz.ActionSiteEdit, authz.RoleOwner, authz.RoleAdmin)
	if !ok {
		return
	}
	var raw json.RawMessage
	if !decodeJSON(w, r, &raw) {
		return
	}
	in, err := coresites.ParseReplaySiteInput(raw)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", err.Error())
		return
	}
	// Recording is switched on only by PANEL_REPLAY_ENABLED. Clients may echo the
	// current value back, but never change it, and it is not stored per site.
	var switchField struct {
		Enabled *bool `json:"enabled"`
	}
	_ = json.Unmarshal(raw, &switchField)
	if switchField.Enabled != nil && *switchField.Enabled != h.deps.ReplayEnv.Enabled {
		writeProblem(w, http.StatusConflict, "Conflict", "session replay is turned on or off only by PANEL_REPLAY_ENABLED")
		return
	}
	in.Enabled = false
	if (in.Mask != nil && h.deps.ReplayEnv.Mask != nil) ||
		(in.SampleRate != nil && h.deps.ReplayEnv.SampleRate != nil) ||
		(in.RetentionDays != nil && h.deps.ReplayEnv.Retention != nil) ||
		(in.QuotaBytes != nil && h.deps.ReplayEnv.QuotaBytes != nil) {
		writeProblem(w, http.StatusConflict, "Conflict", "a replay setting is locked by the environment")
		return
	}
	encoded, _ := json.Marshal(in)
	if err := store.SetSiteReplaySettings(r.Context(), h.deps.Store, site.OrgID, site.ID, encoded); err != nil {
		serviceError(w, err)
		return
	}
	if h.deps.ReplayRefresh != nil {
		if err := h.deps.ReplayRefresh(); err != nil {
			serviceError(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, replay.ResolveSite(h.deps.ReplayEnv, in))
}

func (h *handler) loadReplaySiteSettings(w http.ResponseWriter, r *http.Request, site coresites.Site) (replay.SiteSettings, bool) {
	if h.deps.Store == nil {
		unavailable(w, "replay settings")
		return replay.SiteSettings{}, false
	}
	raw, err := store.SiteReplaySettings(r.Context(), h.deps.Store, site.OrgID, site.ID)
	if err != nil {
		serviceError(w, err)
		return replay.SiteSettings{}, false
	}
	in, err := coresites.ParseReplaySiteInput(raw)
	if err != nil {
		serviceError(w, err)
		return replay.SiteSettings{}, false
	}
	return replay.ResolveSite(h.deps.ReplayEnv, in), true
}

type replayStorageS3Response struct {
	Endpoint     string `json:"endpoint"`
	Bucket       string `json:"bucket"`
	Region       string `json:"region"`
	AccessKey    string `json:"access_key"`
	Prefix       string `json:"prefix"`
	SecretKeySet bool   `json:"secret_key_set"`
}

type replayStorageResponse struct {
	Backend replay.Value[string]                  `json:"backend"`
	Dir     replay.Value[string]                  `json:"dir"`
	S3      replay.Value[replayStorageS3Response] `json:"s3"`
}

func (h *handler) getReplayStorage(w http.ResponseWriter, r *http.Request) {
	if !h.instanceReplayAdmin(w, r) {
		return
	}
	settings, ok := h.loadReplayStorage(w, r)
	if ok {
		writeJSON(w, http.StatusOK, replayStorageAPI(settings))
	}
}

func (h *handler) putReplayStorage(w http.ResponseWriter, r *http.Request) {
	if !h.instanceReplayAdmin(w, r) {
		return
	}
	current, ok := h.loadStoredReplayStorage(w, r)
	if !ok {
		return
	}
	var request struct {
		Backend *string `json:"backend"`
		Dir     *string `json:"dir"`
		S3      *struct {
			Endpoint  string `json:"endpoint"`
			Bucket    string `json:"bucket"`
			Region    string `json:"region"`
			AccessKey string `json:"access_key"`
			SecretKey string `json:"secret_key"`
			Prefix    string `json:"prefix"`
		} `json:"s3"`
		Migrate bool `json:"migrate"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if ((request.Backend != nil || request.S3 != nil) && h.deps.ReplayEnv.S3 != nil) || (request.Dir != nil && h.deps.ReplayEnv.Dir != "") {
		writeProblem(w, http.StatusConflict, "Conflict", "a replay storage setting is locked by the environment")
		return
	}
	next := current
	if request.Backend != nil {
		next.Backend = *request.Backend
	}
	if request.Dir != nil {
		next.Dir = *request.Dir
	}
	if request.S3 != nil {
		secret := request.S3.SecretKey
		if secret == "" {
			secret = current.S3.SecretKey
		}
		next.S3 = replay.S3Settings{Endpoint: request.S3.Endpoint, Bucket: request.S3.Bucket, Region: request.S3.Region, AccessKey: request.S3.AccessKey, SecretKey: secret, Prefix: request.S3.Prefix}
	}
	if next.Backend == "" {
		next.Backend = "disk"
	}
	if next.Backend != "disk" && next.Backend != "s3" {
		writeProblem(w, http.StatusBadRequest, "Bad request", "backend must be disk or s3")
		return
	}
	if next.Backend == "s3" && (next.S3.Endpoint == "" || next.S3.Bucket == "" || next.S3.Region == "" || next.S3.AccessKey == "" || next.S3.SecretKey == "") {
		writeProblem(w, http.StatusBadRequest, "Bad request", "all S3 fields except prefix are required")
		return
	}
	sealed, err := replay.SealStoredStorage(h.deps.SecretKey, next)
	if err != nil {
		serviceError(w, err)
		return
	}
	var migration any
	if request.Migrate {
		from, err := replay.SealStoredStorage(h.deps.SecretKey, current)
		if err != nil {
			serviceError(w, err)
			return
		}
		migration = map[string]any{"from": json.RawMessage(from), "to": json.RawMessage(sealed), "requested_at": time.Now().UTC()}
	}
	if err := store.SetInstanceReplayStorage(r.Context(), h.deps.Store, sealed, migration); err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, replayStorageAPI(replay.ResolveStorage(h.deps.ReplayEnv, h.deps.DataDir, next)))
}

func (h *handler) loadReplayStorage(w http.ResponseWriter, r *http.Request) (replay.StorageSettings, bool) {
	stored, ok := h.loadStoredReplayStorage(w, r)
	if !ok {
		return replay.StorageSettings{}, false
	}
	return replay.ResolveStorage(h.deps.ReplayEnv, h.deps.DataDir, stored), true
}

func (h *handler) loadStoredReplayStorage(w http.ResponseWriter, r *http.Request) (replay.StoredStorage, bool) {
	if h.deps.Store == nil {
		unavailable(w, "replay storage")
		return replay.StoredStorage{}, false
	}
	raw, err := store.InstanceReplayStorage(r.Context(), h.deps.Store)
	if err != nil {
		serviceError(w, err)
		return replay.StoredStorage{}, false
	}
	stored, err := replay.OpenStoredStorage(h.deps.SecretKey, raw)
	if err != nil {
		serviceError(w, err)
		return replay.StoredStorage{}, false
	}
	return stored, true
}

func replayStorageAPI(settings replay.StorageSettings) replayStorageResponse {
	s3 := settings.S3.V
	return replayStorageResponse{
		Backend: settings.Backend,
		Dir:     settings.Dir,
		S3: replay.Value[replayStorageS3Response]{V: replayStorageS3Response{
			Endpoint: s3.Endpoint, Bucket: s3.Bucket, Region: s3.Region, AccessKey: s3.AccessKey, Prefix: s3.Prefix, SecretKeySet: s3.SecretKey != "",
		}, Locked: settings.S3.Locked},
	}
}

func (h *handler) instanceReplayAdmin(w http.ResponseWriter, r *http.Request) bool {
	if validAdminBearer(r.Header.Get("Authorization"), h.deps.AdminAPIKey) {
		return true
	}
	principal, ok := h.privatePrincipal(w, r)
	if !ok {
		return false
	}
	if principal.Role != authz.RoleOwner || h.deps.Store == nil {
		writeProblem(w, http.StatusForbidden, "Forbidden", "instance administration is required")
		return false
	}
	var oldestOrgID int64
	if err := h.deps.Store.Pool.QueryRow(r.Context(), `select id from orgs where deleted_at is null order by created_at,id limit 1`).Scan(&oldestOrgID); err != nil {
		serviceError(w, err)
		return false
	}
	if principal.OrgID != oldestOrgID {
		writeProblem(w, http.StatusForbidden, "Forbidden", "instance administration is required")
		return false
	}
	return true
}

func validAdminBearer(header, key string) bool {
	scheme, credential, ok := strings.Cut(header, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || credential == "" || key == "" {
		return false
	}
	want, got := sha256.Sum256([]byte(key)), sha256.Sum256([]byte(credential))
	return subtle.ConstantTimeCompare(want[:], got[:]) == 1
}

func (h *handler) replaySitePrincipal(w http.ResponseWriter, r *http.Request) (authz.Principal, coresites.Site, bool) {
	principal, site, ok := h.sitePrincipal(w, r, authz.ActionReplayRead, authz.RoleOwner, authz.RoleAdmin, authz.RoleEditor, authz.RoleViewer)
	if !ok || !authorizeSensitiveExport(w, r, principal, site.ID, authz.ActionReplayRead) {
		return authz.Principal{}, coresites.Site{}, false
	}
	return principal, site, true
}

func encodeReplayCursor(cursor replayCursor) string {
	raw, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeReplayCursor(raw string) (replayCursor, bool, error) {
	if raw == "" {
		return replayCursor{}, false, nil
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if err != nil {
		return replayCursor{}, false, err
	}
	var cursor replayCursor
	if err := json.Unmarshal(decoded, &cursor); err != nil || cursor.StartedAt.IsZero() || cursor.SessionID <= 0 {
		return replayCursor{}, false, errors.New("invalid replay cursor")
	}
	return cursor, true, nil
}
