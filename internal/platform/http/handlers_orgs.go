package platformhttp

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"supermetry/internal/platform/authz"
)

func (h *handler) createAPIKey(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.privatePrincipal(w, r)
	if !ok {
		return
	}
	if _, ok := orgIDFromPath(w, r, principal); !ok {
		return
	}
	if !authz.Can(principal, authz.ActionKeysManage, 0) {
		writeProblem(w, http.StatusForbidden, "Forbidden", "the principal cannot manage API keys")
		return
	}
	var request CreateAPIKeyRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if h.deps.Mutations == nil {
		unavailable(w, "API key")
		return
	}
	created, err := h.deps.Mutations.CreateAPIKey(r.Context(), principal, request)
	if err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *handler) listAudit(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.privatePrincipal(w, r)
	if !ok {
		return
	}
	if _, ok := orgIDFromPath(w, r, principal); !ok {
		return
	}
	if !authz.Can(principal, authz.ActionAuditRead, 0) {
		writeProblem(w, http.StatusForbidden, "Forbidden", "the principal cannot read the audit log")
		return
	}
	limit, ok := parseLimit(r)
	if !ok {
		writeProblem(w, http.StatusBadRequest, "Bad request", "limit must be between 1 and 100")
		return
	}
	if h.deps.Store == nil {
		writeJSON(w, http.StatusOK, map[string]any{"data": []any{}, "next_cursor": nil})
		return
	}
	entries, next, err := h.auditEntries(r, principal.OrgID, limit)
	if errors.Is(err, errInvalidAuditCursor) {
		writeProblem(w, http.StatusBadRequest, "Bad request", "cursor is invalid")
		return
	}
	if err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": entries, "next_cursor": nullableCursor(next)})
}

type auditResponse struct {
	ID          int64           `json:"id"`
	ActorUserID *int64          `json:"actor_user_id,omitempty"`
	ActorKeyID  *int64          `json:"actor_key_id,omitempty"`
	Action      string          `json:"action"`
	TargetType  string          `json:"target_type"`
	TargetID    int64           `json:"target_id"`
	Metadata    json.RawMessage `json:"metadata"`
	CreatedAt   time.Time       `json:"created_at"`
}

type auditCursor struct {
	CreatedAt time.Time `json:"created_at"`
	ID        int64     `json:"id"`
}

var errInvalidAuditCursor = errors.New("invalid audit cursor")

func (h *handler) auditEntries(r *http.Request, orgID int64, limit int) ([]auditResponse, string, error) {
	cursor, hasCursor, err := decodeAuditCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		return nil, "", err
	}
	query := `select id,actor_user_id,actor_key_id,action,target_type,target_id,metadata,created_at
		from audit_log where org_id=$1 order by created_at desc,id desc limit $2`
	args := []any{orgID, limit + 1}
	if hasCursor {
		query = `select id,actor_user_id,actor_key_id,action,target_type,target_id,metadata,created_at
			from audit_log where org_id=$1 and (created_at,id)<($2,$3)
			order by created_at desc,id desc limit $4`
		args = []any{orgID, cursor.CreatedAt, cursor.ID, limit + 1}
	}
	rows, err := h.deps.Store.Pool.Query(r.Context(), query, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	entries := make([]auditResponse, 0, limit+1)
	for rows.Next() {
		var entry auditResponse
		if err := rows.Scan(&entry.ID, &entry.ActorUserID, &entry.ActorKeyID, &entry.Action, &entry.TargetType, &entry.TargetID, &entry.Metadata, &entry.CreatedAt); err != nil {
			return nil, "", err
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(entries) > limit {
		last := entries[limit-1]
		next = encodeAuditCursor(auditCursor{CreatedAt: last.CreatedAt, ID: last.ID})
		entries = entries[:limit]
	}
	return entries, next, nil
}

func encodeAuditCursor(cursor auditCursor) string {
	encoded, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func decodeAuditCursor(value string) (auditCursor, bool, error) {
	if value == "" {
		return auditCursor{}, false, nil
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(value)
	if err != nil {
		return auditCursor{}, false, errInvalidAuditCursor
	}
	var cursor auditCursor
	if err := json.Unmarshal(decoded, &cursor); err != nil || cursor.CreatedAt.IsZero() || cursor.ID <= 0 {
		return auditCursor{}, false, errInvalidAuditCursor
	}
	return cursor, true, nil
}

func nullableCursor(cursor string) any {
	if cursor == "" {
		return nil
	}
	return cursor
}

func (h *handler) deleteOrg(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.privatePrincipal(w, r)
	if !ok {
		return
	}
	orgID, ok := orgIDFromPath(w, r, principal)
	if !ok {
		return
	}
	if !authz.Can(principal, authz.ActionOrgDelete, 0) {
		writeProblem(w, http.StatusForbidden, "Forbidden", "the principal cannot delete the organization")
		return
	}
	if h.deps.Mutations == nil {
		unavailable(w, "organization")
		return
	}
	if err := h.deps.Mutations.DeleteOrg(r.Context(), principal, orgID); err != nil {
		serviceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
