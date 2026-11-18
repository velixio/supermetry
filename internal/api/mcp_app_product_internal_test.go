package api

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"supermetry/internal/appquery"
	"supermetry/internal/privacy"
)

type productReaderStub struct{}

func (productReaderStub) ActiveUsers(context.Context, appquery.Scope, appquery.Filter) (appquery.ActiveUsers, error) {
	return appquery.ActiveUsers{Series: []appquery.ActivePoint{{DAU: 2, WAU: 3, MAU: 4}}, Distinct: 4}, nil
}

func (productReaderStub) Engagement(context.Context, appquery.Scope, appquery.Filter) (appquery.Engagement, error) {
	return appquery.Engagement{Sessions: 5}, nil
}

func mcpProductHandler(mode privacy.Mode) (*mcpQueryStub, func(tool, arguments string) *httptest.ResponseRecorder) {
	queries := &mcpQueryStub{}
	handler := NewMCPHandler(MCPDeps{
		TrackingMode: mode, AppProduct: productReaderStub{}, Query: queries, Sessions: mcpSessionStub{},
		Now: func() time.Time { return time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC) },
	})
	return queries, func(tool, arguments string) *httptest.ResponseRecorder { return callMCPApp(handler, tool, arguments) }
}

func TestMCPProductToolsListed(t *testing.T) {
	handler := NewMCPHandler(MCPDeps{TrackingMode: privacy.Identified, AppProduct: productReaderStub{}})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, mcpAppRequest(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, "stats:read"))
	for name := range mcpAppProductToolNames {
		if !strings.Contains(response.Body.String(), `"name":"`+name+`"`) {
			t.Errorf("tool %s is not listed", name)
		}
	}
	bare := NewMCPHandler(MCPDeps{TrackingMode: privacy.Identified})
	response = httptest.NewRecorder()
	bare.ServeHTTP(response, mcpAppRequest(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, "stats:read"))
	if strings.Contains(response.Body.String(), "get_active_users") {
		t.Error("product tools listed without a product reader")
	}
}

// Review Focus 5: an anonymous instance answers active users and engagement
// but refuses the identity-bearing reads exactly as web projects do.
func TestMCPProductToolsInAnonymousMode(t *testing.T) {
	queries, call := mcpProductHandler(privacy.Anonymous)
	base := `"site":4,"period":"7d"`
	for _, tool := range []string{"get_active_users", "get_product_overview"} {
		if body := call(tool, `{`+base+`}`).Body.String(); strings.Contains(body, `"error"`) || strings.Contains(body, `"isError":true`) {
			t.Errorf("%s refused in anonymous mode: %s", tool, body)
		}
	}
	for tool, extra := range map[string]string{
		"get_retention":    ``,
		"get_funnel":       `,"steps":[{"type":"screen","value":"/a"},{"type":"event","value":"b"}]`,
		"get_user_journey": `,"from_screen":"/a"`,
	} {
		body := call(tool, `{`+base+extra+`}`).Body.String()
		if !strings.Contains(body, ErrAnonymousHistory.Error()) {
			t.Errorf("%s in anonymous mode = %s", tool, body)
		}
	}
	if len(queries.funnels) != 0 {
		t.Errorf("an anonymous funnel reached the engine")
	}
}

func TestMCPProductToolsRejectWebSiteAndForeignSite(t *testing.T) {
	_, call := mcpProductHandler(privacy.Identified)
	if body := call("get_active_users", `{"site":1,"period":"7d"}`).Body.String(); !strings.Contains(body, "not an app") {
		t.Errorf("web site = %s", body)
	}
	if body := call("get_active_users", `{"site":5,"period":"7d"}`).Body.String(); !strings.Contains(body, "site not found") && !strings.Contains(body, "not found") {
		t.Errorf("other org site = %s", body)
	}
}
