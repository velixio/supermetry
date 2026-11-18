package platformhttp

import (
	"errors"
	"net/http"

	"supermetry/internal/appquery"
)

// getAppDeploys serves the deploy markers of an app project. Every member role
// may read them; the release query parameter is an exact match.
func (h *handler) getAppDeploys(w http.ResponseWriter, r *http.Request) {
	site, ok := h.appReadSite(w, r)
	if !ok {
		return
	}
	f, ok := appFilter(w, r, site, h.deps.Now())
	if !ok {
		return
	}
	rows, err := h.appQuerier().Deploys(r.Context(), appScope(site), f, f.Release)
	if err != nil {
		appQueryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deploys": rows})
}

// releaseQueryError maps a release read error; an unknown release is a 404.
func releaseQueryError(w http.ResponseWriter, err error) {
	if errors.Is(err, appquery.ErrNotFound) {
		writeProblem(w, http.StatusNotFound, "Not found", "release not found")
		return
	}
	appQueryError(w, err)
}

// getAppRelease serves one release: health, rollout, deploys and issues. The
// release is the path segment; the environment and platform filters apply.
func (h *handler) getAppRelease(w http.ResponseWriter, r *http.Request) {
	site, ok := h.appReadSite(w, r)
	if !ok {
		return
	}
	f, ok := appFilter(w, r, site, h.deps.Now())
	if !ok {
		return
	}
	release := r.PathValue("release")
	if release == "" || len(release) > maxReleaseParam {
		writeProblem(w, http.StatusBadRequest, "Bad request", "release is empty or too long")
		return
	}
	detail, err := h.appQuerier().ReleaseDetail(r.Context(), appScope(site), f, release)
	if err != nil {
		releaseQueryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// compareAppReleases serves two releases side by side (query a and b).
func (h *handler) compareAppReleases(w http.ResponseWriter, r *http.Request) {
	site, ok := h.appReadSite(w, r)
	if !ok {
		return
	}
	f, ok := appFilter(w, r, site, h.deps.Now())
	if !ok {
		return
	}
	a, b := r.URL.Query().Get("a"), r.URL.Query().Get("b")
	if a == "" || b == "" || len(a) > maxReleaseParam || len(b) > maxReleaseParam {
		writeProblem(w, http.StatusBadRequest, "Bad request", "a and b must be release names")
		return
	}
	out, err := h.appQuerier().CompareReleases(r.Context(), appScope(site), f, a, b)
	if err != nil {
		releaseQueryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
