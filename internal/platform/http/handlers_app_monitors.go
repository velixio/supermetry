package platformhttp

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/appmonitors"
	"supermetry/internal/platform/audit"
	"supermetry/internal/platform/authz"
)

func (h *handler) appMonitors() *appmonitors.Store {
	return appmonitors.NewStore(h.deps.Store.Pool, h.deps.Now)
}

func appMonitorError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, appmonitors.ErrNotFound):
		writeProblem(w, http.StatusNotFound, "Not found", "monitor not found")
	case errors.Is(err, appmonitors.ErrInvalidConfig):
		writeProblem(w, http.StatusBadRequest, "Bad request", err.Error())
	default:
		serviceError(w, err)
	}
}

func monitorID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := parseInt64(r.PathValue("monitor"))
	if err != nil || id < 1 {
		writeProblem(w, http.StatusNotFound, "Not found", "monitor not found")
		return 0, false
	}
	return id, true
}

func (h *handler) listAppMonitors(w http.ResponseWriter, r *http.Request) {
	site, ok := h.appReadSite(w, r)
	if !ok {
		return
	}
	monitors, err := h.appMonitors().ListMonitors(r.Context(), site.ID)
	if err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"monitors": monitors})
}

func (h *handler) listAppMonitorCheckins(w http.ResponseWriter, r *http.Request) {
	site, ok := h.appReadSite(w, r)
	if !ok {
		return
	}
	id, ok := monitorID(w, r)
	if !ok {
		return
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 100 {
			writeProblem(w, http.StatusBadRequest, "Bad request", "limit must be between 1 and 100")
			return
		}
		limit = n
	}
	rows, err := h.appMonitors().ListCheckins(r.Context(), site.ID, id, limit)
	if err != nil {
		appMonitorError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"checkins": rows})
}

type appMonitorPatch struct {
	Disabled          *bool `json:"disabled"`
	MarginMinutes     *int  `json:"checkin_margin_minutes"`
	MaxRuntimeMinutes *int  `json:"max_runtime_minutes"`
}

func (h *handler) patchAppMonitor(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.appHealthSite(w, r, authz.ActionSiteEdit, authz.RoleOwner, authz.RoleAdmin, authz.RoleEditor)
	if !ok {
		return
	}
	id, ok := monitorID(w, r)
	if !ok {
		return
	}
	var in appMonitorPatch
	if !decodeJSON(w, r, &in) {
		return
	}
	m, err := h.appMonitors().Update(r.Context(), site.ID, id, appmonitors.Patch{
		Disabled: in.Disabled, MarginMinutes: in.MarginMinutes, MaxRuntimeMinutes: in.MaxRuntimeMinutes,
	})
	if err != nil {
		appMonitorError(w, err)
		return
	}
	if err := h.deps.Store.Tx(r.Context(), func(tx pgx.Tx) error {
		return audit.Write(r.Context(), tx, principal, "site.app_monitor_changed", "site", int64(site.ID), map[string]any{"monitor_id": id})
	}); err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (h *handler) deleteAppMonitor(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.appHealthSite(w, r, authz.ActionSiteEdit, authz.RoleOwner, authz.RoleAdmin, authz.RoleEditor)
	if !ok {
		return
	}
	id, ok := monitorID(w, r)
	if !ok {
		return
	}
	if err := h.appMonitors().Delete(r.Context(), site.ID, id); err != nil {
		appMonitorError(w, err)
		return
	}
	if err := h.deps.Store.Tx(r.Context(), func(tx pgx.Tx) error {
		return audit.Write(r.Context(), tx, principal, "site.app_monitor_deleted", "site", int64(site.ID), map[string]any{"monitor_id": id})
	}); err != nil {
		serviceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
