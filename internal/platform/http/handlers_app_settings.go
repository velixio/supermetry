package platformhttp

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/platform/audit"
	"supermetry/internal/platform/authz"
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

// appLinkBody is {"app_site_id": int|null}. The field must be present.
type appLinkBody struct {
	AppSiteID *int32 `json:"app_site_id"`
}

type appLinkResponse struct {
	AppSiteID *int32 `json:"app_site_id"`
}

// linkSite resolves a web site the caller may edit; apps have no outgoing link.
func (h *handler) linkSite(w http.ResponseWriter, r *http.Request) (authz.Principal, coresites.Site, bool) {
	principal, site, ok := h.sitePrincipal(w, r, authz.ActionSiteEdit, authz.RoleOwner, authz.RoleAdmin)
	if !ok {
		return principal, site, false
	}
	if site.Kind != coresites.KindWeb || h.deps.Store == nil {
		writeProblem(w, http.StatusNotFound, "Not found", "not a web project")
		return principal, site, false
	}
	return principal, site, true
}

func (h *handler) getAppLink(w http.ResponseWriter, r *http.Request) {
	_, site, ok := h.linkSite(w, r)
	if !ok {
		return
	}
	target, err := h.deps.Store.AppLink(r.Context(), site.ID)
	if err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, appLinkResponse{AppSiteID: nonZero(target)})
}

func (h *handler) updateAppLink(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.linkSite(w, r)
	if !ok {
		return
	}
	var raw map[string]json.RawMessage
	if !decodeJSON(w, r, &raw) {
		return
	}
	value, present := raw["app_site_id"]
	if !present || len(raw) != 1 {
		writeProblem(w, http.StatusBadRequest, "Bad request", "app_site_id is required (an app project id, or null to unlink)")
		return
	}
	var in appLinkBody
	if err := json.Unmarshal(value, &in.AppSiteID); err != nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", "app_site_id must be an integer or null")
		return
	}
	if in.AppSiteID != nil {
		valid, err := h.deps.Store.IsAppInOrg(r.Context(), principal.OrgID, *in.AppSiteID)
		if err != nil {
			serviceError(w, err)
			return
		}
		if !valid {
			writeProblem(w, http.StatusBadRequest, "Bad request", "app_site_id must be an app project in this organization")
			return
		}
	}
	err := h.deps.Store.Tx(r.Context(), func(tx pgx.Tx) error {
		if err := h.deps.Store.SetAppLink(r.Context(), tx, site.ID, in.AppSiteID); err != nil {
			return err
		}
		meta := map[string]any{"linked": in.AppSiteID != nil}
		if in.AppSiteID != nil {
			meta["app_site_id"] = *in.AppSiteID
		}
		return audit.Write(r.Context(), tx, principal, "site.app_link_changed", "site", int64(site.ID), meta)
	})
	if err != nil {
		serviceError(w, err)
		return
	}
	if inv, ok := h.deps.Sites.(interface{ Invalidate() error }); ok {
		if err := inv.Invalidate(); err != nil {
			slog.Error("site registry refresh failed after app link change", "site_id", site.ID, "error", err)
		}
	}
	writeJSON(w, http.StatusOK, appLinkResponse{AppSiteID: in.AppSiteID})
}

func nonZero(id int32) *int32 {
	if id == 0 {
		return nil
	}
	return &id
}
