package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"supermetry/internal/appquery"
	"supermetry/internal/sites"
)

// appHealthReader is the part of appquery.Querier the MCP App Health tools use.
type appHealthReader interface {
	Overview(ctx context.Context, scope appquery.Scope, f appquery.Filter) (appquery.Overview, error)
	Issues(ctx context.Context, scope appquery.Scope, in appquery.IssueQuery) (appquery.IssueList, error)
	Issue(ctx context.Context, scope appquery.Scope, id int64, f appquery.Filter) (appquery.IssueDetail, error)
	Compatibility(ctx context.Context, scope appquery.Scope, f appquery.Filter) ([]appquery.CompatRow, error)
	Releases(ctx context.Context, scope appquery.Scope, f appquery.Filter) ([]appquery.ReleaseRow, error)
}

var _ appHealthReader = (*appquery.Querier)(nil)

const (
	mcpAppMaxRelease     = 64
	mcpAppMaxEnvironment = 32
	mcpAppMaxPlatform    = 32
	mcpAppMaxKind        = 32
	mcpAppMaxQuery       = 100
	mcpAppMaxLatest      = 5
	mcpAppMaxRangeDays   = 366
)

var (
	mcpAppToolNames = map[string]bool{
		"get_app_overview": true, "list_app_issues": true, "get_app_issue": true,
		"get_app_compatibility": true, "list_app_releases": true,
	}
	mcpWebToolNames = map[string]bool{
		"get_summary": true, "get_timeseries": true, "get_breakdown": true, "get_realtime": true, "run_funnel": true,
	}
)

type mcpAppRange struct {
	Site     int32  `json:"site"`
	Period   Period `json:"period,omitempty"`
	DateFrom string `json:"date_from,omitempty"`
	DateTo   string `json:"date_to,omitempty"`
}

type mcpAppFilterInput struct {
	mcpAppRange
	Release     string `json:"release,omitempty"`
	Environment string `json:"environment,omitempty"`
	Platform    string `json:"platform,omitempty"`
}

type mcpAppIssuesInput struct {
	mcpAppFilterInput
	Status string `json:"status,omitempty"`
	Kind   string `json:"kind,omitempty"`
	Q      string `json:"q,omitempty"`
	Sort   string `json:"sort,omitempty"`
	Limit  int    `json:"limit,omitempty"`
	Offset int    `json:"offset,omitempty"`
}

type mcpAppIssueInput struct {
	mcpAppFilterInput
	IssueID int64 `json:"issue_id"`
}

type mcpAppCompatInput struct {
	mcpAppRange
	Platform string `json:"platform,omitempty"`
}

type mcpAppReleasesInput struct {
	mcpAppRange
	Environment string `json:"environment,omitempty"`
	Platform    string `json:"platform,omitempty"`
}

// mcpAppScope is the appquery scope of a site: its time zone and retention.
func mcpAppScope(site sites.Site) appquery.Scope {
	return appquery.Scope{SiteID: site.ID, TZ: site.TZ, RetentionDays: int(site.Retention)}
}

// filter turns the period or date inputs into an inclusive date range in the
// site's time zone.
func (in mcpAppRange) filter(loc *time.Location, now time.Time) (appquery.Filter, error) {
	if in.Period != "" && (in.DateFrom != "" || in.DateTo != "") {
		return appquery.Filter{}, invalidMCPParams{detail: "provide either period or date_from/date_to"}
	}
	if in.Period != "" {
		if !in.Period.Valid() || in.Period == Custom {
			return appquery.Filter{}, invalidMCPParams{detail: "period is invalid"}
		}
		from, to, err := mcpPeriodRange(in.Period, loc, now)
		if err != nil {
			return appquery.Filter{}, err
		}
		return appquery.Filter{From: from, To: to}, nil
	}
	if in.DateFrom == "" || in.DateTo == "" {
		return appquery.Filter{}, invalidMCPParams{detail: "period or date_from/date_to is required"}
	}
	from, fromErr := time.Parse(time.DateOnly, in.DateFrom)
	to, toErr := time.Parse(time.DateOnly, in.DateTo)
	if fromErr != nil || toErr != nil || from.After(to) {
		return appquery.Filter{}, invalidMCPParams{detail: "date_from/date_to must be valid inclusive dates"}
	}
	if to.Sub(from) > mcpAppMaxRangeDays*24*time.Hour {
		return appquery.Filter{}, invalidMCPParams{detail: "date range must be at most 366 days"}
	}
	return appquery.Filter{From: from, To: to}, nil
}

func mcpAppDimensions(f appquery.Filter, release, environment, platform string) (appquery.Filter, error) {
	for _, p := range []struct {
		name  string
		value string
		max   int
	}{{"release", release, mcpAppMaxRelease}, {"environment", environment, mcpAppMaxEnvironment}, {"platform", platform, mcpAppMaxPlatform}} {
		if len(p.value) > p.max {
			return f, invalidMCPParams{detail: p.name + " is too long"}
		}
	}
	f.Release, f.Environment, f.Platform = release, environment, platform
	return f, nil
}

// mcpSite returns the registry entry for a site; it must be called after
// requireSite succeeded. The registry comes from the authenticated request.
func mcpSite(r *http.Request, id int32) (sites.Site, error) {
	registry, _ := r.Context().Value(registryContextKey{}).(sites.Registry)
	if registry == nil {
		return sites.Site{}, errMCPSiteNotFound
	}
	site, ok := registry.Site(id)
	if !ok {
		return sites.Site{}, errMCPSiteNotFound
	}
	return site, nil
}

// rejectAppSiteForWebTool refuses the web analytics tools on App projects.
// Ownership and scope errors are left to the tool itself so they keep their
// usual shape and a foreign site never reveals its kind.
func rejectAppSiteForWebTool(r *http.Request, arguments json.RawMessage) error {
	var probe struct {
		Site int32 `json:"site"`
	}
	if json.Unmarshal(arguments, &probe) != nil || probe.Site < 1 || requireSite(r, probe.Site) != nil {
		return nil
	}
	if site, err := mcpSite(r, probe.Site); err == nil && site.Kind == sites.KindApp {
		return invalidMCPParams{detail: "site is an app; use the get_app_* tools"}
	}
	return nil
}

func (h *mcpHandler) runAppTool(ctx context.Context, r *http.Request, name string, arguments json.RawMessage) (any, error) {
	var (
		rng                            mcpAppRange
		release, environment, platform string
		issues                         mcpAppIssuesInput
		issueID                        int64
	)
	switch name {
	case "get_app_overview", "get_app_issue":
		var in mcpAppIssueInput
		if name == "get_app_overview" {
			var o mcpAppFilterInput
			if err := decodeMCPParams(arguments, &o); err != nil {
				return nil, invalidMCPParams{err.Error()}
			}
			in.mcpAppFilterInput = o
		} else {
			if err := decodeMCPParams(arguments, &in); err != nil {
				return nil, invalidMCPParams{err.Error()}
			}
			if in.IssueID < 1 {
				return nil, invalidMCPParams{detail: "issue_id must be a positive integer"}
			}
		}
		rng, release, environment, platform, issueID = in.mcpAppRange, in.Release, in.Environment, in.Platform, in.IssueID
	case "list_app_issues":
		if err := decodeMCPParams(arguments, &issues); err != nil {
			return nil, invalidMCPParams{err.Error()}
		}
		rng, release, environment, platform = issues.mcpAppRange, issues.Release, issues.Environment, issues.Platform
	case "get_app_compatibility":
		var in mcpAppCompatInput
		if err := decodeMCPParams(arguments, &in); err != nil {
			return nil, invalidMCPParams{err.Error()}
		}
		rng, platform = in.mcpAppRange, in.Platform
	case "list_app_releases":
		var in mcpAppReleasesInput
		if err := decodeMCPParams(arguments, &in); err != nil {
			return nil, invalidMCPParams{err.Error()}
		}
		rng, environment, platform = in.mcpAppRange, in.Environment, in.Platform
	default:
		return nil, invalidMCPParams{detail: "unknown tool " + strconv.Quote(name)}
	}
	if rng.Site < 1 {
		return nil, invalidMCPParams{detail: "site must be a positive integer"}
	}
	if err := requireSite(r, rng.Site); err != nil {
		return nil, err
	}
	site, err := mcpSite(r, rng.Site)
	if err != nil {
		return nil, err
	}
	if site.Kind != sites.KindApp {
		return nil, invalidMCPParams{detail: "site is not an app; use get_summary and the other analytics tools"}
	}
	now := time.Now()
	if h.deps.Now != nil {
		now = h.deps.Now()
	}
	f, err := rng.filter(site.TZ, now)
	if err != nil {
		return nil, err
	}
	if f, err = mcpAppDimensions(f, release, environment, platform); err != nil {
		return nil, err
	}
	scope := mcpAppScope(site)
	var out any
	switch name {
	case "get_app_overview":
		out, err = h.deps.AppHealth.Overview(ctx, scope, f)
	case "list_app_issues":
		out, err = h.listAppIssues(ctx, scope, f, issues)
	case "get_app_issue":
		var detail appquery.IssueDetail
		if detail, err = h.deps.AppHealth.Issue(ctx, scope, issueID, f); err == nil {
			if len(detail.LatestEvents) > mcpAppMaxLatest {
				detail.LatestEvents = detail.LatestEvents[:mcpAppMaxLatest]
			}
			// Event ids are for the dashboard's event detail; tools never see them.
			for i := range detail.LatestEvents {
				detail.LatestEvents[i].EventID = ""
			}
			out = detail
		}
	case "get_app_compatibility":
		var rows []appquery.CompatRow
		if rows, err = h.deps.AppHealth.Compatibility(ctx, scope, f); err == nil {
			out = map[string]any{"rows": rows}
		}
	case "list_app_releases":
		var rows []appquery.ReleaseRow
		if rows, err = h.deps.AppHealth.Releases(ctx, scope, f); err == nil {
			out = map[string]any{"releases": rows}
		}
	}
	if err != nil {
		return nil, mcpAppError(err)
	}
	return out, nil
}

func (h *mcpHandler) listAppIssues(ctx context.Context, scope appquery.Scope, f appquery.Filter, in mcpAppIssuesInput) (any, error) {
	switch in.Status {
	case "", "open", "regressed", "resolved", "ignored", "all":
	default:
		return nil, invalidMCPParams{detail: "status is invalid"}
	}
	switch in.Sort {
	case "", "last_seen", "events", "users":
	default:
		return nil, invalidMCPParams{detail: "sort is invalid"}
	}
	if len(in.Kind) > mcpAppMaxKind || len(in.Q) > mcpAppMaxQuery {
		return nil, invalidMCPParams{detail: "kind or q is too long"}
	}
	if in.Limit < 0 || in.Limit > 100 || in.Offset < 0 {
		return nil, invalidMCPParams{detail: "limit must be 1-100 and offset at least 0"}
	}
	query := appquery.IssueQuery{Filter: f, Status: in.Status, Kind: in.Kind, Q: in.Q, Sort: in.Sort, Limit: in.Limit, Offset: in.Offset}
	if query.Status == "" {
		query.Status = "open"
	}
	if query.Limit == 0 {
		query.Limit = 25
	}
	return h.deps.AppHealth.Issues(ctx, scope, query)
}

// mcpAppError maps appquery errors onto tool errors; anything else is hidden.
func mcpAppError(err error) error {
	var invalid invalidMCPParams
	switch {
	case errors.As(err, &invalid):
		return err
	case errors.Is(err, appquery.ErrNotFound):
		return invalidMCPParams{detail: "issue not found"}
	case errors.Is(err, appquery.ErrRange), errors.Is(err, appquery.ErrInvalid):
		return invalidMCPParams{detail: strings.TrimPrefix(err.Error(), appquery.ErrInvalid.Error()+": ")}
	default:
		return errors.New("app health query failed")
	}
}

func mcpAppTools() []MCPTool {
	period := map[string]any{"type": "string", "enum": []string{"today", "yesterday", "7d", "30d", "month", "last_month", "12mo", "year"}}
	date := map[string]any{"type": "string", "format": "date"}
	str := func(max int) map[string]any { return map[string]any{"type": "string", "maxLength": max} }
	rangeProps := func(extra map[string]any) map[string]any {
		props := map[string]any{"site": map[string]any{"type": "integer", "minimum": 1}, "period": period, "date_from": date, "date_to": date}
		for k, v := range extra {
			props[k] = v
		}
		return props
	}
	schema := func(props map[string]any) map[string]any {
		return map[string]any{
			"type": "object", "additionalProperties": false, "required": []string{"site"}, "properties": props,
			"oneOf": []any{map[string]any{"required": []string{"period"}}, map[string]any{"required": []string{"date_from", "date_to"}}},
		}
	}
	filters := map[string]any{"release": str(mcpAppMaxRelease), "environment": str(mcpAppMaxEnvironment), "platform": str(mcpAppMaxPlatform)}
	issueProps := map[string]any{
		"release": filters["release"], "environment": filters["environment"], "platform": filters["platform"],
		"status": map[string]any{"type": "string", "enum": []string{"open", "regressed", "resolved", "ignored", "all"}},
		"kind":   str(mcpAppMaxKind), "q": str(mcpAppMaxQuery),
		"sort":   map[string]any{"type": "string", "enum": []string{"last_seen", "events", "users"}},
		"limit":  map[string]any{"type": "integer", "minimum": 1, "maximum": 100},
		"offset": map[string]any{"type": "integer", "minimum": 0},
	}
	return []MCPTool{
		{Name: "get_app_overview", Description: "App projects only. Crash-free users and sessions, event and issue counts, and release health for a period.", InputSchema: schema(rangeProps(filters))},
		{Name: "list_app_issues", Description: "App projects only. List grouped errors and crashes (issues), by default the open ones, newest first.", InputSchema: schema(rangeProps(issueProps))},
		{Name: "get_app_issue", Description: "App projects only. One issue with its daily series, breakdowns and up to 5 latest events.", InputSchema: func() map[string]any {
			s := schema(rangeProps(map[string]any{"issue_id": map[string]any{"type": "integer", "minimum": 1}, "release": filters["release"], "environment": filters["environment"], "platform": filters["platform"]}))
			s["required"] = []string{"site", "issue_id"}
			return s
		}()},
		{Name: "get_app_compatibility", Description: "App projects only. Crash and error rates by OS version, architecture and device class.", InputSchema: schema(rangeProps(map[string]any{"platform": filters["platform"]}))},
		{Name: "list_app_releases", Description: "App projects only. Releases with their sessions, crash-free rates and new issues.", InputSchema: schema(rangeProps(map[string]any{"environment": filters["environment"], "platform": filters["platform"]}))},
	}
}
