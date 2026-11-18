package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"supermetry/internal/appquery"
	"supermetry/internal/privacy"
	"supermetry/internal/sites"
)

type appReaderStub struct {
	filter  appquery.Filter
	issueQ  appquery.IssueQuery
	hitsLim int
	issueID int64
	events  int
	cmpA    string
	cmpB    string
	minDrop float64
}

func (s *appReaderStub) Overview(_ context.Context, _ appquery.Scope, f appquery.Filter) (appquery.Overview, error) {
	s.filter = f
	v := 99.5
	return appquery.Overview{Events: 3, CrashFreeUsers: &v}, nil
}

func (s *appReaderStub) Issues(_ context.Context, _ appquery.Scope, in appquery.IssueQuery) (appquery.IssueList, error) {
	s.filter, s.issueQ = in.Filter, in
	return appquery.IssueList{Issues: []appquery.IssueRow{{ID: 4, Title: "Null pointer in Save"}}, Total: 1}, nil
}

func (s *appReaderStub) Issue(_ context.Context, _ appquery.Scope, id int64, f appquery.Filter) (appquery.IssueDetail, error) {
	s.filter, s.issueID = f, id
	if id == 404 {
		return appquery.IssueDetail{}, appquery.ErrNotFound
	}
	return appquery.IssueDetail{ID: id, Title: "Null pointer in Save", LatestEvents: make([]appquery.EventSample, s.events)}, nil
}

func (s *appReaderStub) SearchEvents(_ context.Context, _ appquery.Scope, in appquery.IssueQuery, limit int) ([]appquery.EventHit, error) {
	s.filter, s.issueQ, s.hitsLim = in.Filter, in, limit
	return []appquery.EventHit{{EventID: "ev1", IssueID: 4, Release: "1.4.0", Platform: "linux"}}, nil
}

func (s *appReaderStub) Compatibility(_ context.Context, _ appquery.Scope, f appquery.Filter) ([]appquery.CompatRow, error) {
	s.filter = f
	return []appquery.CompatRow{{OSVersion: "14.2"}}, nil
}

func (s *appReaderStub) Releases(_ context.Context, _ appquery.Scope, f appquery.Filter) ([]appquery.ReleaseRow, error) {
	s.filter = f
	return []appquery.ReleaseRow{{Release: "1.4.0"}}, nil
}

func (s *appReaderStub) ReleaseDetail(_ context.Context, _ appquery.Scope, f appquery.Filter, release string) (appquery.ReleaseDetail, error) {
	s.filter = f
	if release == "0.0.0" {
		return appquery.ReleaseDetail{}, appquery.ErrNotFound
	}
	v := 0.97
	return appquery.ReleaseDetail{Release: release, Status: "open", CommitCount: 3, Health: appquery.ReleaseRow{Release: release, CrashFreeSessions: &v},
		Rollout: []appquery.RolloutPoint{{Day: "2026-10-07", Sessions: 12}}}, nil
}

func (s *appReaderStub) CompareReleases(_ context.Context, _ appquery.Scope, f appquery.Filter, a, b string) (appquery.ReleaseComparison, error) {
	s.filter, s.cmpA, s.cmpB = f, a, b
	var out appquery.ReleaseComparison
	d := -0.03
	out.Delta.CrashFreeSessions = &d
	out.OnlyInB = []appquery.TopIssue{{ID: 9, Title: "Only in b", Events: 2}}
	return out, nil
}

func (s *appReaderStub) FindRegressionRelease(_ context.Context, _ appquery.Scope, f appquery.Filter, minDrop float64) (*appquery.RegressionHit, error) {
	s.filter, s.minDrop = f, minDrop
	return &appquery.RegressionHit{Release: "1.2.0", Previous: "1.1.0", NewIssues: []appquery.TopIssue{}}, nil
}

const (
	mcpAppSite     = 4
	mcpAppOtherOrg = 5
	mcpWebSite     = 1
)

func mcpAppHandler(mode privacy.Mode, reader appHealthReader) http.Handler {
	return NewMCPHandler(MCPDeps{
		TrackingMode: mode, AppHealth: reader, Query: &mcpQueryStub{}, Sessions: mcpSessionStub{},
		Now: func() time.Time { return time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC) },
	})
}

func mcpAppRequest(body string, scopes ...string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "http://supermetry.test/api/public/v1/mcp", strings.NewReader(body))
	principal := Principal{OrgID: 7, Scopes: scopes}
	registry := mcpRegistry{
		mcpWebSite:     {ID: mcpWebSite, OrgID: 7, TZ: time.UTC, Kind: sites.KindWeb},
		mcpAppSite:     {ID: mcpAppSite, OrgID: 7, TZ: time.UTC, Kind: sites.KindApp},
		mcpAppOtherOrg: {ID: mcpAppOtherOrg, OrgID: 8, TZ: time.UTC, Kind: sites.KindApp},
	}
	ctx := context.WithValue(request.Context(), principalContextKey{}, principal)
	ctx = context.WithValue(ctx, registryContextKey{}, sites.Registry(registry))
	return request.WithContext(ctx)
}

func callMCPApp(handler http.Handler, tool, arguments string) *httptest.ResponseRecorder {
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + tool + `","arguments":` + arguments + `}}`
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, mcpAppRequest(body, "stats:read"))
	return response
}

func mcpErrorOf(t *testing.T, response *httptest.ResponseRecorder) (int, string) {
	t.Helper()
	var out struct {
		Error *mcpError `json:"error"`
	}
	decodeMCPResponse(t, response, &out)
	if out.Error == nil {
		return 0, ""
	}
	detail, _ := out.Error.Data.(string)
	return out.Error.Code, detail
}

var appToolNames = []string{"get_app_overview", "list_app_issues", "get_app_issue", "get_app_compatibility", "list_app_releases", "search_app_events", "get_release_health", "compare_releases", "find_regression_release"}

func TestMCPAppToolsListed(t *testing.T) {
	handler := mcpAppHandler(privacy.Identified, &appReaderStub{})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, mcpAppRequest(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, "stats:read"))
	var out struct {
		Result struct{ Tools []MCPTool } `json:"result"`
	}
	decodeMCPResponse(t, response, &out)
	found := map[string]MCPTool{}
	for _, tool := range out.Result.Tools {
		found[tool.Name] = tool
	}
	for _, name := range appToolNames {
		tool, ok := found[name]
		if !ok {
			t.Fatalf("%s missing from tools/list", name)
		}
		if tool.InputSchema["additionalProperties"] != false || !strings.Contains(tool.Description, "App projects only.") {
			t.Fatalf("%s schema/description: %+v", name, tool)
		}
		required, _ := tool.InputSchema["required"].([]any)
		if len(required) == 0 || required[0] != "site" {
			t.Fatalf("%s required=%v", name, tool.InputSchema["required"])
		}
	}
	// Without a reader the app tools are not offered.
	response = httptest.NewRecorder()
	NewMCPHandler(MCPDeps{}).ServeHTTP(response, mcpAppRequest(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, "stats:read"))
	if strings.Contains(response.Body.String(), "get_app_overview") {
		t.Fatal("app tools listed without a reader")
	}
}

func TestMCPAppToolsCall(t *testing.T) {
	stub := &appReaderStub{}
	handler := mcpAppHandler(privacy.Identified, stub)
	for _, test := range []struct{ tool, arguments, contains string }{
		{"get_app_overview", `{"site":4,"period":"7d"}`, `crash_free_users`},
		{"list_app_issues", `{"site":4,"period":"7d"}`, `Null pointer in Save`},
		{"get_app_issue", `{"site":4,"issue_id":4,"period":"7d"}`, `Null pointer in Save`},
		{"get_app_compatibility", `{"site":4,"period":"7d"}`, `os_version`},
		{"list_app_releases", `{"site":4,"period":"7d"}`, `1.4.0`},
		{"compare_releases", `{"site":4,"period":"7d","a":"1.3.0","b":"1.4.0"}`, `Only in b`},
		{"find_regression_release", `{"site":4,"period":"7d","min_drop":0.02}`, `1.2.0`},
	} {
		t.Run(test.tool, func(t *testing.T) {
			stub.filter = appquery.Filter{}
			response := callMCPApp(handler, test.tool, test.arguments)
			if !strings.Contains(response.Body.String(), test.contains) || !strings.Contains(response.Body.String(), `"type":"text"`) {
				t.Fatalf("body=%s", response.Body.String())
			}
			want := appquery.Filter{From: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)}
			if stub.filter != want {
				t.Fatalf("filter=%+v want %+v", stub.filter, want)
			}
		})
	}
	callMCPApp(handler, "list_app_issues", `{"site":4,"period":"7d"}`)
	if q := stub.issueQ; q.Status != "open" || q.Limit != 25 || q.Offset != 0 {
		t.Fatalf("defaults: %+v", q)
	}
	callMCPApp(handler, "list_app_issues", `{"site":4,"date_from":"2026-09-01","date_to":"2026-09-30","status":"all","sort":"users","limit":100,"offset":5,"q":"nil","kind":"crash","release":"1.4.0"}`)
	if q := stub.issueQ; q.Status != "all" || q.Sort != "users" || q.Limit != 100 || q.Offset != 5 || q.Release != "1.4.0" || q.Filter.From.Day() != 1 {
		t.Fatalf("passthrough: %+v", q)
	}
}

func TestMCPAppSearch(t *testing.T) {
	stub := &appReaderStub{}
	handler := mcpAppHandler(privacy.Identified, stub)
	callMCPApp(handler, "list_app_issues", `{"site":4,"period":"7d","query":"is:unresolved !level:info events:>10"}`)
	if q := stub.issueQ.Search; q == nil || len(q.Terms) != 3 || q.Terms[1].Key != "level" || !q.Terms[1].Negate {
		t.Fatalf("list query not parsed: %+v", q)
	}
	response := callMCPApp(handler, "search_app_events", `{"site":4,"period":"7d","query":"release:1.4.0","limit":50}`)
	body := response.Body.String()
	if stub.hitsLim != 50 || stub.issueQ.Search == nil || !strings.Contains(body, "ev1") {
		t.Fatalf("search_app_events: limit=%d body=%s", stub.hitsLim, body)
	}
	for _, banned := range []string{"user_hash", "install_hash", "session_hash", "body"} {
		if strings.Contains(body, banned) {
			t.Errorf("search_app_events output carries %q: %s", banned, body)
		}
	}
	for name, test := range map[string]struct{ tool, arguments string }{
		"list unknown key":   {"list_app_issues", `{"site":4,"period":"7d","query":"bogus:1"}`},
		"list nul":           {"list_app_issues", `{"site":4,"period":"7d","query":"a\u0000b"}`},
		"list too long":      {"list_app_issues", `{"site":4,"period":"7d","query":"` + strings.Repeat("a", 201) + `"}`},
		"search no query":    {"search_app_events", `{"site":4,"period":"7d"}`},
		"search limit 51":    {"search_app_events", `{"site":4,"period":"7d","query":"a","limit":51}`},
		"search limit 0 neg": {"search_app_events", `{"site":4,"period":"7d","query":"a","limit":-1}`},
		"search bad syntax":  {"search_app_events", `{"site":4,"period":"7d","query":"is:"}`},
	} {
		t.Run(name, func(t *testing.T) {
			if code, _ := mcpErrorOf(t, callMCPApp(handler, test.tool, test.arguments)); code != -32602 {
				t.Fatalf("code=%d", code)
			}
		})
	}
}

func TestMCPAppIssueLatestEventsCapped(t *testing.T) {
	response := callMCPApp(mcpAppHandler(privacy.Identified, &appReaderStub{events: 12}), "get_app_issue", `{"site":4,"issue_id":4,"period":"7d"}`)
	var out struct {
		Result mcpCallResult `json:"result"`
	}
	decodeMCPResponse(t, response, &out)
	var detail appquery.IssueDetail
	if err := json.Unmarshal([]byte(out.Result.Content[0].Text), &detail); err != nil || len(detail.LatestEvents) != 5 {
		t.Fatalf("err=%v events=%d", err, len(detail.LatestEvents))
	}
	code, detail2 := mcpErrorOf(t, callMCPApp(mcpAppHandler(privacy.Identified, &appReaderStub{}), "get_app_issue", `{"site":4,"issue_id":404,"period":"7d"}`))
	if code != -32602 || detail2 != "issue not found" {
		t.Fatalf("code=%d detail=%q", code, detail2)
	}
}

func TestMCPAppToolsRejectWrongKind(t *testing.T) {
	handler := mcpAppHandler(privacy.Identified, &appReaderStub{})
	for _, tool := range appToolNames {
		arguments := `{"site":1,"period":"7d"}`
		switch tool {
		case "get_app_issue":
			arguments = `{"site":1,"issue_id":1,"period":"7d"}`
		case "get_release_health":
			arguments = `{"site":1,"period":"7d","release":"1.0.0"}`
		case "compare_releases":
			arguments = `{"site":1,"period":"7d","a":"1.0.0","b":"1.1.0"}`
		}
		code, detail := mcpErrorOf(t, callMCPApp(handler, tool, arguments))
		if code != -32602 || detail != "site is not an app; use get_summary and the other analytics tools" {
			t.Fatalf("%s: code=%d detail=%q", tool, code, detail)
		}
	}
	for tool, arguments := range map[string]string{
		"get_summary":    `{"site":4,"period":"7d"}`,
		"get_timeseries": `{"site":4,"period":"7d","interval":"day","metrics":["visitors"]}`,
		"get_breakdown":  `{"site":4,"period":"7d","dimension":"page","metrics":["visitors"]}`,
		"get_realtime":   `{"site":4}`,
		"run_funnel":     `{"site":4,"funnel_id":1,"period":"7d"}`,
	} {
		code, detail := mcpErrorOf(t, callMCPApp(handler, tool, arguments))
		if code != -32602 || detail != "site is an app; use the get_app_* tools" {
			t.Fatalf("%s: code=%d detail=%q", tool, code, detail)
		}
	}
	// A foreign app site reveals nothing about its kind.
	response := callMCPApp(handler, "get_summary", `{"site":5,"period":"7d"}`)
	if !strings.Contains(response.Body.String(), "site not found") {
		t.Fatalf("foreign: %s", response.Body.String())
	}
}

func TestMCPAppToolsAnonymousMode(t *testing.T) {
	handler := mcpAppHandler(privacy.Anonymous, &appReaderStub{})
	if response := callMCPApp(handler, "get_app_overview", `{"site":4,"period":"7d"}`); !strings.Contains(response.Body.String(), "crash_free_users") {
		t.Fatalf("app tool refused: %s", response.Body.String())
	}
	if response := callMCPApp(handler, "get_realtime", `{"site":1}`); !strings.Contains(response.Body.String(), ErrAnonymousHistory.Error()) {
		t.Fatalf("realtime allowed: %s", response.Body.String())
	}
}

func TestMCPAppToolsValidateInput(t *testing.T) {
	handler := mcpAppHandler(privacy.Identified, &appReaderStub{})
	long := func(n int) string { return strings.Repeat("a", n) }
	for name, test := range map[string]struct{ tool, arguments string }{
		"limit too big":       {"list_app_issues", `{"site":4,"period":"7d","limit":101}`},
		"limit negative":      {"list_app_issues", `{"site":4,"period":"7d","limit":-1}`},
		"offset negative":     {"list_app_issues", `{"site":4,"period":"7d","offset":-1}`},
		"bad sort":            {"list_app_issues", `{"site":4,"period":"7d","sort":"bogus"}`},
		"bad status":          {"list_app_issues", `{"site":4,"period":"7d","status":"bogus"}`},
		"q too long":          {"list_app_issues", `{"site":4,"period":"7d","q":"` + long(101) + `"}`},
		"release too long":    {"get_app_overview", `{"site":4,"period":"7d","release":"` + long(65) + `"}`},
		"environment long":    {"list_app_releases", `{"site":4,"period":"7d","environment":"` + long(33) + `"}`},
		"platform long":       {"get_app_compatibility", `{"site":4,"period":"7d","platform":"` + long(33) + `"}`},
		"from after to":       {"get_app_overview", `{"site":4,"date_from":"2026-10-08","date_to":"2026-10-01"}`},
		"range over 366 days": {"get_app_overview", `{"site":4,"date_from":"2025-01-01","date_to":"2026-10-01"}`},
		"period and dates":    {"get_app_overview", `{"site":4,"period":"7d","date_from":"2026-10-01","date_to":"2026-10-02"}`},
		"no range":            {"get_app_overview", `{"site":4}`},
		"bad period":          {"get_app_overview", `{"site":4,"period":"custom"}`},
		"issue id zero":       {"get_app_issue", `{"site":4,"issue_id":0,"period":"7d"}`},
		"unknown field":       {"get_app_overview", `{"site":4,"period":"7d","x":1}`},
		"missing site":        {"get_app_overview", `{"period":"7d"}`},
	} {
		t.Run(name, func(t *testing.T) {
			if code, _ := mcpErrorOf(t, callMCPApp(handler, test.tool, test.arguments)); code != -32602 {
				t.Fatalf("code=%d", code)
			}
		})
	}
	// 366 days apart is the inclusive limit and still accepted.
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -366)
	if code, _ := mcpErrorOf(t, callMCPApp(handler, "get_app_overview", `{"site":4,"date_from":"`+from.Format(time.DateOnly)+`","date_to":"2026-10-01"}`)); code != 0 {
		t.Fatalf("366 days rejected: %d", code)
	}
}

func TestMCPReleaseTools(t *testing.T) {
	stub := &appReaderStub{}
	handler := mcpAppHandler(privacy.Identified, stub)
	response := callMCPApp(handler, "get_release_health", `{"site":4,"period":"7d","release":"1.4.0"}`)
	if body := response.Body.String(); !strings.Contains(body, `commit_count`) || stub.filter.Release != "1.4.0" {
		t.Fatalf("get_release_health: %s filter=%+v", body, stub.filter)
	}
	callMCPApp(handler, "compare_releases", `{"site":4,"period":"7d","a":"1.3.0","b":"1.4.0"}`)
	if stub.cmpA != "1.3.0" || stub.cmpB != "1.4.0" {
		t.Fatalf("compare args = %q %q", stub.cmpA, stub.cmpB)
	}
	callMCPApp(handler, "find_regression_release", `{"site":4,"period":"7d","min_drop":0.02}`)
	if stub.minDrop != 0.02 {
		t.Fatalf("min_drop = %v", stub.minDrop)
	}
	if code, detail := mcpErrorOf(t, callMCPApp(handler, "get_release_health", `{"site":4,"period":"7d","release":"0.0.0"}`)); code != -32602 || detail != "release not found" {
		t.Fatalf("unknown release: %d %q", code, detail)
	}
	for name, test := range map[string]struct{ tool, arguments string }{
		"release missing": {"get_release_health", `{"site":4,"period":"7d"}`},
		"a missing":       {"compare_releases", `{"site":4,"period":"7d","b":"1.4.0"}`},
		"drop too big":    {"find_regression_release", `{"site":4,"period":"7d","min_drop":2}`},
		"release long":    {"get_release_health", `{"site":4,"period":"7d","release":"` + strings.Repeat("x", 65) + `"}`},
	} {
		if code, _ := mcpErrorOf(t, callMCPApp(handler, test.tool, test.arguments)); code != -32602 {
			t.Fatalf("%s: code=%d, want -32602", name, code)
		}
	}
}
