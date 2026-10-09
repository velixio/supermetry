package query_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/api"
	"supermetry/internal/closer"
	"supermetry/internal/db"
	"supermetry/internal/dict"
	platformgoals "supermetry/internal/platform/goals"
	"supermetry/internal/query"
	"supermetry/internal/sites"
	"supermetry/internal/testpg"
)

func TestGoalIDFilterMatchesHandComputedFixturesAcrossSessionTiers(t *testing.T) {
	_, engine, goalIDs := setupGoalFixture(t)
	tests := []struct {
		name        string
		goal        string
		conversions float64
	}{
		{name: "wildcard scroll uses best matching page", goal: "blog_scroll", conversions: 1},
		{name: "exact scroll includes one hundred percent", goal: "pricing_exact", conversions: 1},
		{name: "prefix page goal", goal: "pricing_page", conversions: 2},
		{name: "event goal", goal: "signup", conversions: 1},
		{name: "prefix scroll excludes ninety nine percent", goal: "pricing_prefix", conversions: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := goalFilterRequest(goalIDs[tt.goal])
			for _, route := range []query.Route{query.RouteHot, query.RouteClosed} {
				response, err := engine.WithForcedRoute(route).Query(context.Background(), request)
				if err != nil {
					t.Fatalf("%s query: %v", route.String(), err)
				}
				if response.Totals["conversions"] != tt.conversions || response.Totals["visitors"] != tt.conversions {
					t.Errorf("%s totals = %v, want conversions=visitors=%v", route.String(), response.Totals, tt.conversions)
				}
			}
		})
	}
}

func TestGoalIDFilterRejectsUnavailableAndCrossSiteGoals(t *testing.T) {
	_, engine, goalIDs := setupGoalFixture(t)
	for _, tt := range []struct {
		name string
		id   int64
	}{
		{name: "other site", id: goalIDs["other_site"]},
		{name: "deleted or stale", id: 999999},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := engine.Query(context.Background(), goalFilterRequest(tt.id))
			if err == nil || !strings.Contains(err.Error(), "invalid filter") {
				t.Fatalf("Query() error = %v, want invalid filter", err)
			}
		})
	}
}

func TestPageGoalIDMatchesLegacyPageFiltersAndWildcardIsGlob(t *testing.T) {
	_, engine, goalIDs := setupGoalFixture(t)
	tests := []struct {
		name     string
		goal     string
		operator api.FilterOperator
		value    string
	}{
		{name: "exact", goal: "pricing_exact_page", operator: api.Is, value: "/pricing"},
		{name: "prefix", goal: "pricing_page", operator: api.StartsWith, value: "/pricing"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, route := range []query.Route{query.RouteHot, query.RouteClosed} {
				byGoal := mustGoalQuery(t, engine.WithForcedRoute(route), goalFilterRequest(goalIDs[tt.goal]))
				legacy := goalFilterRequest(0)
				legacy.Filters = &[]api.Filter{{Dimension: "page", Operator: tt.operator, Values: []string{tt.value}}}
				byPage := mustGoalQuery(t, engine.WithForcedRoute(route), legacy)
				if byGoal.Totals["conversions"] != byPage.Totals["visitors"] {
					t.Fatalf("%s goal conversions=%v page visitors=%v", route.String(), byGoal.Totals, byPage.Totals)
				}
			}
		})
	}

	for _, route := range []query.Route{query.RouteHot, query.RouteClosed} {
		glob := mustGoalQuery(t, engine.WithForcedRoute(route), goalFilterRequest(goalIDs["comments_glob"]))
		contains := goalFilterRequest(0)
		contains.Filters = &[]api.Filter{{Dimension: "page", Operator: api.Contains, Values: []string{"/blog/"}}}
		legacy := mustGoalQuery(t, engine.WithForcedRoute(route), contains)
		if glob.Totals["conversions"] != 1 || legacy.Totals["visitors"] != 4 || legacy.Totals["visits"] != 5 {
			t.Fatalf("%s glob totals=%v contains totals=%v", route.String(), glob.Totals, legacy.Totals)
		}
	}
}

func TestEventGoalsHonorMatchTypesAcrossStorageTiers(t *testing.T) {
	_, engine, goalIDs := setupGoalFixture(t)
	for _, tt := range []struct {
		name string
		goal string
		want float64
	}{
		{name: "prefix", goal: "signup_prefix", want: 2},
		{name: "wildcard", goal: "completed_wildcard", want: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, route := range []query.Route{query.RouteHot, query.RouteClosed} {
				got := mustGoalQuery(t, engine.WithForcedRoute(route), goalFilterRequest(goalIDs[tt.goal]))
				if got.Totals["conversions"] != tt.want {
					t.Fatalf("%s conversions=%v, want %v", route.String(), got.Totals["conversions"], tt.want)
				}
			}
		})
	}
}

func TestPropertyGoalCountsBestAvailableSessionsOnClosedDays(t *testing.T) {
	pool, engine, goalIDs := setupGoalFixture(t)
	if _, err := pool.Exec(context.Background(), `update custom_closed set session_id=null where name_id=204`); err != nil {
		t.Fatal(err)
	}
	for _, route := range []query.Route{query.RouteHot, query.RouteClosed} {
		got := mustGoalQuery(t, engine.WithForcedRoute(route), goalFilterRequest(goalIDs["qualified_pro"]))
		if got.Totals["conversions"] != 1 || got.Totals["visitors"] != 1 || got.Totals["visits"] != 1 {
			t.Fatalf("%s totals=%v, want one matching session", route.String(), got.Totals)
		}
	}
}

func TestGoalBreakdownReturnsStableGoalIDsInOneQuery(t *testing.T) {
	_, engine, goalIDs := setupGoalFixture(t)
	dimensions := []string{"goal"}
	request := api.QueryRequest{
		SiteId: 1, DateRange: ptr(dateRange("2026-10-09", "2026-10-09")),
		Dimensions: &dimensions, Metrics: []api.Metric{api.Visitors, api.Conversions},
	}
	for _, route := range []query.Route{query.RouteHot, query.RouteClosed} {
		got := mustGoalQuery(t, engine.WithForcedRoute(route), request)
		rows := make(map[string]map[string]any, len(got.Rows))
		for _, row := range got.Rows {
			rows[fmt.Sprint(row["goal"])] = row
		}
		for _, tt := range []struct {
			goal        string
			visitors    float64
			conversions float64
		}{
			{goal: "signup_prefix", visitors: 2, conversions: 2},
			{goal: "qualified_pro", visitors: 1, conversions: 1},
			{goal: "completed_wildcard", visitors: 1, conversions: 1},
		} {
			row := rows[stringInt64(goalIDs[tt.goal])]
			if row == nil || row["visitors"] != tt.visitors || row["conversions"] != tt.conversions {
				t.Fatalf("%s goal %s row=%v", route.String(), tt.goal, row)
			}
		}
	}
}

func TestGoalBreakdownAppliesSessionFiltersAndKeepsWholeSiteTotals(t *testing.T) {
	_, engine, goalIDs := setupGoalFixture(t)
	dimensions := []string{"goal"}
	filters := []api.Filter{
		{Dimension: "country", Operator: api.Is, Values: []string{"US"}},
		{Dimension: "source", Operator: api.Is, Values: []string{"Google"}},
	}
	request := api.QueryRequest{
		SiteId: 1, DateRange: ptr(dateRange("2026-10-09", "2026-10-10")),
		Dimensions: &dimensions, Metrics: []api.Metric{api.Visitors, api.Visits, api.Conversions}, Filters: &filters,
	}
	normalTotalsRequest := request
	normalTotalsRequest.Dimensions = nil
	normalTotalsRequest.Metrics = []api.Metric{api.Visitors, api.Visits, api.Conversions}
	for _, route := range []query.Route{query.RouteHot, query.RouteClosed} {
		if route == query.RouteHot {
			normalTotalsRequest.DateRange = ptr(dateRange("2026-10-10", "2026-10-10"))
		} else {
			normalTotalsRequest.DateRange = ptr(dateRange("2026-10-09", "2026-10-09"))
		}
		got := mustGoalQuery(t, engine.WithForcedRoute(route), normalTotalsRequest)
		if got.Totals["visits"] != 1 || got.Totals["visitors"] != 1 {
			t.Fatalf("%s normal totals=%v, want visits=visitors=1", route.String(), got.Totals)
		}
	}
	for _, route := range []query.Route{query.RouteHot, query.RouteClosed} {
		tierRequest := request
		if route == query.RouteHot {
			tierRequest.DateRange = ptr(dateRange("2026-10-10", "2026-10-10"))
		} else {
			tierRequest.DateRange = ptr(dateRange("2026-10-09", "2026-10-09"))
		}
		got := mustGoalQuery(t, engine.WithForcedRoute(route), tierRequest)
		rows := goalRows(got)
		if row := rows[stringInt64(goalIDs["signup_prefix"])]; row == nil || row["conversions"] != float64(1) {
			t.Fatalf("%s filtered signup row=%v, want one matching session", route.String(), row)
		}
		if got.Totals["visits"] != 1 || got.Totals["visitors"] != 1 {
			t.Fatalf("%s totals=%v, want filtered whole-site visits=visitors=1", route.String(), got.Totals)
		}
		if got.Totals["conversions"] != 1 {
			t.Fatalf("%s conversion total=%v, want one distinct converting session", route.String(), got.Totals["conversions"])
		}
	}

	mixed := mustGoalQuery(t, engine, request)
	if row := goalRows(mixed)[stringInt64(goalIDs["signup_prefix"])]; row == nil || row["conversions"] != float64(2) {
		t.Fatalf("mixed filtered signup row=%v", row)
	}
	if mixed.Totals["visits"] != 2 {
		t.Fatalf("mixed totals=%v, want filtered whole-site visits=2", mixed.Totals)
	}
	if mixed.Totals["conversions"] != 2 {
		t.Fatalf("mixed totals=%v, want two distinct converting sessions", mixed.Totals)
	}

	unfiltered := request
	unfiltered.Filters = nil
	wholeSite := mustGoalQuery(t, engine, unfiltered)
	if wholeSite.Totals["visits"] != 8 || wholeSite.Totals["visitors"] != 7 {
		t.Fatalf("unfiltered totals=%v, want whole-site visits=8 visitors=7", wholeSite.Totals)
	}
}

func TestGoalBreakdownComparisonKeepsAlignedSavedGoals(t *testing.T) {
	_, engine, goalIDs := setupGoalFixture(t)
	dimensions := []string{"goal"}
	compare := api.PreviousPeriod
	request := api.QueryRequest{
		SiteId: 1, DateRange: ptr(dateRange("2026-10-10", "2026-10-10")), Compare: &compare,
		Dimensions: &dimensions, Metrics: []api.Metric{api.Visits, api.Conversions},
	}
	got := mustGoalQuery(t, engine, request)
	if got.Comparison == nil {
		t.Fatal("comparison=nil")
	}
	current, previous := goalRows(got), make(map[string]map[string]any)
	for _, row := range got.Comparison.Rows {
		previous[fmt.Sprint(row["goal"])] = row
	}
	if len(current) != len(goalIDs)-1 || len(previous) != len(current) {
		t.Fatalf("goal row counts current=%d previous=%d", len(current), len(previous))
	}
	id := stringInt64(goalIDs["signup_prefix"])
	if current[id]["conversions"] != float64(1) || previous[id]["conversions"] != float64(2) {
		t.Fatalf("signup comparison current=%v previous=%v", current[id], previous[id])
	}
}

func goalRows(response api.QueryResponse) map[string]map[string]any {
	rows := make(map[string]map[string]any, len(response.Rows))
	for _, row := range response.Rows {
		rows[fmt.Sprint(row["goal"])] = row
	}
	return rows
}

func TestPlanRoutesGoalIDToSessionTiers(t *testing.T) {
	parts := query.Plan(normalized("2026-10-09", "2026-10-10", nil, []api.Filter{{Dimension: "goal_id", Operator: api.Is, Values: []string{"1"}}}), day("2026-10-09"))
	if len(parts) != 2 || parts[0].Route != query.RouteClosed || parts[1].Route != query.RouteHot {
		t.Fatalf("Plan() = %+v, want closed then hot", parts)
	}
}

type goalLookup map[int32][]platformgoals.Goal

func (g goalLookup) Goals(siteID int32) []platformgoals.Goal {
	return append([]platformgoals.Goal(nil), g[siteID]...)
}

func setupGoalFixture(t *testing.T) (*pgxpool.Pool, *query.Engine, map[string]int64) {
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
	var orgID int64
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Goals','goals') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	var siteID, otherSiteID int32
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class) values ($1,'goalsite0001','Goals','UTC',30) returning id`, orgID).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class) values ($1,'goalsite0002','Other','UTC',30) returning id`, orgID).Scan(&otherSiteID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
insert into dict(kind,id,value) values
  (0,101,'/blog/a'),(0,102,'/blog/b'),(0,103,'/pricing'),
	  (0,104,'/pricing/team'),(0,105,'/blog/post/comments'),
	  (2,301,'Google'),(7,201,'Signup'),(7,202,'Signup Trial'),(7,203,'Purchase Completed'),(7,204,'Qualified')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
insert into sessions
  (site_id,retention_class,session_id,visitor_id,start,last,local_day,entry_path,exit_path,pageviews,events,engagement_ms,bounce)
values
  ($1,30,1,11,'2026-10-09 10:00Z','2026-10-09 10:04Z','2026-10-09',101,102,2,0,0,false),
  ($1,30,2,12,'2026-10-09 11:00Z','2026-10-09 11:01Z','2026-10-09',101,101,1,0,0,true),
  ($1,30,3,13,'2026-10-09 12:00Z','2026-10-09 12:02Z','2026-10-09',103,103,1,1,0,false),
  ($1,30,4,14,'2026-10-09 13:00Z','2026-10-09 13:01Z','2026-10-09',104,104,1,0,0,true),
	  ($1,30,5,15,'2026-10-09 14:00Z','2026-10-09 14:00Z','2026-10-09',105,105,1,0,0,true),
	  ($1,30,6,16,'2026-10-09 15:00Z','2026-10-09 15:01Z','2026-10-09',101,101,1,1,0,false),
	  ($1,30,7,16,'2026-10-09 16:00Z','2026-10-09 16:01Z','2026-10-09',101,101,1,1,0,false)`, siteID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
insert into events
  (site_id,retention_class,local_day,ts,visitor_id,session_id,kind,name_id,path_id,engagement_ms,scroll_pct)
values
  ($1,30,'2026-10-09','2026-10-09 10:00Z',11,1,0,0,101,0,0),
  ($1,30,'2026-10-09','2026-10-09 10:01Z',11,1,2,0,101,0,90),
  ($1,30,'2026-10-09','2026-10-09 10:02Z',11,1,0,0,102,0,0),
  ($1,30,'2026-10-09','2026-10-09 10:03Z',11,1,2,0,102,0,60),
  ($1,30,'2026-10-09','2026-10-09 11:00Z',12,2,0,0,101,0,0),
  ($1,30,'2026-10-09','2026-10-09 11:01Z',12,2,2,0,101,0,0),
  ($1,30,'2026-10-09','2026-10-09 12:00Z',13,3,0,0,103,0,0),
  ($1,30,'2026-10-09','2026-10-09 12:01Z',13,3,2,0,103,0,100),
  ($1,30,'2026-10-09','2026-10-09 12:02Z',13,3,1,201,103,0,0),
  ($1,30,'2026-10-09','2026-10-09 13:00Z',14,4,0,0,104,0,0),
  ($1,30,'2026-10-09','2026-10-09 13:01Z',14,4,2,0,104,0,99),
	  ($1,30,'2026-10-09','2026-10-09 14:00Z',15,5,0,0,105,0,0),
	  ($1,30,'2026-10-09','2026-10-09 15:00Z',16,6,0,0,101,0,0),
	  ($1,30,'2026-10-09','2026-10-09 15:01Z',16,6,1,204,101,0,0),
	  ($1,30,'2026-10-09','2026-10-09 15:01:30Z',16,6,1,204,101,0,0),
	  ($1,30,'2026-10-09','2026-10-09 16:00Z',16,7,0,0,101,0,0),
	  ($1,30,'2026-10-09','2026-10-09 16:01Z',16,7,1,204,101,0,0),
	  ($1,30,'2026-10-09','2026-10-09 11:00:30Z',12,2,1,202,101,0,0),
	  ($1,30,'2026-10-09','2026-10-09 13:00:30Z',14,4,1,203,104,0,0)`, siteID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update events set props=case session_id when 6 then '{"plan":"pro"}'::jsonb else '{"plan":"free"}'::jsonb end where site_id=$1 and name_id=204`, siteID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update sessions set country=case when session_id=3 then 'US' else 'IN' end, source_id=case when session_id=3 then 301 else 0 end where site_id=$1`, siteID); err != nil {
		t.Fatal(err)
	}

	type definition struct {
		key       string
		siteID    int32
		kind      string
		match     string
		value     string
		threshold int16
	}
	definitions := []definition{
		{key: "blog_scroll", siteID: siteID, kind: "scroll", match: "wildcard", value: "/blog/*", threshold: 75},
		{key: "pricing_exact", siteID: siteID, kind: "scroll", match: "exact", value: "/pricing", threshold: 100},
		{key: "pricing_page", siteID: siteID, kind: "page", match: "prefix", value: "/pricing"},
		{key: "signup", siteID: siteID, kind: "event", match: "exact", value: "Signup"},
		{key: "pricing_prefix", siteID: siteID, kind: "scroll", match: "prefix", value: "/pricing", threshold: 100},
		{key: "pricing_exact_page", siteID: siteID, kind: "page", match: "exact", value: "/pricing"},
		{key: "comments_glob", siteID: siteID, kind: "page", match: "wildcard", value: "/blog/*/comments"},
		{key: "signup_prefix", siteID: siteID, kind: "event", match: "prefix", value: "Signup"},
		{key: "completed_wildcard", siteID: siteID, kind: "event", match: "wildcard", value: "*Completed"},
		{key: "qualified_pro", siteID: siteID, kind: "event", match: "exact", value: "Qualified"},
		{key: "other_site", siteID: otherSiteID, kind: "page", match: "exact", value: "/pricing"},
	}
	ids := make(map[string]int64, len(definitions))
	lookup := make(goalLookup)
	for _, definition := range definitions {
		var id int64
		if err := pool.QueryRow(ctx, `insert into goals(site_id,kind,match,value,name,scroll_threshold) values ($1,$2,$3,$4,$5,nullif($6,0)) returning id`, definition.siteID, definition.kind, definition.match, definition.value, definition.key, definition.threshold).Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids[definition.key] = id
		goal := platformgoals.Goal{
			ID: id, SiteID: definition.siteID, Kind: definition.kind, Match: definition.match,
			Value: definition.value, Name: definition.key, ScrollThreshold: definition.threshold,
		}
		if definition.key == "qualified_pro" {
			goal.PropKey, goal.PropValue = "plan", "pro"
			if _, err := pool.Exec(ctx, `update goals set prop_key='plan', prop_value='pro' where id=$1`, id); err != nil {
				t.Fatal(err)
			}
		}
		lookup[definition.siteID] = append(lookup[definition.siteID], goal)
	}
	if _, err := closer.CloseDay(ctx, pool, []int32{siteID}, fixtureDay); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
insert into sessions
  (site_id,retention_class,session_id,visitor_id,start,last,local_day,entry_path,exit_path,pageviews,events,engagement_ms,bounce,country,source_id)
values ($1,30,8,17,'2026-10-10 10:00Z','2026-10-10 10:01Z','2026-10-10',101,101,1,1,0,false,'US',301)`, siteID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into events
  (site_id,retention_class,local_day,ts,visitor_id,session_id,kind,name_id,path_id,engagement_ms,scroll_pct)
values ($1,30,'2026-10-10','2026-10-10 10:00Z',17,8,0,0,101,0,0),
       ($1,30,'2026-10-10','2026-10-10 10:01Z',17,8,1,202,101,0,0)`, siteID); err != nil {
		t.Fatal(err)
	}
	dictionary := dict.New(pool)
	if err := dictionary.Load(ctx); err != nil {
		t.Fatal(err)
	}
	reg := registry{site: sites.Site{ID: siteID, PublicID: "goalsite0001", TZ: time.UTC, Retention: db.Ret30}}
	return pool, identifiedEngine(pool, dictionary, reg, lookup), ids
}

func goalFilterRequest(id int64) api.QueryRequest {
	filters := []api.Filter{{Dimension: "goal_id", Operator: api.Is, Values: []string{stringInt64(id)}}}
	return api.QueryRequest{
		SiteId: 1, DateRange: ptr(dateRange("2026-10-09", "2026-10-09")),
		Metrics: []api.Metric{api.Visitors, api.Visits, api.Conversions}, Filters: &filters,
	}
}

func stringInt64(value int64) string {
	return fmt.Sprintf("%d", value)
}

func mustGoalQuery(t *testing.T, engine *query.Engine, request api.QueryRequest) api.QueryResponse {
	t.Helper()
	response, err := engine.Query(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}
