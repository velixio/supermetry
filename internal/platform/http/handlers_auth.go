package platformhttp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"github.com/mileusna/useragent"
	"net/http"
	"strings"
	"time"

	"supermetry/internal/clientip"
	platformauth "supermetry/internal/platform/auth"
)

func (h *handler) registerAuth(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/private/auth/config", h.authConfig)
	mux.HandleFunc("POST /api/private/auth/login", h.login)
	mux.HandleFunc("POST /api/private/auth/signup", h.signup)
	mux.HandleFunc("POST /api/private/auth/logout", h.logout)
	mux.HandleFunc("POST /api/private/auth/password-reset", h.startPasswordReset)
	mux.HandleFunc("POST /api/private/auth/password-reset/confirm", h.finishPasswordReset)
	mux.HandleFunc("GET /api/private/auth/email-change/confirm", h.confirmEmailChange)
	mux.HandleFunc("POST /api/private/auth/email-change/confirm", h.confirmEmailChange)
	mux.HandleFunc("POST /api/private/setup", h.setup)
}

func (h *handler) authConfig(w http.ResponseWriter, r *http.Request) {
	if h.deps.Auth == nil {
		unavailable(w, "authentication")
		return
	}
	setupRequired, err := h.deps.Auth.SetupRequired(r.Context())
	if err != nil {
		serviceError(w, err)
		return
	}
	mode := "invite"
	if h.deps.Signup == "open" {
		mode = "open"
	}
	homepageURL := h.deps.HomepageURL
	if homepageURL == "" {
		homepageURL = "https://supermetry.com"
	}
	writeJSON(w, http.StatusOK, struct {
		SetupRequired bool   `json:"setup_required"`
		Signup        string `json:"signup"`
		HomepageURL   string `json:"homepage_url"`
	}{SetupRequired: setupRequired, Signup: mode, HomepageURL: homepageURL})
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
	token, err := h.deps.Auth.Login(h.clientContext(r), request.Email, request.Password, request.TOTPCode, request.DeviceLabel)
	if err != nil {
		if errors.Is(err, platformauth.ErrRateLimited) {
			w.Header().Set("Retry-After", "60")
			writeProblem(w, http.StatusTooManyRequests, "Rate limit exceeded", "retry after the indicated delay")
			return
		}
		writeProblem(w, http.StatusUnauthorized, "Unauthorized", "invalid email, password, or verification code")
		return
	}
	h.setAuthCookies(w, r, token, csrfToken)
	writeJSON(w, http.StatusOK, map[string]bool{"authenticated": true})
}

func (h *handler) signup(w http.ResponseWriter, r *http.Request) {
	if h.deps.Signup != "open" {
		writeProblem(w, http.StatusNotFound, "Not found", "resource not found")
		return
	}
	if h.deps.Auth == nil {
		unavailable(w, "authentication")
		return
	}
	var request struct {
		Email    string `json:"email"`
		Name     string `json:"name"`
		Password string `json:"password"`
		OrgName  string `json:"org_name"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	csrfToken, err := randomCookieToken()
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "Internal server error", "the request could not be completed")
		return
	}
	token, err := h.deps.Auth.Signup(h.clientContext(r), request.Email, request.Name, request.Password, request.OrgName, "")
	if err != nil {
		switch {
		case errors.Is(err, platformauth.ErrRateLimited):
			w.Header().Set("Retry-After", "60")
			writeProblem(w, http.StatusTooManyRequests, "Rate limit exceeded", "retry after the indicated delay")
		case errors.Is(err, platformauth.ErrInvalid):
			writeProblem(w, http.StatusBadRequest, "Bad request", "signup details are invalid")
		default:
			writeProblem(w, http.StatusInternalServerError, "Internal server error", "the request could not be completed")
		}
		return
	}
	h.setAuthCookies(w, r, token, csrfToken)
	writeJSON(w, http.StatusCreated, map[string]bool{"authenticated": true})
}

func (h *handler) clientContext(r *http.Request) context.Context {
	client := h.clientAddress(r)
	hash := sha256.Sum256([]byte(client))
	ctx := platformauth.WithClientKey(r.Context(), hex.EncodeToString(hash[:]))
	return platformauth.WithSessionMetadata(ctx, h.sessionMetadata(r))
}

// Capture only account sign-in metadata. Resolve the nearest untrusted hop,
// including private clients, rather than accepting arbitrary forwarded headers.
func (h *handler) sessionMetadata(r *http.Request) platformauth.SessionMetadata {
	var metadata platformauth.SessionMetadata
	if ip, ok := clientip.ResolveAccessClient(r, h.deps.TrustedProxies); ok {
		metadata.IPAddress = ip.String()
		if ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && h.deps.SessionCountry != nil {
			metadata.CountryCode = h.deps.SessionCountry(ip)
		}
	}
	// Do not retain the raw header. Oversized values are unknown, not truncated
	// into a potentially misleading browser identity.
	if ua := r.UserAgent(); len(ua) <= 1024 {
		parsed := useragent.Parse(ua)
		if !parsed.Bot {
			// The parser's fallback Name may contain raw header text. Persist only
			// recognized labels, never that fallback or an arbitrary product token.
			switch name := strings.TrimPrefix(parsed.Name, "Mobile "); name {
			case "Chrome", "Firefox", "Safari", "Edge", "Internet Explorer", "Opera", "Opera Mini", "Opera Touch", "Vivaldi", "Samsung Browser", "Android Browser", "Facebook App", "Instagram App", "TikTok", "Huawei Browser", "Miui Browser":
				metadata.Browser = name
			}
		}
	}
	return metadata
}

func (h *handler) clientAddress(r *http.Request) string {
	if addr, ok := clientip.Resolve(r, h.deps.TrustedProxies); ok {
		return addr.String()
	}
	return r.RemoteAddr
}

func (h *handler) setAuthCookies(w http.ResponseWriter, r *http.Request, token, csrfToken string) {
	h.setCookie(w, r, platformauth.CookieName, token, 90*24*time.Hour)
	http.SetCookie(w, &http.Cookie{Name: "supermetry_csrf", Value: csrfToken, Path: "/", Secure: h.cookieSecure(r), SameSite: http.SameSiteLaxMode})
}

func (h *handler) logout(w http.ResponseWriter, r *http.Request) {
	// The router enforces CSRF. Delete only the presented session directly:
	// authentication lookups collapse database errors into unauthenticated, which
	// cannot establish that logout succeeded. DELETE is idempotent for expired
	// and already-revoked sessions and propagates operational failures.
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
	h.clearCookie(w, r, platformauth.CookieName, true)
	h.clearCookie(w, r, "supermetry_csrf", false)
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

// secureRequest reports whether the client reached us over HTTPS, directly or
// through a TLS-terminating proxy. Cookies are marked Secure only then, so plain
// http (LAN, development) still works; a spoofed header can only make a cookie
// stricter, never expose it.
func secureRequest(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// cookieSecure is always true for https deployments (fail closed) and, for
// plain-http deployments such as LAN or development, follows the request.
func (h *handler) cookieSecure(r *http.Request) bool {
	return h.deps.SecureCookies || secureRequest(r)
}

func (h *handler) setCookie(w http.ResponseWriter, r *http.Request, name, value string, maxAge time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: value, Path: "/", MaxAge: int(maxAge.Seconds()),
		HttpOnly: true, Secure: h.cookieSecure(r), SameSite: http.SameSiteLaxMode,
	})
}

func (h *handler) clearCookie(w http.ResponseWriter, r *http.Request, name string, httpOnly bool) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Path: "/", MaxAge: -1, Expires: time.Unix(1, 0),
		HttpOnly: httpOnly, Secure: h.cookieSecure(r), SameSite: http.SameSiteLaxMode,
	})
}

func randomCookieToken() (string, error) {
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value[:]), nil
}
