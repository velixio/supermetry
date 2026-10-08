package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"
)

const maxSessionLimit = 1000

var (
	ErrSessionNotFound   = errors.New("session not found")
	ErrSessionRawExpired = errors.New("session timeline is beyond the raw retention window")
)

type sessionExploreService interface {
	Sessions(context.Context, int32, ListSessionsParams, []Filter) (SessionList, error)
	Session(context.Context, int32, int64) (SessionDetail, error)
}

func (h *handlers) listSessions(w http.ResponseWriter, r *http.Request) {
	siteValue, err := strconv.ParseInt(r.PathValue("site"), 10, 32)
	if err != nil || siteValue < 1 {
		writeProblem(w, badRequest("site must be a positive integer"))
		return
	}
	siteID := int32(siteValue)
	if !authorizeRequest(w, r, siteID, "stats:read") {
		return
	}
	if h.sessions == nil {
		writeProblem(w, internalProblem())
		return
	}
	from, errFrom := time.Parse(time.DateOnly, r.URL.Query().Get("from"))
	to, errTo := time.Parse(time.DateOnly, r.URL.Query().Get("to"))
	if errFrom != nil || errTo != nil {
		writeProblem(w, badRequest("from and to must be valid dates"))
		return
	}
	if from.After(to) {
		writeProblem(w, badRequest("from must not be after to"))
		return
	}
	params := ListSessionsParams{
		From: openapi_types.Date{Time: from},
		To:   openapi_types.Date{Time: to},
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, parseErr := strconv.Atoi(raw)
		if parseErr != nil || limit < 1 || limit > maxSessionLimit {
			writeProblem(w, badRequest("limit must be between 1 and 1000"))
			return
		}
		value := Limit(limit)
		params.Limit = &value
	}
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		value := Cursor(raw)
		params.Cursor = &value
	}
	if raw := r.URL.Query().Get("has_replay"); raw != "" {
		value, parseErr := strconv.ParseBool(raw)
		if parseErr != nil {
			writeProblem(w, badRequest("has_replay must be true or false"))
			return
		}
		params.HasReplay = &value
	}
	filters, problem := parseStatsFilters(r.URL.Query()["filter"])
	if problem != nil {
		writeProblem(w, *problem)
		return
	}
	response, err := h.sessions.Sessions(r.Context(), siteID, params, filters)
	if err != nil {
		writeSessionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *handlers) getSession(w http.ResponseWriter, r *http.Request) {
	siteID, problem := siteIDParam(r)
	if problem != nil {
		writeProblem(w, *problem)
		return
	}
	if !authorizeRequest(w, r, siteID, "stats:read") {
		return
	}
	if h.sessions == nil {
		writeProblem(w, internalProblem())
		return
	}
	sessionID, err := strconv.ParseInt(r.PathValue("session"), 10, 64)
	if err != nil || sessionID < 1 {
		writeProblem(w, badRequest("session must be a positive integer"))
		return
	}
	response, err := h.sessions.Session(r.Context(), siteID, sessionID)
	if err != nil {
		writeSessionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func writeSessionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrSessionNotFound):
		writeProblem(w, notFound("session not found"))
	case errors.Is(err, ErrSessionRawExpired):
		writeProblem(w, badRequest(ErrSessionRawExpired.Error()))
	default:
		writeQueryError(w, err)
	}
}
