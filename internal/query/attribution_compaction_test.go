package query_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"supermetry/internal/api"
	"supermetry/internal/closer"
	"supermetry/internal/db"
	"supermetry/internal/dict"
	"supermetry/internal/query"
	"supermetry/internal/sites"
	"supermetry/internal/testpg"
)

func TestEventAttributionSurvivesCloseAndRawExpiry(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsurePartitions(ctx, pool, day("2026-10-07"), day("2026-10-09")); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
insert into dict(kind,id,value) values
 (0,1,'/landing'),(2,10,'Acquisition A'),(2,20,'Purchase B'),
 (3,11,'cpc'),(3,21,'email'),(4,12,'landing-a'),(4,22,'upsell-b'),
 (5,13,'entry-content'),(5,23,'purchase-content'),(6,14,'entry-term'),(6,24,'purchase-term'),
 (7,30,'untagged'),(7,31,'purchase'),(7,99,'legacy');
insert into sessions
 (site_id,retention_class,session_id,visitor_id,start,last,local_day,entry_path,exit_path,
  source_id,utm_source_id,medium_id,campaign_id,pageviews,events,bounce)
values
 (1,30,107,1007,'2026-10-07 12:00Z','2026-10-07 12:03Z','2026-10-07',1,1,10,10,11,12,1,2,false),
 (1,30,108,1008,'2026-10-08 12:00Z','2026-10-08 12:03Z','2026-10-08',1,1,10,10,11,12,1,2,false);
insert into events
 (site_id,retention_class,local_day,ts,visitor_id,session_id,kind,name_id,path_id,
  source_id,utm_source_id,medium_id,campaign_id,utm_content_id,utm_term_id,revenue_micros,revenue_currency)
values
 (1,30,'2026-10-07','2026-10-07 12:00Z',1007,107,0,0,1,10,10,11,12,13,14,0,null),
 (1,30,'2026-10-07','2026-10-07 12:01Z',1007,107,1,30,1,0,0,0,0,0,0,0,null),
 (1,30,'2026-10-07','2026-10-07 12:02Z',1007,107,1,31,1,20,20,21,22,23,24,7000000,'USD'),
 (1,30,'2026-10-08','2026-10-08 12:00Z',1008,108,0,0,1,10,10,11,12,13,14,0,null),
 (1,30,'2026-10-08','2026-10-08 12:01Z',1008,108,1,30,1,0,0,0,0,0,0,0,null),
 (1,30,'2026-10-08','2026-10-08 12:02Z',1008,108,1,31,1,20,20,21,22,23,24,11000000,'USD')`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := closer.CloseDay(ctx, pool, []int32{1}, day("2026-10-07")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `insert into custom_closed
 (site_id,day,visitor_id,name_id,path_id,source_id,utm_source_id,medium_id,campaign_id,utm_content_id,utm_term_id,revenue_micros,revenue_currency)
 values(1,'2026-10-07',9999,99,1,10,10,11,12,13,14,5000000,'USD')`); err != nil {
		t.Fatal(err)
	}

	dictionary := dict.New(pool)
	if err := dictionary.Load(ctx); err != nil {
		t.Fatal(err)
	}
	goals := revenueGoalLookup{{ID: 1, SiteID: 1, Kind: "event", Match: "exact", Value: "purchase", Currency: "USD"}}
	engine := identifiedEngine(pool, dictionary, registry{site: sites.Site{ID: 1, PublicID: "attribution", TZ: time.UTC, Retention: db.Ret30, ProfilesEnabled: true, RevenueCurrency: "USD", RevenueCurrencyState: sites.RevenueCurrencyConfigured}}, goals)

	for _, tc := range []struct {
		name, from, to string
		wantRevenue    float64
		coverage       api.QueryEventAttributionCoverage
		legacy         bool
	}{
		{"hot", "2026-10-08", "2026-10-08", 11, api.QueryEventAttributionCoverageExact, false},
		{"closed", "2026-10-07", "2026-10-07", 7, api.QueryEventAttributionCoverageAvailableOnly, true},
		{"mixed", "2026-10-07", "2026-10-08", 18, api.QueryEventAttributionCoverageAvailableOnly, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := revenueGoalRequest(1, tc.from, tc.to, "USD")
			filters := append(*req.Filters,
				filter("utm_source", api.Is, "Purchase B"),
				filter("utm_medium", api.Is, "email"),
			)
			req.Filters = &filters
			dimensions := []string{"source", "channel", "utm_campaign"}
			req.Dimensions = &dimensions
			response := mustQuery(t, engine, req)
			if response.Totals["total_revenue"] != tc.wantRevenue || len(response.Rows) != 1 || response.Rows[0]["source"] != "Purchase B" || response.Rows[0]["channel"] != "Email" || response.Rows[0]["utm_campaign"] != "upsell-b" {
				t.Fatalf("response=%+v", response)
			}
			meta := response.Meta.EventAttribution
			if meta == nil || meta.Basis != api.QueryEventAttributionBasisEvent || meta.Coverage != tc.coverage || meta.LegacyExcluded != tc.legacy {
				t.Fatalf("event attribution metadata=%+v", meta)
			}
		})
	}
	comparisonReq := revenueGoalRequest(1, "2026-10-08", "2026-10-08", "USD")
	comparisonDimensions := []string{"source"}
	comparisonReq.Dimensions = &comparisonDimensions
	comparisonReq.Compare = ptr(api.PreviousPeriod)
	comparisonResponse := mustQuery(t, engine, comparisonReq)
	if comparisonResponse.Meta.EventAttribution == nil || comparisonResponse.Meta.EventAttribution.Coverage != api.QueryEventAttributionCoverageExact || comparisonResponse.Comparison == nil || comparisonResponse.Comparison.EventAttribution == nil || comparisonResponse.Comparison.EventAttribution.Coverage != api.QueryEventAttributionCoverageAvailableOnly {
		t.Fatalf("comparison attribution metadata=%+v comparison=%+v", comparisonResponse.Meta.EventAttribution, comparisonResponse.Comparison)
	}

	// Acquisition remains session-scoped even though the purchase carries B.
	sessionReq := queryRequestRange("2026-10-07", "2026-10-07", []string{"source"}, nil)
	sessionReq.Metrics = []api.Metric{api.Visits}
	sessionResponse := mustQuery(t, engine, sessionReq)
	if len(sessionResponse.Rows) != 1 || sessionResponse.Rows[0]["source"] != "Acquisition A" || sessionResponse.Meta.EventAttribution != nil {
		t.Fatalf("session acquisition changed: %+v", sessionResponse)
	}

	// Legacy rows remain visible to non-attribution history while attribution
	// queries exclude them instead of inventing event tags from session fields.
	unscoped := queryRequestRange("2026-10-07", "2026-10-07", []string{"event"}, nil)
	unscoped.Metrics = []api.Metric{api.Events}
	if response := mustQuery(t, engine, unscoped); len(response.Rows) != 3 || !hasDimensionValue(response.Rows, "event", "legacy") || response.Meta.EventAttribution != nil {
		t.Fatalf("unscoped legacy history=%+v", response)
	}

	if _, err := pool.Exec(ctx, `delete from events where site_id=1 and local_day='2026-10-07'; delete from sessions where site_id=1 and local_day='2026-10-07'`); err != nil {
		t.Fatal(err)
	}
	engine.SetCache(query.NewCache(8))
	closedReq := revenueGoalRequest(1, "2026-10-07", "2026-10-07", "USD")
	dimensions := []string{"source"}
	closedReq.Dimensions = &dimensions
	for pass := range 2 {
		response := mustQuery(t, engine, closedReq)
		if response.Totals["total_revenue"] != 7 || response.Rows[0]["source"] != "Purchase B" || response.Meta.Cached != (pass == 1) || response.Meta.EventAttribution == nil {
			t.Fatalf("closed attribution after raw expiry pass %d=%+v", pass, response)
		}
	}
	if response := mustQuery(t, engine, unscoped); len(response.Rows) != 3 || !hasDimensionValue(response.Rows, "event", "legacy") || response.Meta.EventAttribution != nil {
		t.Fatalf("cache mixed attribution cohorts: %+v", response)
	}

	anonymous := query.NewEngine(pool, dictionary, registry{site: sites.Site{ID: 1, PublicID: "attribution", TZ: time.UTC, Retention: db.Ret30}})
	if _, err := anonymous.Query(ctx, closedReq); !errors.Is(err, api.ErrAnonymousHistory) {
		t.Fatalf("anonymous attribution error=%v", err)
	}
}

func hasDimensionValue(rows []map[string]interface{}, dimension, want string) bool {
	for _, row := range rows {
		if row[dimension] == want {
			return true
		}
	}
	return false
}
