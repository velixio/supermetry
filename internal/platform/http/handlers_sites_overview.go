package platformhttp

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"supermetry/internal/platform/sitesvc"
)

const overviewRequestsPerMinute = 120

type overviewRateWindow struct {
	started time.Time
	count   int
}

func (h *handler) sitesOverview(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.privatePrincipal(w, r)
	if !ok {
		return
	}
	if !h.allowSitesOverview(principal.UserID, h.deps.Now()) {
		w.Header().Set("Retry-After", "60")
		rateLimited(w)
		return
	}
	w.Header().Set("RateLimit-Limit", strconv.Itoa(overviewRequestsPerMinute))
	if h.deps.Overview == nil {
		unavailable(w, "sites overview")
		return
	}
	periodValue := r.URL.Query().Get("period")
	if periodValue == "" {
		periodValue = string(sitesvc.Overview24Hours)
	}
	period, err := sitesvc.ParseOverviewPeriod(periodValue)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", "period must be 24h, 7d, or 30d")
		return
	}
	limit := sitesvc.OverviewMaxLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit <= 0 || limit > sitesvc.OverviewMaxLimit {
			writeProblem(w, http.StatusBadRequest, "Bad request", "limit must be between 1 and 200")
			return
		}
	}
	items, next, err := h.deps.Overview.List(r.Context(), principal.UserID, period, r.URL.Query().Get("cursor"), limit)
	if err != nil {
		if errors.Is(err, sitesvc.ErrInvalid) {
			writeProblem(w, http.StatusBadRequest, "Bad request", err.Error())
			return
		}
		serviceError(w, err)
		return
	}
	for index := range items {
		if h.deps.Snippet != nil {
			items[index].TrackingSnippet = h.deps.Snippet(items[index].PublicID, false)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items, "next_cursor": nullableCursor(next)})
}

func (h *handler) allowSitesOverview(userID int64, now time.Time) bool {
	h.overviewLimitMu.Lock()
	defer h.overviewLimitMu.Unlock()
	window := h.overviewLimits[userID]
	if window.started.IsZero() || !now.Before(window.started.Add(time.Minute)) {
		window = overviewRateWindow{started: now, count: 1}
		h.overviewLimits[userID] = window
		return true
	}
	if window.count >= overviewRequestsPerMinute {
		return false
	}
	window.count++
	h.overviewLimits[userID] = window
	return true
}
