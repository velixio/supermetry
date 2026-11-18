package platformhttp

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/appdebug"
	"supermetry/internal/appquery"
	"supermetry/internal/platform/audit"
	"supermetry/internal/platform/authz"
	coresites "supermetry/internal/sites"
)

// getAppPlatforms serves the per-platform health and symbolication counts.
func (h *handler) getAppPlatforms(w http.ResponseWriter, r *http.Request) {
	site, ok := h.appReadSite(w, r)
	if !ok {
		return
	}
	f, ok := appFilter(w, r, site, h.deps.Now())
	if !ok {
		return
	}
	rows, err := h.appQuerier().PlatformBreakdown(r.Context(), appScope(site), f)
	if err != nil {
		appQueryError(w, err)
		return
	}
	if rows == nil {
		rows = []appquery.PlatformRow{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"platforms": rows})
}

func (h *handler) appDebugStore() *appdebug.Store {
	return appdebug.NewStore(h.deps.Store.Pool, h.deps.ReplayStore)
}

// listAppDebugFiles serves the Symbols page: uploaded debug files and the
// images events still miss. Every member role may read it.
func (h *handler) listAppDebugFiles(w http.ResponseWriter, r *http.Request) {
	site, ok := h.appReadSite(w, r)
	if !ok {
		return
	}
	out, err := h.appDebugStore().List(r.Context(), site.OrgID, site.ID)
	if err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// deleteAppDebugFile removes one debug file the project can see.
func (h *handler) deleteAppDebugFile(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.appHealthSite(w, r, authz.ActionSiteEdit, authz.RoleOwner, authz.RoleAdmin, authz.RoleEditor)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeProblem(w, http.StatusNotFound, "Not found", "no such debug file")
		return
	}
	if err := h.appDebugStore().Delete(r.Context(), site.OrgID, site.ID, id, authz.Can(principal, authz.ActionOrgUpdate, 0)); err != nil {
		if errors.Is(err, appdebug.ErrNotFound) {
			writeProblem(w, http.StatusNotFound, "Not found", "no such debug file")
			return
		}
		serviceError(w, err)
		return
	}
	h.auditDebugFileDeleted(r, principal, site, id)
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) auditDebugFileDeleted(r *http.Request, principal authz.Principal, site coresites.Site, id int64) {
	_ = h.deps.Store.Tx(r.Context(), func(tx pgx.Tx) error {
		return audit.Write(r.Context(), tx, principal, "app.debug_file_deleted", "site", int64(site.ID), map[string]any{"file": id})
	})
}
