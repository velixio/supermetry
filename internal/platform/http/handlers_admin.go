package platformhttp

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"supermetry/internal/platform/admin"
	"supermetry/internal/platform/authz"
	"supermetry/internal/platform/store"
)

const adminRequestsPerMinute = 120

type adminRateWindow struct {
	start time.Time
	count int
}

// MinAdminAPIKeyLength is the shortest SUPERMETRY_ADMIN_API_KEY that may
// authenticate the instance admin routes. Shorter keys leave key auth off.
const MinAdminAPIKeyLength = 32

// adminKeyEnabled reports whether the admin API key may authenticate the
// instance admin routes.
func (h *handler) adminKeyEnabled() bool {
	return len(h.deps.AdminAPIKey) >= MinAdminAPIKeyLength && h.deps.AdminServiceUserID != 0 && h.deps.Admin != nil
}

func (h *handler) adminPrincipal(w http.ResponseWriter, r *http.Request) (authz.Principal, bool) {
	// The admin API key is accepted here only: adminPrincipal guards
	// /api/private/admin/* and nothing else. A bearer credential on these
	// routes must be the key; it never falls back to the session cookie.
	// Other schemes (e.g. Basic from a reverse proxy) fall through to the cookie path.
	if authorization := r.Header.Get("Authorization"); bearerScheme(authorization) {
		if !h.adminKeyEnabled() || !validAdminBearer(authorization, h.deps.AdminAPIKey) {
			writeProblem(w, http.StatusUnauthorized, "Unauthorized", "valid authentication is required")
			return authz.Principal{}, false
		}
		if !h.allowAdminRequest(h.deps.AdminServiceUserID) {
			w.Header().Set("Retry-After", "60")
			writeProblem(w, http.StatusTooManyRequests, "Rate limit exceeded", "retry after the indicated delay")
			return authz.Principal{}, false
		}
		return authz.Principal{UserID: h.deps.AdminServiceUserID}, true
	}
	principal, ok := h.privatePrincipal(w, r)
	if !ok {
		return authz.Principal{}, false
	}
	if h.deps.Admin == nil || principal.UserID == 0 {
		writeProblem(w, http.StatusForbidden, "Forbidden", "instance administration is required")
		return authz.Principal{}, false
	}
	allowed, err := h.deps.Admin.IsInstanceAdmin(r.Context(), principal.UserID)
	if err != nil {
		serviceError(w, err)
		return authz.Principal{}, false
	}
	if !allowed {
		writeProblem(w, http.StatusForbidden, "Forbidden", "instance administration is required")
		return authz.Principal{}, false
	}
	if !h.allowAdminRequest(principal.UserID) {
		w.Header().Set("Retry-After", "60")
		writeProblem(w, http.StatusTooManyRequests, "Rate limit exceeded", "retry after the indicated delay")
		return authz.Principal{}, false
	}
	return principal, true
}

// bearerScheme reports whether an Authorization header uses the Bearer scheme,
// parsed the same way as validAdminBearer.
func bearerScheme(header string) bool {
	scheme, _, ok := strings.Cut(header, " ")
	return ok && strings.EqualFold(scheme, "Bearer")
}

func (h *handler) allowAdminRequest(userID int64) bool {
	now := h.deps.Now()
	h.adminLimitMu.Lock()
	defer h.adminLimitMu.Unlock()
	window := h.adminLimits[userID]
	if window.start.IsZero() || now.Sub(window.start) >= time.Minute {
		window = adminRateWindow{start: now}
	}
	if window.count >= adminRequestsPerMinute {
		h.adminLimits[userID] = window
		return false
	}
	window.count++
	h.adminLimits[userID] = window
	return true
}

func adminLimit(r *http.Request) (int, bool) {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return 50, true
	}
	limit, err := strconv.Atoi(raw)
	return limit, err == nil && limit >= 1 && limit <= admin.MaxListLimit
}

func adminID(r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	return id, err == nil && id > 0
}

func (h *handler) adminListUsers(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.adminPrincipal(w, r); !ok {
		return
	}
	limit, ok := adminLimit(r)
	if !ok || len(r.URL.Query().Get("search")) > 200 {
		writeProblem(w, http.StatusBadRequest, "Bad request", "search or limit is invalid")
		return
	}
	items, err := h.deps.Admin.ListUsers(r.Context(), r.URL.Query().Get("search"), limit)
	if err != nil {
		adminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items})
}

func (h *handler) adminUpdateUser(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.adminPrincipal(w, r)
	if !ok {
		return
	}
	userID, ok := adminID(r, "user")
	if !ok {
		writeProblem(w, http.StatusBadRequest, "Bad request", "user is invalid")
		return
	}
	var request struct {
		Disabled              *bool `json:"disabled"`
		PasswordResetRequired *bool `json:"password_reset_required"`
		InstanceAdmin         *bool `json:"instance_admin"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if err := h.deps.Admin.UpdateUser(r.Context(), principal.UserID, userID, admin.UserPatch(request)); err != nil {
		adminError(w, err)
		return
	}
	user, err := h.deps.Admin.User(r.Context(), userID)
	if err != nil {
		adminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, user)
}

func (h *handler) adminListOrganizations(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.adminPrincipal(w, r); !ok {
		return
	}
	limit, ok := adminLimit(r)
	if !ok || len(r.URL.Query().Get("search")) > 200 {
		writeProblem(w, http.StatusBadRequest, "Bad request", "search or limit is invalid")
		return
	}
	items, err := h.deps.Admin.ListOrganizations(r.Context(), r.URL.Query().Get("search"), limit, h.deps.MaxSitesPerOrg)
	if err != nil {
		adminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items})
}

func (h *handler) adminGetOrganization(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.adminPrincipal(w, r); !ok {
		return
	}
	orgID, ok := adminID(r, "org")
	if !ok {
		writeProblem(w, http.StatusBadRequest, "Bad request", "organization is invalid")
		return
	}
	item, err := h.deps.Admin.GetOrganization(r.Context(), orgID, h.deps.MaxSitesPerOrg)
	if err != nil {
		adminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

// adminOrgDeleter is implemented by Services; custom MutationService
// implementations opt in by providing it.
type adminOrgDeleter interface {
	AdminDeleteOrg(ctx context.Context, actorUserID int64, orgID int64) error
}

func (h *handler) adminDeleteOrganization(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.adminPrincipal(w, r)
	if !ok {
		return
	}
	orgID, ok := adminID(r, "org")
	if !ok {
		writeProblem(w, http.StatusBadRequest, "Bad request", "organization is invalid")
		return
	}
	deleter, ok := h.deps.Mutations.(adminOrgDeleter)
	if !ok {
		unavailable(w, "organization")
		return
	}
	if err := deleter.AdminDeleteOrg(r.Context(), principal.UserID, orgID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeProblem(w, http.StatusNotFound, "Not found", "resource not found")
			return
		}
		serviceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) adminListSites(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.adminPrincipal(w, r); !ok {
		return
	}
	limit, ok := adminLimit(r)
	if !ok || len(r.URL.Query().Get("search")) > 200 {
		writeProblem(w, http.StatusBadRequest, "Bad request", "search or limit is invalid")
		return
	}
	items, err := h.deps.Admin.ListSites(r.Context(), r.URL.Query().Get("search"), limit)
	if err != nil {
		adminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items})
}

func (h *handler) adminGetSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.adminPrincipal(w, r); !ok {
		return
	}
	settings, err := h.deps.Admin.EffectiveSettings(r.Context(), h.deps.Signup, h.deps.ReplayEnv.Enabled)
	if err != nil {
		adminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (h *handler) adminUpdateSettings(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.adminPrincipal(w, r)
	if !ok {
		return
	}
	var request struct {
		SignupMode    *string `json:"signup_mode"`
		ReplayVisible *bool   `json:"replay_visible"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if err := h.deps.Admin.UpdateSettings(r.Context(), principal.UserID, admin.SettingsPatch(request)); err != nil {
		adminError(w, err)
		return
	}
	settings, err := h.deps.Admin.EffectiveSettings(r.Context(), h.deps.Signup, h.deps.ReplayEnv.Enabled)
	if err != nil {
		adminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (h *handler) adminGetTwoFactorPolicy(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.adminPrincipal(w, r); !ok {
		return
	}
	policy, err := h.deps.Admin.TwoFactorPolicy(r.Context())
	if err != nil {
		adminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, policy)
}

func (h *handler) adminUpdateTwoFactorPolicy(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.adminPrincipal(w, r)
	if !ok {
		return
	}
	var policy admin.TwoFactorPolicy
	if !decodeJSON(w, r, &policy) {
		return
	}
	if err := h.deps.Admin.UpdateTwoFactorPolicy(r.Context(), principal.UserID, policy); err != nil {
		adminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, policy)
}

func (h *handler) adminSetTwoFactorExemption(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.adminPrincipal(w, r)
	if !ok {
		return
	}
	userID, ok := adminID(r, "user")
	if !ok {
		writeProblem(w, http.StatusBadRequest, "Bad request", "user is invalid")
		return
	}
	var request struct {
		Reason    string     `json:"reason"`
		ExpiresAt *time.Time `json:"expires_at,omitempty"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	request.Reason = strings.TrimSpace(request.Reason)
	if err := h.deps.Admin.SetExemption(r.Context(), principal.UserID, userID, request.Reason, request.ExpiresAt); err != nil {
		adminError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) adminDeleteTwoFactorExemption(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.adminPrincipal(w, r)
	if !ok {
		return
	}
	userID, ok := adminID(r, "user")
	if !ok {
		writeProblem(w, http.StatusBadRequest, "Bad request", "user is invalid")
		return
	}
	if err := h.deps.Admin.DeleteExemption(r.Context(), principal.UserID, userID); err != nil {
		adminError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) adminListAudit(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.adminPrincipal(w, r); !ok {
		return
	}
	limit, ok := adminLimit(r)
	if !ok {
		writeProblem(w, http.StatusBadRequest, "Bad request", "limit is invalid")
		return
	}
	items, err := h.deps.Admin.ListAudit(r.Context(), limit)
	if err != nil {
		adminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items})
}

func adminError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, admin.ErrForbidden):
		writeProblem(w, http.StatusForbidden, "Forbidden", "instance administration is required")
	case errors.Is(err, admin.ErrNotFound):
		writeProblem(w, http.StatusNotFound, "Not found", "resource not found")
	case errors.Is(err, admin.ErrUpdateActive):
		writeProblem(w, http.StatusConflict, "Update already active", err.Error())
	case errors.Is(err, admin.ErrInvalid), errors.Is(err, admin.ErrLastAdmin), errors.Is(err, admin.ErrWouldLockOut):
		writeProblem(w, http.StatusBadRequest, "Bad request", err.Error())
	default:
		serviceError(w, err)
	}
}
