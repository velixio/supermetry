package platformhttp

import (
	"errors"
	"net/http"

	platformauth "supermetry/internal/platform/auth"
)

func (h *handler) adminGetUser(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.adminPrincipal(w, r); !ok {
		return
	}
	userID, ok := adminID(r, "user")
	if !ok {
		writeProblem(w, http.StatusBadRequest, "Bad request", "user is invalid")
		return
	}
	detail, err := h.deps.Admin.UserDetail(r.Context(), userID)
	if err != nil {
		adminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// adminRevokeUserSessions signs a user out of every session. Audited.
func (h *handler) adminRevokeUserSessions(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.adminPrincipal(w, r)
	if !ok {
		return
	}
	userID, ok := adminID(r, "user")
	if !ok {
		writeProblem(w, http.StatusBadRequest, "Bad request", "user is invalid")
		return
	}
	revoked, err := h.deps.Admin.RevokeUserSessions(r.Context(), principal.UserID, userID)
	if err != nil {
		adminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"revoked": revoked})
}

// adminStartPasswordReset emails the user a reset link, or returns the one-time
// link to the admin when no mail transport is configured. Audited.
func (h *handler) adminStartPasswordReset(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.adminPrincipal(w, r)
	if !ok {
		return
	}
	userID, ok := adminID(r, "user")
	if !ok {
		writeProblem(w, http.StatusBadRequest, "Bad request", "user is invalid")
		return
	}
	result, err := h.deps.Admin.StartPasswordReset(h.clientContext(r), principal.UserID, userID)
	if err != nil {
		if errors.Is(err, platformauth.ErrRateLimited) {
			rateLimited(w)
			return
		}
		adminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
