package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"panel/internal/health"
	"panel/internal/realtime"
	"panel/internal/session"
)

func (h *handlers) statsSummary(w http.ResponseWriter, r *http.Request) {
	h.runStats(w, r, []Metric{Visitors, Visits, Pageviews, BounceRate, VisitDuration}, nil)
}

func (h *handlers) statsTimeseries(w http.ResponseWriter, r *http.Request) {
	interval := r.URL.Query().Get("interval")
	if interval != "" && interval != "day" {
		writeProblem(w, badRequest("interval must be day"))
		return
	}
	h.runStats(w, r, []Metric{Visitors, Visits, Pageviews}, []string{"time"})
}

func (h *handlers) statsBreakdown(w http.ResponseWriter, r *http.Request) {
	dimension := r.URL.Query().Get("dimension")
	if dimension == "" {
		writeProblem(w, badRequest("dimension is required"))
		return
	}
	h.runStats(w, r, []Metric{Visitors, Pageviews}, []string{dimension})
}

func (h *handlers) runStats(w http.ResponseWriter, r *http.Request, metrics []Metric, dimensions []string) {
	if h.query == nil {
		writeProblem(w, internalProblem())
		return
	}
	request, problem := statsQuery(r, metrics, dimensions)
	if problem != nil {
		writeProblem(w, *problem)
		return
	}
	if !authorizeRequest(w, r, request.SiteId, "stats:read") {
		return
	}
	response, err := h.query.Query(r.Context(), request)
	if err != nil {
		writeQueryError(w, err)
		return
	}
	writeQueryResponse(w, r, response)
}

func statsQuery(r *http.Request, metrics []Metric, dimensions []string) (QueryRequest, *Problem) {
	siteID64, err := strconv.ParseInt(r.URL.Query().Get("site_id"), 10, 32)
	if err != nil || siteID64 < 1 {
		problem := badRequest("site_id must be a positive integer")
		return QueryRequest{}, &problem
	}
	period := Period(r.URL.Query().Get("period"))
	if period == "" {
		problem := badRequest("period is required")
		return QueryRequest{}, &problem
	}
	request := QueryRequest{SiteId: int32(siteID64), Period: &period, Metrics: metrics}
	if dimensions != nil {
		request.Dimensions = &dimensions
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, parseErr := strconv.Atoi(raw)
		if parseErr != nil {
			problem := badRequest("limit must be an integer")
			return QueryRequest{}, &problem
		}
		request.Limit = &limit
	}
	if cursor := r.URL.Query().Get("cursor"); cursor != "" {
		request.Cursor = &cursor
	}
	if raw := r.URL.Query().Get("filter"); raw != "" {
		parts := strings.SplitN(raw, ":", 3)
		if len(parts) != 3 || parts[0] == "" || parts[2] == "" {
			problem := badRequest("filter must be dimension:operator:value")
			return QueryRequest{}, &problem
		}
		filters := []Filter{{Dimension: parts[0], Operator: FilterOperator(parts[1]), Values: []string{parts[2]}}}
		request.Filters = &filters
	}
	return request, nil
}

func realtimeHandler(store session.Store, now func() time.Time) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		siteID, problem := siteIDParam(r)
		if problem != nil {
			writeProblem(w, *problem)
			return
		}
		if !authorizeRequest(w, r, siteID, "stats:read") {
			return
		}
		current := time.Now()
		if now != nil {
			current = now()
		}
		visitors, pages := realtime.Snapshot(store, siteID, current)
		response := RealtimeResponse{ActiveVisitors: int64(visitors), TopPages: make([]TopPage, len(pages))}
		for index, page := range pages {
			response.TopPages[index] = TopPage{Page: page}
		}
		writeJSON(w, http.StatusOK, response)
	}
}

func dataHealthHandler(w http.ResponseWriter, r *http.Request) {
	if !authorizeRequest(w, r, 0, "admin:read") {
		return
	}
	snapshot := health.Snapshot()
	bots := int64(0)
	for key, value := range snapshot {
		if strings.HasPrefix(key, "bots_filtered_total{") {
			bots += int64(value)
		}
	}
	writeJSON(w, http.StatusOK, DataHealthResponse{
		BotsFiltered:    bots,
		CloseLagSeconds: snapshot["close_lag_seconds"],
		Drops503:        int64(snapshot["ingest_dropped_total"]),
		IngestRate:      snapshot["ingest_accepted_total"],
		QueueDepth:      int64(snapshot["writer_queue_depth"]),
	})
}

// DataHealthResponse reports the process-local analytics health counters used
// by the private dashboard.
type DataHealthResponse struct {
	BotsFiltered    int64   `json:"bots_filtered"`
	CloseLagSeconds float64 `json:"close_lag_seconds"`
	Drops503        int64   `json:"drops_503"`
	IngestRate      float64 `json:"ingest_rate"`
	QueueDepth      int64   `json:"queue_depth"`
}

func siteIDParam(r *http.Request) (int32, *Problem) {
	value, err := strconv.ParseInt(r.URL.Query().Get("site_id"), 10, 32)
	if err != nil || value < 1 {
		problem := badRequest("site_id must be a positive integer")
		return 0, &problem
	}
	return int32(value), nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
