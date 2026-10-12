package api_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"supermetry/internal/testapi"
)

func TestPrivateAnalyticsMatrixValidMalformedAndTenantBoundaries(t *testing.T) {
	env := testapi.New(t)
	org := env.Org(t)
	site := env.Site(t, org)
	owner := env.Session(t, org.Owner)
	otherOrg := env.Org(t)
	otherSite := env.Site(t, otherOrg)
	otherOwner := env.Session(t, otherOrg.Owner)

	valid := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "stats summary", method: http.MethodGet, path: "/api/private/stats/summary?site_id=%d&period=7d"},
		{name: "stats timeseries", method: http.MethodGet, path: "/api/private/stats/timeseries?site_id=%d&period=7d"},
		{name: "stats breakdown", method: http.MethodGet, path: "/api/private/stats/breakdown?site_id=%d&period=7d&dimension=page"},
		{name: "events", method: http.MethodGet, path: "/api/private/sites/%d/events?from=2026-10-11&to=2026-10-12"},
		{name: "properties", method: http.MethodGet, path: "/api/private/sites/%d/properties?from=2026-10-11&to=2026-10-12"},
		{name: "property values", method: http.MethodGet, path: "/api/private/sites/%d/properties/plan/values?from=2026-10-11&to=2026-10-12"},
		{name: "suggestions", method: http.MethodGet, path: "/api/private/sites/%d/suggestions?from=2026-10-11&to=2026-10-12&dimension=page"},
		{name: "sessions list", method: http.MethodGet, path: "/api/private/sites/%d/sessions?from=2026-10-11&to=2026-10-12"},
		{name: "query", method: http.MethodPost, path: "/api/private/query", body: `{"site_id":%d,"period":"7d","metrics":["visitors"]}`},
		{name: "heatmaps", method: http.MethodPost, path: "/api/private/heatmaps/query", body: `{"site_id":%d,"date_range":{"from":"2026-10-11","to":"2026-10-12"},"path":"/","device":"desktop"}`},
		{name: "vitals", method: http.MethodPost, path: "/api/private/vitals/query", body: `{"site_id":%d,"date_range":{"from":"2026-10-11","to":"2026-10-12"}}`},
		{name: "funnel report", method: http.MethodPost, path: "/api/private/funnels/query", body: `{"site_id":%d,"date_range":{"from":"2026-10-11","to":"2026-10-12"},"steps":[{"type":"page","match":"exact","value":"/"},{"type":"page","match":"exact","value":"/done"}]}`},
		{name: "journey report", method: http.MethodPost, path: "/api/private/journeys/query", body: `{"site_id":%d,"date_range":{"from":"2026-10-11","to":"2026-10-12"},"steps":3,"limit":5}`},
		{name: "retention report", method: http.MethodPost, path: "/api/private/retention/query", body: `{"site_id":%d,"date_range":{"from":"2026-10-11","to":"2026-10-12"},"interval":"day"}`},
		{name: "realtime", method: http.MethodGet, path: "/api/private/realtime?site_id=%d"},
		{name: "realtime summary", method: http.MethodGet, path: "/api/private/realtime/summary?site_id=%d"},
		{name: "health", method: http.MethodGet, path: "/api/private/health"},
	}
	for _, test := range valid {
		t.Run(test.name, func(t *testing.T) {
			path := test.path
			if strings.Contains(path, "%d") {
				path = fmt.Sprintf(path, site.ID)
			}
			body := test.body
			if strings.Contains(body, "%d") {
				body = fmt.Sprintf(body, site.ID)
			}
			request := testapi.Req{Name: t.Name(), Method: test.method, Path: path, Body: bodyOrNil(body), Cookie: owner}
			if test.method != http.MethodGet {
				request.Cookie = nil
				request.Headers = testapi.SessionHeaders(owner)
			}
			response := env.Do(t, request)
			if response.Status != http.StatusOK {
				t.Fatalf("status=%d body=%s, want 200", response.Status, response.Body)
			}
			if !strings.HasPrefix(response.Headers.Get("Content-Type"), "application/json") {
				t.Fatalf("content-type=%q body=%s", response.Headers.Get("Content-Type"), response.Body)
			}
		})
	}

	for _, test := range []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "bad stats site", method: http.MethodGet, path: "/api/private/stats/summary?site_id=bad&period=7d"},
		{name: "bad session id", method: http.MethodGet, path: "/api/private/sessions/bad?site_id=1"},
		{name: "bad query body", method: http.MethodPost, path: "/api/private/query", body: `{`},
		{name: "bad heatmap body", method: http.MethodPost, path: "/api/private/heatmaps/query", body: `{`},
		{name: "bad vitals body", method: http.MethodPost, path: "/api/private/vitals/query", body: `{`},
		{name: "bad funnel body", method: http.MethodPost, path: "/api/private/funnels/query", body: `{`},
		{name: "bad journey body", method: http.MethodPost, path: "/api/private/journeys/query", body: `{`},
		{name: "bad retention body", method: http.MethodPost, path: "/api/private/retention/query", body: `{`},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := testapi.Req{Name: t.Name(), Method: test.method, Path: test.path, Body: bodyOrNil(test.body), Cookie: owner}
			if test.method != http.MethodGet {
				request.Cookie = nil
				request.Headers = testapi.SessionHeaders(owner)
			}
			response := env.Do(t, request)
			if response.Status != http.StatusBadRequest && response.Status != http.StatusUnprocessableEntity && response.Status != http.StatusNotFound {
				t.Fatalf("status=%d body=%s, want 400/404/422", response.Status, response.Body)
			}
			assertPrivateProblem(t, response, response.Status)
		})
	}

	for _, test := range []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "other org stats", method: http.MethodGet, path: fmt.Sprintf("/api/private/stats/summary?site_id=%d&period=7d", site.ID)},
		{name: "other org events", method: http.MethodGet, path: fmt.Sprintf("/api/private/sites/%d/events?from=2026-10-11&to=2026-10-12", site.ID)},
		{name: "other org query", method: http.MethodPost, path: "/api/private/query", body: fmt.Sprintf(`{"site_id":%d,"period":"7d","metrics":["visitors"]}`, site.ID)},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := testapi.Req{Name: t.Name(), Method: test.method, Path: test.path, Body: bodyOrNil(test.body), Cookie: otherOwner}
			if test.method != http.MethodGet {
				request.Cookie = nil
				request.Headers = testapi.SessionHeaders(otherOwner)
			}
			response := env.Do(t, request)
			if response.Status != http.StatusForbidden && response.Status != http.StatusNotFound {
				t.Fatalf("other org status=%d body=%s, want 403/404", response.Status, response.Body)
			}
			assertPrivateProblem(t, response, response.Status)
		})
	}

	_ = otherSite
}

func TestPrivateAnalyticsMatrixListsLimitsAndReviewFocus(t *testing.T) {
	env := testapi.New(t)
	org := env.Org(t)
	owner := env.Session(t, org.Owner)
	site := env.Site(t, org)
	for index := 0; index < 2; index++ {
		at := env.Now.Add(-time.Duration(index+1) * time.Hour)
		if _, err := env.Pool.Exec(context.Background(), `
insert into sessions(site_id,retention_class,session_id,visitor_id,start,last,local_day,pageviews)
values ($1,30,$2,$3,$4,$4,$5,1)`, site.ID, 900+index, 1900+index, at, at.Format(time.DateOnly)); err != nil {
			t.Fatal(err)
		}
	}
	base := fmt.Sprintf("/api/private/sites/%d/sessions?from=2026-10-11&to=2026-10-12", site.ID)
	for _, suffix := range []string{"", "&limit=1000"} {
		response := env.Do(t, testapi.Req{Name: t.Name() + suffix, Method: http.MethodGet, Path: base + suffix, Cookie: owner})
		if response.Status != http.StatusOK {
			t.Fatalf("list suffix=%q status=%d body=%s", suffix, response.Status, response.Body)
		}
	}
	first := env.Do(t, testapi.Req{Name: t.Name() + "/first", Method: http.MethodGet, Path: base + "&limit=1", Cookie: owner})
	cursor := nestedString(first.JSON, "next_cursor")
	if cursor == "" {
		t.Fatal("next_cursor is empty")
	}
	page := env.Do(t, testapi.Req{Name: t.Name() + "/cursor", Method: http.MethodGet, Path: base + "&limit=1&cursor=" + cursor, Cookie: owner})
	if page.Status != http.StatusOK {
		t.Fatalf("cursor status=%d body=%s", page.Status, page.Body)
	}
	assertPrivateFastBounded(t, env, testapi.Req{Name: t.Name() + "/oversized-limit", Method: http.MethodGet, Path: base + "&limit=1000000", Cookie: owner})
	assertPrivateFastBounded(t, env, testapi.Req{Name: t.Name() + "/huge-cursor", Method: http.MethodGet, Path: base + "&cursor=" + strings.Repeat("a", 10_000), Cookie: owner})
	assertPrivateFastBounded(t, env, testapi.Req{Name: t.Name() + "/ten-year-range", Method: http.MethodGet, Path: fmt.Sprintf("/api/private/stats/summary?site_id=%d&period=custom&date_from=2016-10-12&date_to=2026-10-12", site.ID), Cookie: owner})
	detail := env.Do(t, testapi.Req{Name: t.Name() + "/session-detail", Method: http.MethodGet, Path: fmt.Sprintf("/api/private/sessions/900?site_id=%d", site.ID), Cookie: owner})
	if detail.Status != http.StatusOK {
		t.Fatalf("session detail status=%d body=%s", detail.Status, detail.Body)
	}

	values := strings.Repeat(`"x",`, 10_000) + `"z"`
	assertPrivateFastBounded(t, env, testapi.Req{
		Name: t.Name() + "/huge-filter", Method: http.MethodPost, Path: "/api/private/query",
		Body:    fmt.Sprintf(`{"site_id":%d,"period":"7d","metrics":["visitors"],"filters":[{"dimension":"country","operator":"is","values":[%s]}]}`, site.ID, values),
		Headers: testapi.SessionHeaders(owner),
	})

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
		{name: "zero event", site: site, want: http.StatusOK},
		{name: "restorable deletion", site: deleted, want: http.StatusNotFound},
		{name: "demo", site: demo, want: http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, endpoint := range []string{"summary", "timeseries", "breakdown"} {
				path := fmt.Sprintf("/api/private/stats/%s?site_id=%d&period=7d", endpoint, test.site.ID)
				if endpoint == "breakdown" {
					path += "&dimension=page"
				}
				response := env.Do(t, testapi.Req{Name: t.Name() + "/" + endpoint, Method: http.MethodGet, Path: path, Cookie: owner})
				if response.Status != test.want || response.Status == http.StatusInternalServerError {
					t.Fatalf("%s status=%d body=%s, want %d", endpoint, response.Status, response.Body, test.want)
				}
			}
		})
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
			zoned := env.SiteInTimezone(t, org, timezone.zone)
			seedBoundaryEvents(t, env, zoned, timezone.before, timezone.after)
			for _, endpoint := range []string{"summary", "timeseries", "breakdown"} {
				path := fmt.Sprintf("/api/private/stats/%s?site_id=%d&period=custom&date_from=2026-10-10&date_to=2026-10-11", endpoint, zoned.ID)
				if endpoint == "breakdown" {
					path += "&dimension=page"
				}
				response := env.Do(t, testapi.Req{Name: t.Name() + "/" + endpoint, Method: http.MethodGet, Path: path, Cookie: owner})
				if response.Status != http.StatusOK {
					t.Fatalf("%s status=%d body=%s", endpoint, response.Status, response.Body)
				}
				if endpoint == "timeseries" && (!strings.Contains(string(response.Body), `"time":"2026-10-10"`) || !strings.Contains(string(response.Body), `"time":"2026-10-11"`)) {
					t.Fatalf("timeseries status=%d body=%s", response.Status, response.Body)
				}
			}
		})
	}
}

func assertPrivateFastBounded(t *testing.T, env *testapi.Env, request testapi.Req) {
	t.Helper()
	start := time.Now()
	response := env.Do(t, request)
	elapsed := time.Since(start)
	if elapsed >= 2*time.Second {
		t.Fatalf("status=%d elapsed=%s body=%s; BUG: unbounded private input exceeded two seconds", response.Status, elapsed, response.Body)
	}
	if response.Status != http.StatusBadRequest && response.Status != http.StatusUnprocessableEntity && response.Status != http.StatusOK {
		t.Fatalf("status=%d elapsed=%s body=%s, want 200/400/422", response.Status, elapsed, response.Body)
	}
}
