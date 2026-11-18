package platformhttp

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	platformauth "supermetry/internal/platform/auth"
	"supermetry/internal/platform/authz"
)

// reauthRequiredCode is the machine-readable code of the 403 a sensitive action
// returns when the session has not proved the user's identity recently.
const reauthRequiredCode = "reauth_required"

func writeProblemCode(w http.ResponseWriter, status int, title, detail, code string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(problem{
		Type:  "https://supermetry.com/problems/" + code,
		Title: title, Status: status, Detail: detail, Code: code,
	})
}

// requireRecentAuth gates a sensitive action behind a recent re-authentication
// of a cookie session (POST /api/private/auth/reauth). API keys are exempt:
// they hold no password, and what they may do is bounded by their scopes. It
// writes the 403 itself and reports false when the action must not proceed.
func (h *handler) requireRecentAuth(w http.ResponseWriter, r *http.Request, principal authz.Principal) bool {
	if principal.Role == authz.RoleKey || principal.UserID == 0 {
		return true
	}
	var checker StepUpChecker = h.deps.StepUp
	if checker == nil && h.deps.Auth != nil {
		checker = h.deps.Auth
	}
	if checker == nil {
		unavailable(w, "authentication")
		return false
	}
	cookieValue := ""
	if cookie, err := r.Cookie(platformauth.CookieName); err == nil {
		cookieValue = cookie.Value
	}
	if err := checker.RequireRecentAuth(r.Context(), cookieValue); err != nil {
		writeProblemCode(w, http.StatusForbidden, "Re-authentication required",
			"confirm your password to continue with this action", reauthRequiredCode)
		return false
	}
	return true
}

// reauthenticate checks the signed-in user's password (and authentication code
// when two-factor is on) and stamps the session, opening a ten minute window
// for sensitive actions.
func (h *handler) reauthenticate(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.privatePrincipal(w, r)
	if !ok {
		return
	}
	if h.deps.Auth == nil || principal.UserID == 0 {
		unavailable(w, "authentication")
		return
	}
	var request struct {
		Password string `json:"password"`
		TOTPCode string `json:"totp_code,omitempty"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	cookie, err := r.Cookie(platformauth.CookieName)
	if err != nil {
		writeProblem(w, http.StatusUnauthorized, "Unauthorized", "authentication is required")
		return
	}
	err = h.deps.Auth.Reauthenticate(h.clientContext(r), principal.UserID, cookie.Value, request.Password, request.TOTPCode)
	switch {
	case err == nil:
		expires := time.Now().Add(platformauth.StepUpWindow).UTC()
		writeJSON(w, http.StatusOK, map[string]any{
			"reauthenticated":    true,
			"valid_for_seconds":  int(platformauth.StepUpWindow / time.Second),
			"reauth_valid_until": expires,
		})
	case errors.Is(err, platformauth.ErrRateLimited):
		rateLimited(w)
	case errors.Is(err, platformauth.ErrTOTPRequired):
		writeProblemCode(w, http.StatusUnauthorized, "Unauthorized", "an authentication or recovery code is required", "totp_required")
	case errors.Is(err, platformauth.ErrInvalid):
		writeProblemCode(w, http.StatusUnauthorized, "Unauthorized", "password or authentication code is invalid", "invalid_credentials")
	default:
		serviceError(w, err)
	}
}
