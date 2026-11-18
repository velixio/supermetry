package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"supermetry/internal/db"
	"supermetry/internal/spans"
	"supermetry/internal/testapi"
)

const perfTraceHex = "00000000000000000000000000000abc"

// seedMCPPerfSite writes one transaction with a db child, rolled up by nothing
// (read from raw spans), and one error event with a user hash on the trace.
func seedMCPPerfSite(t *testing.T, env *testapi.Env, site testapi.Site, name string) {
	t.Helper()
	ctx := context.Background()
	at := env.Now.Add(-time.Hour)
	day := time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, time.UTC)
	if err := db.EnsureDailyPartitions(ctx, env.Pool, "spans", day.AddDate(0, 0, -1), day.AddDate(0, 0, 2)); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureAppEventPartitions(ctx, env.Pool, day.AddDate(0, 0, -1), day.AddDate(0, 0, 2)); err != nil {
		t.Fatal(err)
	}
	trace := make([]byte, 16)
	trace[14], trace[15] = 0x0a, 0xbc
	for i, s := range []struct {
		id      byte
		parent  any
		segment bool
		op, d   string
		dur     int64
	}{
		{1, nil, true, "http.server", "", 120_000},
		{2, []byte{0, 0, 0, 0, 0, 0, 0, 1}, false, "db", "select * from orders where id = ?", 40_000},
	} {
		n := ""
		if s.segment {
			n = name
		}
		if _, err := env.Pool.Exec(ctx, `insert into spans(site_id,start_ts,duration_us,bucket,trace_id,span_id,parent_span_id,segment_id,is_segment,name,op,description)
values($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, site.ID, at.Add(time.Duration(i)*time.Millisecond), s.dur, spans.Bucket(s.dur), trace,
			[]byte{0, 0, 0, 0, 0, 0, 0, s.id}, s.parent, []byte{0, 0, 0, 0, 0, 0, 0, 1}, s.segment, n, s.op, s.d); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := env.Pool.Exec(ctx, `insert into app_events(site_id,ts,received_at,event_id,kind,trace_id,span_id,user_hash)
values($1,$2,$2,'perf-event-secret','error',$3,$4,decode('deadbeefcafe0000deadbeefcafe0000','hex'))`, site.ID, at, trace, []byte{0, 0, 0, 0, 0, 0, 0, 2}); err != nil {
		t.Fatal(err)
	}
}

func perfToolArgs(site testapi.Site, name string) map[string]string {
	base := `"site":` + site.String()
	return map[string]string{
		"get_performance_overview": `{` + base + `,"period":"7d"}`,
		"list_slow_transactions":   `{` + base + `,"period":"7d"}`,
		"get_transaction":          `{` + base + `,"period":"7d","name":"` + name + `"}`,
		"get_trace":                `{` + base + `,"trace_id":"` + perfTraceHex + `"}`,
	}
}

func TestMCPPerformanceToolsKeepTenantAndScopeBoundaries(t *testing.T) {
	env := testapi.New(t)
	org, otherOrg := env.Org(t), env.Org(t)
	siteA, siteB, foreign := env.AppSite(t, org), env.AppSite(t, org), env.AppSite(t, otherOrg)
	seedMCPPerfSite(t, env, siteA, "GET /a")
	seedMCPPerfSite(t, env, siteB, "GET /b")
	seedMCPPerfSite(t, env, foreign, "GET /foreign-secret")
	statsKey := env.APIKey(t, org, "stats:read")
	restricted := env.APIKeyForSites(t, org, []int32{siteA.ID}, "stats:read")
	noScope := env.APIKey(t, org, "data:export")

	for tool, args := range perfToolArgs(siteB, "GET /b") {
		if r := mcpCall(t, env, statsKey, tool, args); r.Status != http.StatusOK || strings.Contains(string(r.Body), `"isError":true`) || !strings.Contains(string(r.Body), "result") {
			t.Fatalf("%s own site: %d %s", tool, r.Status, r.Body)
		}
	}
	for tool, args := range perfToolArgs(siteA, "GET /a") {
		if r := mcpCall(t, env, restricted, tool, args); strings.Contains(string(r.Body), "site not found") {
			t.Fatalf("%s restricted key on its own site: %s", tool, r.Body)
		}
		if r := mcpCall(t, env, noScope, tool, args); r.Headers.Get("WWW-Authenticate") != `Bearer error="insufficient_scope", scope="stats:read"` || !strings.Contains(string(r.Body), "-32003") {
			t.Fatalf("%s without scope: %s", tool, r.Body)
		}
	}
	for tool, args := range perfToolArgs(siteB, "GET /b") {
		// A key restricted to site A cannot read site B.
		if r := mcpCall(t, env, restricted, tool, args); !strings.Contains(string(r.Body), "site not found") || !strings.Contains(string(r.Body), `"isError":true`) {
			t.Fatalf("%s restricted key, other site: %s", tool, r.Body)
		}
	}
	for tool, args := range perfToolArgs(foreign, "GET /foreign-secret") {
		// Another organisation's app: indistinguishable from an unknown site.
		r := mcpCall(t, env, statsKey, tool, args)
		if !strings.Contains(string(r.Body), "site not found") || strings.Contains(string(r.Body), "foreign-secret") {
			t.Fatalf("%s foreign org: %s", tool, r.Body)
		}
	}
	// This site's tools never return another site's spans, even for its names.
	if r := mcpCall(t, env, statsKey, "get_transaction", perfToolArgs(siteA, "GET /b")["get_transaction"]); !strings.Contains(string(r.Body), "transaction not found") {
		t.Fatalf("site A read a transaction of site B: %s", r.Body)
	}
}

func TestMCPPerformanceOutputHasNoIdentifiers(t *testing.T) {
	env := testapi.New(t)
	org := env.Org(t)
	site := env.AppSite(t, org)
	seedMCPPerfSite(t, env, site, "GET /orders")
	key := env.APIKey(t, org, "stats:read")
	for tool, arguments := range perfToolArgs(site, "GET /orders") {
		r := mcpCall(t, env, key, tool, arguments)
		var out struct {
			Result struct{ Content []struct{ Text string } } `json:"result"`
		}
		if err := json.Unmarshal(r.Body, &out); err != nil || len(out.Result.Content) != 1 {
			t.Fatalf("%s: %v %s", tool, err, r.Body)
		}
		if strings.Contains(string(r.Body), `"isError":true`) {
			t.Fatalf("%s failed: %s", tool, out.Result.Content[0].Text)
		}
		lower := strings.ToLower(out.Result.Content[0].Text)
		for _, banned := range []string{"perf-event-secret", "deadbeef", "3q2+78r+aaderb7vyv4aaa==", "user_hash", "user_id", "ip_address", "@", "smk_", strings.ToLower(key), "0000000000000002"} {
			if strings.Contains(lower, banned) {
				t.Fatalf("%s output contains %q: %s", tool, banned, out.Result.Content[0].Text)
			}
		}
	}
}
