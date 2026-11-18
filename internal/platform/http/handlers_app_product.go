package platformhttp

import (
	"errors"
	"net/http"
	"strconv"

	"supermetry/internal/appquery"
	"supermetry/internal/platform/audit"
	"supermetry/internal/platform/authz"
	coresites "supermetry/internal/sites"

	"github.com/jackc/pgx/v5"
)

// The product reads answer app projects' active users, adoption splits,
// engagement and feature adoption. They return counts only: no install ids,
// hashes or profile ids leave the server.

var appAdoptionDimensions = map[string]bool{"release": true, "platform": true, "country": true, "os": true, "device": true}

// appProductReadSite resolves an app project for a product read. Every member
// role and an API key with stats:read may read; funnels, retention and
// journeys keep their own endpoints.
func (h *handler) appProductReadSite(w http.ResponseWriter, r *http.Request) (coresites.Site, bool) {
	roles := append(linkRoles(), authz.RoleKey)
	_, site, ok := h.appHealthSite(w, r, authz.ActionStatsRead, roles...)
	return site, ok
}

func appProductError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, appquery.ErrNotFound):
		writeProblem(w, http.StatusNotFound, "Not found", "feature set not found")
	case errors.Is(err, appquery.ErrDuplicate):
		writeProblem(w, http.StatusConflict, "Conflict", "a feature set with this name already exists")
	default:
		appQueryError(w, err)
	}
}

func (h *handler) getAppActiveUsers(w http.ResponseWriter, r *http.Request) {
	site, ok := h.appProductReadSite(w, r)
	if !ok {
		return
	}
	f, ok := appFilter(w, r, site, h.deps.Now())
	if !ok {
		return
	}
	out, err := h.appQuerier().ActiveUsers(r.Context(), appScope(site), f)
	if err != nil {
		appProductError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *handler) getAppAdoption(w http.ResponseWriter, r *http.Request) {
	site, ok := h.appProductReadSite(w, r)
	if !ok {
		return
	}
	by := r.URL.Query().Get("by")
	if !appAdoptionDimensions[by] {
		writeProblem(w, http.StatusBadRequest, "Bad request", "by must be release, platform, country, os or device")
		return
	}
	f, ok := appFilter(w, r, site, h.deps.Now())
	if !ok {
		return
	}
	q := h.appQuerier()
	series, err := q.Adoption(r.Context(), appScope(site), f, by)
	if err != nil {
		appProductError(w, err)
		return
	}
	totals, err := q.AdoptionTotals(r.Context(), appScope(site), f, by)
	if err != nil {
		appProductError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"by": by, "series": series, "totals": totals})
}

func (h *handler) getAppEngagement(w http.ResponseWriter, r *http.Request) {
	site, ok := h.appProductReadSite(w, r)
	if !ok {
		return
	}
	f, ok := appFilter(w, r, site, h.deps.Now())
	if !ok {
		return
	}
	out, err := h.appQuerier().Engagement(r.Context(), appScope(site), f)
	if err != nil {
		appProductError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *handler) listAppFeatures(w http.ResponseWriter, r *http.Request) {
	site, ok := h.appProductReadSite(w, r)
	if !ok {
		return
	}
	sets, err := h.appQuerier().ListFeatureSets(r.Context(), appScope(site))
	if err != nil {
		appProductError(w, err)
		return
	}
	if sets == nil {
		sets = []appquery.FeatureSet{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"features": sets})
}

func (h *handler) createAppFeature(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.appHealthSite(w, r, authz.ActionSiteEdit, authz.RoleOwner, authz.RoleAdmin, authz.RoleEditor)
	if !ok {
		return
	}
	var in struct {
		Name       string   `json:"name"`
		EventNames []string `json:"event_names"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	set, err := h.appQuerier().CreateFeatureSet(r.Context(), appScope(site), in.Name, in.EventNames)
	if err != nil {
		appProductError(w, err)
		return
	}
	if err := h.deps.Store.Tx(r.Context(), func(tx pgx.Tx) error {
		return audit.Write(r.Context(), tx, principal, "site.app_feature_created", "site", int64(site.ID),
			map[string]any{"feature": set.ID, "events": len(set.EventNames)})
	}); err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, set)
}

func (h *handler) deleteAppFeature(w http.ResponseWriter, r *http.Request) {
	principal, site, ok := h.appHealthSite(w, r, authz.ActionSiteEdit, authz.RoleOwner, authz.RoleAdmin, authz.RoleEditor)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeProblem(w, http.StatusBadRequest, "Bad request", "id must be a positive integer")
		return
	}
	if err := h.appQuerier().DeleteFeatureSet(r.Context(), appScope(site), id); err != nil {
		appProductError(w, err)
		return
	}
	if err := h.deps.Store.Tx(r.Context(), func(tx pgx.Tx) error {
		return audit.Write(r.Context(), tx, principal, "site.app_feature_deleted", "site", int64(site.ID), map[string]any{"feature": id})
	}); err != nil {
		serviceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) getAppFeatureAdoption(w http.ResponseWriter, r *http.Request) {
	site, ok := h.appProductReadSite(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeProblem(w, http.StatusBadRequest, "Bad request", "id must be a positive integer")
		return
	}
	f, ok := appFilter(w, r, site, h.deps.Now())
	if !ok {
		return
	}
	out, err := h.appQuerier().FeatureAdoption(r.Context(), appScope(site), f, id)
	if err != nil {
		appProductError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
