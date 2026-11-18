package platformhttp

import (
	"errors"
	"net/http"
	"strings"

	"supermetry/internal/obsmetrics"
	"supermetry/internal/platform/authz"
	coresites "supermetry/internal/sites"
)

// obsMetricsSite resolves an app project whose metrics the caller may read:
// every member role through the private API, and API keys with stats:read
// through the public one. Both are scoped to the one site in the path.
func (h *handler) obsMetricsSite(w http.ResponseWriter, r *http.Request) (coresites.Site, bool) {
	_, site, ok := h.appHealthSite(w, r, authz.ActionStatsRead, h.resourceRoles(r)...)
	return site, ok
}

func (h *handler) listObsMetrics(w http.ResponseWriter, r *http.Request) {
	site, ok := h.obsMetricsSite(w, r)
	if !ok {
		return
	}
	metrics, err := obsmetrics.ListMetrics(r.Context(), h.deps.Store.Pool, site.ID)
	if err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"metrics": metrics})
}

func (h *handler) queryObsMetrics(w http.ResponseWriter, r *http.Request) {
	site, ok := h.obsMetricsSite(w, r)
	if !ok {
		return
	}
	var query obsmetrics.MetricQuery
	if !decodeJSON(w, r, &query) {
		return
	}
	series, err := obsmetrics.Query(r.Context(), h.deps.Store.Pool, site.ID, siteLocation(site), query)
	if err != nil {
		if errors.Is(err, obsmetrics.ErrInvalid) {
			writeProblem(w, http.StatusBadRequest, "Bad request", strings.TrimPrefix(err.Error(), obsmetrics.ErrInvalid.Error()+": "))
			return
		}
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"series": series})
}
