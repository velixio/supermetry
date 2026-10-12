package platformhttp

import (
	"errors"
	"net/http"

	"supermetry/internal/api"
	"supermetry/internal/platform/authz"
	"supermetry/internal/platform/dashboards"
)

func (h *handler) dashboardService(w http.ResponseWriter) (*dashboards.Service, bool) {
	if h.deps.Store == nil {
		unavailable(w, "dashboard")
		return nil, false
	}
	return dashboards.New(h.deps.Store), true
}

func (h *handler) listDashboards(w http.ResponseWriter, r *http.Request) {
	_, site, ok := h.sitePrincipal(w, r, authz.ActionStatsRead, h.resourceRoles(r)...)
	if !ok {
		return
	}
	service, ok := h.dashboardService(w)
	if !ok {
		return
	}
	items, err := service.List(r.Context(), site.ID)
	if err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, api.DashboardList{Data: items})
}

func (h *handler) getDefaultDashboard(w http.ResponseWriter, r *http.Request) {
	_, site, ok := h.sitePrincipal(w, r, authz.ActionStatsRead, h.resourceRoles(r)...)
	if !ok {
		return
	}
	service, ok := h.dashboardService(w)
	if !ok {
		return
	}
	dashboard, err := service.Default(r.Context(), site.ID)
	if err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, dashboard)
}

func (h *handler) clearDefaultDashboard(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.sitePrincipal(w, r, authz.ActionSegmentsEdit, h.resourceRoles(r)...)
	if !ok {
		return
	}
	service, ok := h.dashboardService(w)
	if !ok {
		return
	}
	if err := service.ClearDefault(r.Context(), principal, site.ID); err != nil {
		dashboardError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) createDashboard(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.sitePrincipal(w, r, authz.ActionSegmentsEdit, h.resourceRoles(r)...)
	if !ok {
		return
	}
	service, ok := h.dashboardService(w)
	if !ok {
		return
	}
	var input api.DashboardInput
	if !decodeJSON(w, r, &input) {
		return
	}
	created, err := service.Create(r.Context(), principal, site.ID, input.Name, input.Layout)
	if err != nil {
		dashboardError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *handler) getDashboard(w http.ResponseWriter, r *http.Request) {
	_, _, dashboard, ok := h.dashboardPrincipal(w, r, authz.ActionStatsRead)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, dashboard)
}

func (h *handler) updateDashboard(w http.ResponseWriter, r *http.Request) {
	principal, siteID, dashboard, ok := h.dashboardPrincipal(w, r, authz.ActionSegmentsEdit)
	if !ok {
		return
	}
	var input api.DashboardUpdate
	if !decodeJSON(w, r, &input) {
		return
	}
	updated, err := dashboards.New(h.deps.Store).Update(r.Context(), principal, siteID, dashboard.Id, input.Name, input.Layout, input.Version)
	if err != nil {
		dashboardError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *handler) deleteDashboard(w http.ResponseWriter, r *http.Request) {
	principal, siteID, dashboard, ok := h.dashboardPrincipal(w, r, authz.ActionSegmentsEdit)
	if !ok {
		return
	}
	if err := dashboards.New(h.deps.Store).Delete(r.Context(), principal, siteID, dashboard.Id); err != nil {
		dashboardError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) duplicateDashboard(w http.ResponseWriter, r *http.Request) {
	principal, siteID, dashboard, ok := h.dashboardPrincipal(w, r, authz.ActionSegmentsEdit)
	if !ok {
		return
	}
	copied, err := dashboards.New(h.deps.Store).Duplicate(r.Context(), principal, siteID, dashboard.Id)
	if err != nil {
		dashboardError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, copied)
}

func (h *handler) setDefaultDashboard(w http.ResponseWriter, r *http.Request) {
	principal, siteID, dashboard, ok := h.dashboardPrincipal(w, r, authz.ActionSegmentsEdit)
	if !ok {
		return
	}
	updated, err := dashboards.New(h.deps.Store).SetDefault(r.Context(), principal, siteID, dashboard.Id)
	if err != nil {
		dashboardError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// dashboardPrincipal authorizes action on the site named in the path, then
// loads the dashboard scoped to that site. A dashboard id that belongs to
// another site is indistinguishable from an unknown id: 404.
func (h *handler) dashboardPrincipal(w http.ResponseWriter, r *http.Request, action authz.Action) (authz.Principal, int32, api.Dashboard, bool) {
	principal, site, ok := h.sitePrincipal(w, r, action, h.resourceRoles(r)...)
	if !ok {
		return authz.Principal{}, 0, api.Dashboard{}, false
	}
	id, err := parseInt64(r.PathValue("dashboard"))
	if err != nil || h.deps.Store == nil {
		writeProblem(w, http.StatusNotFound, "Not found", "dashboard not found")
		return authz.Principal{}, 0, api.Dashboard{}, false
	}
	dashboard, err := dashboards.New(h.deps.Store).Get(r.Context(), site.ID, id)
	if errors.Is(err, dashboards.ErrNotFound) {
		writeProblem(w, http.StatusNotFound, "Not found", "dashboard not found")
		return authz.Principal{}, 0, api.Dashboard{}, false
	}
	if err != nil {
		serviceError(w, err)
		return authz.Principal{}, 0, api.Dashboard{}, false
	}
	return principal, site.ID, dashboard, true
}

func dashboardError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, dashboards.ErrInvalid):
		writeProblem(w, http.StatusBadRequest, "Bad request", err.Error())
	case errors.Is(err, dashboards.ErrNotFound):
		writeProblem(w, http.StatusNotFound, "Not found", "dashboard not found")
	case errors.Is(err, dashboards.ErrNameTaken), errors.Is(err, dashboards.ErrLimit), errors.Is(err, dashboards.ErrStale):
		writeProblem(w, http.StatusConflict, "Conflict", err.Error())
	default:
		serviceError(w, err)
	}
}
