package query_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"supermetry/internal/api"
	"supermetry/internal/closer"
	"supermetry/internal/db"
	"supermetry/internal/dict"
	platformgoals "supermetry/internal/platform/goals"
	"supermetry/internal/query"
	"supermetry/internal/sites"
	"supermetry/internal/testpg"
)

// These requests match the Goals UI: goal_id and an explicit currency, without
// an event dimension or a redundant free-form event filter.
func revenueGoalRequest(id int64, from, to, currency string) api.QueryRequest {
	req := queryRequestRange(from, to, nil, []api.Filter{
		filter("goal_id", api.Is, fmt.Sprint(id)), filter("revenue_currency", api.Is, currency),
	})
	req.Metrics = []api.Metric{api.TotalRevenue, api.AverageRevenue}
	req.Timezone = ptr("UTC")
	return req
}

func TestRevenueGoalQueriesAcrossStorageTiers(t *testing.T) {
	engine := setupRevenueGoalFixture(t)
	for _, tt := range []struct {
		name, from, to, currency string
		id                       int64
		total, average           float64
	}{
		{"hot property goal excludes other rows and EUR", "2026-10-08", "2026-10-08", "USD", 1, 10, 5},
		{"closed property goal excludes other rows and EUR", "2026-10-07", "2026-10-07", "USD", 1, 10, 5},
		{"mixed closed and hot", "2026-10-07", "2026-10-08", "USD", 1, 20, 5},
		{"event goal without property", "2026-10-07", "2026-10-08", "USD", 2, 220, 110.0 / 3},
		{"no matching events", "2026-10-07", "2026-10-08", "USD", 3, 0, 0},
		{"empty period", "2026-10-01", "2026-10-02", "USD", 1, 0, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			response, err := engine.Query(context.Background(), revenueGoalRequest(tt.id, tt.from, tt.to, tt.currency))
			if err != nil {
				t.Fatal(err)
			}
			wantSources := []string{"hot"}
			if tt.from == "2026-10-07" {
				wantSources = []string{"closed"}
				if tt.to == "2026-10-08" {
					wantSources = append(wantSources, "hot")
				}
			}
			if !slices.Equal(response.Meta.SourcesUsed, wantSources) {
				t.Fatalf("sources = %v, want %v", response.Meta.SourcesUsed, wantSources)
			}
			if response.Totals["total_revenue"] != tt.total || response.Totals["average_revenue"] != tt.average {
				t.Fatalf("totals = %v, want total=%v average=%v", response.Totals, tt.total, tt.average)
			}
		})
	}
	req := revenueGoalRequest(1, "2026-10-07", "2026-10-08", "USD")
	req.Compare = ptr(api.PreviousPeriod)
	response, err := engine.Query(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if response.Totals["total_revenue"] != 20 || response.Comparison == nil || response.Comparison.Totals["total_revenue"] != 10 || response.Comparison.Totals["average_revenue"] != 5 {
		t.Fatalf("comparison = %+v, totals = %v", response.Comparison, response.Totals)
	}
}

func TestRevenueGoalRejectsUnavailableAndUnsupportedGoals(t *testing.T) {
	engine := setupRevenueGoalFixture(t)
	for _, tt := range []struct {
		name string
		id   int64
		want string
	}{
		{"wrong site", 4, "invalid filter"}, {"nonexistent", 99999, "invalid filter"},
		{"page goal", 5, "revenue metrics require an event goal"}, {"scroll goal", 6, "revenue metrics require an event goal"},
		{"legacy mismatching currency", 7, "legacy goal currency EUR does not match project currency USD"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := engine.Query(context.Background(), revenueGoalRequest(tt.id, "2026-10-07", "2026-10-08", "USD"))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

type revenueGoalLookup []platformgoals.Goal

func (goals revenueGoalLookup) Goals(int32) []platformgoals.Goal { return goals }

func setupRevenueGoalFixture(t *testing.T) *query.Engine {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsurePartitions(ctx, pool, day("2026-10-01"), day("2026-10-10")); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into dict(kind,id,value) values (0,101,'/checkout'),(7,201,'purchase'),(7,202,'other');
 insert into sessions (site_id,retention_class,session_id,visitor_id,start,last,local_day,entry_path,exit_path,pageviews,events,bounce)
 select site,30,site*100+d,site*100+d,make_date(2026,10,d)+time '12:00',make_date(2026,10,d)+time '12:10',make_date(2026,10,d),101,101,0,6,false
 from generate_series(6,8) d cross join generate_series(1,2) site;
 insert into events (site_id,retention_class,local_day,ts,visitor_id,session_id,kind,name_id,path_id,props,revenue_micros,revenue_currency)
 select s.site_id,30,s.local_day,s.start + row_number() over() * interval '1 second',s.visitor_id,s.session_id,1,v.name,101,jsonb_build_object('plan',v.plan),v.amount,v.currency
 from sessions s cross join (values
 (201,'pro',3000000,'USD'),(201,'pro',7000000,'USD'),(201,'pro',0,'USD'),
 (201,'pro',4000000,'EUR'),(201,'basic',100000000,'USD'),(202,'pro',20000000,'USD')
 ) v(name,plan,amount,currency)`); err != nil {
		t.Fatal(err)
	}
	for _, closedDay := range []string{"2026-10-06", "2026-10-07"} {
		if _, err := closer.CloseDay(ctx, pool, []int32{1, 2}, day(closedDay)); err != nil {
			t.Fatal(err)
		}
	}
	dictionary := dict.New(pool)
	if err := dictionary.Load(ctx); err != nil {
		t.Fatal(err)
	}
	goals := revenueGoalLookup{
		{ID: 1, SiteID: 1, Kind: "event", Match: "exact", Value: "purchase", PropKey: "plan", PropValue: "pro", Currency: "USD"},
		{ID: 2, SiteID: 1, Kind: "event", Match: "exact", Value: "purchase", Currency: "USD"},
		{ID: 3, SiteID: 1, Kind: "event", Match: "exact", Value: "absent", Currency: "USD"},
		{ID: 4, SiteID: 2, Kind: "event", Match: "exact", Value: "purchase", Currency: "USD"},
		{ID: 5, SiteID: 1, Kind: "page", Match: "exact", Value: "/checkout", Currency: "USD"},
		{ID: 6, SiteID: 1, Kind: "scroll", Match: "exact", Value: "/checkout", ScrollThreshold: 75, Currency: "USD"},
		{ID: 7, SiteID: 1, Kind: "event", Match: "exact", Value: "purchase", Currency: "EUR"},
	}
	return identifiedEngine(pool, dictionary, registry{site: sites.Site{ID: 1, PublicID: "revenue-test", TZ: time.UTC, Retention: db.Ret30, ProfilesEnabled: true, RevenueCurrency: "USD", RevenueCurrencyState: sites.RevenueCurrencyConfigured}}, goals)
}
