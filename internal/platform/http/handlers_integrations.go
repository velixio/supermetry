package platformhttp

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/integrations"
	"supermetry/internal/platform/authz"
)

// IntegrationsService is the transport-facing subset of the integrations service.
type IntegrationsService interface {
	List(context.Context, authz.Principal, int32) ([]integrations.CatalogEntry, error)
	Start(context.Context, authz.Principal, int32, integrations.StartInput) (string, error)
	Callback(ctx context.Context, p authz.Principal, provider, code, state, providerError string) (int32, error)
	Disconnect(context.Context, authz.Principal, int32, string) error
	Backfill(context.Context, authz.Principal, int32, string, int) error
	Top(ctx context.Context, p authz.Principal, siteID int32, dimension string, from, to time.Time, limit int) (integrations.TopResult, error)
}

type connectIntegrationRequest struct {
	Property     string `json:"property"`
	BackfillDays int    `json:"backfill_days,omitempty"`
}

type backfillIntegrationRequest struct {
	Days int `json:"days"`
}

func (h *handler) listIntegrations(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.sitePrincipal(w, r, authz.ActionSiteEdit, authz.RoleOwner, authz.RoleAdmin, authz.RoleEditor)
	if !ok {
		return
	}
	if h.deps.Integrations == nil {
		unavailable(w, "integration")
		return
	}
	entries, err := h.deps.Integrations.List(r.Context(), principal, site.ID)
	if err != nil {
		integrationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": entries})
}

func (h *handler) connectIntegration(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.sitePrincipal(w, r, authz.ActionSiteEdit, authz.RoleOwner, authz.RoleAdmin)
	if !ok {
		return
	}
	var request connectIntegrationRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if h.deps.Integrations == nil {
		unavailable(w, "integration")
		return
	}
	target, err := h.deps.Integrations.Start(r.Context(), principal, site.ID, integrations.StartInput{
		Provider: r.PathValue("provider"), Property: request.Property, BackfillDays: request.BackfillDays,
	})
	if err != nil {
		integrationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"authorization_url": target})
}

func (h *handler) integrationCallback(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.privatePrincipal(w, r)
	if !ok {
		return
	}
	if h.deps.Integrations == nil {
		unavailable(w, "integration")
		return
	}
	query := r.URL.Query()
	provider := r.PathValue("provider")
	siteID, err := h.deps.Integrations.Callback(r.Context(), principal, provider, query.Get("code"), query.Get("state"), query.Get("error"))
	if siteID > 0 && !errors.Is(err, integrations.ErrForbidden) {
		status := "connected"
		switch {
		case errors.Is(err, integrations.ErrDenied):
			status = "denied"
		case err != nil:
			status = "error"
		}
		http.Redirect(w, r, h.integrationSettingsPath(r.Context(), principal.OrgID, siteID, provider, status), http.StatusSeeOther)
		return
	}
	integrationError(w, err)
}

// integrationSettingsPath is the dashboard route that reports an OAuth result.
// When the organization or site cannot be resolved it falls back to the
// dashboard root so the browser never lands on a route that does not exist.
func (h *handler) integrationSettingsPath(ctx context.Context, orgID int64, siteID int32, provider, status string) string {
	query := url.Values{"integration": {provider}, "status": {status}}.Encode()
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
	return "/app/" + url.PathEscape(slug) + "/" + url.PathEscape(site.PublicID) + "/settings/integrations?" + query
}

func (h *handler) disconnectIntegration(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.sitePrincipal(w, r, authz.ActionSiteEdit, authz.RoleOwner, authz.RoleAdmin)
	if !ok {
		return
	}
	if h.deps.Integrations == nil {
		unavailable(w, "integration")
		return
	}
	if err := h.deps.Integrations.Disconnect(r.Context(), principal, site.ID, r.PathValue("provider")); err != nil {
		integrationError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) backfillIntegration(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.sitePrincipal(w, r, authz.ActionSiteEdit, authz.RoleOwner, authz.RoleAdmin)
	if !ok {
		return
	}
	var request backfillIntegrationRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if h.deps.Integrations == nil {
		unavailable(w, "integration")
		return
	}
	if err := h.deps.Integrations.Backfill(r.Context(), principal, site.ID, r.PathValue("provider"), request.Days); err != nil {
		integrationError(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (h *handler) searchConsoleTop(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.sitePrincipal(w, r, authz.ActionStatsRead, authz.RoleOwner, authz.RoleAdmin, authz.RoleEditor, authz.RoleViewer)
	if !ok {
		return
	}
	query := r.URL.Query()
	from, errFrom := time.Parse(time.DateOnly, query.Get("from"))
	to, errTo := time.Parse(time.DateOnly, query.Get("to"))
	if errFrom != nil || errTo != nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", "from and to must be dates in YYYY-MM-DD form")
		return
	}
	limit := 0
	if raw := query.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeProblem(w, http.StatusBadRequest, "Bad request", "limit must be an integer")
			return
		}
		limit = parsed
	}
	if h.deps.Integrations == nil {
		unavailable(w, "integration")
		return
	}
	result, err := h.deps.Integrations.Top(r.Context(), principal, site.ID, query.Get("dimension"), from, to, limit)
	if err != nil {
		integrationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func integrationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, pgx.ErrNoRows), errors.Is(err, integrations.ErrNotFound):
		writeProblem(w, http.StatusNotFound, "Not found", "integration not found")
	case errors.Is(err, integrations.ErrForbidden):
		writeProblem(w, http.StatusForbidden, "Forbidden", "the principal cannot perform this action")
	case errors.Is(err, integrations.ErrInvalid), errors.Is(err, integrations.ErrInvalidState):
		writeProblem(w, http.StatusBadRequest, "Bad request", err.Error())
	case errors.Is(err, integrations.ErrUnavailable):
		writeProblem(w, http.StatusConflict, "Conflict", err.Error())
	case errors.Is(err, integrations.ErrRateLimited):
		w.Header().Set("Retry-After", "600")
		writeProblem(w, http.StatusTooManyRequests, "Rate limit exceeded", err.Error())
	default:
		serviceError(w, err)
	}
}
