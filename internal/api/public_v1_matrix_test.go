package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"supermetry/internal/testapi"
)

type publicRoute struct {
	name   string
	method string
	path   string
	body   string
	want   int
}

func TestPublicV1MatrixEveryRouteRequiresDocumentedAuthentication(t *testing.T) {
	env := testapi.New(t)
	routes := []publicRoute{
		{name: "fallback", method: http.MethodGet, path: "/api/public/v1/not-a-route", want: http.StatusUnauthorized},
		{name: "annotations delete", method: http.MethodDelete, path: "/api/public/v1/annotations/1", want: http.StatusUnauthorized},
		{name: "annotations update", method: http.MethodPatch, path: "/api/public/v1/annotations/1", body: `{}`, want: http.StatusUnauthorized},
		{name: "api keys list", method: http.MethodGet, path: "/api/public/v1/api-keys", want: http.StatusUnauthorized},
		{name: "api keys create", method: http.MethodPost, path: "/api/public/v1/api-keys", body: `{}`, want: http.StatusUnauthorized},
		{name: "api keys revoke", method: http.MethodDelete, path: "/api/public/v1/api-keys/1", want: http.StatusUnauthorized},
		{name: "erasure get", method: http.MethodGet, path: "/api/public/v1/erasures/1", want: http.StatusUnauthorized},
		{name: "event ingest", method: http.MethodPost, path: "/api/public/v1/events", body: `{}`, want: http.StatusAccepted},
		{name: "event preflight", method: http.MethodOptions, path: "/api/public/v1/events", want: http.StatusNoContent},
		{name: "funnel query", method: http.MethodPost, path: "/api/public/v1/funnels/query", body: `{}`, want: http.StatusUnauthorized},
		{name: "funnel delete", method: http.MethodDelete, path: "/api/public/v1/funnels/1", want: http.StatusUnauthorized},
		{name: "funnel update", method: http.MethodPatch, path: "/api/public/v1/funnels/1", body: `{}`, want: http.StatusUnauthorized},
		{name: "goal delete", method: http.MethodDelete, path: "/api/public/v1/goals/1", want: http.StatusUnauthorized},
		{name: "goal update", method: http.MethodPatch, path: "/api/public/v1/goals/1", body: `{}`, want: http.StatusUnauthorized},
		{name: "heatmap query", method: http.MethodPost, path: "/api/public/v1/heatmaps/query", body: `{}`, want: http.StatusUnauthorized},
		{name: "journey query", method: http.MethodPost, path: "/api/public/v1/journeys/query", body: `{}`, want: http.StatusUnauthorized},
		{name: "mcp", method: http.MethodPost, path: "/api/public/v1/mcp", body: `{"jsonrpc":"2.0","id":1,"method":"ping"}`, want: http.StatusOK},
		{name: "profile get", method: http.MethodGet, path: "/api/public/v1/profiles/1", want: http.StatusUnauthorized},
		{name: "query", method: http.MethodPost, path: "/api/public/v1/query", body: `{}`, want: http.StatusUnauthorized},
		{name: "realtime", method: http.MethodGet, path: "/api/public/v1/realtime", want: http.StatusUnauthorized},
		{name: "realtime summary", method: http.MethodGet, path: "/api/public/v1/realtime/summary", want: http.StatusUnauthorized},
		{name: "retention query", method: http.MethodPost, path: "/api/public/v1/retention/query", body: `{}`, want: http.StatusUnauthorized},
		{name: "segment delete", method: http.MethodDelete, path: "/api/public/v1/segments/1", want: http.StatusUnauthorized},
		{name: "segment update", method: http.MethodPatch, path: "/api/public/v1/segments/1", body: `{}`, want: http.StatusUnauthorized},
		{name: "session get", method: http.MethodGet, path: "/api/public/v1/sessions/1?site_id=1", want: http.StatusUnauthorized},
		{name: "sites list", method: http.MethodGet, path: "/api/public/v1/sites", want: http.StatusUnauthorized},
		{name: "sites create", method: http.MethodPost, path: "/api/public/v1/sites", body: `{}`, want: http.StatusUnauthorized},
		{name: "site delete", method: http.MethodDelete, path: "/api/public/v1/sites/1", want: http.StatusUnauthorized},
		{name: "site get", method: http.MethodGet, path: "/api/public/v1/sites/1", want: http.StatusUnauthorized},
		{name: "site update", method: http.MethodPatch, path: "/api/public/v1/sites/1", body: `{}`, want: http.StatusUnauthorized},
		{name: "annotations list", method: http.MethodGet, path: "/api/public/v1/sites/1/annotations?from=2026-10-01&to=2026-10-12", want: http.StatusUnauthorized},
		{name: "annotations create", method: http.MethodPost, path: "/api/public/v1/sites/1/annotations", body: `{}`, want: http.StatusUnauthorized},
		{name: "events list", method: http.MethodGet, path: "/api/public/v1/sites/1/events?from=2026-10-01&to=2026-10-12", want: http.StatusUnauthorized},
		{name: "funnels list", method: http.MethodGet, path: "/api/public/v1/sites/1/funnels", want: http.StatusUnauthorized},
		{name: "funnels create", method: http.MethodPost, path: "/api/public/v1/sites/1/funnels", body: `{}`, want: http.StatusUnauthorized},
		{name: "goals list", method: http.MethodGet, path: "/api/public/v1/sites/1/goals", want: http.StatusUnauthorized},
		{name: "goals create", method: http.MethodPost, path: "/api/public/v1/sites/1/goals", body: `{}`, want: http.StatusUnauthorized},
		{name: "profiles list", method: http.MethodGet, path: "/api/public/v1/sites/1/profiles", want: http.StatusUnauthorized},
		{name: "properties list", method: http.MethodGet, path: "/api/public/v1/sites/1/properties?from=2026-10-01&to=2026-10-12", want: http.StatusUnauthorized},
		{name: "property values", method: http.MethodGet, path: "/api/public/v1/sites/1/properties/plan/values?from=2026-10-01&to=2026-10-12", want: http.StatusUnauthorized},
		{name: "segments list", method: http.MethodGet, path: "/api/public/v1/sites/1/segments", want: http.StatusUnauthorized},
		{name: "segments create", method: http.MethodPost, path: "/api/public/v1/sites/1/segments", body: `{}`, want: http.StatusUnauthorized},
		{name: "sessions list", method: http.MethodGet, path: "/api/public/v1/sites/1/sessions?from=2026-10-01&to=2026-10-12", want: http.StatusUnauthorized},
		{name: "suggestions", method: http.MethodGet, path: "/api/public/v1/sites/1/suggestions?from=2026-10-01&to=2026-10-12&dimension=page", want: http.StatusUnauthorized},
		{name: "stats breakdown", method: http.MethodGet, path: "/api/public/v1/stats/breakdown?site_id=1&period=7d&dimension=page", want: http.StatusUnauthorized},
		{name: "stats summary", method: http.MethodGet, path: "/api/public/v1/stats/summary?site_id=1&period=7d", want: http.StatusUnauthorized},
		{name: "stats timeseries", method: http.MethodGet, path: "/api/public/v1/stats/timeseries?site_id=1&period=7d", want: http.StatusUnauthorized},
		{name: "vitals query", method: http.MethodPost, path: "/api/public/v1/vitals/query", body: `{}`, want: http.StatusUnauthorized},
	}
	for index, route := range routes {
		t.Run(route.name, func(t *testing.T) {
			response := env.Do(t, testapi.Req{
				Name: t.Name(), Method: route.method, Path: route.path, Body: bodyOrNil(route.body),
				RemoteAddr: fmt.Sprintf("192.0.2.%d:1234", index+1),
			})
			if response.Status != route.want {
				t.Fatalf("status=%d body=%s, want %d", response.Status, response.Body, route.want)
			}
			if route.want == http.StatusUnauthorized && (response.Problem == nil || response.Headers.Get("Content-Type") != "application/problem+json") {
				t.Fatalf("content-type=%q problem=%+v body=%s", response.Headers.Get("Content-Type"), response.Problem, response.Body)
			}
		})
	}
}

func TestPublicV1MatrixAuthorizationBoundaries(t *testing.T) {
	env := testapi.New(t)
	org := env.Org(t)
	site := env.Site(t, org)
	otherOrg := env.Org(t)
	otherSite := env.Site(t, otherOrg)
	statsKey := env.APIKey(t, org, "stats:read")
	wrongScopeKey := env.APIKey(t, org, "data:export")
	otherKey := env.APIKey(t, otherOrg, "stats:read", "profiles:read", "goals:write", "annotations:write", "segments:write", "sites:write", "keys:manage")

	env.RunCases(t, []testapi.Case{
		{Name: "wrong scope", Method: http.MethodGet, Path: "/api/public/v1/stats/summary?site_id={site}&period=7d", Auth: testapi.AuthAPIKey, APIKey: wrongScopeKey, Values: map[string]string{"site": site.String()}, Want: http.StatusForbidden, WantProblem: "forbidden"},
		{Name: "other org site", Method: http.MethodGet, Path: "/api/public/v1/sites/{site}", Auth: testapi.AuthAPIKey, APIKey: otherKey, Values: map[string]string{"site": site.String()}, Want: http.StatusNotFound, WantProblem: "not-found"},
		{Name: "other org stats", Method: http.MethodGet, Path: "/api/public/v1/stats/summary?site_id={site}&period=7d", Auth: testapi.AuthAPIKey, APIKey: otherKey, Values: map[string]string{"site": site.String()}, Want: http.StatusNotFound, WantProblem: "not-found"},
		{Name: "malformed site id", Method: http.MethodGet, Path: "/api/public/v1/stats/summary?site_id=bad&period=7d", Auth: testapi.AuthAPIKey, APIKey: statsKey, Want: http.StatusBadRequest, WantProblem: "bad-request"},
		{Name: "malformed body", Method: http.MethodPost, Path: "/api/public/v1/query", Auth: testapi.AuthAPIKey, APIKey: statsKey, Body: `{`, Want: http.StatusBadRequest, WantProblem: "bad-request"},
		{Name: "valid summary", Method: http.MethodGet, Path: "/api/public/v1/stats/summary?site_id={site}&period=7d", Auth: testapi.AuthAPIKey, APIKey: statsKey, Values: map[string]string{"site": site.String()}, Want: http.StatusOK},
		{Name: "empty other site", Method: http.MethodGet, Path: "/api/public/v1/stats/summary?site_id={site}&period=7d", Auth: testapi.AuthAPIKey, APIKey: otherKey, Values: map[string]string{"site": otherSite.String()}, Want: http.StatusOK},
	})
}

func TestPublicV1MatrixListsLimitsCursorsAndRateHeaders(t *testing.T) {
	env := testapi.New(t)
	org := env.Org(t)
	site := env.Site(t, org)
	key := env.APIKey(t, org, "stats:read")
	ctx := context.Background()
	for index := 0; index < 2; index++ {
		at := env.Now.Add(-time.Duration(index+1) * time.Hour)
		if _, err := env.Pool.Exec(ctx, `insert into sessions(site_id,retention_class,session_id,visitor_id,start,last,local_day,pageviews) values ($1,30,$2,$3,$4,$4,$5,1)`, site.ID, 100+index, 200+index, at, at.Format(time.DateOnly)); err != nil {
			t.Fatal(err)
		}
	}
	base := "/api/public/v1/sites/" + site.String() + "/sessions?from=2026-10-11&to=2026-10-12"
	first := env.Do(t, testapi.Req{Name: t.Name() + "/default-limit", Method: http.MethodGet, Path: base, APIKey: key})
	assertMatrixJSON(t, first, http.StatusOK)
	for _, header := range []string{"RateLimit-Limit", "RateLimit-Remaining", "RateLimit-Reset"} {
		if first.Headers.Get(header) == "" {
			t.Errorf("missing %s", header)
		}
	}
	limited := env.Do(t, testapi.Req{Name: t.Name() + "/next-cursor", Method: http.MethodGet, Path: base + "&limit=1", APIKey: key})
	assertMatrixJSON(t, limited, http.StatusOK)
	cursor := nestedString(limited.JSON, "next_cursor")
	if cursor == "" {
		t.Fatal("next_cursor is empty")
	}
	next := env.Do(t, testapi.Req{Name: t.Name() + "/cursor-page", Method: http.MethodGet, Path: base + "&limit=1&cursor=" + cursor, APIKey: key})
	assertMatrixJSON(t, next, http.StatusOK)
	max := env.Do(t, testapi.Req{Name: t.Name() + "/max-limit", Method: http.MethodGet, Path: base + "&limit=1000", APIKey: key})
	assertMatrixJSON(t, max, http.StatusOK)
	oversized := timedRequest(t, env, testapi.Req{Name: t.Name() + "/oversized-limit", Method: http.MethodGet, Path: base + "&limit=1000000", APIKey: key})
	if oversized.response.Status != http.StatusBadRequest || oversized.elapsed >= 2*time.Second {
		t.Fatalf("oversized limit status=%d elapsed=%s body=%s", oversized.response.Status, oversized.elapsed, oversized.response.Body)
	}

	rateEnv := testapi.NewWithRateLimit(t, 0, 1)
	rateOrg := rateEnv.Org(t)
	rateSite := rateEnv.Site(t, rateOrg)
	rateKey := rateEnv.APIKey(t, rateOrg, "stats:read")
	var exhausted testapi.Resp
	for index := 0; index < 2; index++ {
		exhausted = rateEnv.Do(t, testapi.Req{Name: fmt.Sprintf("%s/rate-%02d", t.Name(), index), Method: http.MethodGet, Path: "/api/public/v1/stats/summary?site_id=" + rateSite.String() + "&period=7d", APIKey: rateKey})
	}
	if exhausted.Status != http.StatusTooManyRequests || exhausted.Headers.Get("Retry-After") == "" || exhausted.Problem == nil {
		t.Fatalf("rate response status=%d retry-after=%q problem=%+v", exhausted.Status, exhausted.Headers.Get("Retry-After"), exhausted.Problem)
	}
}

func TestPublicV1MatrixReviewFocusSitesAndRanges(t *testing.T) {
	env := testapi.New(t)
	org := env.Org(t)
	key := env.APIKey(t, org, "stats:read")
	empty := env.Site(t, org)
	deleted := env.Site(t, org)
	demo := env.Site(t, org)
	if _, err := env.Pool.Exec(context.Background(), `update sites set deleted_at=$2 where id=$1`, deleted.ID, env.Now); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Pool.Exec(context.Background(), `update sites set is_demo=true,demo_started_at=$2 where id=$1`, demo.ID, env.Now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	env.RefreshSites(t)

	for _, test := range []struct {
		name string
		site testapi.Site
		want int
	}{
		{name: "zero event", site: empty, want: http.StatusOK},
		{name: "restorable deletion", site: deleted, want: http.StatusNotFound},
		{name: "demo", site: demo, want: http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := env.Do(t, testapi.Req{Name: t.Name(), Method: http.MethodGet, Path: "/api/public/v1/stats/summary?site_id=" + test.site.String() + "&period=7d", APIKey: key})
			if response.Status != test.want || response.Status == http.StatusInternalServerError {
				t.Fatalf("status=%d body=%s, want %d", response.Status, response.Body, test.want)
			}
		})
	}

	long := timedRequest(t, env, testapi.Req{
		Name: t.Name() + "/ten-year-range", Method: http.MethodGet, APIKey: key,
		Path: "/api/public/v1/stats/summary?site_id=" + empty.String() + "&period=custom&date_from=2016-10-12&date_to=2026-10-12",
	})
	if long.elapsed >= 2*time.Second || (long.response.Status != http.StatusBadRequest && long.response.Status != http.StatusOK) {
		t.Fatalf("ten-year range status=%d elapsed=%s body=%s", long.response.Status, long.elapsed, long.response.Body)
	}

	for _, timezone := range []struct {
		name   string
		zone   string
		before time.Time
		after  time.Time
	}{
		{name: "plus-05-30", zone: "Asia/Kolkata", before: time.Date(2026, 10, 10, 18, 29, 0, 0, time.UTC), after: time.Date(2026, 10, 10, 18, 31, 0, 0, time.UTC)},
		{name: "plus-05-45", zone: "Asia/Kathmandu", before: time.Date(2026, 10, 10, 18, 14, 0, 0, time.UTC), after: time.Date(2026, 10, 10, 18, 16, 0, 0, time.UTC)},
	} {
		t.Run(timezone.name, func(t *testing.T) {
			site := env.SiteInTimezone(t, org, timezone.zone)
			seedBoundaryEvents(t, env, site, timezone.before, timezone.after)
			response := env.Do(t, testapi.Req{
				Name: t.Name(), Method: http.MethodGet, APIKey: key,
				Path: "/api/public/v1/stats/timeseries?site_id=" + site.String() + "&period=custom&date_from=2026-10-10&date_to=2026-10-11",
			})
			assertMatrixJSON(t, response, http.StatusOK)
			body := string(response.Body)
			if !strings.Contains(body, `"time":"2026-10-10"`) || !strings.Contains(body, `"time":"2026-10-11"`) {
				t.Fatalf("timeseries does not preserve both local days: %s", body)
			}
		})
	}
}

func bodyOrNil(body string) any {
	if body == "" {
		return nil
	}
	return body
}

func assertMatrixJSON(t *testing.T, response testapi.Resp, want int) {
	t.Helper()
	if response.Status != want || !strings.HasPrefix(response.Headers.Get("Content-Type"), "application/json") {
		t.Fatalf("status=%d content-type=%q body=%s, want %d JSON", response.Status, response.Headers.Get("Content-Type"), response.Body, want)
	}
}

func nestedString(value any, key string) string {
	object, _ := value.(map[string]any)
	result, _ := object[key].(string)
	return result
}

type timedResult struct {
	response testapi.Resp
	elapsed  time.Duration
}

func timedRequest(t *testing.T, env *testapi.Env, request testapi.Req) timedResult {
	t.Helper()
	start := time.Now()
	response := env.Do(t, request)
	return timedResult{response: response, elapsed: time.Since(start)}
}

func seedBoundaryEvents(t *testing.T, env *testapi.Env, site testapi.Site, before, after time.Time) {
	t.Helper()
	// The instants straddle local midnight by construction; literal local days
	// keep the expected boundary independent from query implementation code.
	for index, event := range []struct {
		at  time.Time
		day string
	}{{at: before, day: "2026-10-10"}, {at: after, day: "2026-10-11"}} {
		sessionID := int64(site.ID)*1000 + int64(index+1)
		if _, err := env.Pool.Exec(context.Background(), `
insert into sessions(site_id,retention_class,session_id,visitor_id,start,last,local_day,pageviews)
values ($1,30,$2,$3,$4,$4,$5,1)`, site.ID, sessionID, sessionID+100, event.at, event.day); err != nil {
			t.Fatal(err)
		}
		if _, err := env.Pool.Exec(context.Background(), `
insert into events(site_id,retention_class,local_day,ts,visitor_id,session_id,kind)
values ($1,30,$5,$4,$3,$2,0)`, site.ID, sessionID, sessionID+100, event.at, event.day); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPublicV1MatrixProblemJSONShape(t *testing.T) {
	env := testapi.New(t)
	org := env.Org(t)
	key := env.APIKey(t, org, "stats:read")
	response := env.Do(t, testapi.Req{Name: t.Name(), Method: http.MethodGet, Path: "/api/public/v1/stats/summary?site_id=bad", APIKey: key})
	if response.Problem == nil {
		t.Fatalf("body=%s", response.Body)
	}
	encoded, err := json.Marshal(response.Problem)
	if err != nil || response.Problem.Status != http.StatusBadRequest || response.Problem.Title == "" || response.Problem.Type == "" || !json.Valid(encoded) {
		t.Fatalf("problem=%+v marshal_error=%v", response.Problem, err)
	}
}
