package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type exploreService interface {
	Events(context.Context, EventExploreRequest) (EventExploreList, error)
	Properties(context.Context, PropertyExploreRequest) ([]PropertyKeyCount, error)
	PropertyValues(context.Context, PropertyValueExploreRequest) ([]PropertyValueCount, error)
	Suggestions(context.Context, SuggestionExploreRequest) ([]Suggestion, error)
}

type exploreHandlers struct{ service exploreService }

func registerExplore(mux *http.ServeMux, prefix string, service exploreService) {
	h := exploreHandlers{service: service}
	mux.HandleFunc("GET "+prefix+"/sites/{site}/events", h.events)
	mux.HandleFunc("GET "+prefix+"/sites/{site}/properties", h.properties)
	mux.HandleFunc("GET "+prefix+"/sites/{site}/properties/{key}/values", h.propertyValues)
	mux.HandleFunc("GET "+prefix+"/sites/{site}/suggestions", h.suggestions)
}

func (h exploreHandlers) events(w http.ResponseWriter, r *http.Request) {
	siteID, from, to, ok := exploreRequest(w, r)
	if !ok {
		return
	}
	filters, problem := parseStatsFilters(r.URL.Query()["filter"])
	if problem != nil {
		writeProblem(w, *problem)
		return
	}
	for _, filter := range filters {
		if !exploreDimensionAllowed(filter.Dimension) {
			writeProblem(w, badRequest("filter dimension "+strconv.Quote(filter.Dimension)+" is not available for events"))
			return
		}
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			writeProblem(w, badRequest("limit must be between 1 and 100"))
			return
		}
		limit = parsed
	}
	var cursor *string
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		cursor = &raw
	}
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if len(name) > 2000 {
		writeProblem(w, badRequest("name must not exceed 2000 characters"))
		return
	}
	response, err := h.service.Events(r.Context(), EventExploreRequest{
		SiteID: siteID, From: from, To: to, Name: name,
		Filters: filters, Cursor: cursor, Limit: limit,
	})
	if err != nil {
		writeQueryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h exploreHandlers) properties(w http.ResponseWriter, r *http.Request) {
	siteID, from, to, ok := exploreRequest(w, r)
	if !ok {
		return
	}
	event, ok := exploreEvent(w, r)
	if !ok {
		return
	}
	response, err := h.service.Properties(r.Context(), PropertyExploreRequest{
		SiteID: siteID, From: from, To: to, Event: event,
	})
	if err != nil {
		writeQueryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h exploreHandlers) propertyValues(w http.ResponseWriter, r *http.Request) {
	siteID, from, to, ok := exploreRequest(w, r)
	if !ok {
		return
	}
	key := strings.TrimSpace(r.PathValue("key"))
	if key == "" || len(key) > 2000 {
		writeProblem(w, badRequest("property key must be between 1 and 2000 characters"))
		return
	}
	event, ok := exploreEvent(w, r)
	if !ok {
		return
	}
	response, err := h.service.PropertyValues(r.Context(), PropertyValueExploreRequest{
		SiteID: siteID, Key: key, From: from, To: to, Event: event,
	})
	if err != nil {
		writeQueryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h exploreHandlers) suggestions(w http.ResponseWriter, r *http.Request) {
	siteID, from, to, ok := exploreRequest(w, r)
	if !ok {
		return
	}
	dimension := strings.TrimSpace(r.URL.Query().Get("dimension"))
	if dimension == "" {
		writeProblem(w, badRequest("dimension is required"))
		return
	}
	if !exploreDimensionAllowed(dimension) {
		writeProblem(w, badRequest("dimension "+strconv.Quote(dimension)+" is not available for suggestions"))
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(query) > 2000 {
		writeProblem(w, badRequest("q must not exceed 2000 characters"))
		return
	}
	response, err := h.service.Suggestions(r.Context(), SuggestionExploreRequest{
		SiteID: siteID, Dimension: dimension, Query: query, From: from, To: to,
	})
	if err != nil {
		writeQueryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

// exploreDimensionAllowed mirrors the dimensions the raw events table can
// answer. Property dimensions need a non-empty key of bounded length.
func exploreDimensionAllowed(dimension string) bool {
	if key, ok := strings.CutPrefix(dimension, "prop:"); ok {
		return strings.TrimSpace(key) != "" && len(key) <= 2000
	}
	switch dimension {
	case "page", "path", "source", "utm_source", "utm_medium", "utm_campaign", "utm_content", "utm_term",
		"event", "country", "region", "city", "browser", "os", "device":
		return true
	}
	return false
}

func exploreRequest(w http.ResponseWriter, r *http.Request) (int32, time.Time, time.Time, bool) {
	parsedSite, err := strconv.ParseInt(r.PathValue("site"), 10, 32)
	if err != nil || parsedSite < 1 {
		writeProblem(w, badRequest("site must be a positive integer"))
		return 0, time.Time{}, time.Time{}, false
	}
	siteID := int32(parsedSite)
	if !authorizeRequest(w, r, siteID, "stats:read") {
		return 0, time.Time{}, time.Time{}, false
	}
	from, fromErr := time.Parse("2006-01-02", r.URL.Query().Get("from"))
	to, toErr := time.Parse("2006-01-02", r.URL.Query().Get("to"))
	if fromErr != nil || toErr != nil || from.After(to) {
		writeProblem(w, badRequest("from and to must be valid dates in ascending order"))
		return 0, time.Time{}, time.Time{}, false
	}
	return siteID, from, to, true
}

func exploreEvent(w http.ResponseWriter, r *http.Request) (string, bool) {
	event := strings.TrimSpace(r.URL.Query().Get("event"))
	if len(event) > 2000 {
		writeProblem(w, badRequest("event must not exceed 2000 characters"))
		return "", false
	}
	return event, true
}
