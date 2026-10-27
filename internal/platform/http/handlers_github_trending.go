package platformhttp

import (
	"errors"
	"net/http"

	"supermetry/internal/ghtrending"
	"supermetry/internal/platform/authz"
)

// orgGetStarsTrending serves the watcher status, current episodes and history
// of one repository to any member of the organization that owns it. A
// repository of another organization answers 404.
func (h *handler) orgGetStarsTrending(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.orgManager(w, r, authz.ActionGitHubStarsRead)
	if !ok {
		return
	}
	svc, ok := h.trendingService(w)
	if !ok {
		return
	}
	id, valid := adminID(r, "id")
	if !valid {
		writeProblem(w, http.StatusBadRequest, "Bad request", "repository id is invalid")
		return
	}
	status, err := svc.Status(r.Context(), principal.OrgID, id)
	if err != nil {
		trendingError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (h *handler) trendingService(w http.ResponseWriter) (*ghtrending.Service, bool) {
	if h.deps.GitHubTrending == nil {
		writeProblem(w, http.StatusNotFound, "Not found", "GitHub trending watching is unavailable")
		return nil, false
	}
	return h.deps.GitHubTrending, true
}

// setTrendingEnabled applies the trending_enabled field of the repository
// PATCH, which the caller has already authorized as an owner or admin of the
// organization. It reports false after writing an error response.
func (h *handler) setTrendingEnabled(w http.ResponseWriter, r *http.Request, orgID, repoID int64, enabled bool) bool {
	svc, ok := h.trendingService(w)
	if !ok {
		return false
	}
	if err := svc.SetEnabled(r.Context(), orgID, repoID, enabled); err != nil {
		trendingError(w, err)
		return false
	}
	return true
}

func trendingError(w http.ResponseWriter, err error) {
	if errors.Is(err, ghtrending.ErrNotFound) {
		writeProblem(w, http.StatusNotFound, "Not found", "resource not found")
		return
	}
	serviceError(w, err)
}
