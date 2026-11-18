package platformhttp

import (
	"net/http"
	"strconv"

	"supermetry/internal/appquery"
)

// Performance reads of an app project (spans, transactions, traces). Like the
// other App Health reads they need stats.read, which every member role holds,
// and answer 404 for any site that is not an app project of the caller's
// organisation.

func (h *handler) getAppPerformance(w http.ResponseWriter, r *http.Request) {
	site, ok := h.appReadSite(w, r)
	if !ok {
		return
	}
	f, ok := appFilter(w, r, site, h.deps.Now())
	if !ok {
		return
	}
	out, err := h.appQuerier().PerformanceOverview(r.Context(), appScope(site), f)
	if err != nil {
		appQueryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *handler) listAppTransactions(w http.ResponseWriter, r *http.Request) {
	site, ok := h.appReadSite(w, r)
	if !ok {
		return
	}
	f, ok := appFilter(w, r, site, h.deps.Now())
	if !ok {
		return
	}
	limit := appquery.MaxTransactions
	if raw := r.URL.Query().Get("limit"); raw != "" {
		var err error
		if limit, err = strconv.Atoi(raw); err != nil || limit < 1 || limit > appquery.MaxTransactions {
			writeProblem(w, http.StatusBadRequest, "Bad request", "limit must be 1-100")
			return
		}
	}
	rows, err := h.appQuerier().Transactions(r.Context(), appScope(site), f, r.URL.Query().Get("sort"), limit)
	if err != nil {
		appQueryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"transactions": rows})
}

func (h *handler) getAppTransaction(w http.ResponseWriter, r *http.Request) {
	site, ok := h.appReadSite(w, r)
	if !ok {
		return
	}
	f, ok := appFilter(w, r, site, h.deps.Now())
	if !ok {
		return
	}
	out, err := h.appQuerier().Transaction(r.Context(), appScope(site), f, r.URL.Query().Get("name"))
	if err != nil {
		appQueryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *handler) getAppTrace(w http.ResponseWriter, r *http.Request) {
	site, ok := h.appReadSite(w, r)
	if !ok {
		return
	}
	id, err := appquery.ParseTraceID(r.PathValue("trace_id"))
	if err != nil {
		appQueryError(w, err)
		return
	}
	out, err := h.appQuerier().Trace(r.Context(), appScope(site), id)
	if err != nil {
		appQueryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *handler) getAppRegressions(w http.ResponseWriter, r *http.Request) {
	site, ok := h.appReadSite(w, r)
	if !ok {
		return
	}
	f, ok := appFilter(w, r, site, h.deps.Now())
	if !ok {
		return
	}
	query := r.URL.Query()
	rows, err := h.appQuerier().Regressions(r.Context(), appScope(site), f, query.Get("base"), query.Get("target"))
	if err != nil {
		appQueryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"regressions": rows})
}

func (h *handler) getAppNetworkFailures(w http.ResponseWriter, r *http.Request) {
	site, ok := h.appReadSite(w, r)
	if !ok {
		return
	}
	f, ok := appFilter(w, r, site, h.deps.Now())
	if !ok {
		return
	}
	rows, err := h.appQuerier().NetworkFailures(r.Context(), appScope(site), f)
	if err != nil {
		appQueryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"failures": rows})
}
