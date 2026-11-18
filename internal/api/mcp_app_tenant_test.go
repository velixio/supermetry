package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"supermetry/internal/approllup"
	"supermetry/internal/db"
	"supermetry/internal/testapi"
)

type mcpAppSeed struct {
	issue int64
	rawID string // raw identifier that must never leave the server
}

// seedMCPAppSite writes one issue and one crash event with a recognizable
// install hash and event id, then rolls the day up.
func seedMCPAppSite(t *testing.T, env *testapi.Env, site testapi.Site, title string) mcpAppSeed {
	t.Helper()
	ctx := context.Background()
	day := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	at := day.Add(10 * time.Hour)
	if err := db.EnsureAppEventPartitions(ctx, env.Pool, day, day.AddDate(0, 1, 0)); err != nil {
		t.Fatal(err)
	}
	var issue int64
	if err := env.Pool.QueryRow(ctx, `insert into app_issues(site_id,fingerprint,kind,error_type,title,status,first_release,last_release,first_seen,last_seen,event_count)
values($1,$4,'crash','Panic',$2,'open','1.0.0','1.0.0',$3,$3,1) returning id`, site.ID, title, at, "fp-"+title).Scan(&issue); err != nil {
		t.Fatal(err)
	}
	rawID := "rawevent-secret-" + title
	if _, err := env.Pool.Exec(ctx, `insert into app_events(site_id,ts,received_at,event_id,issue_id,kind,level,release,environment,platform,os_version,arch,device_class,runtime,error_type,frames,occurrences,install_hash,session_hash,tags)
values($1,$2,$2,$3,$4,'crash','fatal','1.0.0','production','linux','24.04','x64','cpu','go','Panic','a.go:1 f',1,decode('deadbeefcafe0000deadbeefcafe0000','hex'),decode('feedfacefeed0000feedfacefeed0000','hex'),'{}')`, site.ID, at, rawID, issue); err != nil {
		t.Fatal(err)
	}
	if err := approllup.RecomputeDay(ctx, env.Pool, site.ID, day); err != nil {
		t.Fatal(err)
	}
	return mcpAppSeed{issue: issue, rawID: rawID}
}

func mcpCall(t *testing.T, env *testapi.Env, key, tool, arguments string) testapi.Resp {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + tool + `","arguments":` + arguments + `}}`
	return env.Do(t, testapi.Req{Method: http.MethodPost, Path: "/api/public/v1/mcp", Body: body, APIKey: key})
}

func TestMCPAppIssueOtherSiteNotFound(t *testing.T) {
	env := testapi.New(t)
	org := env.Org(t)
	siteA, siteB := env.AppSite(t, org), env.AppSite(t, org)
	seedMCPAppSite(t, env, siteA, "Issue A")
	b := seedMCPAppSite(t, env, siteB, "Issue B secret title")
	key := env.APIKey(t, org, "stats:read")

	own := mcpCall(t, env, key, "get_app_issue", `{"site":`+siteB.String()+`,"issue_id":`+itoa(b.issue)+`,"period":"30d"}`)
	if !strings.Contains(string(own.Body), "Issue B secret title") {
		t.Fatalf("own issue not returned: %s", own.Body)
	}
	other := mcpCall(t, env, key, "get_app_issue", `{"site":`+siteA.String()+`,"issue_id":`+itoa(b.issue)+`,"period":"30d"}`)
	if !strings.Contains(string(other.Body), "issue not found") || strings.Contains(string(other.Body), "Issue B secret title") {
		t.Fatalf("cross-site issue leaked: %s", other.Body)
	}
}

func TestMCPAppToolsKeepTenantAndScopeBoundaries(t *testing.T) {
	env := testapi.New(t)
	org, otherOrg := env.Org(t), env.Org(t)
	siteA, siteB, foreign := env.AppSite(t, org), env.AppSite(t, org), env.AppSite(t, otherOrg)
	statsKey := env.APIKey(t, org, "stats:read")
	restricted := env.APIKeyForSites(t, org, []int32{siteA.ID}, "stats:read")
	noScope := env.APIKey(t, org, "data:export")

	args := func(s testapi.Site) string { return `{"site":` + s.String() + `,"period":"30d"}` }
	for _, tool := range []string{"get_app_overview", "list_app_issues", "get_app_compatibility", "list_app_releases"} {
		if r := mcpCall(t, env, statsKey, tool, args(siteB)); strings.Contains(string(r.Body), "site not found") || r.Status != http.StatusOK {
			t.Fatalf("%s own site: %d %s", tool, r.Status, r.Body)
		}
		if r := mcpCall(t, env, restricted, tool, args(siteA)); strings.Contains(string(r.Body), "site not found") {
			t.Fatalf("%s restricted key on its own site: %s", tool, r.Body)
		}
		for name, site := range map[string]testapi.Site{"restricted key, other site": siteB, "foreign org": foreign} {
			key := statsKey
			if name == "restricted key, other site" {
				key = restricted
			}
			if r := mcpCall(t, env, key, tool, args(site)); !strings.Contains(string(r.Body), "site not found") || !strings.Contains(string(r.Body), `"isError":true`) {
				t.Fatalf("%s %s: %s", tool, name, r.Body)
			}
		}
		r := mcpCall(t, env, noScope, tool, args(siteA))
		if got := r.Headers.Get("WWW-Authenticate"); got != `Bearer error="insufficient_scope", scope="stats:read"` || !strings.Contains(string(r.Body), "-32003") {
			t.Fatalf("%s without scope: header=%q body=%s", tool, got, r.Body)
		}
	}
}

func TestMCPAppOutputHasNoIdentifiers(t *testing.T) {
	env := testapi.New(t)
	org := env.Org(t)
	site := env.AppSite(t, org)
	seed := seedMCPAppSite(t, env, site, "Visible title")
	key := env.APIKey(t, org, "stats:read")
	base := `"site":` + site.String() + `,"period":"30d"`
	for tool, arguments := range map[string]string{
		"get_app_overview":        `{` + base + `}`,
		"list_app_issues":         `{` + base + `,"status":"all"}`,
		"get_app_issue":           `{` + base + `,"issue_id":` + itoa(seed.issue) + `}`,
		"get_app_compatibility":   `{` + base + `}`,
		"list_app_releases":       `{` + base + `}`,
		"get_release_health":      `{` + base + `,"release":"1.0.0"}`,
		"compare_releases":        `{` + base + `,"a":"1.0.0","b":"1.0.0"}`,
		"find_regression_release": `{` + base + `}`,
	} {
		r := mcpCall(t, env, key, tool, arguments)
		var out struct {
			Result struct{ Content []struct{ Text string } } `json:"result"`
		}
		if err := json.Unmarshal(r.Body, &out); err != nil || len(out.Result.Content) != 1 {
			t.Fatalf("%s: %v %s", tool, err, r.Body)
		}
		text := out.Result.Content[0].Text
		if strings.Contains(string(r.Body), `"isError":true`) {
			t.Fatalf("%s failed: %s", tool, text)
		}
		lower := strings.ToLower(text)
		for _, banned := range []string{seed.rawID, "deadbeef", "feedface", "3q2+78r+AADerb7vyv4AAA==", "/u36zv7tAAD+7frO/u0AAA==", "install_hash", "session_hash", "smk_", "@", key} {
			if strings.Contains(lower, strings.ToLower(banned)) {
				t.Fatalf("%s output contains %q: %s", tool, banned, text)
			}
		}
	}
}

func itoa(v int64) string { b, _ := json.Marshal(v); return string(b) }
