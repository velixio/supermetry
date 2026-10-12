package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"
)

var (
	// ErrCohortInput marks a cohort or power-user request the caller can correct.
	ErrCohortInput = errors.New("invalid cohort request")
	// ErrCohortNotFound means the cohort does not exist for the site.
	ErrCohortNotFound = errors.New("cohort not found")
	// ErrCohortsUnavailable means person-level audiences are off for the site.
	ErrCohortsUnavailable = errors.New("cohorts and power users need identified tracking with profiles enabled; anonymous mode keeps no person-level history")
)

type cohortService interface {
	EvaluateCohort(ctx context.Context, siteID int32, cohortID int64, definition *CohortDefinition, sampleLimit int, includeSample bool) (CohortEvaluation, error)
	PowerUsers(ctx context.Context, siteID int32, from, to time.Time, metric string, minimum, limit int) (PowerUsers, error)
}

func writeCohortError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrCohortsUnavailable):
		writeProblem(w, forbidden(ErrCohortsUnavailable.Error()))
	case errors.Is(err, ErrCohortNotFound):
		writeProblem(w, notFound("cohort not found"))
	case errors.Is(err, ErrCohortInput):
		writeProblem(w, badRequest(err.Error()))
	default:
		writeQueryError(w, err)
	}
}

func (h *handlers) evaluateCohort(w http.ResponseWriter, r *http.Request) {
	siteValue, err := strconv.ParseInt(r.PathValue("site"), 10, 32)
	if err != nil || siteValue < 1 {
		writeProblem(w, badRequest("site must be a positive integer"))
		return
	}
	siteID := int32(siteValue)
	if !authorizeRequest(w, r, siteID, "stats:read") {
		return
	}
	if h.cohorts == nil {
		writeProblem(w, internalProblem())
		return
	}
	var request CohortEvaluateRequest
	if problem := decodeJSON(w, r, &request); problem != nil {
		writeProblem(w, *problem)
		return
	}
	var cohortID int64
	if request.CohortId != nil {
		cohortID = *request.CohortId
	}
	sample := 0
	if request.SampleLimit != nil {
		sample = *request.SampleLimit
	}
	result, err := h.cohorts.EvaluateCohort(r.Context(), siteID, cohortID, request.Definition, sample, requestHasScope(r, "profiles:read"))
	if err != nil {
		writeCohortError(w, err)
		return
	}
	if result.Sample == nil {
		result.Sample = []CohortMember{}
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *handlers) listPowerUsers(w http.ResponseWriter, r *http.Request) {
	siteID, from, to, ok := parseSiteAndRange(w, r)
	if !ok || !authorizeRequest(w, r, siteID, "profiles:read") {
		return
	}
	if h.cohorts == nil {
		writeProblem(w, internalProblem())
		return
	}
	minimum, ok := optionalInt(w, r, "min")
	if !ok {
		return
	}
	limit, ok := optionalInt(w, r, "limit")
	if !ok {
		return
	}
	result, err := h.cohorts.PowerUsers(r.Context(), siteID, from, to, r.URL.Query().Get("metric"), minimum, limit)
	if err != nil {
		writeCohortError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
