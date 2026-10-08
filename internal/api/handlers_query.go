package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

const maxQueryBodyBytes = 1 << 20

type queryService interface {
	Query(context.Context, QueryRequest) (QueryResponse, error)
	Funnel(context.Context, FunnelRequest) (FunnelResponse, error)
	Journey(context.Context, JourneyRequest) (JourneyResponse, error)
	Retention(context.Context, RetentionRequest) (RetentionResponse, error)
}

type handlers struct {
	query    queryService
	sessions sessionExploreService
	heatmaps heatmapService
	now      func() time.Time
}

func (h *handlers) queryRequest(w http.ResponseWriter, r *http.Request) {
	if h.query == nil {
		writeProblem(w, internalProblem())
		return
	}
	var request QueryRequest
	if problem := decodeJSON(w, r, &request); problem != nil {
		writeProblem(w, *problem)
		return
	}
	if !authorizeRequest(w, r, request.SiteId, "stats:read") {
		return
	}
	if problem := validateFormat(r); problem != nil {
		writeProblem(w, *problem)
		return
	}
	if all, problem := csvAllRequested(r); problem != nil {
		writeProblem(w, *problem)
		return
	} else if all {
		from, to := queryRangeLabels(r, request, h.currentTime())
		streamQueryCSV(w, r, h.query, request, csvFilename(csvSite(r, request.SiteId), "query", from, to))
		return
	}
	response, err := h.query.Query(r.Context(), request)
	if err != nil {
		writeQueryError(w, err)
		return
	}
	from, to := queryRangeLabels(r, request, h.currentTime())
	writeQueryResponse(w, r, response, csvFilename(csvSite(r, request.SiteId), "query", from, to))
}

func (h *handlers) funnelRequest(w http.ResponseWriter, r *http.Request) {
	if h.query == nil {
		writeProblem(w, internalProblem())
		return
	}
	var request FunnelRequest
	if problem := decodeJSON(w, r, &request); problem != nil {
		writeProblem(w, *problem)
		return
	}
	if !authorizeRequest(w, r, request.SiteId, "stats:read") {
		return
	}
	response, err := h.query.Funnel(r.Context(), request)
	if err != nil {
		writeQueryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) *Problem {
	r.Body = http.MaxBytesReader(w, r.Body, maxQueryBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		problem := badRequest("request body must be a valid JSON object")
		return &problem
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		problem := badRequest("request body must contain exactly one JSON object")
		return &problem
	}
	return nil
}

func writeQueryResponse(w http.ResponseWriter, r *http.Request, response QueryResponse, filename string) {
	if wantsCSV(r) {
		writeCSV(w, response, filename)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func acceptsCSV(r *http.Request) bool {
	for _, value := range strings.Split(r.Header.Get("Accept"), ",") {
		if strings.TrimSpace(strings.SplitN(value, ";", 2)[0]) == "text/csv" {
			return true
		}
	}
	return false
}

func wantsCSV(r *http.Request) bool {
	return acceptsCSV(r) || r.URL.Query().Get("format") == "csv"
}

func validateFormat(r *http.Request) *Problem {
	format := r.URL.Query().Get("format")
	if format == "" || format == "json" || format == "csv" {
		return nil
	}
	problem := badRequest("format must be json or csv")
	return &problem
}

func csvAllRequested(r *http.Request) (bool, *Problem) {
	if r.URL.Query().Get("limit") != "all" {
		return false, nil
	}
	if !wantsCSV(r) {
		problem := badRequest("limit=all requires a CSV response")
		return false, &problem
	}
	return true, nil
}

func (h *handlers) currentTime() time.Time {
	if h.now != nil {
		return h.now()
	}
	return time.Now()
}

func writeQueryError(w http.ResponseWriter, err error) {
	detail := err.Error()
	if isClientQueryError(detail) {
		writeProblem(w, badRequest(detail))
		return
	}
	if errors.Is(err, context.DeadlineExceeded) || strings.Contains(strings.ToLower(detail), "statement timeout") {
		writeProblem(w, newProblem(http.StatusServiceUnavailable, "https://panel.dev/problems/query-timeout", "Query timed out", "the query exceeded its execution time limit"))
		return
	}
	writeProblem(w, internalProblem())
}

func isClientQueryError(detail string) bool {
	prefixes := []string{
		"Web Vitals ", "a funnel requires", "at least one metric", "at most ", "date range ",
		"date_range.", "date_range or period", "filter ", "funnel date range",
		"funnel step ", "invalid cursor", "invalid funnel", "invalid timezone",
		"limit ", "order_by ", "provide either", "site_id ", "site timezone",
		"unknown comparison", "unknown dimension", "unknown filter", "unknown heatmap", "unknown metric",
		"unknown order", "unknown period", "unknown site", "unsupported type",
		"heatmap ", "invalid filter", "journey ", "retention ", "range must be within the raw retention window",
		"date range must ", "from ", "session timeline ",
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(detail, prefix) {
			return true
		}
	}
	return false
}
