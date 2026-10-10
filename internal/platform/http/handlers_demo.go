package platformhttp

import (
	"errors"
	"net/http"
	"time"

	"supermetry/internal/demo"
	"supermetry/internal/platform/authz"
	"supermetry/internal/platform/store"
)

func (h *handler) createDemo(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.privatePrincipal(w, r)
	if !ok {
		return
	}
	orgID, ok := orgIDFromPath(w, r, principal)
	if !ok {
		return
	}
	if !authz.Can(principal, authz.ActionSiteCreate, 0) {
		writeProblem(w, http.StatusForbidden, "Forbidden", "the principal cannot create sites")
		return
	}
	var request struct {
		Timezone string `json:"timezone"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if h.deps.Demo == nil {
		unavailable(w, "demo")
		return
	}
	created, err := h.deps.Demo.Create(r.Context(), principal, orgID, request.Timezone)
	switch {
	case errors.Is(err, store.ErrDemoExists):
		if h.deps.Store == nil {
			unavailable(w, "demo")
			return
		}
		existing, found, loadErr := store.DemoSiteForOrg(r.Context(), h.deps.Store.Pool, orgID)
		if loadErr != nil || !found {
			serviceError(w, loadErr)
			return
		}
		writeJSON(w, http.StatusConflict, map[string]any{"error": "demo_exists", "site_id": existing.PublicID})
		return
	case errors.Is(err, demo.ErrInvalidTimezone):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_timezone"})
		return
	case err != nil:
		serviceError(w, err)
		return
	}
	if h.deps.Sites == nil {
		unavailable(w, "site")
		return
	}
	site, found := h.deps.Sites.Site(created.ID)
	if !found {
		writeProblem(w, http.StatusInternalServerError, "Internal server error", "the demo site could not be loaded")
		return
	}
	writeJSON(w, http.StatusAccepted, h.responseSite(r, site))
}

func (h *handler) demoHeartbeat(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.privatePrincipal(w, r)
	if !ok {
		return
	}
	siteID, err := parseInt32(r.PathValue("site"))
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", "site must be a positive integer")
		return
	}
	_, site, ok := h.visibleSite(w, principal, siteID)
	if !ok {
		return
	}
	if !site.Demo {
		writeProblem(w, http.StatusNotFound, "Not found", "site not found")
		return
	}
	if h.deps.Demo == nil {
		unavailable(w, "demo")
		return
	}
	h.deps.Demo.Heartbeat(siteID)
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) dismissDemoPrompt(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.privatePrincipal(w, r)
	if !ok {
		return
	}
	if h.deps.Store == nil || principal.UserID == 0 {
		unavailable(w, "user")
		return
	}
	if err := store.DismissDemoPrompt(r.Context(), h.deps.Store.Pool, principal.UserID, time.Now()); err != nil {
		serviceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
