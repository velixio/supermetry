package platformhttp

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	platformauth "supermetry/internal/platform/auth"
	"supermetry/internal/platform/store"
)

func (h *handler) updateMe(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.privatePrincipal(w, r)
	if !ok {
		return
	}
	var request struct {
		Name        *string `json:"name"`
		Preferences *struct {
			LandingPage *string         `json:"landing_page"`
			SitesView   *string         `json:"sites_view"`
			LastSiteID  json.RawMessage `json:"last_site_id"`
			Locale      *string         `json:"locale"`
		} `json:"preferences"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if h.deps.Store == nil {
		unavailable(w, "authentication")
		return
	}
	user, err := h.deps.Store.UserByID(r.Context(), principal.UserID)
	if err != nil {
		serviceError(w, err)
		return
	}
	var name *string
	if request.Name != nil {
		trimmed := strings.TrimSpace(*request.Name)
		if trimmed == "" {
			writeProblem(w, http.StatusBadRequest, "Bad request", "name is required")
			return
		}
		name = &trimmed
	}
	var preferences *store.UserPreferences
	if request.Preferences != nil {
		merged := user.Preferences
		if request.Preferences.LandingPage != nil {
			if *request.Preferences.LandingPage != "overview" && *request.Preferences.LandingPage != "last_site" {
				writeProblem(w, http.StatusBadRequest, "Bad request", "landing_page must be overview or last_site")
				return
			}
			merged.LandingPage = *request.Preferences.LandingPage
		}
		if request.Preferences.SitesView != nil {
			if *request.Preferences.SitesView != "cards" && *request.Preferences.SitesView != "table" {
				writeProblem(w, http.StatusBadRequest, "Bad request", "sites_view must be cards or table")
				return
			}
			merged.SitesView = *request.Preferences.SitesView
		}
		if request.Preferences.Locale != nil {
			if !store.SupportedLocales[*request.Preferences.Locale] {
				writeProblem(w, http.StatusBadRequest, "Bad request", "locale must be empty, en, es, de or en-XA")
				return
			}
			merged.Locale = *request.Preferences.Locale
		}
		if request.Preferences.LastSiteID != nil {
			if string(request.Preferences.LastSiteID) == "null" {
				merged.LastSiteID = nil
			} else {
				var siteID int32
				if err := json.Unmarshal(request.Preferences.LastSiteID, &siteID); err != nil || siteID <= 0 || !h.userCanReadSite(r, principal.UserID, siteID) {
					writeProblem(w, http.StatusBadRequest, "Bad request", "last_site_id must name a readable site")
					return
				}
				merged.LastSiteID = &siteID
			}
		}
		preferences = &merged
	}
	if name == nil && preferences == nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", "name or preferences is required")
		return
	}
	if err := h.deps.Store.UpdateUserProfile(r.Context(), principal.UserID, name, preferences); err != nil {
		serviceError(w, err)
		return
	}
	user, err = h.deps.Store.UserByID(r.Context(), principal.UserID)
	if err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": user.Name, "email": user.Email, "preferences": effectiveUserPreferences(user.Preferences, h.readableSiteCount(r, principal.UserID))})
}

func (h *handler) userCanReadSite(r *http.Request, userID int64, siteID int32) bool {
	var allowed bool
	err := h.deps.Store.Pool.QueryRow(r.Context(), `
		select exists(
		  select 1 from memberships m join orgs o on o.id=m.org_id and o.deleted_at is null
		  join sites s on s.org_id=m.org_id and s.deleted_at is null
		  where m.user_id=$1 and s.id=$2 and (m.site_ids is null or s.id=any(m.site_ids))
		)`, userID, siteID).Scan(&allowed)
	return err == nil && allowed
}

func (h *handler) readableSiteCount(r *http.Request, userID int64) int {
	var count int
	_ = h.deps.Store.Pool.QueryRow(r.Context(), `
		select count(*) from memberships m join orgs o on o.id=m.org_id and o.deleted_at is null
		join sites s on s.org_id=m.org_id and s.deleted_at is null
		where m.user_id=$1 and (m.site_ids is null or s.id=any(m.site_ids))`, userID).Scan(&count)
	return count
}

func (h *handler) startEmailChange(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.privatePrincipal(w, r)
	if !ok {
		return
	}
	var request struct {
		NewEmail string `json:"new_email"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if h.deps.Auth == nil {
		unavailable(w, "authentication")
		return
	}
	err := h.deps.Auth.StartEmailChange(h.clientContext(r), principal.UserID, request.NewEmail, request.Password)
	switch {
	case errors.Is(err, platformauth.ErrRateLimited):
		rateLimited(w)
	case errors.Is(err, platformauth.ErrSMTPRequired):
		writeProblem(w, http.StatusConflict, "Email unavailable", "email change requires SMTP to be configured")
	case errors.Is(err, platformauth.ErrInvalid):
		writeProblem(w, http.StatusUnauthorized, "Unauthorized", "password or email address is invalid")
	case err != nil:
		serviceError(w, err)
	default:
		w.WriteHeader(http.StatusAccepted)
	}
}

// confirmEmailChange applies an emailed email-change token. It requires the
// session of the account that requested the change; a GET without one is sent
// to the login page and returns here afterwards.
func (h *handler) confirmEmailChange(w http.ResponseWriter, r *http.Request) {
	if h.deps.Auth == nil || h.deps.Authenticator == nil {
		unavailable(w, "authentication")
		return
	}
	principal, authenticated := h.deps.Authenticator.Authenticate(r)
	if r.Method == http.MethodGet {
		if !authenticated || principal.UserID == 0 {
			http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
			return
		}
		token := strings.TrimSpace(r.URL.Query().Get("token"))
		if err := h.deps.Auth.ConfirmEmailChange(r.Context(), principal.UserID, token); err != nil {
			http.Redirect(w, r, "/account/profile?email_changed=0", http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/account/profile?email_changed=1", http.StatusSeeOther)
		return
	}
	if !authenticated || principal.UserID == 0 {
		writeProblem(w, http.StatusUnauthorized, "Unauthorized", "valid authentication is required")
		return
	}
	var request struct {
		Token string `json:"token"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if err := h.deps.Auth.ConfirmEmailChange(r.Context(), principal.UserID, strings.TrimSpace(request.Token)); err != nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", "the email confirmation token is invalid or expired")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) changePassword(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.privatePrincipal(w, r)
	if !ok {
		return
	}
	var request struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if h.deps.Auth == nil {
		unavailable(w, "authentication")
		return
	}
	cookie, err := r.Cookie(platformauth.CookieName)
	if err != nil {
		writeProblem(w, http.StatusUnauthorized, "Unauthorized", "current session is required")
		return
	}
	if err := h.deps.Auth.ChangePassword(h.clientContext(r), principal.UserID, request.CurrentPassword, request.NewPassword, cookie.Value); err != nil {
		if errors.Is(err, platformauth.ErrRateLimited) {
			rateLimited(w)
		} else if errors.Is(err, platformauth.ErrInvalid) {
			writeProblem(w, http.StatusUnauthorized, "Unauthorized", "current password is invalid")
		} else if !writePasswordPolicyProblem(w, err) {
			serviceError(w, err)
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) deleteAccount(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.privatePrincipal(w, r)
	if !ok {
		return
	}
	var request struct {
		Password string `json:"password"`
		TOTPCode string `json:"totp_code,omitempty"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if h.deps.Auth == nil {
		unavailable(w, "authentication")
		return
	}
	err := h.deps.Auth.DeleteAccount(h.clientContext(r), principal.UserID, request.Password, request.TOTPCode)
	switch {
	case errors.Is(err, platformauth.ErrRateLimited):
		rateLimited(w)
	case errors.Is(err, platformauth.ErrLastOwner):
		writeProblem(w, http.StatusConflict, "Account cannot be deleted", "transfer ownership or delete the organization first")
	case errors.Is(err, platformauth.ErrTOTPRequired):
		writeProblem(w, http.StatusBadRequest, "Verification required", "a TOTP code is required")
	case errors.Is(err, platformauth.ErrInvalid):
		writeProblem(w, http.StatusUnauthorized, "Unauthorized", "password or verification code is invalid")
	case err != nil:
		serviceError(w, err)
	default:
		h.clearCookie(w, r, platformauth.CookieName, true)
		h.clearCookie(w, r, "supermetry_csrf", false)
		w.WriteHeader(http.StatusNoContent)
	}
}

func rateLimited(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "60")
	writeProblem(w, http.StatusTooManyRequests, "Rate limit exceeded", "retry after the indicated delay")
}
