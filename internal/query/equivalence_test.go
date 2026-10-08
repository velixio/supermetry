package query_test

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"panel/internal/api"
	"panel/internal/closer"
	"panel/internal/db"
	"panel/internal/dict"
	"panel/internal/query"
	"panel/internal/sites"
	"panel/internal/testpg"
)

type registry struct{ site sites.Site }

func (r registry) ByPublicID(id string) (sites.Site, bool) {
	return r.site, id == r.site.PublicID
}

func (r registry) Site(id int32) (sites.Site, bool) { return r.site, id == r.site.ID }
func (r registry) All() []sites.Site                { return []sites.Site{r.site} }

func TestRouteEquivalence(t *testing.T) {
	_, engine := setupTiny(t)
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
		{name: "entry", req: queryRequest([]string{"entry"}, nil), eligible: []query.Route{query.RouteRollup, query.RouteHot}},
		{name: "country filter", req: queryRequest([]string{"source"}, []api.Filter{filter("country", api.Is, "US")}), eligible: []query.Route{query.RouteCube, query.RouteHot}},
		{name: "country and source", req: queryRequest([]string{"country"}, []api.Filter{filter("country", api.Is, "US"), filter("source", api.Is, "Google")}), eligible: []query.Route{query.RouteCube, query.RouteHot}},
		{name: "region filter", req: queryRequest([]string{"city"}, []api.Filter{filter("region", api.Is, "California")}), eligible: []query.Route{query.RouteHot}},
		{name: "city filter", req: queryRequest([]string{"source"}, []api.Filter{filter("city", api.Is, "Bengaluru")}), eligible: []query.Route{query.RouteHot}},
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
	engine := query.NewEngine(pool, dictionary, registry{site: sites.Site{
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
	req := queryRequest([]string{"event", "region"}, []api.Filter{filter("region", api.Is, "California")})
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
	if visitors := stepVisitors(got); !reflect.DeepEqual(visitors, want) {
		t.Fatalf("step visitors = %v, want %v", visitors, want)
	}
	if conversions := []float64{got.Steps[0].Conversion, got.Steps[1].Conversion, got.Steps[2].Conversion}; !closeFloats(conversions, []float64{100, 75, 50}) {
		t.Fatalf("conversions = %v", conversions)
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
	reg := registry{site: sites.Site{ID: 1, PublicID: "tiny", TZ: time.UTC, Retention: db.Ret30}}
	return pool, query.NewEngine(pool, dictionary, reg)
}

func loadTinyRows(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	_, err := pool.Exec(ctx, `
insert into dict(kind, id, value) values
  (0, 1, '/'), (0, 2, '/pricing'), (0, 3, '/blog/post'), (0, 4, '/docs'),
  (2, 10, 'Google'), (2, 11, 'Direct'),
  (7, 20, 'signup'), (9, 30, 'Chrome'), (9, 31, 'Firefox'),
  (10, 40, 'Linux'), (10, 41, 'Windows'),
  (11, 50, 'California'), (11, 51, 'Karnataka'),
  (12, 60, 'Los Angeles'), (12, 61, 'San Francisco'), (12, 62, 'Bengaluru');

insert into sessions
  (site_id, retention_class, session_id, visitor_id, start, last, local_day,
   entry_path, exit_path, source_id, country, region_id, city_id, browser_id, os_id, device,
   pageviews, events, engagement_ms, bounce)
values
  (1,30,101,1001,'2026-10-06 10:00Z','2026-10-06 10:03Z','2026-10-06',1,2,10,'US',50,60,30,40,1,2,1,60000,false),
  (1,30,102,1002,'2026-10-06 11:00Z','2026-10-06 11:00Z','2026-10-06',3,3,11,'IN',0,0,31,41,2,1,0,10000,true),
  (1,30,103,1001,'2026-10-06 12:00Z','2026-10-06 12:01Z','2026-10-06',1,4,10,'US',50,60,30,40,1,2,0,30000,false),
  (1,30,201,2001,'2026-10-07 09:00Z','2026-10-07 09:02Z','2026-10-07',1,2,10,'US',50,60,30,40,1,2,1,45000,false),
  (1,30,202,2002,'2026-10-07 11:00Z','2026-10-07 11:00Z','2026-10-07',3,3,11,'IN',51,62,31,41,2,1,0,5000,true),
	  (1,30,301,3001,'2026-10-08 08:00Z','2026-10-08 08:02Z','2026-10-08',1,2,10,'US',50,60,30,40,1,2,0,30000,false),
  (1,30,302,3002,'2026-10-08 12:00Z','2026-10-08 12:00Z','2026-10-08',4,4,11,'IN',0,0,31,41,2,1,0,0,true);

insert into events
  (site_id, retention_class, local_day, ts, visitor_id, session_id, kind,
   name_id, path_id, source_id, country, region_id, city_id, browser_id, os_id, device, engagement_ms, scroll_pct, props)
values
  (1,30,'2026-10-06','2026-10-06 10:00Z',1001,101,0,0,1,10,'US',50,60,30,40,1,0,0,null),
  (1,30,'2026-10-06','2026-10-06 10:01Z',1001,101,0,0,2,10,'US',50,60,30,40,1,0,0,null),
  (1,30,'2026-10-06','2026-10-06 10:02Z',1001,101,1,20,2,10,'US',50,60,30,40,1,0,0,'{"plan":"pro"}'),
  (1,30,'2026-10-06','2026-10-06 11:00Z',1002,102,0,0,3,11,'IN',0,0,31,41,2,0,0,null),
  (1,30,'2026-10-06','2026-10-06 12:00Z',1001,103,0,0,1,10,'US',50,60,30,40,1,0,0,null),
  (1,30,'2026-10-06','2026-10-06 12:01Z',1001,103,0,0,4,10,'US',50,60,30,40,1,0,0,null),
  (1,30,'2026-10-07','2026-10-07 09:00Z',2001,201,0,0,1,10,'US',50,60,30,40,1,0,0,null),
  (1,30,'2026-10-07','2026-10-07 09:01Z',2001,201,0,0,2,10,'US',50,60,30,40,1,0,0,null),
  (1,30,'2026-10-07','2026-10-07 09:02Z',2001,201,1,20,2,10,'US',50,60,30,40,1,0,0,'{"plan":"pro"}'),
  (1,30,'2026-10-07','2026-10-07 11:00Z',2002,202,0,0,3,11,'IN',51,62,31,41,2,0,0,null),
  (1,30,'2026-10-08','2026-10-08 08:00Z',3001,301,0,0,1,10,'US',50,60,30,40,1,0,0,null),
  (1,30,'2026-10-08','2026-10-08 08:01Z',3001,301,0,0,2,10,'US',50,60,30,40,1,0,0,null),
	  (1,30,'2026-10-08','2026-10-08 12:00Z',3002,302,0,0,4,11,'IN',0,0,31,41,2,0,0,null)`)
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

func stepVisitors(response api.FunnelResponse) []int64 {
	visitors := make([]int64, len(response.Steps))
	for i, step := range response.Steps {
		visitors[i] = step.Visitors
	}
	return visitors
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
