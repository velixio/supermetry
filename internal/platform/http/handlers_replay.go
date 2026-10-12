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
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/platform/audit"
	"supermetry/internal/platform/authz"
	"supermetry/internal/platform/store"
	"supermetry/internal/replay"
	coresites "supermetry/internal/sites"
)

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
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if utf8.RuneCountInString(query) > 200 {
		writeProblem(w, http.StatusBadRequest, "Bad request", "q must be at most 200 characters")
		return
	}
	minDuration, ok := replayDurationFilter(w, r, "min_duration")
	if !ok {
		return
	}
	maxDuration, ok := replayDurationFilter(w, r, "max_duration")
	if !ok {
		return
	}
	if minDuration != nil && maxDuration != nil && *minDuration > *maxDuration {
		writeProblem(w, http.StatusBadRequest, "Bad request", "min_duration must not exceed max_duration")
		return
	}
	hasEvents, ok := replayBooleanFilter(w, r, "has_events")
	if !ok {
		return
	}
	saved, ok := replayBooleanFilter(w, r, "saved")
	if !ok {
		return
	}
	cursor, hasCursor, err := decodeReplayCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", "cursor is invalid")
		return
	}
	filter := replay.ListFilter{
		SiteID: site.ID, UserID: principal.UserID, Retention: int16(site.Retention), From: from, To: to, Limit: limit,
		Query: query, MinDuration: minDuration, MaxDuration: maxDuration, HasEvents: hasEvents, Saved: saved, TotalCap: listResultTotalCap,
	}
	if hasCursor {
		filter.Cursor = &replay.ListCursor{StartedAt: cursor.StartedAt, SessionID: cursor.SessionID}
	}
	page, err := replay.List(r.Context(), h.deps.Store.Pool, filter)
	if err != nil {
		serviceError(w, err)
		return
	}
	for index := range page.Items {
		if !authz.Can(principal, authz.ActionProfilesRead, site.ID) {
			page.Items[index].ProfileID = nil
		}
	}
	var next *string
	if page.Next != nil {
		value := encodeReplayCursor(replayCursor{StartedAt: page.Next.StartedAt, SessionID: page.Next.SessionID})
		next = &value
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": page.Items, "next_cursor": next, "total": page.Total, "total_capped": page.TotalCapped,
	})
}

func replayDurationFilter(w http.ResponseWriter, r *http.Request, name string) (*int64, bool) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return nil, true
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 || value > 31_536_000 {
		writeProblem(w, http.StatusBadRequest, "Bad request", name+" must be a whole number from 0 to 31536000")
		return nil, false
	}
	return &value, true
}

func replayBooleanFilter(w http.ResponseWriter, r *http.Request, name string) (*bool, bool) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return nil, true
	}
	if raw != "true" && raw != "false" {
		writeProblem(w, http.StatusBadRequest, "Bad request", name+" must be true or false")
		return nil, false
	}
	value := raw == "true"
	return &value, true
}

var errReplayNotFound = errors.New("replay not found")

func (h *handler) saveReplay(w http.ResponseWriter, r *http.Request) {
	h.setReplaySaved(w, r, true)
}

func (h *handler) unsaveReplay(w http.ResponseWriter, r *http.Request) {
	h.setReplaySaved(w, r, false)
}

func (h *handler) setReplaySaved(w http.ResponseWriter, r *http.Request, saved bool) {
	principal, site, ok := h.replaySitePrincipal(w, r)
	if !ok {
		return
	}
	if h.deps.Store == nil {
		unavailable(w, "replay")
		return
	}
	if principal.UserID == 0 {
		writeProblem(w, http.StatusForbidden, "Forbidden", "saved replays require a user account")
		return
	}
	sessionID, err := strconv.ParseInt(r.PathValue("session"), 10, 64)
	if err != nil || sessionID <= 0 {
		writeProblem(w, http.StatusBadRequest, "Bad request", "session must be a positive integer")
		return
	}
	err = h.deps.Store.Tx(r.Context(), func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(r.Context(), `select exists(select 1 from replay_sessions where site_id=$1 and session_id=$2)`, site.ID, sessionID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return errReplayNotFound
		}
		var changed bool
		if saved {
			result, execErr := tx.Exec(r.Context(), `insert into replay_saved(site_id,user_id,session_id) values ($1,$2,$3) on conflict do nothing`, site.ID, principal.UserID, sessionID)
			err, changed = execErr, result.RowsAffected() != 0
		} else {
			result, execErr := tx.Exec(r.Context(), `delete from replay_saved where site_id=$1 and user_id=$2 and session_id=$3`, site.ID, principal.UserID, sessionID)
			err, changed = execErr, result.RowsAffected() != 0
		}
		if err != nil || !changed {
			return err
		}
		action := "replay.saved"
		if !saved {
			action = "replay.unsaved"
		}
		return audit.Write(r.Context(), tx, principal, action, "replay", sessionID, map[string]any{"site_id": site.ID})
	})
	if errors.Is(err, errReplayNotFound) {
		writeProblem(w, http.StatusNotFound, "Not found", "replay not found")
		return
	}
	if err != nil {
		serviceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
	var dayText string
	err = h.deps.Store.Pool.QueryRow(r.Context(), `select blob_key,day::text from replay_sessions where site_id=$1 and session_id=$2 order by started_at desc limit 1`, site.ID, sessionID).Scan(&key, &dayText)
	if errors.Is(err, pgx.ErrNoRows) {
		writeProblem(w, http.StatusNotFound, "Not found", "replay not found")
		return
	}
	if err != nil {
		serviceError(w, err)
		return
	}
	day, err := time.Parse(time.DateOnly, dayText)
	if err != nil || key != replay.SessionKey(site.ID, day, sessionID) {
		writeProblem(w, http.StatusNotFound, "Not found", "replay not found")
		return
	}
	body, err := h.deps.ReplayStore.Open(r.Context(), key)
	if err != nil {
		writeProblem(w, http.StatusNotFound, "Not found", "replay data not found")
		return
	}
	defer body.Close()
	// Large replay downloads can legitimately outlive the server-wide write timeout.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
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
	if err := validateReplayPolicy(h.deps.ReplayEnv, in); err != nil {
		writeProblem(w, http.StatusConflict, "Conflict", err.Error())
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
	if !h.allowHistoricalIdentity(w) {
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
