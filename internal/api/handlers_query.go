package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

const maxQueryBodyBytes = 1 << 20

type queryService interface {
	Query(context.Context, QueryRequest) (QueryResponse, error)
	Funnel(context.Context, FunnelRequest) (FunnelResponse, error)
}

type handlers struct {
	query queryService
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
	response, err := h.query.Query(r.Context(), request)
	if err != nil {
		writeQueryError(w, err)
		return
	}
	writeQueryResponse(w, r, response)
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

func writeQueryResponse(w http.ResponseWriter, r *http.Request, response QueryResponse) {
	if acceptsCSV(r) {
		writeCSV(w, response)
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
		"unknown comparison", "unknown dimension", "unknown filter", "unknown metric",
		"unknown order", "unknown period", "unknown site", "unsupported type",
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(detail, prefix) {
			return true
		}
	}
	return false
}
