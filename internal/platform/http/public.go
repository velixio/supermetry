package platformhttp

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"supermetry/internal/api"
	"supermetry/internal/platform/authn"
	"supermetry/internal/platform/authz"
	"supermetry/internal/platform/shares"
	coresites "supermetry/internal/sites"
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
	mux.HandleFunc("GET /api/public/v1/erasures/{erasure}", h.getPublicErasure)
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
	principal, info, site, ok := h.resolveShareRequest(w, r)
	if !ok {
		return
	}
	var response any = map[string]any{"columns": []string{}, "rows": []any{}, "totals": map[string]float64{}}
	if shares.HasSection(info.Config, shares.SectionKPIs) {
		request, err := shareQueryRequest(info.Config, site.ID, site.TZ, h.deps.Now(), shares.SectionKPIs)
		if err != nil {
			serviceError(w, err)
			return
		}
		queried, err := h.deps.Query.Query(r.Context(), request)
		if err != nil {
			writeProblem(w, http.StatusBadRequest, "Bad request", err.Error())
			return
		}
		response = queried
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"sections":               info.Config.Sections,
		"theme":                  info.Config.Theme,
		"date_label":             shareDateLabel(info.Config.Date),
		"transparent_background": info.Config.Embed.Enabled,
		"data":                   response,
		"read_only":              principal.Role == authz.RoleShare,
	})
}

func (h *handler) shareQuery(w http.ResponseWriter, r *http.Request) {
	_, info, site, ok := h.resolveShareRequest(w, r)
	if !ok {
		return
	}
	var input struct {
		Section shares.Section `json:"section"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Section == shares.SectionKPIs || !shares.HasSection(info.Config, input.Section) {
		writeProblem(w, http.StatusForbidden, "Forbidden", "this section is not shared")
		return
	}
	request, err := shareQueryRequest(info.Config, site.ID, site.TZ, h.deps.Now(), input.Section)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", err.Error())
		return
	}
	response, err := h.deps.Query.Query(r.Context(), request)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *handler) resolveShareRequest(w http.ResponseWriter, r *http.Request) (authz.Principal, shares.ShareInfo, coresites.Site, bool) {
	var principal authz.Principal
	var info shares.ShareInfo
	if h.deps.Shares != nil {
		clientHash := sha256.Sum256([]byte(h.clientAddress(r)))
		limitContext := shares.WithClientKey(r.Context(), hex.EncodeToString(clientHash[:]))
		if !h.deps.Shares.AllowView(limitContext, r.PathValue("slug")) {
			w.Header().Set("Retry-After", "60")
			writeProblem(w, http.StatusTooManyRequests, "Rate limit exceeded", "retry after the indicated delay")
			return authz.Principal{}, shares.ShareInfo{}, coresites.Site{}, false
		}
		cookieValue := ""
		if cookie, err := r.Cookie(authn.ShareCookieName); err == nil {
			cookieValue = cookie.Value
		}
		var err error
		info, err = h.deps.Shares.ResolveShare(r.Context(), r.PathValue("slug"), cookieValue)
		if errors.Is(err, shares.ErrNotFound) {
			writeProblem(w, http.StatusNotFound, "Not found", "resource not found")
			return authz.Principal{}, shares.ShareInfo{}, coresites.Site{}, false
		}
		if errors.Is(err, shares.ErrPasswordRequired) {
			writeProblem(w, http.StatusUnauthorized, "Unauthorized", "share password required")
			return authz.Principal{}, shares.ShareInfo{}, coresites.Site{}, false
		}
		if err != nil {
			serviceError(w, err)
			return authz.Principal{}, shares.ShareInfo{}, coresites.Site{}, false
		}
		principal = authz.Principal{OrgID: info.OrgID, ShareID: info.ID, Role: authz.RoleShare, SiteIDs: []int32{info.SiteID}}
	} else {
		var ok bool
		principal, ok = h.principal(w, r)
		if !ok {
			return authz.Principal{}, shares.ShareInfo{}, coresites.Site{}, false
		}
		if len(principal.SiteIDs) != 1 {
			writeProblem(w, http.StatusNotFound, "Not found", "site not found")
			return authz.Principal{}, shares.ShareInfo{}, coresites.Site{}, false
		}
		info = shares.ShareInfo{ID: principal.ShareID, SiteID: principal.SiteIDs[0], Config: shares.LegacyConfig()}
	}
	if len(principal.SiteIDs) != 1 {
		writeProblem(w, http.StatusNotFound, "Not found", "site not found")
		return authz.Principal{}, shares.ShareInfo{}, coresites.Site{}, false
	}
	_, site, visible := h.visibleSite(w, principal, principal.SiteIDs[0])
	if !visible {
		return authz.Principal{}, shares.ShareInfo{}, coresites.Site{}, false
	}
	if principal.Role != authz.RoleShare || !authz.Can(principal, authz.ActionStatsRead, principal.SiteIDs[0]) {
		writeProblem(w, http.StatusForbidden, "Forbidden", "the credential cannot use this route")
		return authz.Principal{}, shares.ShareInfo{}, coresites.Site{}, false
	}
	if !authorizeSensitiveExport(w, r, principal, principal.SiteIDs[0]) {
		return authz.Principal{}, shares.ShareInfo{}, coresites.Site{}, false
	}
	if h.deps.Query == nil {
		unavailable(w, "query")
		return authz.Principal{}, shares.ShareInfo{}, coresites.Site{}, false
	}
	return principal, info, site, true
}

func shareQueryRequest(config shares.Config, siteID int32, location *time.Location, now time.Time, section shares.Section) (api.QueryRequest, error) {
	if location == nil {
		location = time.UTC
	}
	request := api.QueryRequest{SiteId: siteID}
	filters := append([]api.Filter(nil), config.Filters...)
	request.Filters = &filters
	from, to, err := shareDateRange(config.Date, location, now)
	if err != nil {
		return api.QueryRequest{}, err
	}
	request.DateRange = &api.DateRange{From: openapi_types.Date{Time: from}, To: openapi_types.Date{Time: to}}
	switch section {
	case shares.SectionKPIs:
		request.Metrics = []api.Metric{api.Visitors, api.Visits, api.Pageviews, api.ViewsPerVisit, api.BounceRate, api.VisitDuration, api.EngagementTime, api.ScrollDepth}
	case shares.SectionChart:
		request.Metrics = []api.Metric{api.Visitors, api.Pageviews}
		request.Dimensions = stringSlicePointer("time")
	case shares.SectionPages:
		request.Metrics = []api.Metric{api.Visitors, api.Pageviews}
		request.Dimensions = stringSlicePointer("page")
	case shares.SectionSources:
		request.Metrics = []api.Metric{api.Visitors, api.Visits}
		request.Dimensions = stringSlicePointer("source")
	case shares.SectionLocations, shares.SectionWhere:
		request.Metrics = []api.Metric{api.Visitors, api.Visits}
		request.Dimensions = stringSlicePointer("country")
	case shares.SectionDevices:
		request.Metrics = []api.Metric{api.Visitors, api.Visits}
		request.Dimensions = stringSlicePointer("device")
	case shares.SectionWeeklyRhythm:
		request.Metrics = []api.Metric{api.Visitors, api.Visits}
		request.Dimensions = stringSlicePointer("weekday", "hour_of_day")
	case shares.SectionGoals:
		request.Metrics = []api.Metric{api.Conversions}
		request.Dimensions = stringSlicePointer("goal")
	default:
		return api.QueryRequest{}, shares.ErrInvalid
	}
	return request, nil
}

func shareDateRange(config shares.DateConfig, location *time.Location, now time.Time) (time.Time, time.Time, error) {
	if config.Mode == shares.DateFixed {
		from, fromErr := time.Parse(time.DateOnly, config.From)
		to, toErr := time.Parse(time.DateOnly, config.To)
		if fromErr != nil || toErr != nil {
			return time.Time{}, time.Time{}, shares.ErrInvalid
		}
		return from, to, nil
	}
	local := now.In(location)
	to := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
	return to.AddDate(0, 0, 1-config.Days), to, nil
}

func shareDateLabel(config shares.DateConfig) string {
	if config.Mode == shares.DateFixed {
		return config.From + " – " + config.To
	}
	return "Last " + strconv.Itoa(config.Days) + " days"
}

func stringSlicePointer(values ...string) *[]string { return &values }

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
