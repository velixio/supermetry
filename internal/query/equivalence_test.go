package query_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"supermetry/internal/api"
	"supermetry/internal/closer"
	"supermetry/internal/db"
	"supermetry/internal/dict"
	"supermetry/internal/query"
	"supermetry/internal/sites"
	"supermetry/internal/testpg"
)

type registry struct{ site sites.Site }

func (r registry) ByPublicID(id string) (sites.Site, bool) {
	return r.site, id == r.site.PublicID
}

func (r registry) Site(id int32) (sites.Site, bool) { return r.site, id == r.site.ID }
func (r registry) All() []sites.Site                { return []sites.Site{r.site} }

func TestRouteEquivalence(t *testing.T) {
	_, engine := setupTinyDims(t)
	tests := []struct {
		name     string
		req      api.QueryRequest
		eligible []query.Route
	}{
		{name: "totals", req: queryRequest([]string{}, nil), eligible: []query.Route{query.RouteRollup, query.RouteVisitorDay, query.RouteCube, query.RouteHot}},
		{name: "page", req: queryRequest([]string{"page"}, nil), eligible: []query.Route{query.RouteRollup, query.RouteHot}},
		{name: "source", req: queryRequest([]string{"source"}, nil), eligible: []query.Route{query.RouteRollup, query.RouteCube, query.RouteHot}},
		{name: "country", req: queryRequest([]string{"country"}, nil), eligible: []query.Route{query.RouteRollup, query.RouteVisitorDay, query.RouteCube, query.RouteHot}},
		{name: "region", req: queryRequest([]string{"region"}, nil), eligible: []query.Route{query.RouteRollup, query.RouteHot}},
		{name: "city", req: queryRequest([]string{"city"}, nil), eligible: []query.Route{query.RouteRollup, query.RouteHot}},
		{name: "browser version", req: queryRequest([]string{"browser_version"}, nil), eligible: []query.Route{query.RouteRollup, query.RouteHot}},
		{name: "OS version", req: queryRequest([]string{"os_version"}, nil), eligible: []query.Route{query.RouteRollup, query.RouteHot}},
		{name: "screen size", req: queryRequest([]string{"screen_size"}, nil), eligible: []query.Route{query.RouteRollup, query.RouteHot}},
		{name: "language", req: queryRequest([]string{"language"}, nil), eligible: []query.Route{query.RouteRollup, query.RouteHot}},
		{name: "hostname", req: queryRequest([]string{"hostname"}, nil), eligible: []query.Route{query.RouteRollup, query.RouteHot}},
		{name: "entry", req: queryRequest([]string{"entry"}, nil), eligible: []query.Route{query.RouteRollup, query.RouteHot}},
		{name: "country filter", req: queryRequest([]string{"source"}, []api.Filter{filter("country", api.Is, "US")}), eligible: []query.Route{query.RouteCube, query.RouteHot}},
		{name: "country and source", req: queryRequest([]string{"country"}, []api.Filter{filter("country", api.Is, "US"), filter("source", api.Is, "Google")}), eligible: []query.Route{query.RouteCube, query.RouteHot}},
		{name: "region filter", req: queryRequest([]string{"city"}, []api.Filter{filter("region", api.Is, "California", "US")}), eligible: []query.Route{query.RouteHot}},
		{name: "city filter", req: queryRequest([]string{"source"}, []api.Filter{filter("city", api.Is, "Bengaluru", "IN")}), eligible: []query.Route{query.RouteHot}},
		{name: "language filter", req: queryRequest([]string{"source"}, []api.Filter{filter("language", api.Is, "en")}), eligible: []query.Route{query.RouteHot}},
		{name: "page contains", req: queryRequest([]string{"page"}, []api.Filter{filter("page", api.Contains, "/blog")}), eligible: []query.Route{query.RouteHot}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := mustQuery(t, engine.WithForcedRoute(query.RouteClosed), tt.req)
			for _, route := range tt.eligible {
				got := mustQuery(t, engine.WithForcedRoute(route), tt.req)
				if !equivalent(got, want) {
					gotJSON, _ := json.Marshal(got)
					wantJSON, _ := json.Marshal(want)
					t.Fatalf("route %v differs\ngot  %s\nwant %s", route, gotJSON, wantJSON)
				}
			}
		})
	}
}

func TestNewDimensionsMatchAcrossTiers(t *testing.T) {
	_, engine := setupTinyDims(t)
	want := map[string]map[string][3]int64{
		"browser_version": {"Chrome 126": {1, 2, 4}, "Firefox 131": {2, 2, 2}, "": {1, 1, 2}},
		"os_version":      {"Windows 10/11": {2, 2, 2}, "": {2, 3, 6}},
		"screen_size":     {"laptop": {1, 2, 4}, "mobile": {1, 1, 1}, "tablet": {1, 1, 1}, "": {1, 1, 2}},
		"language":        {"en": {1, 2, 4}, "hi": {2, 2, 2}, "": {1, 1, 2}},
		"hostname":        {"docs.example.com": {3, 3, 4}, "example.com": {1, 1, 2}, "": {1, 1, 2}},
	}
	for dimension, expectedRows := range want {
		t.Run(dimension, func(t *testing.T) {
			for _, route := range []query.Route{query.RouteClosed, query.RouteRollup, query.RouteHot} {
				req := queryRequestRange("2026-10-06", "2026-10-07", []string{dimension}, nil)
				req.Metrics = []api.Metric{api.Visitors, api.Visits, api.Pageviews}
				got := mustQuery(t, engine.WithForcedRoute(route), req)
				if len(got.Rows) != len(expectedRows) {
					t.Fatalf("route %v returned %d rows, want %d: %+v", route, len(got.Rows), len(expectedRows), got.Rows)
				}
				for _, row := range got.Rows {
					label, _ := row[dimension].(string)
					expected, ok := expectedRows[label]
					if !ok || int64(row["visitors"].(float64)) != expected[0] || int64(row["visits"].(float64)) != expected[1] || int64(row["pageviews"].(float64)) != expected[2] {
						t.Errorf("route %v row %+v, want %q %v", route, row, label, expected)
					}
				}
			}
		})
	}
}

func TestNewDimensionsMatchForCustomEvents(t *testing.T) {
	_, engine := setupTinyDims(t)
	for _, dimension := range []string{"browser_version", "os_version", "screen_size", "language", "hostname"} {
		t.Run(dimension, func(t *testing.T) {
			dimensions := []string{"event", dimension}
			req := queryRequest(dimensions, nil)
			req.Metrics = []api.Metric{api.Events, api.Conversions}
			closed := mustQuery(t, engine.WithForcedRoute(query.RouteClosed), req)
			hot := mustQuery(t, engine.WithForcedRoute(query.RouteHot), req)
			if !equivalent(hot, closed) {
				t.Fatalf("custom-event dimension differs: hot=%+v closed=%+v", hot.Rows, closed.Rows)
			}
		})
	}
}

func TestPageMetricsMatchHotClosedAndRollupTiers(t *testing.T) {
	_, engine := setupPageMetricFixture(t)
	req := pageMetricRequest(nil)

	want := map[string]struct {
		pageviews  float64
		exitRate   float64
		timeOnPage float64
	}{
		"/a":      {pageviews: 4, exitRate: 50, timeOnPage: 65.0 / 3.0},
		"/b":      {pageviews: 2, exitRate: 100, timeOnPage: 0},
		"/repeat": {pageviews: 2, exitRate: 50, timeOnPage: 30},
	}
	for _, route := range []query.Route{query.RouteHot, query.RouteClosed, query.RouteRollup} {
		response := mustQuery(t, engine.WithForcedRoute(route), req)
		assertPageMetrics(t, route.String(), response, want)
	}
}

func TestHotAndClosedAgreeForEventDerivedMetricsAndFiltersAcrossUTCMidnight(t *testing.T) {
	_, engine := setupPageMetricFixture(t)
	tests := []struct {
		name       string
		dimensions []string
		filters    []api.Filter
	}{
		{name: "totals"},
		{name: "page breakdown", dimensions: []string{"page"}},
		{name: "page filter", dimensions: []string{"page"}, filters: []api.Filter{filter("page", api.Is, "/a")}},
		{name: "event filter", dimensions: []string{"page"}, filters: []api.Filter{filter("event", api.Is, "signup")}},
		{name: "goal filter", dimensions: []string{"page"}, filters: []api.Filter{filter("goal", api.Is, "signup")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := queryRequestRange("2026-10-09", "2026-10-09", tt.dimensions, tt.filters)
			req.Metrics = []api.Metric{api.Visitors, api.Visits, api.Pageviews, api.ScrollDepth}
			if len(tt.dimensions) == 1 && tt.dimensions[0] == "page" {
				req.Metrics = append(req.Metrics, api.Metric(query.ExitRate), api.Metric(query.TimeOnPage))
			}
			closed := mustQuery(t, engine.WithForcedRoute(query.RouteClosed), req)
			hot := mustQuery(t, engine.WithForcedRoute(query.RouteHot), req)
			if !equivalent(hot, closed) {
				t.Fatalf("hot and closed differ: hot=%+v closed=%+v", hot, closed)
			}
		})
	}
}

func TestHotTierCivilDateIsIndependentOfConnectionTimezone(t *testing.T) {
	pool, _ := setupPageMetricFixture(t)
	config := pool.Config()
	afterConnect := config.AfterConnect
	config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		if afterConnect != nil {
			if err := afterConnect(ctx, conn); err != nil {
				return err
			}
		}
		_, err := conn.Exec(ctx, "set timezone='America/New_York'")
		return err
	}
	timezonePool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(timezonePool.Close)
	dictionary := dict.New(timezonePool)
	if err := dictionary.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	engine := identifiedEngine(timezonePool, dictionary, registry{site: sites.Site{
		ID: 1, PublicID: "pages", TZ: time.UTC, Retention: db.Ret30,
	}}).WithForcedRoute(query.RouteHot)
	req := queryRequestRange("2026-10-09", "2026-10-09", []string{"source"}, nil)
	req.Metrics = []api.Metric{api.Visits, api.Pageviews}
	response := mustQuery(t, engine, req)
	if response.Totals["visits"] != 5 || response.Totals["pageviews"] != 8 {
		t.Fatalf("New York connection omitted today's local day: totals=%v", response.Totals)
	}
}

func TestHotSourceBreakdownPerformanceGuard(t *testing.T) {
	if testing.Short() {
		t.Skip("performance guard is skipped in short mode")
	}
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsurePartitions(ctx, pool, day("2026-09-10"), day("2026-10-10")); err != nil {
		t.Fatal(err)
	}
	_, err := pool.Exec(ctx, `
insert into dict(kind,id,value)
select 2, id, 'source-' || id from generate_series(1,10) id;
insert into sessions
  (site_id,retention_class,session_id,visitor_id,start,last,local_day,source_id,pageviews,events,engagement_ms,bounce)
select 1,30,id,id,'2026-10-09 00:00Z'::timestamptz + id * interval '1 second',
       '2026-10-09 00:01Z'::timestamptz + id * interval '1 second','2026-10-09',
       1 + id % 10,6 + case when id <= 2000 then 1 else 0 end,0,1000,false
from generate_series(1,3000) id;
insert into events
  (site_id,retention_class,local_day,ts,visitor_id,session_id,kind,path_id,source_id,engagement_ms)
select 1,30,'2026-10-09',s.start + n * interval '1 millisecond',s.visitor_id,s.session_id,
       case when n=2 then 2 else 0 end,1,s.source_id,case when n=2 then 1000 else 0 end
from sessions s
cross join lateral generate_series(1,6 + case when s.session_id <= 2000 then 1 else 0 end) n
where s.site_id=1`)
	if err != nil {
		t.Fatal(err)
	}
	dictionary := dict.New(pool)
	if err := dictionary.Load(ctx); err != nil {
		t.Fatal(err)
	}
	engine := identifiedEngine(pool, dictionary, registry{site: sites.Site{
		ID: 1, PublicID: "perf", TZ: time.UTC, Retention: db.Ret30,
	}}).WithForcedRoute(query.RouteHot)
	req := queryRequestRange("2026-09-10", "2026-10-09", []string{"source"}, nil)
	req.Metrics = []api.Metric{api.Visitors, api.Visits, api.Pageviews}
	started := time.Now()
	response := mustQuery(t, engine, req)
	elapsed := time.Since(started)
	t.Logf("30-day hot source breakdown: %s", elapsed)
	if len(response.Rows) != 10 || response.Totals["visits"] != 3000 || response.Totals["pageviews"] != 20000 {
		t.Fatalf("unexpected source breakdown: rows=%d totals=%v", len(response.Rows), response.Totals)
	}
	if elapsed >= time.Second {
		t.Fatalf("30-day hot source breakdown took %s, want under 1s", elapsed)
	}
}

func TestFilteredPageMetricsMatchHotAndClosedTiers(t *testing.T) {
	_, engine := setupPageMetricFixture(t)
	req := pageMetricRequest([]api.Filter{filter("country", api.Is, "IN")})

	want := mustQuery(t, engine.WithForcedRoute(query.RouteClosed), req)
	got := mustQuery(t, engine.WithForcedRoute(query.RouteHot), req)
	if !equivalent(got, want) {
		t.Fatalf("filtered page metrics differ: hot=%+v closed=%+v", got, want)
	}
	normalized, problem := query.Normalize(req, sites.Site{ID: 1, TZ: time.UTC}, day("2026-10-10"))
	if problem != nil {
		t.Fatal(problem.Title)
	}
	parts := query.Plan(normalized, day("2026-10-09"))
	if len(parts) != 1 || parts[0].Route != query.RouteClosed {
		t.Fatalf("filtered page metric plan = %+v, want closed route", parts)
	}
}

func TestExitRateMatchesPageLikeDimensionsAcrossTiers(t *testing.T) {
	_, engine := setupPageMetricFixture(t)
	tests := []struct {
		name      string
		dimension string
		want      map[string]float64
	}{
		{name: "entry", dimension: "entry", want: map[string]float64{"/a": 100.0 / 3.0, "/repeat": 50}},
		{name: "exit", dimension: "exit", want: map[string]float64{"/a": 100, "/b": 50, "/repeat": 50}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dimensions := []string{tt.dimension}
			req := api.QueryRequest{
				SiteId: 1, DateRange: ptr(dateRange("2026-10-09", "2026-10-09")),
				Metrics: []api.Metric{api.Metric(query.ExitRate)}, Dimensions: &dimensions,
			}
			for _, route := range []query.Route{query.RouteHot, query.RouteClosed, query.RouteRollup} {
				response := mustQuery(t, engine.WithForcedRoute(route), req)
				if len(response.Rows) != len(tt.want) {
					t.Fatalf("%s rows = %+v, want %d", route.String(), response.Rows, len(tt.want))
				}
				for _, row := range response.Rows {
					value := row[tt.dimension].(string)
					if got := row["exit_rate"].(float64); math.Abs(got-tt.want[value]) > 1e-9 {
						t.Errorf("%s %s=%s exit_rate=%v, want %v", route.String(), tt.dimension, value, got, tt.want[value])
					}
				}
			}
		})
	}
}

func TestRegionAndCityBreakdownsUseLabelsAndExcludeUnknown(t *testing.T) {
	_, engine := setupTiny(t)
	tests := []struct {
		name      string
		dimension string
		want      []string
	}{
		{name: "region", dimension: "region", want: []string{"California", "Karnataka"}},
		{name: "city", dimension: "city", want: []string{"Los Angeles", "Bengaluru"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, route := range []query.Route{query.RouteClosed, query.RouteRollup, query.RouteHot} {
				got := mustQuery(t, engine.WithForcedRoute(route), queryRequest([]string{tt.dimension}, nil))
				labels := make([]string, 0, len(got.Rows))
				for _, row := range got.Rows {
					label, _ := row[tt.dimension].(string)
					if label == "" || label == "0" {
						t.Fatalf("route %v returned unknown %s row: %+v", route, tt.dimension, row)
					}
					labels = append(labels, label)
				}
				if len(labels) != len(tt.want) {
					t.Errorf("route %v labels = %v, want exactly %v", route, labels, tt.want)
				}
				for _, want := range tt.want {
					if !containsString(labels, want) {
						t.Errorf("route %v labels = %v, missing %q", route, labels, want)
					}
				}
			}
		})
	}
}

func TestCityIsNotKeepsOtherCountriesAcrossStorageTiers(t *testing.T) {
	_, engine := setupLocationFilterFixture(t)
	filter := []api.Filter{{Dimension: "city", Operator: api.IsNot, Values: []string{"Paris", "FR"}}}
	for _, tt := range []struct {
		name  string
		route query.Route
		day   string
	}{
		{name: "closed", route: query.RouteClosed, day: "2026-10-06"},
		{name: "hot", route: query.RouteHot, day: "2026-10-08"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			request := queryRequestRange(tt.day, tt.day, []string{"city"}, filter)
			request.Metrics = []api.Metric{api.Visits}
			got := mustQuery(t, engine.WithForcedRoute(tt.route), request)
			if got.Totals["visits"] != 2 {
				t.Fatalf("totals=%v, want Lyon FR and Berlin DE", got.Totals)
			}
			cities := make([]string, 0, len(got.Rows))
			for _, row := range got.Rows {
				cities = append(cities, fmt.Sprint(row["city"]))
			}
			if !containsString(cities, "Lyon") || !containsString(cities, "Berlin") || containsString(cities, "Paris") {
				t.Fatalf("cities=%v, want Lyon and Berlin", cities)
			}
		})
	}
}

func TestQueryMergesClosedAndHotParts(t *testing.T) {
	_, engine := setupTiny(t)
	req := queryRequestRange("2026-10-06", "2026-10-08", []string{"country"}, nil)
	got := mustQuery(t, engine, req)
	if got.Totals["visitors"] != 6 || got.Totals["visits"] != 7 || got.Totals["pageviews"] != 11 {
		t.Fatalf("totals = %v, want visitors=6 visits=7 pageviews=11", got.Totals)
	}
	if !reflect.DeepEqual(got.Meta.SourcesUsed, []string{"rollup", "hot"}) {
		t.Fatalf("sources = %v", got.Meta.SourcesUsed)
	}
}

func TestQueryDoesNotSkipGapBeforeLatestClosedDay(t *testing.T) {
	pool, engine := setupTiny(t)
	if _, err := pool.Exec(context.Background(), `delete from day_close where site_id=1 and day='2026-10-06'; delete from rollup_day where site_id=1 and day='2026-10-06'`); err != nil {
		t.Fatal(err)
	}
	got := mustQuery(t, engine, queryRequest(nil, nil))
	if got.Totals["visitors"] != 4 || got.Totals["visits"] != 5 {
		t.Fatalf("totals across close gap = %v", got.Totals)
	}
}

func TestRollupUsesCompletedDayBlocks(t *testing.T) {
	pool, engine := setupTiny(t)
	req := queryRequest([]string{"country"}, nil)
	want := mustQuery(t, engine.WithForcedRoute(query.RouteClosed), req)
	if _, err := pool.Exec(context.Background(), `delete from rollup_day_dim where site_id=1 and day between '2026-10-06' and '2026-10-07'`); err != nil {
		t.Fatal(err)
	}
	got := mustQuery(t, engine.WithForcedRoute(query.RouteRollup), req)
	if !equivalent(got, want) {
		t.Fatalf("block-backed rollup differs: got %+v want %+v", got, want)
	}
}

func TestQueryPaginatesAndOrdersDimensions(t *testing.T) {
	_, engine := setupTiny(t)
	limit := 1
	ascending := api.Asc
	req := queryRequest([]string{"country"}, nil)
	req.Limit = &limit
	req.OrderBy = &[]api.OrderBy{{Column: "country", Direction: &ascending}}
	first := mustQuery(t, engine, req)
	if len(first.Rows) != 1 || first.Rows[0]["country"] != "IN" || first.NextCursor == nil {
		t.Fatalf("first page = %+v", first)
	}
	req.Cursor = first.NextCursor
	second := mustQuery(t, engine, req)
	if len(second.Rows) != 1 || second.Rows[0]["country"] != "US" || second.NextCursor != nil {
		t.Fatalf("second page = %+v", second)
	}
}

func TestHourlyTimeseriesUsesSiteTimezoneWithMinuteOffset(t *testing.T) {
	pool, _ := setupTiny(t)
	kolkata, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		t.Fatal(err)
	}
	dictionary := dict.New(pool)
	if err := dictionary.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	engine := identifiedEngine(pool, dictionary, registry{site: sites.Site{
		ID: 1, PublicID: "tiny", TZ: kolkata, Retention: db.Ret30,
	}})
	req := queryRequestRange("2026-10-06", "2026-10-06", []string{"hour"}, nil)
	response := mustQuery(t, engine, req)
	if len(response.Rows) != 3 {
		t.Fatalf("rows=%+v, want three local-hour buckets", response.Rows)
	}
	want := []string{"2026-10-06T15:00:00", "2026-10-06T16:00:00", "2026-10-06T17:00:00"}
	for i, label := range want {
		if response.Rows[i]["hour"] != label {
			t.Fatalf("row %d hour=%v, want %s", i, response.Rows[i]["hour"], label)
		}
	}
}

func TestQueryAddsPreviousPeriodComparison(t *testing.T) {
	_, engine := setupTiny(t)
	req := queryRequestRange("2026-10-08", "2026-10-08", nil, nil)
	compare := api.PreviousPeriod
	req.Compare = &compare
	response := mustQuery(t, engine, req)
	if response.Comparison == nil {
		t.Fatal("comparison is nil")
	}
	if got := response.Comparison.DateRange.From.Format("2006-01-02"); got != "2026-10-07" {
		t.Fatalf("comparison from=%s, want 2026-10-07", got)
	}
	if response.Comparison.Totals["visitors"] != 2 || response.Comparison.Totals["visits"] != 2 || response.Comparison.Totals["pageviews"] != 3 {
		t.Fatalf("comparison totals=%v", response.Comparison.Totals)
	}
}

func TestEventAndPropertyFilters(t *testing.T) {
	_, engine := setupTiny(t)
	tests := []struct {
		name   string
		filter api.Filter
		visits float64
	}{
		{name: "event", filter: filter("event", api.Is, "signup"), visits: 2},
		{name: "goal", filter: filter("goal", api.Is, "signup"), visits: 2},
		{name: "property", filter: filter("prop:plan", api.Is, "pro"), visits: 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := queryRequest([]string{"country"}, []api.Filter{tt.filter})
			got := mustQuery(t, engine.WithForcedRoute(query.RouteClosed), req)
			if got.Totals["visitors"] != 2 || got.Totals["visits"] != tt.visits {
				t.Fatalf("totals = %v", got.Totals)
			}
		})
	}
}

func TestCustomEventBreakdowns(t *testing.T) {
	_, engine := setupTiny(t)
	tests := []struct {
		name      string
		dimension string
		want      string
	}{
		{name: "event name", dimension: "event", want: "signup"},
		{name: "property value", dimension: "prop:plan", want: "pro"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := queryRequest([]string{tt.dimension}, nil)
			req.Metrics = []api.Metric{api.Visitors, api.Events, api.Conversions}
			got := mustQuery(t, engine.WithForcedRoute(query.RouteClosed), req)
			if len(got.Rows) != 1 || got.Rows[0][tt.dimension] != tt.want || got.Rows[0]["events"] != float64(2) || got.Rows[0]["conversions"] != float64(2) {
				t.Fatalf("breakdown = %+v", got)
			}
		})
	}
}

func TestCustomEventRegionFilterMatchesClosedAndHot(t *testing.T) {
	_, engine := setupTiny(t)
	req := queryRequest([]string{"event", "region"}, []api.Filter{filter("region", api.Is, "California", "US")})
	req.Metrics = []api.Metric{api.Events, api.Conversions}
	want := mustQuery(t, engine.WithForcedRoute(query.RouteClosed), req)
	got := mustQuery(t, engine.WithForcedRoute(query.RouteHot), req)
	if !equivalent(got, want) {
		t.Fatalf("custom event region filter differs: got %+v want %+v", got, want)
	}
	if len(got.Rows) != 1 || got.Rows[0]["event"] != "signup" || got.Rows[0]["region"] != "California" || got.Rows[0]["events"] != float64(2) {
		t.Fatalf("custom event region breakdown = %+v", got.Rows)
	}
}

func TestRevenueMetricsMatchHotAndClosedTiers(t *testing.T) {
	pool, engine := setupTiny(t)
	seedRevenueRows(t, pool)
	tests := []struct {
		name      string
		currency  string
		wantTotal float64
		wantAvg   float64
	}{
		{name: "USD excludes EUR", currency: "USD", wantTotal: 10, wantAvg: 5},
	}
	mismatch := queryRequest(nil, []api.Filter{filter("goal", api.Is, "signup"), filter("revenue_currency", api.Is, "EUR")})
	mismatch.Metrics = []api.Metric{api.TotalRevenue}
	if _, err := engine.Query(context.Background(), mismatch); err == nil || !strings.Contains(err.Error(), "permanent currency USD") {
		t.Fatalf("currency mismatch error=%v", err)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := queryRequest(nil, []api.Filter{
				filter("goal", api.Is, "signup"),
				filter("revenue_currency", api.Is, tt.currency),
			})
			req.Metrics = []api.Metric{api.TotalRevenue, api.AverageRevenue}
			closed := mustQuery(t, engine.WithForcedRoute(query.RouteClosed), req)
			hot := mustQuery(t, engine.WithForcedRoute(query.RouteHot), req)
			for route, got := range map[string]api.QueryResponse{"closed": closed, "hot": hot} {
				if got.Totals["total_revenue"] != tt.wantTotal || got.Totals["average_revenue"] != tt.wantAvg {
					t.Errorf("%s totals = %v, want total %v average %v", route, got.Totals, tt.wantTotal, tt.wantAvg)
				}
			}
		})
	}
}

func TestRevenueMetricsSupportEventDimension(t *testing.T) {
	pool, engine := setupTiny(t)
	seedRevenueRows(t, pool)
	req := queryRequest([]string{"event"}, []api.Filter{filter("revenue_currency", api.Is, "USD")})
	req.Metrics = []api.Metric{api.TotalRevenue, api.AverageRevenue}
	got := mustQuery(t, engine.WithForcedRoute(query.RouteClosed), req)
	if len(got.Rows) != 1 || got.Rows[0]["event"] != "signup" || got.Rows[0]["total_revenue"] != float64(10) || got.Rows[0]["average_revenue"] != float64(5) {
		t.Fatalf("revenue event breakdown = %+v", got.Rows)
	}
}

func TestFunnelMatchesTruth(t *testing.T) {
	_, engine := setupTiny(t)
	got, err := engine.Funnel(context.Background(), api.FunnelRequest{
		SiteId:    1,
		DateRange: dateRange("2026-10-06", "2026-10-08"),
		Steps: []api.FunnelStep{
			{Type: api.Page, Match: "exact", Value: "/"},
			{Type: api.Page, Match: "exact", Value: "/pricing"},
			{Type: api.Event, Match: "exact", Value: "signup"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []int64{4, 3, 2}
	if visitors := stepCounts(got); !reflect.DeepEqual(visitors, want) {
		t.Fatalf("step visitors = %v, want %v", visitors, want)
	}
	if conversions := []float64{got.Steps[0].Conversion, got.Steps[1].Conversion, got.Steps[2].Conversion}; !closeFloats(conversions, []float64{100, 75, 50}) {
		t.Fatalf("conversions = %v", conversions)
	}
}

func TestFunnelMatchModesAcrossStorageTiers(t *testing.T) {
	_, engine := setupTiny(t)
	tests := []struct {
		name       string
		from       string
		to         string
		pageMatch  string
		pageValue  string
		eventMatch string
		eventValue string
		want       []int64
	}{
		{name: "prefix closed through close boundary", from: "2026-10-06", to: "2026-10-07", pageMatch: "prefix", pageValue: "/", eventMatch: "prefix", eventValue: "sign", want: []int64{5, 2}},
		{name: "wildcard hot", from: "2026-10-08", to: "2026-10-08", pageMatch: "wildcard", pageValue: "/*", eventMatch: "wildcard", eventValue: "sign*", want: []int64{2, 0}},
		{name: "wildcard mixed", from: "2026-10-07", to: "2026-10-08", pageMatch: "wildcard", pageValue: "/*", eventMatch: "wildcard", eventValue: "sign*", want: []int64{4, 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := engine.Funnel(context.Background(), api.FunnelRequest{
				SiteId:    1,
				DateRange: dateRange(tt.from, tt.to),
				Steps: []api.FunnelStep{
					{Type: api.Page, Match: api.FunnelStepMatch(tt.pageMatch), Value: tt.pageValue},
					{Type: api.Event, Match: api.FunnelStepMatch(tt.eventMatch), Value: tt.eventValue},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if visitors := stepCounts(got); !reflect.DeepEqual(visitors, tt.want) {
				t.Fatalf("step visitors = %v, want %v", visitors, tt.want)
			}
		})
	}
}

func TestFunnelEscapedLiteralsAndSiteIsolation(t *testing.T) {
	_, engine := setupFunnelMatching(t)
	tests := []struct {
		name       string
		from       string
		to         string
		pageMatch  string
		pageValue  string
		eventMatch string
		eventValue string
		want       []int64
	}{
		{name: "exact closed", from: "2026-10-06", to: "2026-10-07", pageMatch: "exact", pageValue: "/literal%_star*value", eventMatch: "exact", eventValue: "evt%_star*done", want: []int64{2, 2}},
		{name: "prefix hot keeps star literal", from: "2026-10-08", to: "2026-10-08", pageMatch: "prefix", pageValue: "/literal%_star*", eventMatch: "prefix", eventValue: "evt%_star*", want: []int64{1, 1}},
		{name: "wildcard mixed keeps percent and underscore literal", from: "2026-10-07", to: "2026-10-08", pageMatch: "wildcard", pageValue: "/literal%_*value", eventMatch: "wildcard", eventValue: "evt%_*done", want: []int64{2, 2}},
		{name: "injection text matches nothing", from: "2026-10-06", to: "2026-10-08", pageMatch: "wildcard", pageValue: `' OR true --*`, eventMatch: "wildcard", eventValue: "*", want: []int64{0, 0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := engine.Funnel(context.Background(), api.FunnelRequest{
				SiteId:    1,
				DateRange: dateRange(tt.from, tt.to),
				Steps: []api.FunnelStep{
					{Type: api.Page, Match: api.FunnelStepMatch(tt.pageMatch), Value: tt.pageValue},
					{Type: api.Event, Match: api.FunnelStepMatch(tt.eventMatch), Value: tt.eventValue},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if visitors := stepCounts(got); !reflect.DeepEqual(visitors, tt.want) {
				t.Fatalf("step visitors = %v, want %v", visitors, tt.want)
			}
		})
	}
}

func TestFunnelPropertyConditionsApplyAcrossStorageTiers(t *testing.T) {
	pool, engine := setupFunnelMatching(t)
	if _, err := pool.Exec(context.Background(), `
insert into sessions
  (site_id,retention_class,session_id,visitor_id,start,last,local_day,
   entry_path,exit_path,pageviews,events,engagement_ms,bounce)
values (1,30,99999,10401,'2026-10-06 11:00Z','2026-10-06 11:01Z','2026-10-06',5,5,1,1,0,false);
insert into events
  (site_id,retention_class,local_day,ts,visitor_id,session_id,kind,name_id,path_id,props)
values
  (1,30,'2026-10-06','2026-10-06 10:01:30Z',10401,1401,1,21,5,'{"plan":"pro"}'),
  (1,30,'2026-10-06','2026-10-06 11:00Z',10401,99999,0,0,5,null),
  (1,30,'2026-10-06','2026-10-06 11:01Z',10401,99999,1,21,5,'{"plan":"free"}')`); err != nil {
		t.Fatal(err)
	}
	if _, err := closer.CloseDay(context.Background(), pool, []int32{1}, day("2026-10-06")); err != nil {
		t.Fatal(err)
	}
	key, value := "plan", "pro"
	for _, tt := range []struct {
		name string
		from string
		to   string
		want []int64
	}{
		{name: "closed visitor-day property assigned to one session", from: "2026-10-06", to: "2026-10-06", want: []int64{2, 1}},
		{name: "hot mismatching property", from: "2026-10-08", to: "2026-10-08", want: []int64{1, 0}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := engine.Funnel(context.Background(), api.FunnelRequest{
				SiteId: 1, DateRange: dateRange(tt.from, tt.to),
				Steps: []api.FunnelStep{
					{Type: api.Page, Match: api.FunnelStepMatchExact, Value: "/literal%_star*value"},
					{Type: api.Event, Match: api.FunnelStepMatchExact, Value: "evt%_star*done", PropKey: &key, PropValue: &value},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if counts := stepCounts(got); !reflect.DeepEqual(counts, tt.want) {
				t.Fatalf("step counts = %v, want %v", counts, tt.want)
			}
		})
	}
}

func TestFunnelCountBySessionsAndVisitors(t *testing.T) {
	pool, engine := setupFunnelMatching(t)
	if _, err := pool.Exec(context.Background(), `
insert into sessions
  (site_id, retention_class, session_id, visitor_id, start, last, local_day,
   entry_path, exit_path, pageviews, events, engagement_ms, bounce)
values (1,30,9991,10601,'2026-10-08 12:00Z','2026-10-08 12:01Z','2026-10-08',5,5,1,1,60000,false);
insert into events
  (site_id, retention_class, local_day, ts, visitor_id, session_id, kind, name_id, path_id, engagement_ms, props)
values
  (1,30,'2026-10-08','2026-10-08 12:00Z',10601,9991,0,0,5,0,null),
  (1,30,'2026-10-08','2026-10-08 12:01Z',10601,9991,1,21,5,0,null)`); err != nil {
		t.Fatal(err)
	}
	steps := []api.FunnelStep{
		{Type: api.Page, Match: api.FunnelStepMatchExact, Value: "/literal%_star*value"},
		{Type: api.Event, Match: api.FunnelStepMatchExact, Value: "evt%_star*done"},
	}
	for _, tt := range []struct {
		name    string
		countBy *api.FunnelRequestCountBy
		wantBy  api.FunnelResponseCountBy
		want    []int64
	}{
		{name: "default sessions", wantBy: api.FunnelResponseCountBySessions, want: []int64{2, 2}},
		{name: "visitors", countBy: ptr(api.FunnelCountByVisitors), wantBy: api.FunnelResponseCountByVisitors, want: []int64{1, 1}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := engine.Funnel(context.Background(), api.FunnelRequest{
				SiteId: 1, DateRange: dateRange("2026-10-08", "2026-10-08"), CountBy: tt.countBy, Steps: steps,
			})
			if err != nil {
				t.Fatal(err)
			}
			if got.CountBy != tt.wantBy || !reflect.DeepEqual(stepCounts(got), tt.want) {
				t.Fatalf("response = %+v, want count_by=%s counts=%v", got, tt.wantBy, tt.want)
			}
		})
	}
}

func TestFunnelWindowsForSessionsAndCrossSessionVisitors(t *testing.T) {
	_, engine := setupFunnelWindowFixture(t)
	steps := []api.FunnelStep{
		{Type: api.Page, Match: api.FunnelStepMatchExact, Value: "/start"},
		{Type: api.Event, Match: api.FunnelStepMatchExact, Value: "finish"},
	}
	for _, tt := range []struct {
		name    string
		countBy api.FunnelRequestCountBy
		window  api.FunnelRequestWindow
		want    []int64
	}{
		{name: "sessions session", countBy: api.FunnelCountBySessions, window: api.FunnelWindowSession, want: []int64{5, 2}},
		{name: "sessions one hour", countBy: api.FunnelCountBySessions, window: api.FunnelWindow1h, want: []int64{5, 1}},
		{name: "sessions twenty four hours", countBy: api.FunnelCountBySessions, window: api.FunnelWindow24h, want: []int64{5, 2}},
		{name: "sessions seven days", countBy: api.FunnelCountBySessions, window: api.FunnelWindow7d, want: []int64{5, 2}},
		{name: "visitors session", countBy: api.FunnelCountByVisitors, window: api.FunnelWindowSession, want: []int64{4, 2}},
		{name: "visitors one hour", countBy: api.FunnelCountByVisitors, window: api.FunnelWindow1h, want: []int64{4, 2}},
		{name: "visitors twenty four hours", countBy: api.FunnelCountByVisitors, window: api.FunnelWindow24h, want: []int64{4, 4}},
		{name: "visitors seven days", countBy: api.FunnelCountByVisitors, window: api.FunnelWindow7d, want: []int64{4, 4}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := engine.Funnel(context.Background(), api.FunnelRequest{
				SiteId: 1, DateRange: dateRange("2026-10-08", "2026-10-09"), Steps: steps,
				CountBy: &tt.countBy, Window: &tt.window,
			})
			if err != nil {
				t.Fatal(err)
			}
			if counts := stepCounts(got); !reflect.DeepEqual(counts, tt.want) {
				t.Fatalf("step counts=%v, want %v", counts, tt.want)
			}
		})
	}
}

func TestTimedFunnelKeepsCodesPairedWithTimestampsAfterBackfill(t *testing.T) {
	pool, engine := setupFunnelWindowFixture(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
insert into events(site_id,retention_class,local_day,ts,visitor_id,session_id,profile_id,kind,name_id,path_id)
values (1,30,null,'2026-10-08 16:30Z',106,6,1002,1,999,5)`); err != nil {
		t.Fatal(err)
	}
	oldLayout := closer.NewRollupLayout([]int16{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18}, closer.RollupCounterColumns())
	if _, err := pool.Exec(ctx, `
insert into rollup_state(singleton,layout_version,dimension_set,counter_set,days_done,total_days,started_at,finished_at)
values (true,$1,$2,$3,0,0,now(),now())`, oldLayout.Version(), oldLayout.Dimensions, oldLayout.Counters); err != nil {
		t.Fatal(err)
	}
	currentLayout := closer.NewRollupLayout(oldLayout.Dimensions,
		append(closer.RollupCounterColumns(), "custom_closed.session_id", "sessions_closed.seq_at"))
	if _, err := closer.NewRollupBackfill(pool, currentLayout, nil).Run(ctx); err != nil {
		t.Fatal(err)
	}
	var codes, timestamps int
	if err := pool.QueryRow(ctx, `select cardinality(seq),cardinality(seq_at) from sessions_closed where site_id=1 and day='2026-10-08' and session_id=6`).Scan(&codes, &timestamps); err != nil {
		t.Fatal(err)
	}
	if codes != timestamps {
		t.Fatalf("closed sequence cardinalities=(%d,%d), want equal paired arrays", codes, timestamps)
	}

	window := api.FunnelWindow1h
	got, err := engine.Funnel(ctx, api.FunnelRequest{
		SiteId: 1, DateRange: dateRange("2026-10-08", "2026-10-08"), Window: &window,
		Steps: []api.FunnelStep{
			{Type: api.Page, Match: api.FunnelStepMatchExact, Value: "/start"},
			{Type: api.Event, Match: api.FunnelStepMatchExact, Value: "finish"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if counts := stepCounts(got); !reflect.DeepEqual(counts, []int64{5, 1}) {
		t.Fatalf("timed funnel step counts=%v, want [5 1] with the 18:00 finish outside one hour", counts)
	}
}

func setupTiny(t *testing.T) (*pgxpool.Pool, *query.Engine) {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsurePartitions(ctx, pool, day("2026-10-05"), day("2026-10-10")); err != nil {
		t.Fatal(err)
	}
	loadTinyRows(t, pool)
	for _, value := range []string{"2026-10-06", "2026-10-07"} {
		if _, err := closer.CloseDay(ctx, pool, []int32{1}, day(value)); err != nil {
			t.Fatal(err)
		}
	}
	dictionary := dict.New(pool)
	if err := dictionary.Load(ctx); err != nil {
		t.Fatal(err)
	}
	reg := registry{site: sites.Site{ID: 1, PublicID: "tiny", TZ: time.UTC, Retention: db.Ret30, ProfilesEnabled: true, RevenueCurrency: "USD", RevenueCurrencyState: sites.RevenueCurrencyConfigured}}
	return pool, identifiedEngine(pool, dictionary, reg)
}

func setupLocationFilterFixture(t *testing.T) (*pgxpool.Pool, *query.Engine) {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsurePartitions(ctx, pool, day("2026-10-06"), day("2026-10-09")); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
insert into dict(kind,id,value) values (0,1,'/'),(12,601,'Paris'),(12,602,'Lyon'),(12,603,'Berlin');
insert into sessions
  (site_id,retention_class,session_id,visitor_id,start,last,local_day,entry_path,exit_path,country,city_id,pageviews,events,engagement_ms,bounce)
select 1,30,id,id,day_value + offset_value,day_value + offset_value,day_value::date,1,1,country,city_id,1,0,0,true
from (values
  (1, '2026-10-06'::timestamptz, interval '10 hours', 'FR'::char(2), 601),
  (2, '2026-10-06'::timestamptz, interval '11 hours', 'FR'::char(2), 602),
  (3, '2026-10-06'::timestamptz, interval '12 hours', 'DE'::char(2), 603),
  (4, '2026-10-08'::timestamptz, interval '10 hours', 'FR'::char(2), 601),
  (5, '2026-10-08'::timestamptz, interval '11 hours', 'FR'::char(2), 602),
  (6, '2026-10-08'::timestamptz, interval '12 hours', 'DE'::char(2), 603)
) fixture(id,day_value,offset_value,country,city_id);
insert into events
  (site_id,retention_class,local_day,ts,visitor_id,session_id,kind,path_id,country,city_id)
select site_id,retention_class,local_day,start,visitor_id,session_id,0,1,country,city_id from sessions`); err != nil {
		t.Fatal(err)
	}
	if _, err := closer.CloseDay(ctx, pool, []int32{1}, day("2026-10-06")); err != nil {
		t.Fatal(err)
	}
	dictionary := dict.New(pool)
	if err := dictionary.Load(ctx); err != nil {
		t.Fatal(err)
	}
	reg := registry{site: sites.Site{ID: 1, PublicID: "locations", TZ: time.UTC, Retention: db.Ret30}}
	return pool, identifiedEngine(pool, dictionary, reg)
}

func setupFunnelMatching(t *testing.T) (*pgxpool.Pool, *query.Engine) {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsurePartitions(ctx, pool, day("2026-10-05"), day("2026-10-10")); err != nil {
		t.Fatal(err)
	}
	loadTinyRows(t, pool)
	_, err := pool.Exec(ctx, `
insert into dict(kind, id, value) values
  (0, 5, '/literal%_star*value'), (7, 21, 'evt%_star*done');

insert into sessions
  (site_id, retention_class, session_id, visitor_id, start, last, local_day,
   entry_path, exit_path, pageviews, events, engagement_ms, bounce)
select site_id, 30, site_id * 1000 + day_number, site_id * 10000 + day_number,
       day_value + interval '10 hours', day_value + interval '10 hours 1 minute', day_value::date,
       5, 5, 1, 1, 60000, false
from (values (1), (2)) sites(site_id)
cross join (values
  (401, '2026-10-06'::timestamptz),
  (501, '2026-10-07'::timestamptz),
  (601, '2026-10-08'::timestamptz)
) days(day_number, day_value);

insert into events
  (site_id, retention_class, local_day, ts, visitor_id, session_id, kind, name_id, path_id, engagement_ms, props)
select site_id, 30, local_day, start, visitor_id, session_id, 0, 0, 5, 0, null
from sessions where site_id in (1, 2) and entry_path=5;
insert into events
  (site_id, retention_class, local_day, ts, visitor_id, session_id, kind, name_id, path_id, engagement_ms, props)
select site_id, 30, local_day, start + interval '1 minute', visitor_id, session_id, 1, 21, 5, 0, null
from sessions where site_id in (1, 2) and entry_path=5;`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update events set props=case when local_day='2026-10-08' then '{"plan":"free"}'::jsonb else '{"plan":"pro"}'::jsonb end where site_id=1 and name_id=21`); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"2026-10-06", "2026-10-07"} {
		if _, err := closer.CloseDay(ctx, pool, []int32{1, 2}, day(value)); err != nil {
			t.Fatal(err)
		}
	}
	dictionary := dict.New(pool)
	if err := dictionary.Load(ctx); err != nil {
		t.Fatal(err)
	}
	reg := registry{site: sites.Site{ID: 1, PublicID: "funnel-matching", TZ: time.UTC, Retention: db.Ret30}}
	return pool, identifiedEngine(pool, dictionary, reg)
}

func setupFunnelWindowFixture(t *testing.T) (*pgxpool.Pool, *query.Engine) {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsurePartitions(ctx, pool, day("2026-10-08"), day("2026-10-10")); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
insert into dict(kind,id,value) values (0,5,'/start'),(7,21,'finish');
insert into orgs(id,name,slug) overriding system value values (1,'Funnels','funnels');
insert into sites(id,org_id,public_id,name,timezone,retention_class) overriding system value
values (1,1,'funnelwin001','Funnel windows','UTC',30);
insert into profiles(id,site_id,external_id,first_seen_at,last_seen_at) overriding system value values
  (1001,1,'profile-1','2026-10-08 10:00Z','2026-10-08 12:30Z'),
  (1002,1,'profile-2','2026-10-08 16:00Z','2026-10-08 18:00Z'),
  (1003,1,'profile-3','2026-10-08 20:00Z','2026-10-09 00:30Z');
insert into sessions
  (site_id,retention_class,session_id,visitor_id,start,last,local_day,entry_path,exit_path,pageviews,events,engagement_ms,bounce)
values
  (1,30,1,101,'2026-10-08 10:00Z','2026-10-08 10:30Z','2026-10-08',5,5,1,1,0,false),
  (1,30,2,102,'2026-10-08 12:00Z','2026-10-08 12:01Z','2026-10-08',5,5,1,0,0,true),
  (1,30,3,103,'2026-10-08 12:30Z','2026-10-08 12:31Z','2026-10-08',0,0,0,1,0,false),
  (1,30,4,200,'2026-10-08 14:00Z','2026-10-08 14:01Z','2026-10-08',5,5,1,0,0,true),
  (1,30,5,200,'2026-10-08 14:30Z','2026-10-08 14:31Z','2026-10-08',0,0,0,1,0,false),
  (1,30,6,106,'2026-10-08 16:00Z','2026-10-08 18:00Z','2026-10-08',5,5,1,1,0,false),
  (1,30,7,107,'2026-10-08 20:00Z','2026-10-08 20:01Z','2026-10-08',5,5,1,0,0,true),
  (1,30,8,108,'2026-10-09 00:30Z','2026-10-09 00:31Z','2026-10-09',0,0,0,1,0,false);
insert into events
  (site_id,retention_class,local_day,ts,visitor_id,session_id,profile_id,kind,name_id,path_id)
values
  (1,30,'2026-10-08','2026-10-08 10:00Z',101,1,1001,0,0,5),
  (1,30,'2026-10-08','2026-10-08 10:30Z',101,1,1001,1,21,5),
  (1,30,'2026-10-08','2026-10-08 12:00Z',102,2,1001,0,0,5),
  (1,30,'2026-10-08','2026-10-08 12:30Z',103,3,1001,1,21,0),
  (1,30,'2026-10-08','2026-10-08 14:00Z',200,4,NULL,0,0,5),
  (1,30,'2026-10-08','2026-10-08 14:30Z',200,5,NULL,1,21,0),
  (1,30,'2026-10-08','2026-10-08 16:00Z',106,6,1002,0,0,5),
  (1,30,'2026-10-08','2026-10-08 18:00Z',106,6,1002,1,21,5),
  (1,30,'2026-10-08','2026-10-08 20:00Z',107,7,1003,0,0,5),
  (1,30,'2026-10-09','2026-10-09 00:30Z',108,8,1003,1,21,0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := closer.CloseDay(ctx, pool, []int32{1}, day("2026-10-08")); err != nil {
		t.Fatal(err)
	}
	dictionary := dict.New(pool)
	if err := dictionary.Load(ctx); err != nil {
		t.Fatal(err)
	}
	reg := registry{site: sites.Site{ID: 1, PublicID: "funnel-window", TZ: time.UTC, Retention: db.Ret30, ProfilesEnabled: true}}
	return pool, identifiedEngine(pool, dictionary, reg)
}

func setupTinyDims(t *testing.T) (*pgxpool.Pool, *query.Engine) {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsurePartitions(ctx, pool, day("2026-10-05"), day("2026-10-10")); err != nil {
		t.Fatal(err)
	}
	loadTinyRows(t, pool)
	if _, err := pool.Exec(ctx, tinyDimsSQL); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"2026-10-06", "2026-10-07"} {
		if _, err := closer.CloseDay(ctx, pool, []int32{1}, day(value)); err != nil {
			t.Fatal(err)
		}
	}
	dictionary := dict.New(pool)
	if err := dictionary.Load(ctx); err != nil {
		t.Fatal(err)
	}
	reg := registry{site: sites.Site{ID: 1, PublicID: "tiny", TZ: time.UTC, Retention: db.Ret30, ProfilesEnabled: true}}
	return pool, identifiedEngine(pool, dictionary, reg)
}

const tinyDimsSQL = `
insert into dict(kind, id, value) values
  (13, 70, 'Chrome 126'), (13, 71, 'Firefox 131'), (14, 80, 'Windows 10/11'),
  (15, 90, 'en'), (15, 91, 'hi'), (16, 95, 'example.com'), (16, 96, 'docs.example.com');
update sessions s set browser_ver_id = v.bv, os_ver_id = v.ov, screen_bucket = v.sb, language_id = v.lang, hostname_id = v.host
from (values (101,70,0,3,90,95), (102,71,80,1,91,96), (103,70,0,3,90,96),
             (202,71,80,2,91,96), (301,70,0,4,90,95), (302,71,80,5,0,0)
) v(id, bv, ov, sb, lang, host) where s.session_id = v.id;
update events e set browser_ver_id = s.browser_ver_id, os_ver_id = s.os_ver_id,
  language_id = s.language_id, hostname_id = s.hostname_id,
  screen = case s.screen_bucket when 1 then 375 when 2 then 800 when 3 then 1280 when 4 then 1600 when 5 then 1920 else 0 end
from sessions s where s.session_id = e.session_id;`

func setupPageMetricFixture(t *testing.T) (*pgxpool.Pool, *query.Engine) {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	fixtureDay := day("2026-10-09")
	if err := db.EnsurePartitions(ctx, pool, fixtureDay, fixtureDay.AddDate(0, 0, 2)); err != nil {
		t.Fatal(err)
	}
	_, err := pool.Exec(ctx, `
insert into dict(kind, id, value) values
  (0, 101, '/a'), (0, 102, '/b'), (0, 103, '/repeat'), (7, 201, 'signup');

insert into sessions
  (site_id, retention_class, session_id, visitor_id, start, last, local_day,
   entry_path, exit_path, country, pageviews, events, engagement_ms, bounce)
values
  (1,30,1,11,'2026-10-09 10:00Z','2026-10-09 10:04Z','2026-10-09',101,102,'IN',2,0,30000,false),
  (1,30,2,12,'2026-10-09 11:00Z','2026-10-09 11:01Z','2026-10-09',101,101,'IN',1,0,30000,true),
  (1,30,3,13,'2026-10-09 12:00Z','2026-10-09 12:01Z','2026-10-09',101,102,'US',2,0,0,false),
  (1,30,4,14,'2026-10-09 13:00Z','2026-10-09 13:03Z','2026-10-09',103,103,'CA',2,0,30000,false),
  (1,30,5,15,'2026-10-09 23:59Z','2026-10-10 00:01Z','2026-10-09',101,101,'IN',1,1,5000,false);

insert into events
  (site_id, retention_class, local_day, ts, visitor_id, session_id, kind,
   name_id, path_id, country, engagement_ms, scroll_pct, props)
values
  (1,30,'2026-10-09','2026-10-09 10:00Z',11,1,0,0,101,'IN',0,0,null),
  (1,30,'2026-10-09','2026-10-09 10:01Z',11,1,2,0,101,'IN',10000,0,null),
  (1,30,'2026-10-09','2026-10-09 10:02Z',11,1,2,0,101,'IN',20000,75,null),
  (1,30,'2026-10-09','2026-10-09 10:02:30Z',11,1,1,201,101,'IN',0,0,'{"plan":"pro"}'),
  (1,30,'2026-10-09','2026-10-09 10:03Z',11,1,0,0,102,'IN',0,0,null),
  (1,30,'2026-10-09','2026-10-09 10:04Z',11,1,2,0,102,'IN',0,0,null),
  (1,30,'2026-10-09','2026-10-09 11:00Z',12,2,0,0,101,'IN',0,0,null),
  (1,30,'2026-10-09','2026-10-09 11:01Z',12,2,2,0,101,'IN',30000,50,null),
  (1,30,'2026-10-09','2026-10-09 12:00Z',13,3,0,0,101,'US',0,0,null),
  (1,30,'2026-10-09','2026-10-09 12:01Z',13,3,0,0,102,'US',0,0,null),
  (1,30,'2026-10-09','2026-10-09 13:00Z',14,4,0,0,103,'CA',0,0,null),
  (1,30,'2026-10-09','2026-10-09 13:01Z',14,4,2,0,103,'CA',10000,40,null),
  (1,30,'2026-10-09','2026-10-09 13:02Z',14,4,0,0,103,'CA',0,0,null),
  (1,30,'2026-10-09','2026-10-09 13:03Z',14,4,2,0,103,'CA',20000,90,null),
  (1,30,'2026-10-09','2026-10-09 23:59Z',15,5,0,0,101,'IN',0,0,null),
  (1,30,'2026-10-09','2026-10-10 00:00Z',15,5,1,201,101,'IN',0,0,'{"plan":"pro"}'),
  (1,30,'2026-10-09','2026-10-10 00:01Z',15,5,2,0,101,'IN',5000,80,null)`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := closer.CloseDay(ctx, pool, []int32{1}, fixtureDay); err != nil {
		t.Fatal(err)
	}
	dictionary := dict.New(pool)
	if err := dictionary.Load(ctx); err != nil {
		t.Fatal(err)
	}
	reg := registry{site: sites.Site{ID: 1, PublicID: "pages", TZ: time.UTC, Retention: db.Ret30}}
	return pool, identifiedEngine(pool, dictionary, reg)
}

func pageMetricRequest(filters []api.Filter) api.QueryRequest {
	dimensions := []string{"page"}
	return api.QueryRequest{
		SiteId:     1,
		DateRange:  ptr(dateRange("2026-10-09", "2026-10-09")),
		Metrics:    []api.Metric{api.Pageviews, api.Metric(query.ExitRate), api.Metric(query.TimeOnPage)},
		Dimensions: &dimensions,
		Filters:    ptr(filters),
	}
}

func assertPageMetrics(t *testing.T, route string, response api.QueryResponse, want map[string]struct {
	pageviews  float64
	exitRate   float64
	timeOnPage float64
}) {
	t.Helper()
	if len(response.Rows) != len(want) {
		t.Fatalf("%s rows = %+v, want %d", route, response.Rows, len(want))
	}
	for _, row := range response.Rows {
		path, _ := row["page"].(string)
		expected, ok := want[path]
		if !ok {
			t.Fatalf("%s returned unexpected page row %+v", route, row)
		}
		if row["pageviews"] != expected.pageviews || math.Abs(row["exit_rate"].(float64)-expected.exitRate) > 1e-9 || row["time_on_page"] != expected.timeOnPage {
			t.Errorf("%s %s = %+v, want pageviews=%v exit_rate=%v time_on_page=%v", route, path, row, expected.pageviews, expected.exitRate, expected.timeOnPage)
		}
	}
}

func loadTinyRows(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	_, err := pool.Exec(ctx, `
insert into dict(kind, id, value) values
  (0, 1, '/'), (0, 2, '/pricing'), (0, 3, '/blog/post'), (0, 4, '/docs'),
  (2, 10, 'Google'), (2, 11, 'Direct'), (2, 12, 'newsletter'),
  (7, 20, 'signup'), (9, 30, 'Chrome'), (9, 31, 'Firefox'),
  (10, 40, 'Linux'), (10, 41, 'Windows'),
  (11, 50, 'California'), (11, 51, 'Karnataka'),
  (12, 60, 'Los Angeles'), (12, 61, 'San Francisco'), (12, 62, 'Bengaluru');

insert into sessions
  (site_id, retention_class, session_id, visitor_id, start, last, local_day,
   entry_path, exit_path, source_id, utm_source_id, country, region_id, city_id, browser_id, os_id, device,
   pageviews, events, engagement_ms, bounce)
values
  (1,30,101,1001,'2026-10-06 10:00Z','2026-10-06 10:03Z','2026-10-06',1,2,10,0,'US',50,60,30,40,1,2,1,60000,false),
  (1,30,102,1002,'2026-10-06 11:00Z','2026-10-06 11:00Z','2026-10-06',3,3,11,0,'IN',0,0,31,41,2,1,0,10000,true),
  (1,30,103,1001,'2026-10-06 12:00Z','2026-10-06 12:01Z','2026-10-06',1,4,10,12,'US',50,60,30,40,1,2,0,30000,false),
  (1,30,201,2001,'2026-10-07 09:00Z','2026-10-07 09:02Z','2026-10-07',1,2,10,12,'US',50,60,30,40,1,2,1,45000,false),
  (1,30,202,2002,'2026-10-07 11:00Z','2026-10-07 11:00Z','2026-10-07',3,3,11,0,'IN',51,62,31,41,2,1,0,5000,true),
	  (1,30,301,3001,'2026-10-08 08:00Z','2026-10-08 08:02Z','2026-10-08',1,2,10,12,'US',50,60,30,40,1,2,0,30000,false),
  (1,30,302,3002,'2026-10-08 12:00Z','2026-10-08 12:00Z','2026-10-08',4,4,11,0,'IN',0,0,31,41,2,1,0,0,true);

insert into events
  (site_id, retention_class, local_day, ts, visitor_id, session_id, kind,
   name_id, path_id, source_id, utm_source_id, country, region_id, city_id, browser_id, os_id, device, engagement_ms, scroll_pct, props)
values
  (1,30,'2026-10-06','2026-10-06 10:00Z',1001,101,0,0,1,10,0,'US',50,60,30,40,1,0,0,null),
  (1,30,'2026-10-06','2026-10-06 10:01Z',1001,101,0,0,2,10,0,'US',50,60,30,40,1,0,0,null),
  (1,30,'2026-10-06','2026-10-06 10:02Z',1001,101,1,20,2,10,0,'US',50,60,30,40,1,0,0,'{"plan":"pro"}'),
  (1,30,'2026-10-06','2026-10-06 11:00Z',1002,102,0,0,3,11,0,'IN',0,0,31,41,2,0,0,null),
  (1,30,'2026-10-06','2026-10-06 12:00Z',1001,103,0,0,1,10,12,'US',50,60,30,40,1,0,0,null),
  (1,30,'2026-10-06','2026-10-06 12:01Z',1001,103,0,0,4,10,12,'US',50,60,30,40,1,0,0,null),
  (1,30,'2026-10-07','2026-10-07 09:00Z',2001,201,0,0,1,10,12,'US',50,60,30,40,1,0,0,null),
  (1,30,'2026-10-07','2026-10-07 09:01Z',2001,201,0,0,2,10,12,'US',50,60,30,40,1,0,0,null),
  (1,30,'2026-10-07','2026-10-07 09:02Z',2001,201,1,20,2,10,12,'US',50,60,30,40,1,0,0,'{"plan":"pro"}'),
  (1,30,'2026-10-07','2026-10-07 11:00Z',2002,202,0,0,3,11,0,'IN',51,62,31,41,2,0,0,null),
  (1,30,'2026-10-08','2026-10-08 08:00Z',3001,301,0,0,1,10,12,'US',50,60,30,40,1,0,0,null),
  (1,30,'2026-10-08','2026-10-08 08:01Z',3001,301,0,0,2,10,12,'US',50,60,30,40,1,0,0,null),
	  (1,30,'2026-10-08','2026-10-08 12:00Z',3002,302,0,0,4,11,0,'IN',0,0,31,41,2,0,0,null)`)
	if err != nil {
		t.Fatal(err)
	}
}

func seedRevenueRows(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
update events set revenue_micros=2500000, revenue_currency='USD'
where site_id=1 and kind=1 and local_day='2026-10-06';
update events set revenue_micros=7500000, revenue_currency='USD'
where site_id=1 and kind=1 and local_day='2026-10-07';
update custom_closed set revenue_micros=2500000, revenue_currency='USD'
where site_id=1 and name_id=20 and day='2026-10-06';
update custom_closed set revenue_micros=7500000, revenue_currency='USD'
where site_id=1 and name_id=20 and day='2026-10-07';
insert into events
  (site_id, retention_class, local_day, ts, visitor_id, session_id, kind,
   name_id, path_id, source_id, country, region_id, city_id, browser_id, os_id, device,
   engagement_ms, scroll_pct, props, revenue_micros, revenue_currency)
values
  (1,30,'2026-10-07','2026-10-07 11:00:30Z',2002,202,1,
   20,3,11,'IN',51,62,31,41,2,0,0,'{"plan":"starter"}',4000000,'EUR');
insert into custom_closed
  (site_id, day, visitor_id, name_id, path_id, source_id, medium_id, campaign_id,
   utm_content_id, utm_term_id, country, region_id, city_id, browser_id, os_id, device,
   entry_path, exit_path, props, revenue_micros, revenue_currency)
select site_id, day, 2002, name_id, 3, 11, medium_id, campaign_id,
       utm_content_id, utm_term_id, 'IN', 51, 62, 31, 41, 2,
       3, 3, '{"plan":"starter"}'::jsonb, 4000000, 'EUR'
from custom_closed
where site_id=1 and name_id=20 and day='2026-10-07'
limit 1`)
	if err != nil {
		t.Fatal(err)
	}
}

func queryRequest(dimensions []string, filters []api.Filter) api.QueryRequest {
	return queryRequestRange("2026-10-06", "2026-10-07", dimensions, filters)
}

func queryRequestRange(from, to string, dimensions []string, filters []api.Filter) api.QueryRequest {
	return api.QueryRequest{
		SiteId:     1,
		DateRange:  ptr(dateRange(from, to)),
		Metrics:    []api.Metric{api.Visitors, api.Visits, api.Pageviews, api.BounceRate, api.VisitDuration, api.EngagementTime},
		Dimensions: ptr(dimensions),
		Filters:    ptr(filters),
	}
}

func dateRange(from, to string) api.DateRange {
	return api.DateRange{
		From: openapi_types.Date{Time: day(from)},
		To:   openapi_types.Date{Time: day(to)},
	}
}

func mustQuery(t *testing.T, engine *query.Engine, req api.QueryRequest) api.QueryResponse {
	t.Helper()
	response, err := engine.Query(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func equivalent(got, want api.QueryResponse) bool {
	got.Meta, want.Meta = api.QueryMeta{}, api.QueryMeta{}
	return reflect.DeepEqual(got, want)
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func stepCounts(response api.FunnelResponse) []int64 {
	counts := make([]int64, len(response.Steps))
	for i, step := range response.Steps {
		counts[i] = step.Count
	}
	return counts
}

func closeFloats(got, want []float64) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if math.Abs(got[i]-want[i]) > 1e-9 {
			return false
		}
	}
	return true
}

func ptr[T any](value T) *T { return &value }
