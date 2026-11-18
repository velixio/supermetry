package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"supermetry/internal/appfixture"
	"supermetry/internal/approllup"
	"supermetry/internal/closer"
	"supermetry/internal/db"
	"supermetry/internal/testapi"
)

// seedMCPProduct loads the plan's hand-computed product dataset (installs A-D,
// 2026-10-06..10-09, D's hits late) into an app site and rolls the days up.
func seedMCPProduct(t *testing.T, env *testapi.Env, site testapi.Site) {
	t.Helper()
	ctx := context.Background()
	from, to := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	if err := db.EnsureAppEventPartitions(ctx, env.Pool, from, to); err != nil {
		t.Fatal(err)
	}
	p, err := appfixture.AttachProduct(env.Pool, site.ID, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	p.AddDataset(t, true)
	for _, day := range []string{"2026-10-06", "2026-10-07", "2026-10-08", "2026-10-09"} {
		if err := approllup.RecomputeDay(ctx, env.Pool, site.ID, appfixture.Day(day)); err != nil {
			t.Fatal(err)
		}
		if _, err := closer.CloseDay(ctx, env.Pool, []int32{site.ID}, appfixture.Day(day)); err != nil {
			t.Fatal(err)
		}
	}
	env.ReloadDictionary(t)
}

// mcpProductResult returns the decoded text payload of a successful tool call.
func mcpProductResult(t *testing.T, env *testapi.Env, key, tool, arguments string) (map[string]any, string) {
	t.Helper()
	r := mcpCall(t, env, key, tool, arguments)
	var out struct {
		Result struct {
			IsError bool
			Content []struct{ Text string }
		} `json:"result"`
	}
	if err := json.Unmarshal(r.Body, &out); err != nil || len(out.Result.Content) != 1 || out.Result.IsError {
		t.Fatalf("%s %s: %v %s", tool, arguments, err, r.Body)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(out.Result.Content[0].Text), &decoded); err != nil {
		t.Fatalf("%s payload: %v", tool, err)
	}
	return decoded, out.Result.Content[0].Text
}

func floats(rows any, key string) []float64 {
	var out []float64
	for _, r := range rows.([]any) {
		out = append(out, r.(map[string]any)[key].(float64))
	}
	return out
}

const mcpProductRange = `"date_from":"2026-10-06","date_to":"2026-10-09"`

var mcpLongHex = regexp.MustCompile(`[0-9a-fA-F]{32,}`)

func TestMCPProductToolsHandComputed(t *testing.T) {
	env := testapi.New(t)
	org := env.Org(t)
	site := env.AppSite(t, org)
	seedMCPProduct(t, env, site)
	key := env.APIKey(t, org, "stats:read")
	base := `"site":` + site.String() + `,` + mcpProductRange

	var texts []string
	call := func(tool, extra string) map[string]any {
		out, text := mcpProductResult(t, env, key, tool, `{`+base+extra+`}`)
		texts = append(texts, text)
		return out
	}

	active := call("get_active_users", ``)
	if got := floats(active["series"], "dau"); !reflect.DeepEqual(got, []float64{3, 2, 2, 1}) {
		t.Fatalf("DAU = %v", got)
	}
	if active["distinct"].(float64) != 4 || active["stickiness"].(float64) != 0.5 {
		t.Fatalf("active = %v", active)
	}

	overview := call("get_product_overview", ``)
	latest := overview["latest"].(map[string]any)
	if latest["dau"].(float64) != 1 || latest["wau"].(float64) != 4 || overview["stickiness"].(float64) != 0.5 {
		t.Fatalf("overview = %v", overview)
	}
	screens := overview["top_screens"].([]any)
	if len(screens) == 0 || screens[0].(map[string]any)["page"] != "/home" {
		t.Fatalf("top screens = %v", screens)
	}
	if events := overview["top_events"].([]any); len(events) != 1 || events[0].(map[string]any)["event"] != "purchase" {
		t.Fatalf("top events = %v", events)
	}

	steps := `[{"type":"screen","value":"/home"},{"type":"screen","value":"/paywall"},{"type":"event","value":"purchase"}]`
	byInstalls := call("get_funnel", `,"steps":`+steps+`,"count_by":"installs"`)
	if got := floats(byInstalls["steps"], "count"); !reflect.DeepEqual(got, []float64{4, 3, 2}) {
		t.Fatalf("funnel by installs = %v", got)
	}
	bySessions := call("get_funnel", `,"steps":`+steps)
	if got := floats(bySessions["steps"], "count"); !reflect.DeepEqual(got, []float64{8, 3, 2}) {
		t.Fatalf("funnel by sessions = %v", got)
	}

	retention := call("get_retention", `,"interval":"day"`)
	cohorts := retention["cohorts"].([]any)
	first := cohorts[0].(map[string]any)
	if first["size"].(float64) != 3 || !reflect.DeepEqual(floats([]any{map[string]any{"v": first["retained"].([]any)[0]}}, "v"), []float64{3}) {
		t.Fatalf("cohort = %v", first)
	}
	var retained []float64
	for _, v := range first["retained"].([]any) {
		retained = append(retained, v.(float64))
	}
	if !reflect.DeepEqual(retained, []float64{3, 1, 2, 1}) {
		t.Fatalf("retained = %v", retained)
	}

	journey := call("get_user_journey", `,"from_screen":"/home","depth":2`)
	var toPaywall float64
	for _, l := range journey["links"].([]any) {
		link := l.(map[string]any)
		if link["from_path"] == "/home" && link["to_path"] == "/paywall" {
			toPaywall = link["visitors"].(float64)
		}
	}
	if toPaywall != 3 {
		t.Fatalf("journey /home > /paywall = %v (%v)", toPaywall, journey)
	}

	byEvent := call("get_event_breakdown", `,"by":"event"`)
	rows := byEvent["rows"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["event"] != "purchase" || rows[0].(map[string]any)["events"].(float64) != 3 {
		t.Fatalf("events = %v", rows)
	}
	byScreen := call("get_event_breakdown", `,"by":"screen"`)
	views := map[string]float64{}
	for _, r := range byScreen["rows"].([]any) {
		row := r.(map[string]any)
		views[row["page"].(string)] = row["pageviews"].(float64)
	}
	if !reflect.DeepEqual(views, map[string]float64{"/home": 8, "/paywall": 3}) {
		t.Fatalf("screen views = %v", views)
	}

	// Privacy: no emails, user hashes, install ids or profile ids in any output.
	for _, text := range texts {
		lower := strings.ToLower(text)
		for _, banned := range []string{`"i:`, "@", "install_id", "install_hash", "visitor_id", "hash", "profile", "external", "email"} {
			if strings.Contains(lower, banned) {
				t.Fatalf("output contains %q: %s", banned, text)
			}
		}
		if mcpLongHex.MatchString(text) {
			t.Fatalf("output contains a long hex string: %s", text)
		}
	}
}

func TestMCPProductToolInputValidation(t *testing.T) {
	env := testapi.New(t)
	org := env.Org(t)
	site := env.AppSite(t, org)
	key := env.APIKey(t, org, "stats:read")
	base := `"site":` + site.String() + `,"period":"30d"`
	for name, c := range map[string]struct{ tool, extra string }{
		"one step":      {"get_funnel", `,"steps":[{"type":"screen","value":"/a"}]`},
		"nine steps":    {"get_funnel", `,"steps":[` + strings.Repeat(`{"type":"screen","value":"/a"},`, 8) + `{"type":"screen","value":"/a"}]`},
		"bad step type": {"get_funnel", `,"steps":[{"type":"page","value":"/a"},{"type":"event","value":"b"}]`},
		"bad count_by":  {"get_funnel", `,"steps":[{"type":"screen","value":"/a"},{"type":"event","value":"b"}],"count_by":"users"`},
		"no start":      {"get_user_journey", ``},
		"deep journey":  {"get_user_journey", `,"from_screen":"/a","depth":6`},
		"bad interval":  {"get_retention", `,"interval":"hour"`},
		"bad by":        {"get_event_breakdown", `,"by":"user"`},
		"unknown field": {"get_active_users", `,"install":"x"`},
	} {
		r := mcpCall(t, env, key, c.tool, `{`+base+c.extra+`}`)
		if !strings.Contains(string(r.Body), `"error"`) || !strings.Contains(string(r.Body), "-32602") {
			t.Errorf("%s: want invalid params, got %d %s", name, r.Status, r.Body)
		}
	}
}

// Review Focus 4: another org, a restricted key, a key without the scope and a
// web project never reach product data.
func TestMCPProductToolsKeepTenantAndScopeBoundaries(t *testing.T) {
	env := testapi.New(t)
	org, otherOrg := env.Org(t), env.Org(t)
	siteA, siteB, foreign, web := env.AppSite(t, org), env.AppSite(t, org), env.AppSite(t, otherOrg), env.Site(t, org)
	seedMCPProduct(t, env, siteB)
	seedMCPProduct(t, env, foreign)
	statsKey := env.APIKey(t, org, "stats:read")
	restricted := env.APIKeyForSites(t, org, []int32{siteA.ID}, "stats:read")
	noScope := env.APIKey(t, org, "data:export")

	tools := map[string]string{
		"get_product_overview": ``, "get_active_users": ``, "get_retention": ``,
		"get_funnel":          `,"steps":[{"type":"screen","value":"/home"},{"type":"event","value":"purchase"}]`,
		"get_event_breakdown": ``, "get_user_journey": `,"from_screen":"/home"`,
	}
	for tool, extra := range tools {
		args := func(s testapi.Site) string {
			return `{"site":` + s.String() + `,"date_from":"2026-10-06","date_to":"2026-10-09"` + extra + `}`
		}
		if r := mcpCall(t, env, statsKey, tool, args(siteB)); strings.Contains(string(r.Body), "site not found") || strings.Contains(string(r.Body), `"isError":true`) || r.Status != http.StatusOK {
			t.Fatalf("%s own site: %d %s", tool, r.Status, r.Body)
		}
		if r := mcpCall(t, env, restricted, tool, args(siteA)); strings.Contains(string(r.Body), "site not found") {
			t.Fatalf("%s restricted key on its own site: %s", tool, r.Body)
		}
		for name, c := range map[string]struct {
			key  string
			site testapi.Site
		}{"restricted key, other site": {restricted, siteB}, "foreign org": {statsKey, foreign}} {
			if r := mcpCall(t, env, c.key, tool, args(c.site)); !strings.Contains(string(r.Body), "site not found") || !strings.Contains(string(r.Body), `"isError":true`) {
				t.Fatalf("%s %s: %s", tool, name, r.Body)
			}
		}
		r := mcpCall(t, env, noScope, tool, args(siteA))
		if got := r.Headers.Get("WWW-Authenticate"); got != `Bearer error="insufficient_scope", scope="stats:read"` || !strings.Contains(string(r.Body), "-32003") {
			t.Fatalf("%s without scope: header=%q body=%s", tool, got, r.Body)
		}
		if r := mcpCall(t, env, statsKey, tool, args(web)); !strings.Contains(string(r.Body), "not an app") {
			t.Fatalf("%s on a web project: %s", tool, r.Body)
		}
	}
}
