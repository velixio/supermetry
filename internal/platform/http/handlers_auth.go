package platformhttp

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"time"

	platformauth "panel/internal/platform/auth"
)

func (h *handler) registerAuth(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/private/auth/login", h.login)
	mux.HandleFunc("POST /api/private/auth/logout", h.logout)
	mux.HandleFunc("POST /api/private/auth/password-reset", h.startPasswordReset)
	mux.HandleFunc("POST /api/private/auth/password-reset/confirm", h.finishPasswordReset)
	mux.HandleFunc("POST /api/private/setup", h.setup)
}

func (h *handler) login(w http.ResponseWriter, r *http.Request) {
	if h.deps.Auth == nil {
		unavailable(w, "authentication")
		return
	}
	var request struct {
		Email       string `json:"email"`
		Password    string `json:"password"`
		TOTPCode    string `json:"totp_code,omitempty"`
		DeviceLabel string `json:"device_label,omitempty"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	csrfToken, err := randomCookieToken()
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "Internal server error", "the request could not be completed")
		return
	}
	token, err := h.deps.Auth.Login(r.Context(), request.Email, request.Password, request.TOTPCode, request.DeviceLabel)
	if err != nil {
		if errors.Is(err, platformauth.ErrRateLimited) {
			w.Header().Set("Retry-After", "60")
			writeProblem(w, http.StatusTooManyRequests, "Rate limit exceeded", "retry after the indicated delay")
			return
		}
		writeProblem(w, http.StatusUnauthorized, "Unauthorized", "invalid email, password, or verification code")
		return
	}
	setCookie(w, platformauth.CookieName, token, 90*24*time.Hour)
	http.SetCookie(w, &http.Cookie{Name: "panel_csrf", Value: csrfToken, Path: "/", Secure: true, SameSite: http.SameSiteLaxMode})
	writeJSON(w, http.StatusOK, map[string]bool{"authenticated": true})
}

func (h *handler) logout(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.privatePrincipal(w, r); !ok {
		return
	}
	if h.deps.Auth == nil {
		unavailable(w, "authentication")
		return
	}
	if cookie, err := r.Cookie(platformauth.CookieName); err == nil {
		if err := h.deps.Auth.Logout(r.Context(), cookie.Value); err != nil {
			serviceError(w, err)
			return
		}
	}
	clearCookie(w, platformauth.CookieName, true)
	clearCookie(w, "panel_csrf", false)
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) startPasswordReset(w http.ResponseWriter, r *http.Request) {
	if h.deps.Auth == nil {
		unavailable(w, "authentication")
		return
	}
	var request struct {
		Email string `json:"email"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if _, err := h.deps.Auth.StartReset(r.Context(), request.Email); err != nil {
		serviceError(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (h *handler) finishPasswordReset(w http.ResponseWriter, r *http.Request) {
	if h.deps.Auth == nil {
		unavailable(w, "authentication")
		return
	}
	var request struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if err := h.deps.Auth.FinishReset(r.Context(), request.Token, request.Password); err != nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", "the reset token is invalid or expired")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) setup(w http.ResponseWriter, r *http.Request) {
	if h.deps.Auth == nil {
		unavailable(w, "authentication")
		return
	}
	var request struct {
		Token    string `json:"token"`
		Email    string `json:"email"`
		Name     string `json:"name"`
		Password string `json:"password"`
		OrgName  string `json:"org_name"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	userID, orgID, err := h.deps.Auth.CompleteSetup(r.Context(), request.Token, request.Email, request.Name, request.Password, request.OrgName)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", "setup is invalid or has already completed")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]int64{"user_id": userID, "org_id": orgID})
}

func setCookie(w http.ResponseWriter, name, value string, maxAge time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: value, Path: "/", MaxAge: int(maxAge.Seconds()),
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
	})
}

func clearCookie(w http.ResponseWriter, name string, httpOnly bool) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Path: "/", MaxAge: -1, Expires: time.Unix(1, 0),
		HttpOnly: httpOnly, Secure: true, SameSite: http.SameSiteLaxMode,
	})
}

func randomCookieToken() (string, error) {
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value[:]), nil
}
