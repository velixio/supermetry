package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMCPDirectQueriesKeepTenantAndScopeChecks(t *testing.T) {
	for _, test := range []struct {
		name      string
		principal Principal
		accept    string
		want      string
	}{
		{"foreign before scope", Principal{OrgID: 8}, "", "site not found"},
		{"stats required", Principal{OrgID: 7}, "", "required scope"},
		{"stats does not export", Principal{OrgID: 7, Scopes: []string{"stats:read"}}, "text/csv", "data:export"},
		{"export and stats", Principal{OrgID: 7, Scopes: []string{"stats:read", "data:export"}}, "text/csv", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &mcpQueryStub{}
			h := &mcpHandler{deps: MCPDeps{Query: service}}
			r := httptest.NewRequest("POST", "http://supermetry.test/api/public/v1/mcp", nil)
			r.Header.Set("Accept", test.accept)
			ctx := context.WithValue(r.Context(), principalContextKey{}, test.principal)
			ctx = context.WithValue(ctx, registryContextKey{}, fakeRegistry{1: {ID: 1, OrgID: 7}})
			r = r.WithContext(ctx)
			_, err := h.runTool(ctx, r, "get_summary", json.RawMessage(`{"site":1,"period":"7d"}`))
			if test.want == "" {
				if err != nil || len(service.queries) != 1 {
					t.Fatalf("err=%v calls=%d", err, len(service.queries))
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.want) || len(service.queries) != 0 {
				t.Fatalf("err=%v want=%s calls=%d", err, test.want, len(service.queries))
			}
		})
	}
}
