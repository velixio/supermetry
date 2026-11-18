package api_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"supermetry/internal/db"
	"supermetry/internal/testapi"
)

const mcpTraceHex = "5b8efff798038103d269b633813fc60c"

// seedMCPLogs writes logs at the testapi clock (2026-10-12T12:00Z): a trace of
// two rows whose text and attributes carry an address that intake would have
// scrubbed (the tool must still mask it), a user hash, and an old row.
func seedMCPLogs(t *testing.T, env *testapi.Env, site testapi.Site) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	if err := db.EnsureDailyPartitions(ctx, env.Pool, "logs", now.AddDate(0, 0, -8), now.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	for _, r := range []struct {
		rid         int
		ts, body    string
		sev         int
		attrs, hexT string
	}{
		{1, "2026-10-12T10:00:00Z", "charge failed for kim@corp.example", 17, `{"contact":"kim@corp.example","http.route":"/pay"}`, mcpTraceHex},
		{2, "2026-10-12T10:00:05Z", "retrying charge", 13, `{}`, mcpTraceHex},
		{3, "2026-10-12T09:00:00Z", "heartbeat", 9, `{}`, ""},
		{4, "2026-10-06T09:00:00Z", "charge failed long ago", 17, `{}`, ""},
	} {
		var trace any
		if r.hexT != "" {
			trace = `\x` + r.hexT
		}
		if _, err := env.Pool.Exec(ctx, `insert into logs(site_id,ts,rid,observed_at,severity,service,environment,release,trace_id,user_hash,body,attributes,source)
			values($1,$2,$3,$2,$4,'api','prod','1.0',$5::text::bytea,decode('abcdef0123456789abcdef0123456789','hex'),$6,$7::jsonb,'otlp')`, site.ID, r.ts, r.rid, r.sev, trace, r.body, r.attrs); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMCPLogToolsAreScrubbedAndKeepBoundaries(t *testing.T) {
	env := testapi.New(t)
	org, otherOrg := env.Org(t), env.Org(t)
	site, other, foreign := env.AppSite(t, org), env.AppSite(t, org), env.AppSite(t, otherOrg)
	seedMCPLogs(t, env, site)
	key := env.APIKey(t, org, "stats:read")
	restricted := env.APIKeyForSites(t, org, []int32{other.ID}, "stats:read")
	noScope := env.APIKey(t, org, "data:export")

	calls := map[string]string{
		"search_logs":    `{"site":` + site.String() + `,"query":"charge"}`,
		"get_trace_logs": `{"site":` + site.String() + `,"trace_id":"` + mcpTraceHex + `"}`,
	}
	for tool, args := range calls {
		body := string(mcpCall(t, env, key, tool, args).Body)
		if strings.Contains(body, `"isError":true`) || !strings.Contains(body, "charge") {
			t.Fatalf("%s failed: %s", tool, body)
		}
		for _, banned := range []string{"@", "kim", "corp.example", "user_hash", "abcdef0123"} {
			if strings.Contains(strings.ToLower(body), banned) {
				t.Fatalf("%s output contains %q: %s", tool, banned, body)
			}
		}
		if !strings.Contains(body, "[email]") {
			t.Fatalf("%s did not mask the address: %s", tool, body)
		}
		// The same call on another organization's site, a site outside a
		// restricted key, and a key without the scope.
		otherArgs := strings.Replace(args, site.String(), foreign.String(), 1)
		if r := mcpCall(t, env, key, tool, otherArgs); !strings.Contains(string(r.Body), "site not found") {
			t.Fatalf("%s foreign org: %s", tool, r.Body)
		}
		if r := mcpCall(t, env, restricted, tool, args); !strings.Contains(string(r.Body), "site not found") {
			t.Fatalf("%s restricted key: %s", tool, r.Body)
		}
		if r := mcpCall(t, env, noScope, tool, args); !strings.Contains(string(r.Body), "-32003") {
			t.Fatalf("%s without scope: %s", tool, r.Body)
		}
	}
	// A site of the same organization sees none of the first site's logs.
	if r := string(mcpCall(t, env, key, "search_logs", `{"site":`+other.String()+`,"query":"charge"}`).Body); strings.Contains(r, "charge failed") {
		t.Fatalf("other site leaked: %s", r)
	}
}

func TestMCPSearchLogsFilteringAndLimits(t *testing.T) {
	env := testapi.New(t)
	org := env.Org(t)
	site := env.AppSite(t, org)
	seedMCPLogs(t, env, site)
	key := env.APIKey(t, org, "stats:read")
	s := site.String()

	// Severity filter over the default 24 hours: the two trace rows are in, the heartbeat is out.
	body := string(mcpCall(t, env, key, "search_logs", `{"site":`+s+`,"query":"severity:>=warn"}`).Body)
	if !strings.Contains(body, "retrying charge") || strings.Contains(body, "heartbeat") {
		t.Fatalf("severity filter: %s", body)
	}
	// The 6-day-old row needs an explicit window.
	body = string(mcpCall(t, env, key, "search_logs", `{"site":`+s+`,"query":"long ago","from":"2026-10-05T00:00:00Z","to":"2026-10-12T12:00:00Z"}`).Body)
	if !strings.Contains(body, "charge failed long ago") {
		t.Fatalf("explicit window: %s", body)
	}
	if _, err := env.Pool.Exec(context.Background(), `update sites set settings='{"app":{"log_retention_days":3}}' where id=$1`, site.ID); err != nil {
		t.Fatal(err)
	}
	body = string(mcpCall(t, env, key, "search_logs", `{"site":`+s+`,"query":"long ago","from":"2026-10-05T00:00:00Z","to":"2026-10-12T12:00:00Z"}`).Body)
	if !strings.Contains(body, `\"logs\":[]`) {
		t.Fatalf("a 3-day site returned a 7-day-old row: %s", body)
	}
	for args, want := range map[string]string{
		`{"site":` + s + `,"limit":51}`:                    "limit must be between 1 and 50",
		`{"site":` + s + `,"query":"ab"}`:                  "at least 3 characters",
		`{"site":` + s + `,"query":"foo:x"}`:               "unknown search key",
		`{"site":` + s + `,"from":"yesterday"}`:            "RFC 3339",
		`{"site":` + s + `,"from":"2026-01-01T00:00:00Z"}`: "30 days",
	} {
		if r := string(mcpCall(t, env, key, "search_logs", args).Body); !strings.Contains(r, want) {
			t.Errorf("%s: %s, want %q", args, r, want)
		}
	}
	if r := string(mcpCall(t, env, key, "get_trace_logs", `{"site":`+s+`,"trace_id":"abc"}`).Body); !strings.Contains(r, "32 hex") {
		t.Errorf("short trace id: %s", r)
	}
	if r := string(mcpCall(t, env, key, "get_trace_logs", `{"site":`+s+`,"trace_id":"`+mcpTraceHex+`","limit":101}`).Body); !strings.Contains(r, "limit must be between 1 and 100") {
		t.Errorf("trace limit: %s", r)
	}
	tr := string(mcpCall(t, env, key, "get_trace_logs", `{"site":`+s+`,"trace_id":"`+mcpTraceHex+`"}`).Body)
	if strings.Index(tr, "charge failed") > strings.Index(tr, "retrying charge") || !strings.Contains(tr, "retrying charge") {
		t.Errorf("trace logs not oldest first: %s", tr)
	}
}
