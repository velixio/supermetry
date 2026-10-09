package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"panel/internal/privacy"
	"strings"
	"testing"
	"time"

	"panel/internal/session"
	"panel/internal/sites"
)

type mcpQueryStub struct {
	queries []QueryRequest
	funnels []FunnelRequest
}

func (s *mcpQueryStub) Query(_ context.Context, request QueryRequest) (QueryResponse, error) {
	s.queries = append(s.queries, request)
	return QueryResponse{Columns: []string{"visitors"}, Rows: []map[string]any{{"visitors": float64(3)}}, Totals: map[string]float64{"visitors": 3}}, nil
}

func (*mcpQueryStub) Journey(context.Context, JourneyRequest) (JourneyResponse, error) {
	return JourneyResponse{}, nil
}

func (*mcpQueryStub) Retention(context.Context, RetentionRequest) (RetentionResponse, error) {
	return RetentionResponse{}, nil
}

func (s *mcpQueryStub) Funnel(_ context.Context, request FunnelRequest) (FunnelResponse, error) {
	s.funnels = append(s.funnels, request)
	return FunnelResponse{Steps: []FunnelStepResult{{Step: 1, Visitors: 3, Conversion: 100}}}, nil
}

type mcpSessionStub struct{}

func (mcpSessionStub) Touch(session.Hit) session.Result            { return session.Result{} }
func (mcpSessionStub) Dirty(int) []session.Snapshot                { return nil }
func (mcpSessionStub) ActiveSince(int32, time.Time) (int, []int32) { return 2, []int32{7} }
func (mcpSessionStub) Restore([]session.Snapshot)                  {}
func (mcpSessionStub) Expire(time.Time) int                        { return 0 }
func (mcpSessionStub) RealtimeSnapshot(_ int32, now time.Time) session.RealtimeSnapshot {
	minutes := make([]session.MinuteVisitors, 30)
	for i := range minutes {
		minutes[i] = session.MinuteVisitors{Minute: now.Add(time.Duration(i-29) * time.Minute), Visitors: i}
	}
	return session.RealtimeSnapshot{
		ActiveVisitors: 2, Pages: []session.ActiveValue{{ID: 7, Visitors: 2}},
		VisitorsPerMinute: minutes, Pageviews: 3,
		RecentActivity: []session.RecentActivity{{Kind: 0, PathID: 7, Timestamp: now}},
	}
}

type mcpRegistry map[int32]sites.Site

func (r mcpRegistry) ByPublicID(string) (sites.Site, bool) { return sites.Site{}, false }
func (r mcpRegistry) Site(id int32) (sites.Site, bool)     { site, ok := r[id]; return site, ok }
func (r mcpRegistry) All() []sites.Site                    { return nil }

func TestMCPHandler_ProtocolMethods(t *testing.T) {
	handler := NewMCPHandler(MCPDeps{TrackingMode: privacy.Identified})

	t.Run("initialize negotiates current protocol", func(t *testing.T) {
		response := callAuthorizedMCP(t, handler, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`)
		if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/json" {
			t.Fatalf("status=%d content-type=%q body=%s", response.Code, response.Header().Get("Content-Type"), response.Body.String())
		}
		var envelope struct {
			Result struct {
				ProtocolVersion string `json:"protocolVersion"`
				Capabilities    struct {
					Tools map[string]any `json:"tools"`
				} `json:"capabilities"`
				ServerInfo struct {
					Name    string `json:"name"`
					Version string `json:"version"`
				} `json:"serverInfo"`
			} `json:"result"`
		}
		decodeMCPResponse(t, response, &envelope)
		if envelope.Result.ProtocolVersion != MCPProtocolVersion || envelope.Result.Capabilities.Tools == nil || envelope.Result.ServerInfo.Name != "panel" || envelope.Result.ServerInfo.Version == "" {
			t.Fatalf("unexpected initialize result: %+v", envelope.Result)
		}
	})

	t.Run("ping returns empty result", func(t *testing.T) {
		response := callAuthorizedMCP(t, handler, `{"jsonrpc":"2.0","id":"p","method":"ping"}`)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"result":{}`) {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	})

	t.Run("tools list exposes schemas", func(t *testing.T) {
		response := callAuthorizedMCP(t, handler, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
		var envelope struct {
			Result struct {
				Tools []MCPTool `json:"tools"`
			} `json:"result"`
		}
		decodeMCPResponse(t, response, &envelope)
		want := map[string][]string{
			"list_sites": {}, "get_summary": {"site"},
			"get_timeseries": {"site", "period", "interval", "metrics"},
			"get_breakdown":  {"site", "dimension", "metrics", "period"},
			"get_realtime":   {"site"}, "list_goals": {"site"},
			"run_funnel": {"site", "funnel_id", "period"},
		}
		for _, tool := range envelope.Result.Tools {
			required, expected := want[tool.Name]
			if !expected {
				t.Fatalf("unexpected tool %q", tool.Name)
			}
			delete(want, tool.Name)
			if tool.InputSchema["type"] != "object" || tool.InputSchema["properties"] == nil {
				t.Fatalf("tool %s has invalid schema: %#v", tool.Name, tool.InputSchema)
			}
			gotRequired := make([]string, 0)
			if raw, ok := tool.InputSchema["required"].([]any); ok {
				for _, value := range raw {
					gotRequired = append(gotRequired, value.(string))
				}
			}
			if strings.Join(gotRequired, ",") != strings.Join(required, ",") {
				t.Fatalf("tool %s required=%v want=%v", tool.Name, gotRequired, required)
			}
		}
		if len(want) != 0 {
			t.Fatalf("missing tools: %v", want)
		}
		for _, tool := range envelope.Result.Tools {
			if tool.Name == "get_realtime" && !strings.Contains(tool.Description, "30 minutes") {
				t.Fatalf("get_realtime description=%q, want 30 minute window", tool.Description)
			}
		}
	})

	t.Run("notification is accepted without a response body", func(t *testing.T) {
		response := callAuthorizedMCP(t, handler, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
		if response.Code != http.StatusAccepted || response.Body.Len() != 0 {
			t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
		}
	})
}

func TestMCPHandler_ToolsCall(t *testing.T) {
	query := &mcpQueryStub{}
	resources := http.NewServeMux()
	resources.HandleFunc("GET /api/public/v1/sites", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"data": []map[string]any{{"id": 1, "name": "Example"}}, "next_cursor": nil})
	})
	resources.HandleFunc("GET /api/public/v1/sites/{site}/goals", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, []map[string]any{{"id": 9, "site_id": 1, "name": "Signup"}})
	})
	resources.HandleFunc("GET /api/public/v1/sites/{site}/funnels", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, []SavedFunnel{{Id: 11, SiteId: 1, Name: "Signup", Steps: []FunnelStep{{Type: Page, Match: "exact", Value: "/"}, {Type: Event, Match: "exact", Value: "signup"}}}})
	})
	handler := NewMCPHandler(MCPDeps{TrackingMode: privacy.Identified, Query: query, Resources: resources, Sessions: mcpSessionStub{}, Now: func() time.Time { return time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC) }})

	tests := []struct{ name, tool, arguments, contains string }{
		{name: "list sites", tool: "list_sites", arguments: `{}`, contains: `Example`},
		{name: "summary", tool: "get_summary", arguments: `{"site":1,"period":"30d","filters":[{"dimension":"country","operator":"is","values":["IN"]}]}`, contains: `visitors`},
		{name: "timeseries", tool: "get_timeseries", arguments: `{"site":1,"period":"7d","interval":"day","metrics":["visitors","pageviews"],"filters":[]}`, contains: `visitors`},
		{name: "breakdown", tool: "get_breakdown", arguments: `{"site":1,"dimension":"page","metrics":["visitors"],"period":"month","filters":[],"limit":5}`, contains: `visitors`},
		{name: "realtime", tool: "get_realtime", arguments: `{"site":1}`, contains: `pageviews_last_30m\":3`},
		{name: "list goals", tool: "list_goals", arguments: `{"site":1}`, contains: `Signup`},
		{name: "run funnel", tool: "run_funnel", arguments: `{"site":1,"funnel_id":11,"period":"7d"}`, contains: `conversion`},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := `{"jsonrpc":"2.0","id":` + string(rune('1'+index)) + `,"method":"tools/call","params":{"name":"` + test.tool + `","arguments":` + test.arguments + `}}`
			response := callAuthorizedMCP(t, handler, body)
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), test.contains) || !strings.Contains(response.Body.String(), `"type":"text"`) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
	if len(query.queries) != 3 || len(query.funnels) != 1 {
		t.Fatalf("query calls=%d funnel calls=%d", len(query.queries), len(query.funnels))
	}
	if got := query.queries[1]; got.Dimensions == nil || len(*got.Dimensions) != 1 || (*got.Dimensions)[0] != "time" {
		t.Fatalf("timeseries request=%+v", got)
	}
}

func TestMCPHandler_Errors(t *testing.T) {
	handler := NewMCPHandler(MCPDeps{TrackingMode: privacy.Identified})
	tests := []struct {
		name, body string
		code       int
	}{
		{name: "parse error", body: `{`, code: -32700},
		{name: "batch unsupported", body: `[]`, code: -32600},
		{name: "wrong JSON-RPC version", body: `{"jsonrpc":"1.0","id":1,"method":"ping"}`, code: -32600},
		{name: "unknown method", body: `{"jsonrpc":"2.0","id":1,"method":"missing"}`, code: -32601},
		{name: "invalid tool arguments", body: `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_realtime","arguments":{}}}`, code: -32602},
		{name: "oversized body", body: `{"jsonrpc":"2.0","id":1,"method":"ping","params":{"pad":"` + strings.Repeat("x", maxQueryBodyBytes) + `"}}`, code: -32700},
		{name: "null tool arguments", body: `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_sites","arguments":null}}`, code: -32602},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := callAuthorizedMCP(t, handler, test.body)
			var envelope struct {
				Error struct {
					Code int `json:"code"`
				} `json:"error"`
			}
			decodeMCPResponse(t, response, &envelope)
			if response.Code != http.StatusOK || envelope.Error.Code != test.code {
				t.Fatalf("status=%d error=%+v body=%s", response.Code, envelope.Error, response.Body.String())
			}
		})

	}

	t.Run("unsupported protocol header is bad request", func(t *testing.T) {
		request := authorizedMCPRequest(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)
		request.Header.Set("MCP-Protocol-Version", "2099-01-01")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	})

	t.Run("cross-origin request is forbidden", func(t *testing.T) {
		request := authorizedMCPRequest(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)
		request.Header.Set("Origin", "https://attacker.example")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusForbidden {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	})
}

func TestMCPHandler_Authorization(t *testing.T) {
	handler := NewMCPHandler(MCPDeps{TrackingMode: privacy.Identified, Sessions: mcpSessionStub{}})

	t.Run("missing principal is unauthorized", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodPost, "http://panel.test/api/public/v1/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	})

	t.Run("missing stats scope is forbidden", func(t *testing.T) {
		request := authorizedMCPRequest(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)
		ctx := context.WithValue(request.Context(), principalContextKey{}, Principal{OrgID: 7, Scopes: []string{"sites:read"}, SiteIDs: []int32{1}})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request.WithContext(ctx))
		if response.Code != http.StatusForbidden {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	})

	t.Run("foreign and unassigned sites are denied on every site tool", func(t *testing.T) {
		query := &mcpQueryStub{}
		denying := NewMCPHandler(MCPDeps{TrackingMode: privacy.Identified, Query: query, Resources: http.NewServeMux(), Sessions: mcpSessionStub{}})
		calls := []string{
			`{"name":"get_summary","arguments":{"site":%d,"period":"7d"}}`,
			`{"name":"get_timeseries","arguments":{"site":%d,"period":"7d","interval":"day","metrics":["visitors"]}}`,
			`{"name":"get_breakdown","arguments":{"site":%d,"period":"7d","dimension":"page","metrics":["visitors"]}}`,
			`{"name":"get_realtime","arguments":{"site":%d}}`,
			`{"name":"list_goals","arguments":{"site":%d}}`,
			`{"name":"run_funnel","arguments":{"site":%d,"funnel_id":1,"period":"7d"}}`,
		}
		// Site 2 belongs to another organization; site 3 is in the key's
		// organization but outside its site restriction.
		for _, site := range []int{2, 3} {
			for _, call := range calls {
				body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":` + fmt.Sprintf(call, site) + `}`
				response := callAuthorizedMCP(t, denying, body)
				var envelope struct {
					Result mcpCallResult `json:"result"`
				}
				decodeMCPResponse(t, response, &envelope)
				if response.Code != http.StatusOK || !envelope.Result.IsError || len(envelope.Result.Content) != 1 || !strings.Contains(envelope.Result.Content[0].Text, "site not found") {
					t.Fatalf("site %d call %s: status=%d body=%s", site, call, response.Code, response.Body.String())
				}
			}
		}
		if len(query.queries) != 0 || len(query.funnels) != 0 {
			t.Fatalf("denied calls reached the query engine: queries=%d funnels=%d", len(query.queries), len(query.funnels))
		}
	})
}

func callAuthorizedMCP(t *testing.T, handler http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := authorizedMCPRequest(body)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func authorizedMCPRequest(body string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "http://panel.test/api/public/v1/mcp", strings.NewReader(body))
	principal := Principal{OrgID: 7, Scopes: []string{"stats:read"}, SiteIDs: []int32{1}}
	registry := mcpRegistry{1: {ID: 1, OrgID: 7, TZ: time.UTC}, 2: {ID: 2, OrgID: 8, TZ: time.UTC}, 3: {ID: 3, OrgID: 7, TZ: time.UTC}}
	ctx := context.WithValue(request.Context(), principalContextKey{}, principal)
	ctx = context.WithValue(ctx, registryContextKey{}, sites.Registry(registry))
	return request.WithContext(ctx)
}

func decodeMCPResponse(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), target); err != nil {
		t.Fatalf("decode %q: %v", response.Body.String(), err)
	}
}
