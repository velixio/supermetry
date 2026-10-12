package platformhttp

import (
	"errors"
	"net/http"

	"supermetry/internal/api"
	"supermetry/internal/platform/authz"
	"supermetry/internal/platform/cohorts"
)

type cohortInput struct {
	Name       string               `json:"name"`
	Definition api.CohortDefinition `json:"definition"`
}

func (h *handler) cohortService(w http.ResponseWriter) (*cohorts.Service, bool) {
	if h.deps.Store == nil {
		unavailable(w, "cohort")
		return nil, false
	}
	return cohorts.New(h.deps.Store), true
}

func (h *handler) listCohorts(w http.ResponseWriter, r *http.Request) {
	_, site, ok := h.sitePrincipal(w, r, authz.ActionStatsRead, h.resourceRoles(r)...)
	if !ok {
		return
	}
	service, ok := h.cohortService(w)
	if !ok {
		return
	}
	items, err := service.List(r.Context(), site.ID)
	if err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, api.CohortList{Data: items})
}

func (h *handler) createCohort(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.sitePrincipal(w, r, authz.ActionSegmentsEdit, h.resourceRoles(r)...)
	if !ok {
		return
	}
	service, ok := h.cohortService(w)
	if !ok {
		return
	}
	var input cohortInput
	if !decodeJSON(w, r, &input) {
		return
	}
	created, err := service.Create(r.Context(), principal, site.ID, input.Name, input.Definition)
	if err != nil {
		cohortError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *handler) updateCohort(w http.ResponseWriter, r *http.Request) {
	principal, cohort, ok := h.cohortPrincipal(w, r)
	if !ok {
		return
	}
	var input cohortInput
	if !decodeJSON(w, r, &input) {
		return
	}
	updated, err := cohorts.New(h.deps.Store).Update(r.Context(), principal, cohort.Id, input.Name, input.Definition)
	if err != nil {
		cohortError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *handler) deleteCohort(w http.ResponseWriter, r *http.Request) {
	principal, cohort, ok := h.cohortPrincipal(w, r)
	if !ok {
		return
	}
	if err := cohorts.New(h.deps.Store).Delete(r.Context(), principal, cohort.Id); err != nil {
		cohortError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) duplicateCohort(w http.ResponseWriter, r *http.Request) {
	principal, cohort, ok := h.cohortPrincipal(w, r)
	if !ok {
		return
	}
	copied, err := cohorts.New(h.deps.Store).Duplicate(r.Context(), principal, cohort.Id)
	if err != nil {
		cohortError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, copied)
}

// cohortPrincipal resolves a cohort by id and authorizes a write against its
// site. Unknown ids and cohorts of sites the caller cannot see are both 404.
func (h *handler) cohortPrincipal(w http.ResponseWriter, r *http.Request) (authz.Principal, api.Cohort, bool) {
	principal, ok := h.principal(w, r)
	if !ok {
		return authz.Principal{}, api.Cohort{}, false
	}
	id, err := parseInt64(r.PathValue("cohort"))
	if err != nil || h.deps.Store == nil {
		writeProblem(w, http.StatusNotFound, "Not found", "cohort not found")
		return authz.Principal{}, api.Cohort{}, false
	}
	cohort, err := cohorts.New(h.deps.Store).Get(r.Context(), id)
	if errors.Is(err, cohorts.ErrNotFound) {
		writeProblem(w, http.StatusNotFound, "Not found", "cohort not found")
		return authz.Principal{}, api.Cohort{}, false
	}
	if err != nil {
		serviceError(w, err)
		return authz.Principal{}, api.Cohort{}, false
	}
	if _, _, ok := h.visibleSite(w, principal, cohort.SiteId); !ok {
		return authz.Principal{}, api.Cohort{}, false
	}
	if !roleAllowed(principal.Role, h.resourceRoles(r)) || !authz.Can(principal, authz.ActionSegmentsEdit, cohort.SiteId) {
		writeProblem(w, http.StatusForbidden, "Forbidden", "the principal cannot perform this action")
		return authz.Principal{}, api.Cohort{}, false
	}
	return principal, cohort, true
}

func cohortError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, cohorts.ErrInvalid):
		writeProblem(w, http.StatusBadRequest, "Bad request", err.Error())
	case errors.Is(err, cohorts.ErrNotFound):
		writeProblem(w, http.StatusNotFound, "Not found", "cohort not found")
	case errors.Is(err, cohorts.ErrNameTaken):
		writeProblem(w, http.StatusConflict, "Conflict", "a cohort with this name already exists")
	case errors.Is(err, cohorts.ErrLimit):
		writeProblem(w, http.StatusConflict, "Conflict", err.Error())
	default:
		serviceError(w, err)
	}
}
