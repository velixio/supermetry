package platformhttp

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
)

const (
	minReplayRetentionCapDays = 1
	maxReplayRetentionCapDays = 365
)

var errOrgNotFound = errors.New("organization not found")

type replayRetentionCapResponse struct {
	Days   *int   `json:"days"`
	Source string `json:"source"` // "org" when the organization has a cap, otherwise "none"
}

// adminReplayRetentionCap shows an organization's replay retention cap.
func (h *handler) adminReplayRetentionCap(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.adminPrincipal(w, r); !ok {
		return
	}
	orgID, ok := adminID(r, "org")
	if !ok || h.deps.Store == nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", "organization is invalid")
		return
	}
	h.writeReplayRetentionCap(w, r, orgID)
}

// adminSetReplayRetentionCap sets the cap to 1..365 days, or removes it with null.
// Recordings older than the new effective retention are deleted at the next
// replay retention run.
func (h *handler) adminSetReplayRetentionCap(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.adminPrincipal(w, r)
	if !ok {
		return
	}
	orgID, valid := adminID(r, "org")
	if !valid || h.deps.Store == nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", "organization is invalid")
		return
	}
	var request struct {
		Days json.RawMessage `json:"days"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	var days *int
	switch raw := string(request.Days); {
	case raw == "":
		writeProblem(w, http.StatusBadRequest, "Bad request", "days is required: a whole number from 1 to 365, or null for no cap")
		return
	case raw != "null":
		var n int
		if err := json.Unmarshal(request.Days, &n); err != nil || n < minReplayRetentionCapDays || n > maxReplayRetentionCapDays {
			writeProblem(w, http.StatusBadRequest, "Bad request", "days must be a whole number from 1 to 365, or null")
			return
		}
		days = &n
	}
	h.changeReplayRetentionCap(w, r, orgID, days, principal.UserID)
}

// adminResetReplayRetentionCap removes the cap; it is the same as setting null.
func (h *handler) adminResetReplayRetentionCap(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.adminPrincipal(w, r)
	if !ok {
		return
	}
	orgID, valid := adminID(r, "org")
	if !valid || h.deps.Store == nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", "organization is invalid")
		return
	}
	h.changeReplayRetentionCap(w, r, orgID, nil, principal.UserID)
}

func (h *handler) changeReplayRetentionCap(w http.ResponseWriter, r *http.Request, orgID int64, days *int, actor int64) {
	err := pgx.BeginFunc(r.Context(), h.deps.Store.Pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(r.Context(), `update orgs set replay_retention_cap_days=$2 where id=$1`, orgID, days)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errOrgNotFound
		}
		meta, _ := json.Marshal(map[string]any{"days": days})
		_, err = tx.Exec(r.Context(), `
			insert into audit_log(org_id,actor_user_id,action,target_type,target_id,metadata)
			values ($1,nullif($2::bigint,0),'replay.retention_cap_changed','organization',$1,$3)`, orgID, actor, meta)
		return err
	})
	if errors.Is(err, errOrgNotFound) {
		writeProblem(w, http.StatusNotFound, "Not found", "organization not found")
		return
	}
	if err != nil {
		serviceError(w, err)
		return
	}
	if h.deps.ReplayRefresh != nil {
		if err := h.deps.ReplayRefresh(); err != nil {
			serviceError(w, err)
			return
		}
	}
	h.writeReplayRetentionCap(w, r, orgID)
}

func (h *handler) writeReplayRetentionCap(w http.ResponseWriter, r *http.Request, orgID int64) {
	var days *int
	err := h.deps.Store.Pool.QueryRow(r.Context(), `select replay_retention_cap_days from orgs where id=$1`, orgID).Scan(&days)
	if errors.Is(err, pgx.ErrNoRows) {
		writeProblem(w, http.StatusNotFound, "Not found", "organization not found")
		return
	}
	if err != nil {
		serviceError(w, err)
		return
	}
	out := replayRetentionCapResponse{Days: days, Source: "none"}
	if days != nil {
		out.Source = "org"
	}
	writeJSON(w, http.StatusOK, out)
}
