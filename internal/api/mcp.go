package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"panel/internal/session"
	"panel/internal/sites"
)

const MCPProtocolVersion = "2025-06-18"

const mcpLegacyProtocolVersion = "2025-03-26"

// MCPDeps contains the existing application services exposed as MCP tools.
// Resources is the platform HTTP handler used for sites, goals, and saved
// funnels so those tools retain the public API's authorization and behavior.
type MCPDeps struct {
	Query      queryService
	Resources  http.Handler
	Sessions   session.Store
	Dictionary dictionaryResolver
	Now        func() time.Time
}

// MCPTool is the tools/list representation of one Panel operation.
type MCPTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type mcpHandler struct {
	deps  MCPDeps
	tools []MCPTool
}

type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type mcpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type mcpResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *mcpError       `json:"error,omitempty"`
}

type mcpCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type mcpTextContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type mcpCallResult struct {
	Content []mcpTextContent `json:"content"`
	IsError bool             `json:"isError,omitempty"`
}

type invalidMCPParams struct{ detail string }

func (e invalidMCPParams) Error() string { return e.detail }

// errMCPSiteNotFound is returned as a tool error, not an HTTP status, so MCP
// clients see a normal JSON-RPC response for unknown, foreign or unassigned
// sites, mirroring the public API's 404 without revealing which case applied.
var errMCPSiteNotFound = errors.New("site not found")

// requireSite enforces the key's organization and site restriction for a
// tool's site argument. Scope was checked once for the whole request.
func requireSite(r *http.Request, siteID int32) error {
	principal, ok := r.Context().Value(principalContextKey{}).(Principal)
	if !ok || !siteAllowed(r, principal, siteID) {
		return errMCPSiteNotFound
	}
	return nil
}

// NewMCPHandler returns the JSON-only Streamable HTTP MCP endpoint. Mount it
// below the public Bearer-key authentication and rate-limit middleware.
func NewMCPHandler(deps MCPDeps) http.Handler {
	return &mcpHandler{deps: deps, tools: mcpTools()}
}

func (h *mcpHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !validMCPOrigin(r) {
		writeProblem(w, forbidden("the request origin is not allowed"))
		return
	}
	if !validMCPProtocolHeader(r.Header.Get("MCP-Protocol-Version")) {
		writeProblem(w, badRequest("MCP-Protocol-Version is unsupported"))
		return
	}
	if !authorizeRequest(w, r, 0, "stats:read") {
		return
	}

	request, hasID, responseOnly, parseError := decodeMCPRequest(w, r)
	if parseError != nil {
		writeMCPError(w, nil, parseError.Code, parseError.Message, parseError.Data)
		return
	}
	if !hasID || responseOnly {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	switch request.Method {
	case "initialize":
		h.initialize(w, request)
	case "ping":
		writeMCPResult(w, request.ID, map[string]any{})
	case "tools/list":
		writeMCPResult(w, request.ID, map[string]any{"tools": h.tools})
	case "tools/call":
		h.callTool(w, r, request)
	default:
		writeMCPError(w, request.ID, -32601, "Method not found", nil)
	}
}

func (h *mcpHandler) initialize(w http.ResponseWriter, request mcpRequest) {
	var params struct {
		ProtocolVersion string `json:"protocolVersion"`
		Capabilities    any    `json:"capabilities"`
		ClientInfo      any    `json:"clientInfo"`
	}
	if len(request.Params) == 0 || decodeMCPParams(request.Params, &params) != nil || params.ProtocolVersion == "" {
		writeMCPError(w, request.ID, -32602, "Invalid params", "protocolVersion is required")
		return
	}
	writeMCPResult(w, request.ID, map[string]any{
		"protocolVersion": MCPProtocolVersion,
		"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
		"serverInfo":      map[string]string{"name": "panel", "version": "1.0.0"},
	})
}

func (h *mcpHandler) callTool(w http.ResponseWriter, r *http.Request, request mcpRequest) {
	var params mcpCallParams
	if len(request.Params) == 0 || decodeMCPParams(request.Params, &params) != nil || params.Name == "" {
		writeMCPError(w, request.ID, -32602, "Invalid params", "tool name and arguments are required")
		return
	}
	if len(params.Arguments) == 0 {
		params.Arguments = json.RawMessage(`{}`)
	}
	result, err := h.runTool(r.Context(), r, params.Name, params.Arguments)
	if err != nil {
		var invalid invalidMCPParams
		if errors.As(err, &invalid) {
			writeMCPError(w, request.ID, -32602, "Invalid params", invalid.detail)
			return
		}
		payload, _ := json.Marshal(map[string]string{"error": err.Error()})
		writeMCPResult(w, request.ID, mcpCallResult{Content: []mcpTextContent{{Type: "text", Text: string(payload)}}, IsError: true})
		return
	}
	payload, err := json.Marshal(result)
	if err != nil {
		writeMCPError(w, request.ID, -32603, "Internal error", nil)
		return
	}
	writeMCPResult(w, request.ID, mcpCallResult{Content: []mcpTextContent{{Type: "text", Text: string(payload)}}})
}

func (h *mcpHandler) runTool(ctx context.Context, r *http.Request, name string, arguments json.RawMessage) (any, error) {
	switch name {
	case "list_sites":
		var input struct{}
		if err := decodeMCPParams(arguments, &input); err != nil {
			return nil, invalidMCPParams{err.Error()}
		}
		return h.resource(ctx, r, "/api/public/v1/sites?limit=100")
	case "get_summary":
		var input mcpSummaryInput
		if err := decodeMCPParams(arguments, &input); err != nil {
			return nil, invalidMCPParams{err.Error()}
		}
		request, err := input.queryRequest([]Metric{Visitors, Visits, Pageviews, BounceRate, VisitDuration}, nil)
		if err != nil {
			return nil, err
		}
		return h.query(ctx, r, request)
	case "get_timeseries":
		var input mcpTimeseriesInput
		if err := decodeMCPParams(arguments, &input); err != nil {
			return nil, invalidMCPParams{err.Error()}
		}
		if input.Interval != "day" {
			return nil, invalidMCPParams{detail: "interval must be day"}
		}
		if len(input.Metrics) == 0 {
			return nil, invalidMCPParams{detail: "metrics is required"}
		}
		dimensions := []string{"time"}
		request, err := input.queryRequest(input.Metrics, &dimensions)
		if err != nil {
			return nil, err
		}
		return h.query(ctx, r, request)
	case "get_breakdown":
		var input mcpBreakdownInput
		if err := decodeMCPParams(arguments, &input); err != nil {
			return nil, invalidMCPParams{err.Error()}
		}
		if input.Dimension == "" || len(input.Metrics) == 0 {
			return nil, invalidMCPParams{detail: "dimension and metrics are required"}
		}
		dimensions := []string{input.Dimension}
		request, err := input.queryRequest(input.Metrics, &dimensions)
		if err != nil {
			return nil, err
		}
		if input.Limit != 0 {
			request.Limit = &input.Limit
		}
		return h.query(ctx, r, request)
	case "get_realtime":
		var input mcpSiteInput
		if err := decodeMCPParams(arguments, &input); err != nil || input.Site < 1 {
			return nil, invalidMCPParams{detail: "site must be a positive integer"}
		}
		if err := requireSite(r, input.Site); err != nil {
			return nil, err
		}
		now := time.Now()
		if h.deps.Now != nil {
			now = h.deps.Now()
		}
		return realtimeResponse(h.deps.Sessions, h.deps.Dictionary, input.Site, now), nil
	case "list_goals":
		var input mcpSiteInput
		if err := decodeMCPParams(arguments, &input); err != nil || input.Site < 1 {
			return nil, invalidMCPParams{detail: "site must be a positive integer"}
		}
		if err := requireSite(r, input.Site); err != nil {
			return nil, err
		}
		return h.resource(ctx, r, "/api/public/v1/sites/"+strconv.FormatInt(int64(input.Site), 10)+"/goals")
	case "run_funnel":
		var input mcpFunnelInput
		if err := decodeMCPParams(arguments, &input); err != nil || input.Site < 1 || input.FunnelID < 1 {
			return nil, invalidMCPParams{detail: "site and funnel_id must be positive integers"}
		}
		if err := requireSite(r, input.Site); err != nil {
			return nil, err
		}
		return h.runFunnel(ctx, r, input)
	default:
		return nil, invalidMCPParams{detail: "unknown tool " + strconv.Quote(name)}
	}
}

type mcpSiteInput struct {
	Site int32 `json:"site"`
}

type mcpQueryInput struct {
	Site     int32    `json:"site"`
	Period   Period   `json:"period,omitempty"`
	DateFrom string   `json:"date_from,omitempty"`
	DateTo   string   `json:"date_to,omitempty"`
	Filters  []Filter `json:"filters,omitempty"`
}

type mcpSummaryInput struct{ mcpQueryInput }

type mcpTimeseriesInput struct {
	mcpQueryInput
	Interval string   `json:"interval"`
	Metrics  []Metric `json:"metrics"`
}

type mcpBreakdownInput struct {
	mcpQueryInput
	Dimension string   `json:"dimension"`
	Metrics   []Metric `json:"metrics"`
	Limit     int      `json:"limit,omitempty"`
}

type mcpFunnelInput struct {
	Site     int32  `json:"site"`
	FunnelID int64  `json:"funnel_id"`
	Period   Period `json:"period"`
}

func (input mcpQueryInput) queryRequest(metrics []Metric, dimensions *[]string) (QueryRequest, error) {
	if input.Site < 1 {
		return QueryRequest{}, invalidMCPParams{detail: "site must be a positive integer"}
	}
	request := QueryRequest{SiteId: input.Site, Metrics: metrics, Dimensions: dimensions}
	if len(input.Filters) != 0 {
		request.Filters = &input.Filters
	}
	if input.Period != "" && (input.DateFrom != "" || input.DateTo != "") {
		return QueryRequest{}, invalidMCPParams{detail: "provide either period or date_from/date_to"}
	}
	if input.Period != "" {
		if !input.Period.Valid() || input.Period == Custom {
			return QueryRequest{}, invalidMCPParams{detail: "period is invalid"}
		}
		request.Period = &input.Period
		return request, nil
	}
	if input.DateFrom == "" || input.DateTo == "" {
		return QueryRequest{}, invalidMCPParams{detail: "period or date_from/date_to is required"}
	}
	from, fromErr := time.Parse(time.DateOnly, input.DateFrom)
	to, toErr := time.Parse(time.DateOnly, input.DateTo)
	if fromErr != nil || toErr != nil || from.After(to) {
		return QueryRequest{}, invalidMCPParams{detail: "date_from/date_to must be valid inclusive dates"}
	}
	request.DateRange = &DateRange{From: openapi_types.Date{Time: from}, To: openapi_types.Date{Time: to}}
	return request, nil
}

func (h *mcpHandler) query(ctx context.Context, r *http.Request, request QueryRequest) (any, error) {
	if err := requireSite(r, request.SiteId); err != nil {
		return nil, err
	}
	if h.deps.Query == nil {
		return nil, errors.New("query service is unavailable")
	}
	response, err := h.deps.Query.Query(ctx, request)
	if err != nil {
		if isClientQueryError(err.Error()) {
			return nil, invalidMCPParams{detail: err.Error()}
		}
		return nil, errors.New("query failed")
	}
	return response, nil
}

func (h *mcpHandler) runFunnel(ctx context.Context, r *http.Request, input mcpFunnelInput) (any, error) {
	if h.deps.Query == nil {
		return nil, errors.New("query service is unavailable")
	}
	if !input.Period.Valid() || input.Period == Custom {
		return nil, invalidMCPParams{detail: "period is invalid"}
	}
	value, err := h.resource(ctx, r, "/api/public/v1/sites/"+strconv.FormatInt(int64(input.Site), 10)+"/funnels")
	if err != nil {
		return nil, err
	}
	encoded, _ := json.Marshal(value)
	var funnels []SavedFunnel
	if err := json.Unmarshal(encoded, &funnels); err != nil {
		return nil, errors.New("saved funnel response is invalid")
	}
	var selected *SavedFunnel
	for index := range funnels {
		if funnels[index].Id == input.FunnelID {
			selected = &funnels[index]
			break
		}
	}
	if selected == nil || selected.SiteId != input.Site {
		return nil, errors.New("saved funnel not found")
	}
	registry, _ := r.Context().Value(registryContextKey{}).(sites.Registry)
	if registry == nil {
		return nil, errors.New("site registry is unavailable")
	}
	site, ok := registry.Site(input.Site)
	if !ok {
		return nil, errors.New("site not found")
	}
	now := time.Now()
	if h.deps.Now != nil {
		now = h.deps.Now()
	}
	from, to, err := mcpPeriodRange(input.Period, site.TZ, now)
	if err != nil {
		return nil, err
	}
	response, err := h.deps.Query.Funnel(ctx, FunnelRequest{SiteId: input.Site, DateRange: DateRange{From: openapi_types.Date{Time: from}, To: openapi_types.Date{Time: to}}, Steps: selected.Steps})
	if err != nil {
		if isClientQueryError(err.Error()) {
			return nil, invalidMCPParams{detail: err.Error()}
		}
		return nil, errors.New("funnel query failed")
	}
	return response, nil
}

func (h *mcpHandler) resource(ctx context.Context, source *http.Request, path string) (any, error) {
	if h.deps.Resources == nil {
		return nil, errors.New("resource service is unavailable")
	}
	request := httptest.NewRequest(http.MethodGet, "http://panel.internal"+path, nil).WithContext(ctx)
	request.Header = source.Header.Clone()
	response := httptest.NewRecorder()
	h.deps.Resources.ServeHTTP(response, request)
	if response.Code < 200 || response.Code >= 300 {
		return nil, fmt.Errorf("resource service returned HTTP %d", response.Code)
	}
	var value any
	decoder := json.NewDecoder(response.Body)
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, errors.New("resource service returned invalid JSON")
	}
	return value, nil
}

func decodeMCPRequest(w http.ResponseWriter, r *http.Request) (mcpRequest, bool, bool, *mcpError) {
	r.Body = http.MaxBytesReader(w, r.Body, maxQueryBodyBytes)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return mcpRequest{}, false, false, &mcpError{Code: -32700, Message: "Parse error"}
	}
	var raw any
	if len(bytes.TrimSpace(data)) == 0 || json.Unmarshal(data, &raw) != nil {
		return mcpRequest{}, false, false, &mcpError{Code: -32700, Message: "Parse error"}
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(data, &object) != nil || object == nil {
		return mcpRequest{}, false, false, &mcpError{Code: -32600, Message: "Invalid Request"}
	}
	var request mcpRequest
	if json.Unmarshal(data, &request) != nil || request.JSONRPC != "2.0" {
		return mcpRequest{}, false, false, &mcpError{Code: -32600, Message: "Invalid Request"}
	}
	_, hasID := object["id"]
	_, hasResult := object["result"]
	_, hasError := object["error"]
	if request.Method == "" {
		if hasResult || hasError {
			return request, hasID, true, nil
		}
		return mcpRequest{}, false, false, &mcpError{Code: -32600, Message: "Invalid Request"}
	}
	return request, hasID, false, nil
}

func decodeMCPParams(data []byte, target any) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) < 2 || trimmed[0] != '{' || trimmed[len(trimmed)-1] != '}' {
		return errors.New("parameters must be a JSON object")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("arguments must contain exactly one JSON object")
	}
	return nil
}

func validMCPProtocolHeader(value string) bool {
	return value == "" || value == MCPProtocolVersion || value == mcpLegacyProtocolVersion
}

func validMCPOrigin(r *http.Request) bool {
	value := r.Header.Get("Origin")
	if value == "" {
		return true
	}
	origin, err := url.Parse(value)
	return err == nil && origin.Host != "" && strings.EqualFold(origin.Host, r.Host)
}

func writeMCPResult(w http.ResponseWriter, id json.RawMessage, result any) {
	writeMCPResponse(w, mcpResponse{JSONRPC: "2.0", ID: id, Result: result})
}

func writeMCPError(w http.ResponseWriter, id json.RawMessage, code int, message string, data any) {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	writeMCPResponse(w, mcpResponse{JSONRPC: "2.0", ID: id, Error: &mcpError{Code: code, Message: message, Data: data}})
}

func writeMCPResponse(w http.ResponseWriter, response mcpResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(response)
}

func mcpPeriodRange(period Period, location *time.Location, now time.Time) (time.Time, time.Time, error) {
	if location == nil {
		return time.Time{}, time.Time{}, errors.New("site timezone is unavailable")
	}
	local := now.In(location)
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
	switch period {
	case Today:
		return today, today, nil
	case Yesterday:
		day := today.AddDate(0, 0, -1)
		return day, day, nil
	case N7d:
		return today.AddDate(0, 0, -6), today, nil
	case N30d:
		return today.AddDate(0, 0, -29), today, nil
	case Month:
		return time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC), today, nil
	case LastMonth:
		start := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC)
		return start.AddDate(0, -1, 0), start.AddDate(0, 0, -1), nil
	case N12mo:
		return today.AddDate(-1, 0, 1), today, nil
	case Year:
		return time.Date(today.Year(), time.January, 1, 0, 0, 0, 0, time.UTC), today, nil
	default:
		return time.Time{}, time.Time{}, invalidMCPParams{detail: "period is invalid"}
	}
}

func mcpTools() []MCPTool {
	period := map[string]any{"type": "string", "enum": []string{"today", "yesterday", "7d", "30d", "month", "last_month", "12mo", "year"}}
	metrics := map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"type": "string", "enum": []string{"visitors", "visits", "pageviews", "views_per_visit", "bounce_rate", "visit_duration", "engagement_time", "scroll_depth", "events", "conversions", "conversion_rate"}}}
	filters := map[string]any{"type": "array", "items": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"dimension", "operator", "values"}, "properties": map[string]any{"dimension": map[string]any{"type": "string"}, "operator": map[string]any{"type": "string", "enum": []string{"is", "is_not", "contains", "starts_with"}}, "values": map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"type": "string"}}}}}
	site := map[string]any{"type": "integer", "minimum": 1}
	object := func(required []string, properties map[string]any) map[string]any {
		schema := map[string]any{"type": "object", "additionalProperties": false, "properties": properties}
		if len(required) != 0 {
			schema["required"] = required
		}
		return schema
	}
	return []MCPTool{
		{Name: "list_sites", Description: "List sites accessible to this API key.", InputSchema: object(nil, map[string]any{})},
		{Name: "get_summary", Description: "Get summary metrics for a site and date range.", InputSchema: mergeMCPSchema(object([]string{"site"}, map[string]any{"site": site, "period": period, "date_from": map[string]any{"type": "string", "format": "date"}, "date_to": map[string]any{"type": "string", "format": "date"}, "filters": filters}), map[string]any{"oneOf": []any{map[string]any{"required": []string{"period"}}, map[string]any{"required": []string{"date_from", "date_to"}}}})},
		{Name: "get_timeseries", Description: "Get site metrics grouped into daily time buckets.", InputSchema: object([]string{"site", "period", "interval", "metrics"}, map[string]any{"site": site, "period": period, "interval": map[string]any{"type": "string", "enum": []string{"day"}}, "metrics": metrics, "filters": filters})},
		{Name: "get_breakdown", Description: "Break down metrics by an analytics dimension.", InputSchema: object([]string{"site", "dimension", "metrics", "period"}, map[string]any{"site": site, "dimension": map[string]any{"type": "string"}, "metrics": metrics, "period": period, "filters": filters, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 1000}})},
		{Name: "get_realtime", Description: "Get active visitors and top pages from the last five minutes.", InputSchema: object([]string{"site"}, map[string]any{"site": site})},
		{Name: "list_goals", Description: "List the saved goals for a site.", InputSchema: object([]string{"site"}, map[string]any{"site": site})},
		{Name: "run_funnel", Description: "Run a saved funnel over a relative period.", InputSchema: object([]string{"site", "funnel_id", "period"}, map[string]any{"site": site, "funnel_id": map[string]any{"type": "integer", "minimum": 1}, "period": period})},
	}
}

func mergeMCPSchema(schema map[string]any, extra map[string]any) map[string]any {
	for key, value := range extra {
		schema[key] = value
	}
	return schema
}
