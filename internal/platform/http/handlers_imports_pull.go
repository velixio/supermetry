package platformhttp

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"supermetry/internal/imports"
	"supermetry/internal/platform/authz"
)

// ImportPullService is the transport-facing subset of imports.PullService:
// importing aggregates straight from Plausible, Matomo or Google Analytics.
type ImportPullService interface {
	Capabilities() imports.Capabilities
	Create(ctx context.Context, siteID int32, orgID, userID int64, request imports.PullRequest) (imports.Import, error)
	Preview(ctx context.Context, siteID int32, orgID int64, request imports.PullRequest) (imports.PullPreview, error)
	StartGA4(ctx context.Context, userID, orgID int64, siteID int32) (string, error)
	CallbackGA4(ctx context.Context, userID, orgID int64, code, state, providerError string) (int32, error)
	ListGA4Connections(ctx context.Context, orgID int64) ([]imports.GA4Connection, error)
	DeleteGA4Connection(ctx context.Context, orgID, connectionID int64) error
	ListGA4Properties(ctx context.Context, orgID, connectionID int64) ([]imports.GA4Property, error)
}

type pullImportRequest struct {
	Method       string `json:"method"`
	BaseURL      string `json:"base_url"`
	RemoteSiteID string `json:"remote_site_id"`
	// APIKey is the Plausible API key; TokenAuth the Matomo token_auth.
	APIKey       string `json:"api_key"`
	TokenAuth    string `json:"token_auth"`
	Property     string `json:"property"`
	ConnectionID int64  `json:"connection_id"`
	From         string `json:"from"`
	To           string `json:"to"`
}

func (r pullImportRequest) toPull() imports.PullRequest {
	request := imports.PullRequest{
		Method: imports.PullMethod(r.Method), BaseURL: r.BaseURL, RemoteSiteID: r.RemoteSiteID,
		Property: r.Property, ConnectionID: r.ConnectionID, From: r.From, To: r.To,
	}
	switch request.Method {
	case imports.MethodPlausibleAPI:
		request.Secret = r.APIKey
	case imports.MethodMatomoAPI:
		request.Secret = r.TokenAuth
	}
	return request
}

func (h *handler) pullService(w http.ResponseWriter) (ImportPullService, bool) {
	if h.deps.ImportPulls == nil {
		unavailable(w, "import")
		return nil, false
	}
	return h.deps.ImportPulls, true
}

// importCapabilities tells the wizard which pull sources this installation
// can use, and why one is unavailable.
func (h *handler) importCapabilities(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := h.sitePrincipal(w, r, authz.ActionSiteEdit, authz.RoleOwner, authz.RoleAdmin); !ok {
		return
	}
	service, ok := h.pullService(w)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, service.Capabilities())
}

// previewPullImport fetches the range into memory and reports what importing
// it would add, exactly like a file preview. Nothing is stored.
func (h *handler) previewPullImport(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.sitePrincipal(w, r, authz.ActionSiteEdit, authz.RoleOwner, authz.RoleAdmin)
	if !ok {
		return
	}
	if !h.allowHistoricalIdentity(w) {
		return
	}
	service, ok := h.pullService(w)
	if !ok {
		return
	}
	var body pullImportRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	select {
	case importPreviewSlots <- struct{}{}:
		defer func() { <-importPreviewSlots }()
	default:
		w.Header().Set("Retry-After", "5")
		writeProblem(w, http.StatusTooManyRequests, "Too many requests", "another import preview is already running")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), importPreviewTimeout)
	defer cancel()
	request := body.toPull()
	preview, err := service.Preview(ctx, site.ID, principal.OrgID, request)
	if err != nil {
		pullError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, importPreview{
		Source: string(request.Source()), FileName: request.FileLabel(), Summary: preview.Summary, Overlap: preview.Overlap,
	})
}

// createPullImport validates the request, stores it with sealed credentials
// and queues the background job. Poll GET /imports/{id} for progress.
func (h *handler) createPullImport(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.sitePrincipal(w, r, authz.ActionSiteEdit, authz.RoleOwner, authz.RoleAdmin)
	if !ok {
		return
	}
	if !h.allowHistoricalIdentity(w) {
		return
	}
	service, ok := h.pullService(w)
	if !ok {
		return
	}
	if h.deps.Imports == nil || h.deps.ImportQueue == nil {
		unavailable(w, "import")
		return
	}
	var body pullImportRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	created, err := service.Create(r.Context(), site.ID, principal.OrgID, principal.UserID, body.toPull())
	if err != nil {
		pullError(w, err)
		return
	}
	if err := h.deps.ImportQueue.Queue(imports.Job{ImportID: created.ID, Source: created.Source, Pull: true}); err != nil {
		_ = h.deps.Imports.SetFailed(context.Background(), created.ID, err)
		writeProblem(w, http.StatusServiceUnavailable, "Service unavailable", "the import worker is busy")
		return
	}
	if err := h.auditMutation(r.Context(), principal, "import.created", "import", created.ID, site.ID); err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, created)
}

// getImport returns one import, including pull progress.
func (h *handler) getImport(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.principal(w, r)
	if !ok {
		return
	}
	if h.deps.Imports == nil {
		unavailable(w, "import")
		return
	}
	id, err := parseInt64(r.PathValue("import"))
	if err != nil {
		writeProblem(w, http.StatusNotFound, "Not found", "import not found")
		return
	}
	item, err := h.deps.Imports.Get(r.Context(), id)
	if errors.Is(err, imports.ErrNotFound) {
		writeProblem(w, http.StatusNotFound, "Not found", "import not found")
		return
	}
	if err != nil {
		serviceError(w, err)
		return
	}
	principal, _, ok = h.visibleSite(w, principal, item.SiteID)
	if !ok {
		return
	}
	if !roleAllowed(principal.Role, []authz.Role{authz.RoleOwner, authz.RoleAdmin}) || !authz.Can(principal, authz.ActionSiteEdit, item.SiteID) {
		writeProblem(w, http.StatusForbidden, "Forbidden", "the principal cannot perform this action")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

// connectGA4 starts the Google consent flow and returns its URL.
func (h *handler) connectGA4(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.sitePrincipal(w, r, authz.ActionSiteEdit, authz.RoleOwner, authz.RoleAdmin)
	if !ok {
		return
	}
	service, ok := h.pullService(w)
	if !ok {
		return
	}
	target, err := service.StartGA4(r.Context(), principal.UserID, principal.OrgID, site.ID)
	if err != nil {
		pullError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"authorization_url": target})
}

// ga4Callback finishes the consent flow and sends the browser back to the
// site's import settings with the outcome in the query string.
func (h *handler) ga4Callback(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.privatePrincipal(w, r)
	if !ok {
		return
	}
	service, ok := h.pullService(w)
	if !ok {
		return
	}
	query := r.URL.Query()
	siteID, err := service.CallbackGA4(r.Context(), principal.UserID, principal.OrgID, query.Get("code"), query.Get("state"), query.Get("error"))
	if siteID > 0 {
		status := "connected"
		switch {
		case errors.Is(err, imports.ErrGA4Denied):
			status = "denied"
		case err != nil:
			status = "error"
		}
		http.Redirect(w, r, h.importSettingsPath(r.Context(), principal.OrgID, siteID, status), http.StatusSeeOther)
		return
	}
	pullError(w, err)
}

// importSettingsPath is the dashboard route that reports the GA4 consent
// result; it falls back to the dashboard root when the site cannot be resolved.
func (h *handler) importSettingsPath(ctx context.Context, orgID int64, siteID int32, status string) string {
	query := url.Values{"ga4": {status}}.Encode()
	if h.deps.Store == nil || h.deps.Sites == nil {
		return "/app?" + query
	}
	site, found := h.deps.Sites.Site(siteID)
	if !found || site.PublicID == "" {
		return "/app?" + query
	}
	var slug string
	if err := h.deps.Store.Pool.QueryRow(ctx, `select slug from orgs where id=$1`, orgID).Scan(&slug); err != nil {
		return "/app?" + query
	}
	return "/app/" + url.PathEscape(slug) + "/" + url.PathEscape(site.PublicID) + "/settings/imports?" + query
}

func (h *handler) listGA4Connections(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.principal(w, r, authz.RoleOwner, authz.RoleAdmin)
	if !ok {
		return
	}
	service, ok := h.pullService(w)
	if !ok {
		return
	}
	connections, err := service.ListGA4Connections(r.Context(), principal.OrgID)
	if err != nil {
		pullError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": connections})
}

func (h *handler) listGA4Properties(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.principal(w, r, authz.RoleOwner, authz.RoleAdmin)
	if !ok {
		return
	}
	service, ok := h.pullService(w)
	if !ok {
		return
	}
	id, err := parseInt64(r.PathValue("connection"))
	if err != nil {
		writeProblem(w, http.StatusNotFound, "Not found", "connection not found")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	properties, err := service.ListGA4Properties(ctx, principal.OrgID, id)
	if err != nil {
		pullError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": properties})
}

func (h *handler) deleteGA4Connection(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.principal(w, r, authz.RoleOwner, authz.RoleAdmin)
	if !ok {
		return
	}
	service, ok := h.pullService(w)
	if !ok {
		return
	}
	id, err := parseInt64(r.PathValue("connection"))
	if err != nil {
		writeProblem(w, http.StatusNotFound, "Not found", "connection not found")
		return
	}
	if err := service.DeleteGA4Connection(r.Context(), principal.OrgID, id); err != nil {
		pullError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// pullError maps pull and GA4 errors to problem responses. Remote failures
// carry a credential-free message the wizard can show.
func pullError(w http.ResponseWriter, err error) {
	var remote *imports.RemoteError
	switch {
	case errors.Is(err, imports.ErrGA4NotConfigured):
		writeProblem(w, http.StatusConflict, "Not configured", err.Error())
	case errors.Is(err, imports.ErrGA4Connection):
		writeProblem(w, http.StatusNotFound, "Not found", "Google Analytics connection not found")
	case errors.Is(err, imports.ErrGA4InvalidState), errors.Is(err, imports.ErrGA4NoRefresh):
		writeProblem(w, http.StatusBadRequest, "Bad request", err.Error())
	case errors.Is(err, imports.ErrPullInvalid):
		writeProblem(w, http.StatusBadRequest, "Bad request", strings.TrimPrefix(err.Error(), imports.ErrPullInvalid.Error()+": "))
	case errors.Is(err, context.DeadlineExceeded):
		writeProblem(w, http.StatusGatewayTimeout, "Timeout", "the remote service did not answer in time; try a shorter date range")
	case errors.As(err, &remote) && errors.Is(err, imports.ErrRemoteAuth):
		writeProblem(w, http.StatusUnprocessableEntity, "Credentials rejected", remote.Error())
	case errors.As(err, &remote):
		writeProblem(w, http.StatusBadGateway, "Remote service error", remote.Error())
	default:
		serviceError(w, err)
	}
}
