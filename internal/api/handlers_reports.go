package api

import (
	"net/http"
	"strings"
)

func (h *handlers) journeyRequest(w http.ResponseWriter, r *http.Request) {
	if h.query == nil {
		writeProblem(w, internalProblem())
		return
	}
	var request JourneyRequest
	if problem := decodeJSON(w, r, &request); problem != nil {
		writeProblem(w, *problem)
		return
	}
	if !authorizeRequest(w, r, request.SiteId, "stats:read") {
		return
	}
	if problem := validateJourneyRequest(request); problem != nil {
		writeProblem(w, *problem)
		return
	}
	response, err := h.query.Journey(r.Context(), request)
	if err != nil {
		writeQueryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func validateJourneyRequest(request JourneyRequest) *Problem {
	if request.SiteId < 1 {
		return problemPointer(badRequest("site_id must be positive"))
	}
	if request.DateRange.From.Time.IsZero() || request.DateRange.To.Time.IsZero() {
		return problemPointer(badRequest("date_range is required"))
	}
	if request.DateRange.From.Time.After(request.DateRange.To.Time) {
		return problemPointer(badRequest("date_range.from must not be after date_range.to"))
	}
	if request.Steps < 2 || request.Steps > 5 {
		return problemPointer(badRequest("journey steps must be between 2 and 5"))
	}
	if request.Limit < 1 || request.Limit > 10 {
		return problemPointer(badRequest("journey limit must be between 1 and 10"))
	}
	if request.StartPath != nil && strings.TrimSpace(*request.StartPath) == "" {
		return problemPointer(badRequest("start_path must not be empty"))
	}
	if request.EndPath != nil && strings.TrimSpace(*request.EndPath) == "" {
		return problemPointer(badRequest("end_path must not be empty"))
	}
	if request.StartPath != nil && request.EndPath != nil {
		return problemPointer(badRequest("provide either start_path or end_path, not both"))
	}
	return nil
}

func (h *handlers) retentionRequest(w http.ResponseWriter, r *http.Request) {
	if h.query == nil {
		writeProblem(w, internalProblem())
		return
	}
	var request RetentionRequest
	if problem := decodeJSON(w, r, &request); problem != nil {
		writeProblem(w, *problem)
		return
	}
	if !authorizeRequest(w, r, request.SiteId, "stats:read") {
		return
	}
	if request.Identity != nil && *request.Identity == ProfileRetentionIdentity && !authorizeRequest(w, r, request.SiteId, "profiles:read") {
		return
	}
	if problem := validateRetentionRequest(request); problem != nil {
		writeProblem(w, *problem)
		return
	}
	response, err := h.query.Retention(r.Context(), request)
	if err != nil {
		writeQueryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func validateRetentionRequest(request RetentionRequest) *Problem {
	if request.SiteId < 1 {
		return problemPointer(badRequest("site_id must be positive"))
	}
	if request.DateRange.From.Time.IsZero() || request.DateRange.To.Time.IsZero() {
		return problemPointer(badRequest("date_range is required"))
	}
	if request.DateRange.From.Time.After(request.DateRange.To.Time) {
		return problemPointer(badRequest("date_range.from must not be after date_range.to"))
	}
	if !request.Interval.Valid() {
		return problemPointer(badRequest("retention interval must be day, week, or month"))
	}
	if request.Identity != nil && !request.Identity.Valid() {
		return problemPointer(badRequest("retention identity must be anonymous or profile"))
	}
	return nil
}

func problemPointer(problem Problem) *Problem { return &problem }
