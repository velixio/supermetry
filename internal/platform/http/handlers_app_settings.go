package platformhttp

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/platform/audit"
	coresites "supermetry/internal/sites"
)

func (h *handler) getAppSettings(w http.ResponseWriter, r *http.Request) {
	_, site, ok := h.appSite(w, r)
	if !ok {
		return
	}
	raw, err := h.deps.Store.AppSettings(r.Context(), site.ID)
	if err != nil {
		serviceError(w, err)
		return
	}
	settings, err := coresites.ParseAppSettings(raw)
	if err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (h *handler) updateAppSettings(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.appSite(w, r)
	if !ok {
		return
	}
	var in coresites.AppSettings
	if !decodeJSON(w, r, &in) {
		return
	}
	settings, err := in.Normalize()
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", err.Error())
		return
	}
	raw, err := json.Marshal(settings)
	if err != nil {
		serviceError(w, err)
		return
	}
	err = h.deps.Store.Tx(r.Context(), func(tx pgx.Tx) error {
		if err := h.deps.Store.SetAppSettings(r.Context(), tx, site.ID, raw); err != nil {
			return err
		}
		return audit.Write(r.Context(), tx, principal, "site.app_settings_changed", "site", int64(site.ID), map[string]any{
			"max_report_age_days": settings.MaxReportAgeDays, "tags": len(settings.TagAllowlist), "environments": len(settings.Environments),
		})
	})
	if err != nil {
		serviceError(w, err)
		return
	}
	if inv, ok := h.deps.Sites.(interface{ Invalidate() error }); ok {
		if err := inv.Invalidate(); err != nil {
			slog.Error("site registry refresh failed after app settings change", "site_id", site.ID, "error", err)
		}
	}
	writeJSON(w, http.StatusOK, settings)
}
