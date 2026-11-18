package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"supermetry/internal/appquery"
	"supermetry/internal/sites"
)

// appPerformanceReader is the part of appquery.Querier the MCP performance
// tools use. The tools are offered only when the App Health reader has it.
type appPerformanceReader interface {
	PerformanceOverview(ctx context.Context, scope appquery.Scope, f appquery.Filter) (appquery.PerformanceOverview, error)
	Transactions(ctx context.Context, scope appquery.Scope, f appquery.Filter, sort string, limit int) ([]appquery.TransactionRow, error)
	Transaction(ctx context.Context, scope appquery.Scope, f appquery.Filter, name string) (appquery.TransactionDetail, error)
	Trace(ctx context.Context, scope appquery.Scope, traceID [16]byte) (appquery.Trace, error)
}

var _ appPerformanceReader = (*appquery.Querier)(nil)

const (
	mcpMaxSlowTransactions = 20
	mcpTraceSpans          = 500
	mcpMaxTransactionName  = 200
)

var mcpPerformanceToolNames = map[string]bool{
	"get_performance_overview": true, "list_slow_transactions": true, "get_transaction": true, "get_trace": true,
}

func init() {
	for name := range mcpPerformanceToolNames {
		mcpAppToolNames[name] = true
	}
}

type mcpSlowInput struct {
	mcpAppFilterInput
	Limit int `json:"limit,omitempty"`
}

type mcpTransactionInput struct {
	mcpAppFilterInput
	Name string `json:"name"`
}

type mcpTraceInput struct {
	Site    int32  `json:"site"`
	TraceID string `json:"trace_id"`
}

// mcpTraceOut is a trace as tools see it: no span ids, no event ids, parent
// links only through depth, descriptions already parameterised at ingest.
type mcpTraceOut struct {
	TraceID    string             `json:"trace_id"`
	DurationMS float64            `json:"duration_ms"`
	Truncated  bool               `json:"truncated"`
	Spans      []mcpTraceSpanOut  `json:"spans"`
	Errors     []mcpTraceErrorOut `json:"errors"`
}

type mcpTraceSpanOut struct {
	Depth         int     `json:"depth"`
	Name          string  `json:"name,omitempty"`
	Op            string  `json:"op"`
	Description   string  `json:"description,omitempty"`
	Status        string  `json:"status,omitempty"`
	HTTPStatus    *int16  `json:"http_status,omitempty"`
	StartOffsetMS float64 `json:"start_offset_ms"`
	DurationMS    float64 `json:"duration_ms"`
}

type mcpTraceErrorOut struct {
	IssueID *int64 `json:"issue_id"`
	Title   string `json:"title"`
}

func (h *mcpHandler) runPerformanceTool(ctx context.Context, r *http.Request, name string, arguments json.RawMessage) (any, error) {
	reader, ok := h.deps.AppHealth.(appPerformanceReader)
	if !ok {
		return nil, invalidMCPParams{detail: "unknown tool " + name}
	}
	var (
		siteID int32
		rng    mcpAppRange
		dims   mcpAppFilterInput
		limit  int
		txName string
		trace  string
	)
	switch name {
	case "get_performance_overview":
		if err := decodeMCPParams(arguments, &dims); err != nil {
			return nil, invalidMCPParams{err.Error()}
		}
		siteID, rng = dims.Site, dims.mcpAppRange
	case "list_slow_transactions":
		var in mcpSlowInput
		if err := decodeMCPParams(arguments, &in); err != nil {
			return nil, invalidMCPParams{err.Error()}
		}
		if in.Limit < 0 || in.Limit > mcpMaxSlowTransactions {
			return nil, invalidMCPParams{detail: "limit must be 1-20"}
		}
		dims, siteID, rng, limit = in.mcpAppFilterInput, in.Site, in.mcpAppRange, in.Limit
	case "get_transaction":
		var in mcpTransactionInput
		if err := decodeMCPParams(arguments, &in); err != nil {
			return nil, invalidMCPParams{err.Error()}
		}
		if in.Name == "" || len(in.Name) > mcpMaxTransactionName {
			return nil, invalidMCPParams{detail: "name is required and at most 200 bytes"}
		}
		dims, siteID, rng, txName = in.mcpAppFilterInput, in.Site, in.mcpAppRange, in.Name
	case "get_trace":
		var in mcpTraceInput
		if err := decodeMCPParams(arguments, &in); err != nil {
			return nil, invalidMCPParams{err.Error()}
		}
		siteID, trace = in.Site, in.TraceID
	}
	if siteID < 1 {
		return nil, invalidMCPParams{detail: "site must be a positive integer"}
	}
	if err := requireSite(r, siteID); err != nil {
		return nil, err
	}
	site, err := mcpSite(r, siteID)
	if err != nil {
		return nil, err
	}
	if site.Kind != sites.KindApp {
		return nil, invalidMCPParams{detail: "site is not an app; use get_summary and the other analytics tools"}
	}
	scope := mcpAppScope(site)
	if name == "get_trace" {
		id, err := appquery.ParseTraceID(trace)
		if err != nil {
			return nil, mcpAppError(err)
		}
		t, err := reader.Trace(ctx, scope, id)
		if err != nil {
			return nil, mcpPerformanceError(err, "trace not found")
		}
		return mcpTrace(t), nil
	}
	now := time.Now()
	if h.deps.Now != nil {
		now = h.deps.Now()
	}
	f, err := rng.filter(site.TZ, now)
	if err != nil {
		return nil, err
	}
	if f, err = mcpAppDimensions(f, dims.Release, dims.Environment, dims.Platform); err != nil {
		return nil, err
	}
	var out any
	switch name {
	case "get_performance_overview":
		out, err = reader.PerformanceOverview(ctx, scope, f)
	case "list_slow_transactions":
		if limit == 0 {
			limit = 10
		}
		var rows []appquery.TransactionRow
		if rows, err = reader.Transactions(ctx, scope, f, "p95", limit); err == nil {
			out = map[string]any{"transactions": rows}
		}
	case "get_transaction":
		out, err = reader.Transaction(ctx, scope, f, txName)
	}
	if err != nil {
		return nil, mcpPerformanceError(err, "transaction not found")
	}
	return out, nil
}

func mcpPerformanceError(err error, notFound string) error {
	if errors.Is(err, appquery.ErrNotFound) {
		return invalidMCPParams{detail: notFound}
	}
	return mcpAppError(err)
}

func mcpTrace(t appquery.Trace) mcpTraceOut {
	out := mcpTraceOut{TraceID: t.TraceID, DurationMS: t.DurationMS, Truncated: t.Truncated,
		Spans: make([]mcpTraceSpanOut, 0, min(len(t.Spans), mcpTraceSpans)), Errors: []mcpTraceErrorOut{}}
	for i, s := range t.Spans {
		if i == mcpTraceSpans {
			out.Truncated = true
			break
		}
		out.Spans = append(out.Spans, mcpTraceSpanOut{Depth: s.Depth, Name: s.Name, Op: s.Op, Description: s.Description,
			Status: s.Status, HTTPStatus: s.HTTPStatus, StartOffsetMS: s.StartOffsetMS, DurationMS: s.DurationMS})
	}
	for _, e := range t.Errors {
		out.Errors = append(out.Errors, mcpTraceErrorOut{IssueID: e.IssueID, Title: e.Title})
	}
	return out
}

func mcpPerformanceTools() []MCPTool {
	period := map[string]any{"type": "string", "enum": []string{"today", "yesterday", "7d", "30d", "month", "last_month", "12mo", "year"}}
	date := map[string]any{"type": "string", "format": "date"}
	str := func(max int) map[string]any { return map[string]any{"type": "string", "maxLength": max} }
	rangeProps := func(extra map[string]any) map[string]any {
		props := map[string]any{"site": map[string]any{"type": "integer", "minimum": 1}, "period": period, "date_from": date, "date_to": date,
			"release": str(mcpAppMaxRelease), "environment": str(mcpAppMaxEnvironment), "platform": str(mcpAppMaxPlatform)}
		for k, v := range extra {
			props[k] = v
		}
		return props
	}
	schema := func(required []string, props map[string]any) map[string]any {
		return map[string]any{
			"type": "object", "additionalProperties": false, "required": required, "properties": props,
			"oneOf": []any{map[string]any{"required": []string{"period"}}, map[string]any{"required": []string{"date_from", "date_to"}}},
		}
	}
	return []MCPTool{
		{Name: "get_performance_overview", Description: "App projects only. Transaction totals with p50 and p95 and error rate, the five slowest transactions, the five with the highest error rate, and app start times for a period. Percentiles are estimates within 4.9 % of the exact value.", InputSchema: schema([]string{"site"}, rangeProps(nil))},
		{Name: "list_slow_transactions", Description: "App projects only. Transactions ordered by p95 duration, slowest first, with throughput, error rate and p50-p99 in milliseconds.", InputSchema: schema([]string{"site"}, rangeProps(map[string]any{"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": mcpMaxSlowTransactions}}))},
		{Name: "get_transaction", Description: "App projects only. One transaction by name: daily p50/p95 series, the slowest child span groups (parameterised descriptions), slowest samples with trace ids and app start times.", InputSchema: schema([]string{"site", "name"}, rangeProps(map[string]any{"name": str(mcpMaxTransactionName)}))},
		{Name: "get_trace", Description: "App projects only. One trace by its 32-character hex id: up to 500 spans depth first with start offsets and durations in milliseconds, and the issues of errors captured inside it. Span descriptions are parameterised; no user data.", InputSchema: map[string]any{
			"type": "object", "additionalProperties": false, "required": []string{"site", "trace_id"},
			"properties": map[string]any{"site": map[string]any{"type": "integer", "minimum": 1}, "trace_id": map[string]any{"type": "string", "pattern": "^[0-9a-fA-F]{32}$", "maxLength": 32}},
		}},
	}
}
