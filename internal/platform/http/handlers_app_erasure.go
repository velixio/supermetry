package platformhttp

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"supermetry/internal/profileerasure"

	"github.com/jackc/pgx/v5"
)

const maxAppUserIDBytes = 512

// eraseAppUser queues the erasure of what an app project stores about one end
// user: the stored event bodies of their events and the user hash on their
// event rows. A worker does the batched deletion, so the call returns 202 with
// a status URL at once. The request stores only the keyed hash of the user id
// (never the id), and a repeat call for the same user returns the live request.
// The audit row (counts only, no user id) is written when the worker finishes.
func (h *handler) eraseAppUser(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.appSite(w, r)
	if !ok {
		return
	}
	if h.deps.AppErasures == nil {
		unavailable(w, "app user erasure")
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
	var request profileerasure.AppRequest
	err := h.deps.Store.Tx(r.Context(), func(tx pgx.Tx) error {
		var err error
		request, err = h.deps.AppErasures.CreateOrGetTx(r.Context(), tx, site.ID, in.UserID, erasureRequester(principal))
		return err
	})
	if err != nil {
		serviceError(w, err)
		return
	}
	status := "/api/private/sites/" + strconv.FormatInt(int64(site.ID), 10) + "/app/users/erasures/" + strconv.FormatInt(request.ID, 10)
	w.Header().Set("Location", status)
	body := newAppErasureResponse(request)
	body["status_url"] = status
	writeJSON(w, http.StatusAccepted, body)
}

func (h *handler) getAppUserErasure(w http.ResponseWriter, r *http.Request) {
	_, site, ok := h.appSite(w, r)
	if !ok {
		return
	}
	if h.deps.AppErasures == nil {
		unavailable(w, "app user erasure")
		return
	}
	id, err := parseInt64(r.PathValue("erasure"))
	if err != nil {
		writeProblem(w, http.StatusNotFound, "Not found", "erasure request not found")
		return
	}
	request, err := h.deps.AppErasures.GetForSite(r.Context(), id, site.ID)
	if errors.Is(err, profileerasure.ErrNotFound) {
		writeProblem(w, http.StatusNotFound, "Not found", "erasure request not found")
		return
	}
	if err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newAppErasureResponse(request))
}

func newAppErasureResponse(request profileerasure.AppRequest) map[string]any {
	return map[string]any{
		"id": request.ID, "status": request.Status,
		"bodies_deleted": request.BodiesDeleted, "events_anonymized": request.EventsAnonymized,
		"attempts": request.Attempts, "last_error": request.LastError,
		"created_at": request.CreatedAt, "completed_at": request.CompletedAt,
	}
}
