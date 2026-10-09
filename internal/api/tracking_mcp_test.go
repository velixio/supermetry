package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
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
			if err == nil || !strings.Contains(err.Error(), "anonymous tracking policy") || len(service.queries) != 0 {
				t.Fatalf("err=%v calls=%d", err, len(service.queries))
			}
		})
	}
}
