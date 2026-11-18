package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"
	"unicode/utf8"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"supermetry/internal/appquery"
	"supermetry/internal/privacy"
	"supermetry/internal/sites"
)

// appProductReader is the part of appquery.Querier the MCP product tools use.
type appProductReader interface {
	ActiveUsers(ctx context.Context, scope appquery.Scope, f appquery.Filter) (appquery.ActiveUsers, error)
	Engagement(ctx context.Context, scope appquery.Scope, f appquery.Filter) (appquery.Engagement, error)
}

var _ appProductReader = (*appquery.Querier)(nil)

var mcpAppProductToolNames = map[string]bool{
	"get_product_overview": true, "get_active_users": true, "get_retention": true,
	"get_funnel": true, "get_event_breakdown": true, "get_user_journey": true,
}

const (
	mcpProductMinSteps  = 2
	mcpProductMaxSteps  = 8
	mcpProductMaxValue  = 200
	mcpProductMaxDepth  = 5
	mcpProductTopN      = 5
	mcpProductBreakdown = 50
)

// mcpProductDimensions maps the by argument of get_event_breakdown to the web
// engine's dimension. Every answer is a count of events or screen views; none
// names an install, a user or a profile.
var mcpProductDimensions = map[string]string{
	"event": "event", "screen": "page", "country": "country", "platform": "hostname", "os": "os", "device": "device",
}

type mcpProductFunnelStep struct {
	Type  string `json:"type"`
	Value string `json:"value"`
	Match string `json:"match,omitempty"`
}

type mcpProductInput struct {
	mcpAppFilterInput
	Interval   string                 `json:"interval,omitempty"`
	Steps      []mcpProductFunnelStep `json:"steps,omitempty"`
	CountBy    string                 `json:"count_by,omitempty"`
	Event      string                 `json:"event,omitempty"`
	By         string                 `json:"by,omitempty"`
	FromScreen string                 `json:"from_screen,omitempty"`
	Depth      int                    `json:"depth,omitempty"`
}

// runProductTool serves the read-only product analytics tools of app projects.
// Retention, funnels and journeys follow the tracking mode exactly as the web
// ones do: an anonymous instance answers them with ErrAnonymousHistory, while
// active users, engagement and the breakdowns keep working.
func (h *mcpHandler) runProductTool(ctx context.Context, r *http.Request, name string, arguments json.RawMessage) (any, error) {
	var in mcpProductInput
	if err := decodeMCPParams(arguments, &in); err != nil {
		return nil, invalidMCPParams{err.Error()}
	}
	if in.Site < 1 {
		return nil, invalidMCPParams{detail: "site must be a positive integer"}
	}
	if err := requireSite(r, in.Site); err != nil {
		return nil, err
	}
	site, err := mcpSite(r, in.Site)
	if err != nil {
		return nil, err
	}
	if site.Kind != sites.KindApp {
		return nil, invalidMCPParams{detail: "site is not an app; use get_summary and the other analytics tools"}
	}
	now := h.clock()
	f, err := in.mcpAppRange.filter(site.TZ, now)
	if err != nil {
		return nil, err
	}
	if f, err = mcpAppDimensions(f, in.Release, in.Environment, in.Platform); err != nil {
		return nil, err
	}
	scope := mcpAppScope(site)
	var out any
	switch name {
	case "get_active_users":
		out, err = h.deps.AppProduct.ActiveUsers(ctx, scope, f)
	case "get_product_overview":
		out, err = h.productOverview(ctx, scope, f, in)
	case "get_retention":
		out, err = h.productRetention(ctx, in, f)
	case "get_funnel":
		out, err = h.productFunnel(ctx, in, f)
	case "get_event_breakdown":
		out, err = h.productBreakdown(ctx, in, f)
	case "get_user_journey":
		out, err = h.productJourney(ctx, in, f)
	default:
		return nil, invalidMCPParams{detail: "unknown tool " + strconv.Quote(name)}
	}
	if err != nil {
		return nil, mcpProductError(err)
	}
	return out, nil
}

func (h *mcpHandler) clock() time.Time {
	if h.deps.Now != nil {
		return h.deps.Now()
	}
	return time.Now()
}

func mcpProductError(err error) error {
	var invalid invalidMCPParams
	switch {
	case errors.As(err, &invalid), errors.Is(err, ErrAnonymousHistory):
		return err
	case errors.Is(err, appquery.ErrRange), errors.Is(err, appquery.ErrInvalid):
		return mcpAppError(err)
	case isClientQueryErr(err):
		return invalidMCPParams{detail: err.Error()}
	default:
		return errors.New("product query failed")
	}
}

func isClientQueryErr(err error) bool { return err != nil && isClientQueryError(err.Error()) }

func mcpDateRange(f appquery.Filter) DateRange {
	return DateRange{From: openapi_types.Date{Time: f.From}, To: openapi_types.Date{Time: f.To}}
}

// requireIdentity refuses the history-bearing tools on an anonymous instance.
func (h *mcpHandler) requireIdentity() error {
	if h.deps.TrackingMode != privacy.Identified {
		return ErrAnonymousHistory
	}
	return nil
}

func (h *mcpHandler) requireQuery() error {
	if h.deps.Query == nil {
		return errors.New("query service is unavailable")
	}
	return nil
}

func (h *mcpHandler) productRetention(ctx context.Context, in mcpProductInput, f appquery.Filter) (any, error) {
	if err := h.requireIdentity(); err != nil {
		return nil, err
	}
	if err := h.requireQuery(); err != nil {
		return nil, err
	}
	interval := RetentionRequestInterval(in.Interval)
	switch interval {
	case "":
		interval = RetentionDay
	case RetentionDay, RetentionWeek, RetentionMonth:
	default:
		return nil, invalidMCPParams{detail: "interval must be day, week or month"}
	}
	return h.deps.Query.Retention(ctx, RetentionRequest{SiteId: in.Site, DateRange: mcpDateRange(f), Interval: interval})
}

func (h *mcpHandler) productFunnel(ctx context.Context, in mcpProductInput, f appquery.Filter) (any, error) {
	if len(in.Steps) < mcpProductMinSteps || len(in.Steps) > mcpProductMaxSteps {
		return nil, invalidMCPParams{detail: "steps must hold 2 to 8 entries"}
	}
	steps := make([]FunnelStep, 0, len(in.Steps))
	for _, s := range in.Steps {
		if s.Value == "" || utf8.RuneCountInString(s.Value) > mcpProductMaxValue {
			return nil, invalidMCPParams{detail: "step value must be 1 to 200 characters"}
		}
		step := FunnelStep{Value: s.Value, Match: FunnelStepMatchExact}
		switch s.Type {
		case "screen":
			step.Type = FunnelStepTypePage
		case "event":
			step.Type = FunnelStepTypeEvent
		default:
			return nil, invalidMCPParams{detail: "step type must be screen or event"}
		}
		switch s.Match {
		case "", "exact":
		case "prefix":
			step.Match = FunnelStepMatchPrefix
		default:
			return nil, invalidMCPParams{detail: "step match must be exact or prefix"}
		}
		steps = append(steps, step)
	}
	countBy := FunnelCountBySessions
	switch in.CountBy {
	case "", "sessions":
	case "installs":
		countBy = FunnelCountByVisitors
	default:
		return nil, invalidMCPParams{detail: "count_by must be installs or sessions"}
	}
	if err := h.requireIdentity(); err != nil {
		return nil, err
	}
	if err := h.requireQuery(); err != nil {
		return nil, err
	}
	resp, err := h.deps.Query.Funnel(ctx, FunnelRequest{SiteId: in.Site, DateRange: mcpDateRange(f), Steps: steps, CountBy: &countBy})
	if err != nil {
		return nil, err
	}
	// App projects count installs, not visitors; the deprecated visitors alias is dropped.
	type step struct {
		Step       int     `json:"step"`
		Count      int64   `json:"count"`
		Conversion float64 `json:"conversion"`
	}
	out := struct {
		CountBy string `json:"count_by"`
		Steps   []step `json:"steps"`
	}{CountBy: "sessions", Steps: make([]step, 0, len(resp.Steps))}
	if countBy == FunnelCountByVisitors {
		out.CountBy = "installs"
	}
	for _, s := range resp.Steps {
		out.Steps = append(out.Steps, step{Step: s.Step, Count: s.Count, Conversion: s.Conversion})
	}
	return out, nil
}

func (h *mcpHandler) productJourney(ctx context.Context, in mcpProductInput, f appquery.Filter) (any, error) {
	if in.FromScreen == "" || utf8.RuneCountInString(in.FromScreen) > mcpProductMaxValue {
		return nil, invalidMCPParams{detail: "from_screen is required (at most 200 characters)"}
	}
	depth := in.Depth
	if depth == 0 {
		depth = 3
	}
	if depth < 2 || depth > mcpProductMaxDepth {
		return nil, invalidMCPParams{detail: "depth must be 2 to 5"}
	}
	if err := h.requireIdentity(); err != nil {
		return nil, err
	}
	if err := h.requireQuery(); err != nil {
		return nil, err
	}
	start := in.FromScreen
	return h.deps.Query.Journey(ctx, JourneyRequest{SiteId: in.Site, DateRange: mcpDateRange(f), StartPath: &start, Steps: depth, Limit: mcpProductTopN})
}

// breakdown runs one web-engine breakdown over product hits.
func (h *mcpHandler) breakdown(ctx context.Context, in mcpProductInput, f appquery.Filter, dimension string, metrics []Metric, limit int, filters []Filter) (QueryResponse, error) {
	if err := h.requireQuery(); err != nil {
		return QueryResponse{}, err
	}
	dims := []string{dimension}
	request := QueryRequest{SiteId: in.Site, Dimensions: &dims, Metrics: metrics, DateRange: ptr(mcpDateRange(f)), Limit: &limit}
	if len(filters) > 0 {
		request.Filters = &filters
	}
	if h.deps.TrackingMode != privacy.Identified {
		if err := ValidateAnonymousQuery(request); err != nil {
			return QueryResponse{}, err
		}
	}
	response, err := h.deps.Query.Query(ctx, request)
	if err != nil {
		return QueryResponse{}, err
	}
	if h.deps.TrackingMode != privacy.Identified {
		return SanitizeAnonymousResponseWithPolicy(response, request, h.deps.TrackingPaths)
	}
	return response, nil
}

func ptr[T any](v T) *T { return &v }

func (h *mcpHandler) productBreakdown(ctx context.Context, in mcpProductInput, f appquery.Filter) (any, error) {
	by := in.By
	if by == "" {
		by = "event"
	}
	dimension, ok := mcpProductDimensions[by]
	if !ok {
		return nil, invalidMCPParams{detail: "by must be event, screen, country, platform, os or device"}
	}
	if utf8.RuneCountInString(in.Event) > mcpProductMaxValue {
		return nil, invalidMCPParams{detail: "event is too long"}
	}
	var filters []Filter
	if in.Event != "" {
		filters = append(filters, Filter{Dimension: "event", Operator: Is, Values: []string{in.Event}})
	}
	metrics := []Metric{Events}
	if by == "screen" {
		metrics = []Metric{Pageviews}
	}
	resp, err := h.breakdown(ctx, in, f, dimension, metrics, mcpProductBreakdown, filters)
	if err != nil {
		return nil, err
	}
	return map[string]any{"by": by, "columns": resp.Columns, "rows": resp.Rows}, nil
}

func (h *mcpHandler) productOverview(ctx context.Context, scope appquery.Scope, f appquery.Filter, in mcpProductInput) (any, error) {
	active, err := h.deps.AppProduct.ActiveUsers(ctx, scope, f)
	if err != nil {
		return nil, err
	}
	engagement, err := h.deps.AppProduct.Engagement(ctx, scope, f)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"stickiness": active.Stickiness, "distinct_active_installs": active.Distinct, "engagement": engagement}
	if n := len(active.Series); n > 0 {
		out["latest"] = active.Series[n-1]
	}
	// The top lists are best effort: a refusal (an anonymous instance hides
	// screen names) leaves them out rather than failing the overview.
	if resp, err := h.breakdown(ctx, in, f, "page", []Metric{Pageviews}, mcpProductTopN, nil); err == nil {
		out["top_screens"] = resp.Rows
	}
	if resp, err := h.breakdown(ctx, in, f, "event", []Metric{Events}, mcpProductTopN, nil); err == nil {
		out["top_events"] = resp.Rows
	}
	return out, nil
}

func mcpAppProductTools() []MCPTool {
	period := map[string]any{"type": "string", "enum": []string{"today", "yesterday", "7d", "30d", "month", "last_month", "12mo", "year"}}
	date := map[string]any{"type": "string", "format": "date"}
	str := func(max int) map[string]any { return map[string]any{"type": "string", "maxLength": max} }
	schema := func(required []string, extra map[string]any) map[string]any {
		props := map[string]any{"site": map[string]any{"type": "integer", "minimum": 1}, "period": period, "date_from": date, "date_to": date,
			"release": str(mcpAppMaxRelease), "environment": str(mcpAppMaxEnvironment), "platform": str(mcpAppMaxPlatform)}
		for k, v := range extra {
			props[k] = v
		}
		return map[string]any{
			"type": "object", "additionalProperties": false, "required": required, "properties": props,
			"oneOf": []any{map[string]any{"required": []string{"period"}}, map[string]any{"required": []string{"date_from", "date_to"}}},
		}
	}
	site := []string{"site"}
	stepSchema := map[string]any{"type": "object", "additionalProperties": false, "required": []string{"type", "value"}, "properties": map[string]any{
		"type": map[string]any{"type": "string", "enum": []string{"screen", "event"}}, "value": str(mcpProductMaxValue),
		"match": map[string]any{"type": "string", "enum": []string{"exact", "prefix"}},
	}}
	return []MCPTool{
		{Name: "get_product_overview", Description: "App projects only. Latest daily, weekly and monthly active installs, stickiness, the top 5 screens and events, and engagement (sessions, session length). Counts only: no user or install identifiers.", InputSchema: schema(site, nil)},
		{Name: "get_active_users", Description: "App projects only. Daily series of active installs (DAU, WAU, MAU), distinct installs, stickiness and new installs per day.", InputSchema: schema(site, nil)},
		{Name: "get_retention", Description: "App projects only. Install retention cohorts by day, week or month. Not available when the instance tracks anonymously.", InputSchema: schema(site, map[string]any{"interval": map[string]any{"type": "string", "enum": []string{"day", "week", "month"}}})},
		{Name: "get_funnel", Description: "App projects only. Ad-hoc funnel over 2 to 8 ordered steps, each a screen or a custom event, counted by installs or sessions. Not available when the instance tracks anonymously.", InputSchema: schema([]string{"site", "steps"}, map[string]any{
			"steps":    map[string]any{"type": "array", "minItems": mcpProductMinSteps, "maxItems": mcpProductMaxSteps, "items": stepSchema},
			"count_by": map[string]any{"type": "string", "enum": []string{"installs", "sessions"}},
		})},
		{Name: "get_event_breakdown", Description: "App projects only. Counts of custom events or screen views grouped by event, screen, country, platform, OS or device class; optionally limited to one event name.", InputSchema: schema(site, map[string]any{
			"event": str(mcpProductMaxValue), "by": map[string]any{"type": "string", "enum": []string{"event", "screen", "country", "platform", "os", "device"}},
		})},
		{Name: "get_user_journey", Description: "App projects only. The screens installs visit after a starting screen, up to 5 steps deep (top 5 per step). Not available when the instance tracks anonymously.", InputSchema: schema([]string{"site", "from_screen"}, map[string]any{
			"from_screen": str(mcpProductMaxValue), "depth": map[string]any{"type": "integer", "minimum": 2, "maximum": mcpProductMaxDepth},
		})},
	}
}
