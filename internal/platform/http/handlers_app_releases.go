package platformhttp

import "net/http"

// getAppDeploys serves the deploy markers of an app project. Every member role
// may read them; the release query parameter is an exact match.
func (h *handler) getAppDeploys(w http.ResponseWriter, r *http.Request) {
	site, ok := h.appReadSite(w, r)
	if !ok {
		return
	}
	f, ok := appFilter(w, r, site, h.deps.Now())
	if !ok {
		return
	}
	rows, err := h.appQuerier().Deploys(r.Context(), appScope(site), f, f.Release)
	if err != nil {
		appQueryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deploys": rows})
}
