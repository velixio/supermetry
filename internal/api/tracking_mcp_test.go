package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"panel/internal/privacy"
	"strings"
	"testing"
)

func TestAnonymousMCPRejectsHistoricIdentity(t *testing.T) {
	for _, tc := range []struct{ name, args string }{
		{"get_breakdown", `{"site":1,"period":"7d","dimension":"page","metrics":["visitors"]}`},
		{"get_summary", `{"site":1,"period":"7d","filters":[{"dimension":"prop:email","operator":"is","values":["private"]}]}`},
		{"get_realtime", `{"site":1}`},
		{"list_goals", `{"site":1}`},
		{"run_funnel", `{"site":1,"funnel_id":1,"period":"7d"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := &mcpQueryStub{}
			h := &mcpHandler{deps: MCPDeps{Query: service}}
			req := httptest.NewRequest("POST", "/api/public/v1/mcp", nil)
			ctx := context.WithValue(req.Context(), principalContextKey{}, Principal{AllSites: true, Scopes: []string{"stats:read"}})
			req = req.WithContext(ctx)
			_, err := h.runTool(ctx, req, tc.name, json.RawMessage(tc.args))
			wantCalls := 0
			if tc.name == "get_breakdown" {
				wantCalls = 1
			} // Allowed shape, rejected unverified response.
			if err == nil || !strings.Contains(err.Error(), "anonymous tracking policy") || len(service.queries) != wantCalls {
				t.Fatalf("err=%v calls=%d", err, len(service.queries))
			}
		})
	}
}

type verifiedMCPQuery struct {
	mcpQueryStub
	response QueryResponse
}

func (q *verifiedMCPQuery) Query(context.Context, QueryRequest) (QueryResponse, error) {
	return q.response, nil
}

func TestAnonymousMCPVerifiedPathsUseCurrentPolicy(t *testing.T) {
	paths, err := privacy.NewPathPolicy([]string{"/public"})
	if err != nil {
		t.Fatal(err)
	}
	for _, valid := range []bool{false, true} {
		name := "missing verification"
		if valid {
			name = "current verified policy"
		}
		t.Run(name, func(t *testing.T) {
			response := QueryResponse{Rows: []map[string]any{{"page": "/public", "visitors": 1}}, Totals: map[string]float64{"visitors": 1}, Meta: QueryMeta{SourcesUsed: []string{"verified_hot"}}}
			if valid {
				response.Meta.Privacy = &QueryPrivacy{View: "verified_paths", Coverage: "verified_only", PolicyId: paths.Fingerprint(), PathSanitizerVersion: int(privacy.PathSanitizerVersion), ImportsExcluded: true, UnverifiedExcluded: true}
			}
			h := NewMCPHandler(MCPDeps{Query: &verifiedMCPQuery{response: response}, TrackingPaths: paths})
			req := httptest.NewRequest("POST", "/api/public/v1/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_breakdown","arguments":{"site":1,"period":"7d","dimension":"page","metrics":["visitors"]}}}`))
			req = req.WithContext(context.WithValue(req.Context(), principalContextKey{}, Principal{AllSites: true, Scopes: []string{"stats:read"}}))
			r := httptest.NewRecorder()
			h.ServeHTTP(r, req)
			if strings.Contains(r.Body.String(), "/public") != valid {
				t.Fatalf("verified=%v body=%s", valid, r.Body.String())
			}
			if valid && !strings.Contains(r.Body.String(), "verified_only") {
				t.Fatalf("missing verified coverage: %s", r.Body.String())
			}
		})
	}
}
