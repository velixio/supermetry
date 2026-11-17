package platformhttp

import (
	"net/http"
	"strings"

	"supermetry/internal/platform/audit"
	"supermetry/internal/profileerasure"

	"github.com/jackc/pgx/v5"
)

const maxAppUserIDBytes = 512

// eraseAppUser removes what an app project stores about one end user: the
// stored event bodies of their events and the user hash on their event rows.
// The work is batched and repeatable, so a repeated call is safe and reports
// zero. The user id is never written to the audit log, only the counts.
func (h *handler) eraseAppUser(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.appSite(w, r)
	if !ok {
		return
	}
	var in struct {
		UserID string `json:"user_id"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.UserID) == "" || len(in.UserID) > maxAppUserIDBytes {
		writeProblem(w, http.StatusBadRequest, "Bad request", "user_id is required and at most 512 bytes")
		return
	}
	bodies, events, err := profileerasure.EraseAppUser(r.Context(), h.deps.Store.Pool, site.ID, in.UserID, 0)
	if err != nil {
		serviceError(w, err)
		return
	}
	err = h.deps.Store.Tx(r.Context(), func(tx pgx.Tx) error {
		return audit.Write(r.Context(), tx, principal, "app.user_erased", "site", int64(site.ID), map[string]any{
			"bodies": bodies, "events": events,
		})
	})
	if err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"bodies_deleted": bodies, "events_anonymized": events})
}
