package platformhttp

import (
	"net/http"
	"strconv"

	"panel/internal/api"
	"panel/internal/platform/authz"
)

func (h *handler) registerPublic(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/public/v1/api-keys", h.listPublicAPIKeys)
	mux.HandleFunc("POST /api/public/v1/api-keys", h.createPublicAPIKey)
	mux.HandleFunc("DELETE /api/public/v1/api-keys/{key}", h.revokePublicAPIKey)
	mux.HandleFunc("GET /api/public/v1/sites", h.listSites)
	mux.HandleFunc("POST /api/public/v1/sites", h.createSite)
	mux.HandleFunc("GET /api/public/v1/sites/{site}", h.getSite)
	mux.HandleFunc("PATCH /api/public/v1/sites/{site}", h.updateSite)
	mux.HandleFunc("DELETE /api/public/v1/sites/{site}", h.deleteSite)
	mux.HandleFunc("GET /api/public/v1/sites/{site}/goals", h.listGoals)
	mux.HandleFunc("GET /api/public/v1/sites/{site}/annotations", h.listAnnotations)
	mux.HandleFunc("POST /api/public/v1/sites/{site}/annotations", h.createAnnotation)
	mux.HandleFunc("PATCH /api/public/v1/annotations/{annotation}", h.updateAnnotation)
	mux.HandleFunc("DELETE /api/public/v1/annotations/{annotation}", h.deleteAnnotation)
	mux.HandleFunc("POST /api/public/v1/sites/{site}/goals", h.createGoal)
	mux.HandleFunc("PATCH /api/public/v1/goals/{goal}", h.updateGoal)
	mux.HandleFunc("DELETE /api/public/v1/goals/{goal}", h.deleteGoal)
	mux.HandleFunc("GET /api/public/v1/sites/{site}/funnels", h.listFunnels)
	mux.HandleFunc("POST /api/public/v1/sites/{site}/funnels", h.createFunnel)
	mux.HandleFunc("PATCH /api/public/v1/funnels/{funnel}", h.updateFunnel)
	mux.HandleFunc("DELETE /api/public/v1/funnels/{funnel}", h.deleteFunnel)
	mux.HandleFunc("GET /api/public/v1/sites/{site}/segments", h.listSegments)
	mux.HandleFunc("POST /api/public/v1/sites/{site}/segments", h.createSegment)
	mux.HandleFunc("PATCH /api/public/v1/segments/{segment}", h.updateSegment)
	mux.HandleFunc("DELETE /api/public/v1/segments/{segment}", h.deleteSegment)
	mux.HandleFunc("GET /api/public/v1/sites/{site}/profiles", h.listProfiles)
	mux.HandleFunc("GET /api/public/v1/profiles/{profile}", h.getProfile)
	mux.HandleFunc("GET /api/public/v1/stats/summary", h.publicStats)
}

func (h *handler) publicStats(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.principal(w, r)
	if !ok {
		return
	}
	siteID, err := strconv.ParseInt(r.URL.Query().Get("site_id"), 10, 32)
	if err != nil || siteID <= 0 {
		writeProblem(w, http.StatusBadRequest, "Bad request", "site_id must be a positive integer")
		return
	}
	if _, _, ok := h.visibleSite(w, principal, int32(siteID)); !ok {
		return
	}
	if principal.Role != authz.RoleKey || !authz.Can(principal, authz.ActionStatsRead, int32(siteID)) {
		writeProblem(w, http.StatusForbidden, "Forbidden", "the credential cannot use this route")
		return
	}
	h.statsResponse(w, r, int32(siteID))
}

func (h *handler) shareStats(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.principal(w, r)
	if !ok {
		return
	}
	if len(principal.SiteIDs) != 1 {
		writeProblem(w, http.StatusNotFound, "Not found", "site not found")
		return
	}
	if _, _, ok := h.visibleSite(w, principal, principal.SiteIDs[0]); !ok {
		return
	}
	if principal.Role != authz.RoleShare || !authz.Can(principal, authz.ActionStatsRead, principal.SiteIDs[0]) {
		writeProblem(w, http.StatusForbidden, "Forbidden", "the credential cannot use this route")
		return
	}
	h.statsResponse(w, r, principal.SiteIDs[0])
}

func (h *handler) statsResponse(w http.ResponseWriter, r *http.Request, siteID int32) {
	if h.deps.Query == nil {
		unavailable(w, "query")
		return
	}
	period := api.Period(r.URL.Query().Get("period"))
	if !period.Valid() {
		writeProblem(w, http.StatusBadRequest, "Bad request", "period is invalid")
		return
	}
	response, err := h.deps.Query.Query(r.Context(), api.QueryRequest{
		SiteId: siteID,
		Period: &period,
		Metrics: []api.Metric{
			api.Visitors, api.Visits, api.Pageviews, api.ViewsPerVisit,
			api.BounceRate, api.VisitDuration, api.EngagementTime, api.ScrollDepth,
		},
	})
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, response)
}
