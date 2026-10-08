// Package platformhttp serves the application-platform HTTP API.
package platformhttp

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"panel/internal/api"
	"panel/internal/imports"
	"panel/internal/platform/audit"
	platformauth "panel/internal/platform/auth"
	"panel/internal/platform/authz"
	"panel/internal/platform/invites"
	"panel/internal/platform/keys"
	"panel/internal/platform/orgs"
	"panel/internal/platform/reports"
	"panel/internal/platform/shares"
	"panel/internal/platform/sitesvc"
	"panel/internal/platform/store"
	coresites "panel/internal/sites"
)

const maxBodyBytes = 1 << 20

// Authenticator resolves a request to the complete platform principal used by
// authz.Can and by transactional audit writes.
type Authenticator interface {
	Authenticate(*http.Request) (authz.Principal, bool)
}

// MutationService contains the write operations exposed by this task. The
// concrete Services adapter delegates to the platform services, while tests
// can supply a small fake without bypassing route authorization.
type MutationService interface {
	CreateSite(context.Context, authz.Principal, CreateSiteRequest) (coresites.Site, error)
	UpdateSite(context.Context, authz.Principal, int32, sitesvc.SitePatch) (coresites.Site, error)
	DeleteSite(context.Context, authz.Principal, int32) error
	CreateAPIKey(context.Context, authz.Principal, CreateAPIKeyRequest) (CreatedAPIKey, error)
	DeleteOrg(context.Context, authz.Principal, int64) error
}

type siteIconMutationService interface {
	RefreshSiteIcon(context.Context, authz.Principal, int32) (coresites.Site, error)
}

type Deps struct {
	// SecureCookies forces the Secure flag on every cookie. Set it when
	// PANEL_BASE_URL is https so production never downgrades, even behind a
	// proxy that omits X-Forwarded-Proto.
	SecureCookies bool
	// Snippet returns the tracking snippet (absolute script and event URLs built
	// from PANEL_BASE_URL) for a site's public ID.
	Snippet        func(publicID string, full bool) string
	Signup         string
	TrustedProxies []netip.Prefix
	Authenticator  Authenticator
	Sites          coresites.Registry
	Mutations      MutationService
	Store          *store.Store
	Definitions    interface{ Invalidate() error }
	Auth           *platformauth.Auth
	Orgs           *orgs.Orgs
	Keys           *keys.Keys
	Invites        *invites.Invites
	Shares         *shares.Shares
	Reports        *reports.Reports
	Alerts         AlertsService
	Query          interface {
		Query(context.Context, api.QueryRequest) (api.QueryResponse, error)
	}
	Imports interface {
		Create(context.Context, imports.CreateInput) (imports.Import, error)
		Get(context.Context, int64) (imports.Import, error)
		List(context.Context, int32) ([]imports.Import, error)
		Delete(context.Context, int64) (bool, error)
		SetFailed(context.Context, int64, error) error
	}
	ImportQueue interface {
		Queue(imports.Job) error
	}
	Exporter interface {
		Write(context.Context, io.Writer, int32, time.Time, time.Time) error
	}
}

// Services adapts the services built by Tasks 104 and 105 to MutationService.
type Services struct {
	Sites *sitesvc.Sites
	Keys  *keys.Keys
	Store *store.Store
}

type CreateSiteRequest struct {
	Name          string   `json:"name"`
	Hosts         []string `json:"hosts"`
	Timezone      string   `json:"timezone"`
	RetentionDays int      `json:"retention_days"`
}

type CreateAPIKeyRequest struct {
	Name      string     `json:"name"`
	Scopes    []string   `json:"scopes"`
	SiteIDs   []int32    `json:"site_ids,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

type CreatedAPIKey struct {
	ID  int64  `json:"id"`
	Key string `json:"key"`
}

type handler struct {
	deps          Deps
	iconRefetchMu sync.Mutex
	iconRefetched map[int32]time.Time
}

// New returns the platform route set. It owns platform authentication so the
// complete authz.Principal is available; the analytics router may mount this
// handler as a fallback under its public and private route trees.
func New(deps Deps) http.Handler {
	h := &handler{deps: deps, iconRefetched: make(map[int32]time.Time)}
	mux := http.NewServeMux()
	h.registerPrivate(mux)
	h.registerAuth(mux)
	h.registerPublic(mux)
	return csrf(mux)
}

func (h *handler) registerPrivate(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/private/sites", h.listSites)
	mux.HandleFunc("POST /api/private/sites", h.createSite)
	mux.HandleFunc("GET /api/private/sites/{site}", h.getSite)
	mux.HandleFunc("GET /api/private/sites/{site}/snippet", h.siteSnippet)
	mux.HandleFunc("POST /api/private/sites/{site}/icon/refetch", h.refetchSiteIcon)
	mux.HandleFunc("PATCH /api/private/sites/{site}", h.updateSite)
	mux.HandleFunc("DELETE /api/private/sites/{site}", h.deleteSite)
	mux.HandleFunc("POST /api/private/orgs/{org}/api-keys", h.createAPIKey)
	mux.HandleFunc("GET /api/private/orgs/{org}/audit", h.listAudit)
	mux.HandleFunc("DELETE /api/private/orgs/{org}", h.deleteOrg)
	mux.HandleFunc("GET /api/private/share/{slug}/stats/summary", h.shareStats)
	mux.HandleFunc("GET /api/private/sites/{site}/goals", h.listGoals)
	mux.HandleFunc("POST /api/private/sites/{site}/goals", h.createGoal)
	mux.HandleFunc("PATCH /api/private/goals/{goal}", h.updateGoal)
	mux.HandleFunc("DELETE /api/private/goals/{goal}", h.deleteGoal)
	mux.HandleFunc("GET /api/private/sites/{site}/annotations", h.listAnnotations)
	mux.HandleFunc("POST /api/private/sites/{site}/annotations", h.createAnnotation)
	mux.HandleFunc("PATCH /api/private/annotations/{annotation}", h.updateAnnotation)
	mux.HandleFunc("DELETE /api/private/annotations/{annotation}", h.deleteAnnotation)
	mux.HandleFunc("GET /api/private/sites/{site}/funnels", h.listFunnels)
	mux.HandleFunc("POST /api/private/sites/{site}/funnels", h.createFunnel)
	mux.HandleFunc("PATCH /api/private/funnels/{funnel}", h.updateFunnel)
	mux.HandleFunc("DELETE /api/private/funnels/{funnel}", h.deleteFunnel)
	mux.HandleFunc("GET /api/private/sites/{site}/segments", h.listSegments)
	mux.HandleFunc("POST /api/private/sites/{site}/segments", h.createSegment)
	mux.HandleFunc("PATCH /api/private/segments/{segment}", h.updateSegment)
	mux.HandleFunc("DELETE /api/private/segments/{segment}", h.deleteSegment)
	mux.HandleFunc("POST /api/private/sites/{site}/imports", h.createImport)
	mux.HandleFunc("GET /api/private/sites/{site}/imports", h.listImports)
	mux.HandleFunc("DELETE /api/private/imports/{import}", h.deleteImport)
	mux.HandleFunc("GET /api/private/sites/{site}/export", h.exportSite)
	mux.HandleFunc("GET /api/private/sites/{site}/profiles", h.listProfiles)
	mux.HandleFunc("GET /api/private/profiles/{profile}", h.getProfile)
	mux.HandleFunc("DELETE /api/private/profiles/{profile}", h.deleteProfile)
	mux.HandleFunc("GET /api/private/me", h.me)
	mux.HandleFunc("GET /api/private/me/ip", h.currentIP)
	mux.HandleFunc("GET /api/private/me/sessions", h.listSessions)
	mux.HandleFunc("DELETE /api/private/me/sessions/{session}", h.revokeSession)
	mux.HandleFunc("POST /api/private/me/totp", h.enrollTOTP)
	mux.HandleFunc("PATCH /api/private/me/totp", h.confirmTOTP)
	mux.HandleFunc("GET /api/private/orgs", h.listOrgs)
	mux.HandleFunc("GET /api/private/orgs/{org}", h.getOrg)
	mux.HandleFunc("GET /api/private/orgs/{org}/members", h.listMembers)
	mux.HandleFunc("PATCH /api/private/orgs/{org}/members/{user}", h.updateMember)
	mux.HandleFunc("DELETE /api/private/orgs/{org}/members/{user}", h.deleteMember)
	mux.HandleFunc("POST /api/private/orgs/{org}/invitations", h.createInvitation)
	mux.HandleFunc("GET /api/private/orgs/{org}/invitations", h.listInvitations)
	mux.HandleFunc("DELETE /api/private/orgs/{org}/invitations/{invitation}", h.revokeInvitation)
	mux.HandleFunc("GET /api/private/orgs/{org}/api-keys", h.listAPIKeys)
	mux.HandleFunc("DELETE /api/private/orgs/{org}/api-keys/{key}", h.revokeAPIKey)
	mux.HandleFunc("POST /api/private/sites/{site}/shares", h.createShare)
	mux.HandleFunc("GET /api/private/sites/{site}/shares", h.listShares)
	mux.HandleFunc("DELETE /api/private/sites/{site}/shares/{share}", h.revokeShare)
	mux.HandleFunc("POST /api/private/sites/{site}/reports", h.subscribeReport)
	mux.HandleFunc("GET /api/private/sites/{site}/reports", h.listReports)
	mux.HandleFunc("DELETE /api/private/sites/{site}/reports/{report}", h.deleteReport)
	mux.HandleFunc("GET /api/private/orgs/{org}/channels", h.listChannels)
	mux.HandleFunc("POST /api/private/orgs/{org}/channels", h.createChannel)
	mux.HandleFunc("PATCH /api/private/orgs/{org}/channels/{channel}", h.updateChannel)
	mux.HandleFunc("DELETE /api/private/orgs/{org}/channels/{channel}", h.deleteChannel)
	mux.HandleFunc("POST /api/private/orgs/{org}/channels/{channel}/test", h.testChannel)
	mux.HandleFunc("GET /api/private/sites/{site}/alerts", h.listAlerts)
	mux.HandleFunc("POST /api/private/sites/{site}/alerts", h.createAlert)
	mux.HandleFunc("PATCH /api/private/sites/{site}/alerts/{alert}", h.updateAlert)
	mux.HandleFunc("DELETE /api/private/sites/{site}/alerts/{alert}", h.deleteAlert)
	mux.HandleFunc("POST /api/private/invitations/{token}/accept", h.acceptInvitation)
	mux.HandleFunc("POST /api/private/share/{slug}/open", h.openShare)
}

func (s Services) CreateSite(ctx context.Context, principal authz.Principal, request CreateSiteRequest) (coresites.Site, error) {
	if s.Sites == nil {
		return coresites.Site{}, errors.New("site service is unavailable")
	}
	return s.Sites.Create(ctx, principal, request.Name, request.Hosts, request.Timezone, request.RetentionDays)
}

func (s Services) UpdateSite(ctx context.Context, principal authz.Principal, id int32, patch sitesvc.SitePatch) (coresites.Site, error) {
	if s.Sites == nil {
		return coresites.Site{}, errors.New("site service is unavailable")
	}
	return s.Sites.Update(ctx, principal, id, patch)
}

func (s Services) RefreshSiteIcon(ctx context.Context, principal authz.Principal, id int32) (coresites.Site, error) {
	if s.Sites == nil {
		return coresites.Site{}, errors.New("site service is unavailable")
	}
	return s.Sites.RefreshIcon(ctx, principal, id)
}

func (s Services) DeleteSite(ctx context.Context, principal authz.Principal, id int32) error {
	if s.Sites == nil {
		return errors.New("site service is unavailable")
	}
	return s.Sites.Delete(ctx, principal, id)
}

func (s Services) CreateAPIKey(ctx context.Context, principal authz.Principal, request CreateAPIKeyRequest) (CreatedAPIKey, error) {
	if s.Keys == nil {
		return CreatedAPIKey{}, errors.New("API key service is unavailable")
	}
	plain, info, err := s.Keys.Create(ctx, principal, request.Name, request.Scopes, request.SiteIDs, request.ExpiresAt)
	return CreatedAPIKey{ID: info.ID, Key: plain}, err
}

func (s Services) DeleteOrg(ctx context.Context, principal authz.Principal, id int64) error {
	if s.Store == nil {
		return errors.New("organization service is unavailable")
	}
	return s.Store.Tx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `update orgs set deleted_at=now() where id=$1 and id=$2 and deleted_at is null`, id, principal.OrgID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return sitesvc.ErrNotFound
		}
		return audit.Write(ctx, tx, principal, "organization.deleted", "organization", id, nil)
	})
}

func (h *handler) principal(w http.ResponseWriter, r *http.Request, allowedRoles ...authz.Role) (authz.Principal, bool) {
	if h.deps.Authenticator == nil {
		writeProblem(w, http.StatusUnauthorized, "Unauthorized", "valid authentication is required")
		return authz.Principal{}, false
	}
	principal, ok := h.deps.Authenticator.Authenticate(r)
	if !ok {
		writeProblem(w, http.StatusUnauthorized, "Unauthorized", "valid authentication is required")
		return authz.Principal{}, false
	}
	if len(allowedRoles) == 0 || roleAllowed(principal.Role, allowedRoles) {
		return principal, true
	}
	writeProblem(w, http.StatusForbidden, "Forbidden", "the credential cannot use this route")
	return authz.Principal{}, false
}

func (h *handler) privatePrincipal(w http.ResponseWriter, r *http.Request) (authz.Principal, bool) {
	return h.principal(w, r, authz.RoleOwner, authz.RoleAdmin, authz.RoleEditor, authz.RoleViewer)
}

func (h *handler) resourcePrincipal(w http.ResponseWriter, r *http.Request) (authz.Principal, bool) {
	if strings.HasPrefix(r.URL.Path, "/api/public/v1/") {
		return h.principal(w, r, authz.RoleKey)
	}
	return h.privatePrincipal(w, r)
}

func (h *handler) sitePrincipal(w http.ResponseWriter, r *http.Request, action authz.Action, roles ...authz.Role) (authz.Principal, coresites.Site, bool) {
	principal, ok := h.principal(w, r)
	if !ok {
		return authz.Principal{}, coresites.Site{}, false
	}
	siteID, err := parseInt32(r.PathValue("site"))
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", "site must be a positive integer")
		return authz.Principal{}, coresites.Site{}, false
	}
	principal, site, ok := h.visibleSite(w, principal, siteID)
	if !ok {
		return authz.Principal{}, coresites.Site{}, false
	}
	if !roleAllowed(principal.Role, roles) || !authz.Can(principal, action, siteID) {
		writeProblem(w, http.StatusForbidden, "Forbidden", "the principal cannot perform this action")
		return authz.Principal{}, coresites.Site{}, false
	}
	return principal, site, true
}

func (h *handler) authorizeSite(w http.ResponseWriter, principal authz.Principal, siteID int32, action authz.Action) (authz.Principal, coresites.Site, bool) {
	principal, site, ok := h.visibleSite(w, principal, siteID)
	if !ok {
		return authz.Principal{}, coresites.Site{}, false
	}
	if !authz.Can(principal, action, siteID) {
		writeProblem(w, http.StatusForbidden, "Forbidden", "the principal cannot perform this action")
		return authz.Principal{}, coresites.Site{}, false
	}
	return principal, site, true
}

func (h *handler) visibleSite(w http.ResponseWriter, principal authz.Principal, siteID int32) (authz.Principal, coresites.Site, bool) {
	if h.deps.Sites == nil {
		writeProblem(w, http.StatusNotFound, "Not found", "site not found")
		return authz.Principal{}, coresites.Site{}, false
	}
	site, found := h.deps.Sites.Site(siteID)
	if !found || site.OrgID != principal.OrgID || !allowsSite(principal.SiteIDs, siteID) {
		writeProblem(w, http.StatusNotFound, "Not found", "site not found")
		return authz.Principal{}, coresites.Site{}, false
	}
	return principal, site, true
}

func roleAllowed(role authz.Role, allowed []authz.Role) bool {
	for _, candidate := range allowed {
		if role == candidate {
			return true
		}
	}
	return false
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	reader := http.MaxBytesReader(w, r.Body, maxBodyBytes)
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", "invalid JSON request body")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeProblem(w, http.StatusBadRequest, "Bad request", "request body must contain one JSON value")
		return false
	}
	return true
}

func parseInt32(value string) (int32, error) {
	parsed, err := strconv.ParseInt(value, 10, 32)
	if err != nil || parsed <= 0 {
		return 0, errors.New("invalid positive integer")
	}
	return int32(parsed), nil
}

func parseInt64(value string) (int64, error) {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return 0, errors.New("invalid positive integer")
	}
	return parsed, nil
}

func allowsSite(siteIDs []int32, siteID int32) bool {
	if siteIDs == nil {
		return true
	}
	for _, allowed := range siteIDs {
		if allowed == siteID {
			return true
		}
	}
	return false
}

func csrf(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/private/") || r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions || csrfExempt(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		header := r.Header.Get("X-CSRF-Token")
		cookie, err := r.Cookie("panel_csrf")
		if err != nil || header == "" || cookie.Value == "" || subtle.ConstantTimeCompare([]byte(header), []byte(cookie.Value)) != 1 {
			writeProblem(w, http.StatusForbidden, "Forbidden", "CSRF token is missing or invalid")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func csrfExempt(path string) bool {
	switch path {
	case "/api/private/auth/login", "/api/private/auth/signup", "/api/private/auth/password-reset", "/api/private/auth/password-reset/confirm", "/api/private/setup":
		return true
	default:
		return strings.HasPrefix(path, "/api/private/invitations/") && strings.HasSuffix(path, "/accept") ||
			strings.HasPrefix(path, "/api/private/share/") && strings.HasSuffix(path, "/open")
	}
}

type problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail"`
}

func writeProblem(w http.ResponseWriter, status int, title, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(problem{
		Type:  "https://panel.dev/problems/" + strings.ReplaceAll(strings.ToLower(title), " ", "-"),
		Title: title, Status: status, Detail: detail,
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func serviceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, sitesvc.ErrNotFound), errors.Is(err, keys.ErrNotFound),
		errors.Is(err, orgs.ErrNotFound), errors.Is(err, invites.ErrNotFound),
		errors.Is(err, shares.ErrNotFound), errors.Is(err, reports.ErrSiteNotFound):
		writeProblem(w, http.StatusNotFound, "Not found", "resource not found")
	case errors.Is(err, sitesvc.ErrForbidden), errors.Is(err, keys.ErrForbidden),
		errors.Is(err, orgs.ErrForbidden), errors.Is(err, invites.ErrForbidden),
		errors.Is(err, shares.ErrForbidden), errors.Is(err, reports.ErrForbidden):
		writeProblem(w, http.StatusForbidden, "Forbidden", "the principal cannot perform this action")
	case errors.Is(err, sitesvc.ErrInvalid), errors.Is(err, orgs.ErrInvalidRole),
		errors.Is(err, orgs.ErrLastOwner), errors.Is(err, invites.ErrInvalid),
		errors.Is(err, shares.ErrInvalid), errors.Is(err, shares.ErrPasswordRequired),
		errors.Is(err, reports.ErrInvalidCadence), errors.Is(err, platformauth.ErrInvalid):
		writeProblem(w, http.StatusBadRequest, "Bad request", err.Error())
	default:
		writeProblem(w, http.StatusInternalServerError, "Internal server error", "the request could not be completed")
	}
}

func orgIDFromPath(w http.ResponseWriter, r *http.Request, principal authz.Principal) (int64, bool) {
	orgID, err := parseInt64(r.PathValue("org"))
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", "organization must be a positive integer")
		return 0, false
	}
	if orgID != principal.OrgID {
		writeProblem(w, http.StatusNotFound, "Not found", "organization not found")
		return 0, false
	}
	return orgID, true
}

func unavailable(w http.ResponseWriter, resource string) {
	writeProblem(w, http.StatusInternalServerError, "Internal server error", fmt.Sprintf("%s service is unavailable", resource))
}
