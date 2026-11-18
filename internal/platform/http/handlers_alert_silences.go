package platformhttp

import (
	"context"
	"net/http"

	"supermetry/internal/alerts"
	"supermetry/internal/platform/authz"
)

// AlertSilenceService is the silence half of the alerts service. The alerts
// dependency implements it; a dependency that does not leaves the routes at 503.
type AlertSilenceService interface {
	ListSilences(context.Context, authz.Principal, int32) ([]alerts.Silence, error)
	CreateSilence(context.Context, authz.Principal, int32, alerts.SilenceInput) (alerts.Silence, error)
	DeleteSilence(context.Context, authz.Principal, int32, int64) error
}

func (h *handler) silenceService(w http.ResponseWriter) (AlertSilenceService, bool) {
	if h.deps.Alerts != nil {
		if svc, ok := h.deps.Alerts.(AlertSilenceService); ok {
			return svc, true
		}
	}
	unavailable(w, "alert silence")
	return nil, false
}

func (h *handler) listAlertSilences(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.sitePrincipal(w, r, authz.ActionAlertsManage, authz.RoleOwner, authz.RoleAdmin, authz.RoleEditor)
	if !ok {
		return
	}
	svc, ok := h.silenceService(w)
	if !ok {
		return
	}
	items, err := svc.ListSilences(r.Context(), principal, site.ID)
	if err != nil {
		alertServiceError(w, err, "alert silence")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items})
}

func (h *handler) createAlertSilence(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.sitePrincipal(w, r, authz.ActionAlertsManage, authz.RoleOwner, authz.RoleAdmin, authz.RoleEditor)
	if !ok {
		return
	}
	var input alerts.SilenceInput
	if !decodeJSON(w, r, &input) {
		return
	}
	svc, ok := h.silenceService(w)
	if !ok {
		return
	}
	created, err := svc.CreateSilence(r.Context(), principal, site.ID, input)
	if err != nil {
		alertServiceError(w, err, "alert silence")
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *handler) deleteAlertSilence(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.sitePrincipal(w, r, authz.ActionAlertsManage, authz.RoleOwner, authz.RoleAdmin, authz.RoleEditor)
	if !ok {
		return
	}
	id, err := parseInt64(r.PathValue("silence"))
	if err != nil {
		writeProblem(w, http.StatusNotFound, "Not found", "alert silence not found")
		return
	}
	svc, ok := h.silenceService(w)
	if !ok {
		return
	}
	if err := svc.DeleteSilence(r.Context(), principal, site.ID, id); err != nil {
		alertServiceError(w, err, "alert silence")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
