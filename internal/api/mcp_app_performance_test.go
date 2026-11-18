package api

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"supermetry/internal/appquery"
	"supermetry/internal/db"
	"supermetry/internal/privacy"
	"supermetry/internal/spans"
	"supermetry/internal/testpg"
)

var perfToolNames = []string{"get_performance_overview", "list_slow_transactions", "get_transaction", "get_trace"}

func TestMCPPerformanceToolsListed(t *testing.T) {
	// A reader without the performance methods gets no performance tools.
	for name, reader := range map[string]appHealthReader{"stub": &appReaderStub{}, "querier": appquery.New(nil, nil)} {
		handler := mcpAppHandler(privacy.Identified, reader)
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
		for _, n := range perfToolNames {
			tool, ok := found[n]
			if (name == "querier") != ok {
				t.Fatalf("%s: tool %s listed=%v", name, n, ok)
			}
			if ok && (tool.InputSchema["additionalProperties"] != false || !strings.Contains(tool.Description, "App projects only.")) {
				t.Errorf("%s schema/description: %+v", n, tool)
			}
		}
	}
}

func perfMCPSetup(t *testing.T) (call func(tool, args string) (string, int, string), userHash []byte, trace, big string) {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into orgs(id,name,slug) overriding system value values (7,'Org','org7'),(8,'Other','org8')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into sites(id,org_id,public_id,name,timezone,retention_class,kind) overriding system value
values (1,7,'mcpperfweb01','Web','UTC',30,'web'),(4,7,'mcpperfapp04','App','UTC',30,'app'),(5,8,'mcpperfapp05','Other','UTC',30,'app')`); err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	if err := db.EnsureDailyPartitions(ctx, pool, "spans", day.AddDate(0, 0, -2), day.AddDate(0, 0, 3)); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureAppEventPartitions(ctx, pool, day.AddDate(0, 0, -2), day.AddDate(0, 0, 3)); err != nil {
		t.Fatal(err)
	}
	start := day.Add(10 * time.Hour)
	insert := func(site int32, trace [16]byte, span, parent byte, segment bool, name, op, desc string, at time.Time, durUS int64) {
		var parentArg any
		if parent != 0 {
			parentArg = []byte{0, 0, 0, 0, 0, 0, 0, parent}
		}
		seg := []byte{0, 0, 0, 0, 0, 0, 0, 1}
		if _, err := pool.Exec(ctx, `insert into spans(site_id,start_ts,duration_us,bucket,trace_id,span_id,parent_span_id,segment_id,is_segment,name,op,description)
values($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, site, at, durUS, spans.Bucket(durUS), trace[:], []byte{0, 0, 0, 0, 0, 0, 0, span}, parentArg, seg, segment, name, op, desc); err != nil {
			t.Fatal(err)
		}
	}
	var tr [16]byte
	tr[15] = 0x11
	insert(4, tr, 1, 0, true, "GET /items", "http.server", "", start, 120_000)
	insert(4, tr, 2, 1, false, "", "db", "select * from users where email = ?", start.Add(time.Millisecond), 40_000)
	insert(4, tr, 3, 2, false, "", "http.client", "GET https://api.example.com/v1/items/*", start.Add(2*time.Millisecond), 10_000)
	var bigTr [16]byte
	bigTr[15] = 0x22
	for i := 0; i < mcpTraceSpans+1; i++ {
		insert(4, bigTr, 1, 0, i == 0, "GET /big", "task", "t", start.Add(time.Duration(i)*time.Millisecond), 1000)
	}
	// Another organisation's trace and transaction.
	var foreign [16]byte
	foreign[15] = 0x33
	insert(5, foreign, 1, 0, true, "GET /foreign", "http.server", "", start, 90_000)

	userHash = []byte("secret-user-hash-0123456789abcdef")
	var issue int64
	if err := pool.QueryRow(ctx, `insert into app_issues(site_id,fingerprint,kind,error_type,title,first_seen,last_seen,event_count)
values(4,'fp','error','Err','Boom in handler',$1,$1,1) returning id`, start).Scan(&issue); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into app_events(site_id,ts,received_at,event_id,issue_id,kind,trace_id,span_id,user_hash)
values(4,$1,$1,'evt-secret-1',$2,'error',$3,$4,$5)`, start, issue, tr[:], []byte{0, 0, 0, 0, 0, 0, 0, 2}, userHash); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	handler := NewMCPHandler(MCPDeps{
		TrackingMode: privacy.Identified, AppHealth: appquery.New(pool, func() time.Time { return now }),
		Query: &mcpQueryStub{}, Sessions: mcpSessionStub{}, Now: func() time.Time { return now },
	})
	call = func(tool, args string) (string, int, string) {
		t.Helper()
		response := callMCPApp(handler, tool, args)
		code, detail := mcpErrorOf(t, response)
		if code == 0 && strings.Contains(response.Body.String(), `"isError":true`) {
			// Tool errors (site not found) are results flagged isError.
			code, detail = -1, response.Body.String()
		}
		return response.Body.String(), code, detail
	}
	return call, userHash, hex.EncodeToString(tr[:]), hex.EncodeToString(bigTr[:])
}

func TestMCPPerformanceTools(t *testing.T) {
	call, _, trace, big := perfMCPSetup(t)

	out, code, detail := call("get_performance_overview", `{"site":4,"period":"today"}`)
	if code != 0 || !strings.Contains(out, `GET /items`) {
		t.Fatalf("overview = %d %s %s", code, detail, out)
	}
	out, code, detail = call("list_slow_transactions", `{"site":4,"period":"today","limit":2}`)
	if code != 0 || !strings.Contains(out, `GET /items`) {
		t.Fatalf("slow = %d %s %s", code, detail, out)
	}
	if _, code, _ = call("list_slow_transactions", `{"site":4,"period":"today","limit":21}`); code != -32602 {
		t.Errorf("limit 21 code = %d, want -32602", code)
	}
	out, code, detail = call("get_transaction", `{"site":4,"period":"today","name":"GET /items"}`)
	if code != 0 || !strings.Contains(out, "select * from users where email = ?") {
		t.Fatalf("transaction = %d %s %s", code, detail, out)
	}
	if _, code, detail = call("get_transaction", `{"site":4,"period":"today","name":"missing"}`); code != -32602 || !strings.Contains(detail, "transaction not found") {
		t.Errorf("missing transaction = %d %q", code, detail)
	}
	out, code, detail = call("get_trace", fmt.Sprintf(`{"site":4,"trace_id":%q}`, trace))
	if code != 0 || !strings.Contains(out, "Boom in handler") {
		t.Fatalf("trace = %d %s %s", code, detail, out)
	}
	// Malformed ids are parameter errors, never internal errors.
	for _, id := range []string{"xyz", strings.Repeat("g", 32), strings.Repeat("a", 300), strings.Repeat("0", 31)} {
		if _, code, _ = call("get_trace", fmt.Sprintf(`{"site":4,"trace_id":%q}`, id)); code != -32602 {
			t.Errorf("trace id %.8q code = %d, want -32602", id, code)
		}
	}
	if _, code, detail = call("get_trace", fmt.Sprintf(`{"site":4,"trace_id":%q}`, strings.Repeat("0", 32))); code != -32602 || !strings.Contains(detail, "trace not found") {
		t.Errorf("unknown trace = %d %q", code, detail)
	}
	// 501 spans in the trace: at most 500 in the output, flagged truncated.
	out, code, _ = call("get_trace", fmt.Sprintf(`{"site":4,"trace_id":%q}`, big))
	var parsed struct {
		Result struct {
			Content []struct{ Text string } `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil || code != 0 || len(parsed.Result.Content) == 0 {
		t.Fatalf("big trace = %d %v %s", code, err, out)
	}
	var tr struct {
		Spans     []json.RawMessage `json:"spans"`
		Truncated bool              `json:"truncated"`
	}
	if err := json.Unmarshal([]byte(parsed.Result.Content[0].Text), &tr); err != nil || len(tr.Spans) != mcpTraceSpans || !tr.Truncated {
		t.Errorf("big trace spans=%d truncated=%v err=%v", len(tr.Spans), tr.Truncated, err)
	}
}

func TestMCPPerformanceToolsAreSiteScoped(t *testing.T) {
	call, _, trace, _ := perfMCPSetup(t)
	for tool, args := range map[string]string{
		"get_performance_overview": `{"site":%d,"period":"today"}`,
		"list_slow_transactions":   `{"site":%d,"period":"today"}`,
		"get_transaction":          `{"site":%d,"period":"today","name":"GET /foreign"}`,
		"get_trace":                `{"site":%d,"trace_id":"` + trace + `"}`,
	} {
		// Another organisation's app: the same error as an unknown site.
		if _, code, detail := call(tool, fmt.Sprintf(args, 5)); code == 0 || !strings.Contains(detail, "site not found") {
			t.Errorf("%s other org = %d %q", tool, code, detail)
		}
		// A web site of the caller's organisation is not an app.
		if _, code, detail := call(tool, fmt.Sprintf(args, 1)); code == 0 || !strings.Contains(detail, "not an app") {
			t.Errorf("%s web site = %d %q", tool, code, detail)
		}
		// A site that does not exist at all.
		if _, code, detail := call(tool, fmt.Sprintf(args, 999)); code == 0 || !strings.Contains(detail, "site not found") {
			t.Errorf("%s unknown site = %d %q", tool, code, detail)
		}
	}
	// Another organisation's data is not visible through this site's tools.
	if out, code, _ := call("list_slow_transactions", `{"site":4,"period":"today"}`); code != 0 || strings.Contains(out, "GET /foreign") {
		t.Errorf("site 4 sees foreign transactions: %s", out)
	}
	// The key needs stats:read.
	response := httptest.NewRecorder()
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_trace","arguments":{"site":4,"trace_id":"` + trace + `"}}}`
	mcpAppHandler(privacy.Identified, appquery.New(nil, nil)).ServeHTTP(response, mcpAppRequest(body))
	if code, _ := mcpErrorOf(t, response); code == 0 {
		t.Errorf("a key without stats:read read a trace: %s", response.Body)
	}
}

var ipv4 = regexp.MustCompile(`\b\d{1,3}(\.\d{1,3}){3}\b`)

// Trace and transaction output carries no user identifier, IP, email, event id
// or span id, although the trace has an error event with a user hash.
func TestMCPPerformanceToolsArePrivate(t *testing.T) {
	call, userHash, trace, _ := perfMCPSetup(t)
	outputs := map[string]string{}
	outputs["get_trace"], _, _ = call("get_trace", fmt.Sprintf(`{"site":4,"trace_id":%q}`, trace))
	outputs["get_transaction"], _, _ = call("get_transaction", `{"site":4,"period":"today","name":"GET /items"}`)
	outputs["get_performance_overview"], _, _ = call("get_performance_overview", `{"site":4,"period":"today"}`)
	outputs["list_slow_transactions"], _, _ = call("list_slow_transactions", `{"site":4,"period":"today"}`)
	if !strings.Contains(outputs["get_trace"], "Boom in handler") {
		t.Fatalf("the trace output lacks the linked error; the test would prove nothing: %s", outputs["get_trace"])
	}
	for tool, out := range outputs {
		for _, leak := range []string{
			"@", "user_hash", "user_id", "ip_address", "evt-secret-1", "event_id", "span_id", "parent_id",
			hex.EncodeToString(userHash), base64.StdEncoding.EncodeToString(userHash), "0000000000000002",
		} {
			if strings.Contains(out, leak) {
				t.Errorf("%s leaks %q: %s", tool, leak, out)
			}
		}
		if m := ipv4.FindString(out); m != "" {
			t.Errorf("%s holds an IP-like value %q: %s", tool, m, out)
		}
	}
}
