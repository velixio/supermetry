package query_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"supermetry/internal/api"
	"supermetry/internal/db"
	"supermetry/internal/dict"
	"supermetry/internal/privacy"
	"supermetry/internal/query"
	"supermetry/internal/sites"
	"supermetry/internal/testpg"
)

// Fixture (all UTC, site currency USD, retention 30):
//
//	profile 1 "cust-a": sessions s1 google (Oct 1), s2 newsletter/spring (Oct 3), s3 twitter (Oct 5 10:00)
//	  orders on s3: 30 USD (US, pro, /checkout) at 10:05 and 10 USD (US, basic) at 10:20
//	profile 2 "cust-b": session s4 twitter (Oct 5 11:00), order 20 USD (DE, pro) at 11:10
//	anonymous visitor 31: session s5 with no source (Oct 5 12:00), order 5 USD (DE, basic) at 12:10
//
// Totals: 4 orders, 65 USD, average order 16.25, 2 customers (1 repeat, 50%),
// 5 daily visitors, 13 USD per visitor.
func setupRevenueReportFixture(t *testing.T, mode privacy.Mode, extraEUR bool) *query.Engine {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsurePartitions(ctx, pool, day("2026-10-01"), day("2026-10-10")); err != nil {
		t.Fatal(err)
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`insert into orgs(id,name,slug) overriding system value values (1,'Org','org')`)
	exec(`insert into sites(id,org_id,public_id,name,timezone,retention_class) overriding system value values (1,1,'revreport001','Rev','UTC',30)`)
	exec(`insert into dict(kind,id,value) values (0,101,'/checkout'),(2,1,'google'),(2,2,'newsletter'),(2,3,'twitter'),(4,5,'spring')`)
	exec(`insert into profiles(id,site_id,external_id,first_seen_at,last_seen_at) overriding system value
		values (1,1,'cust-a','2026-10-01','2026-10-05'),(2,1,'cust-b','2026-10-05','2026-10-05')`)
	exec(`insert into sessions(site_id,retention_class,session_id,visitor_id,start,last,local_day,source_id,campaign_id) values
		(1,30,1,11,'2026-10-01 10:00+00','2026-10-01 10:10+00','2026-10-01',1,0),
		(1,30,2,12,'2026-10-03 10:00+00','2026-10-03 10:10+00','2026-10-03',2,5),
		(1,30,3,13,'2026-10-05 10:00+00','2026-10-05 10:30+00','2026-10-05',3,0),
		(1,30,4,21,'2026-10-05 11:00+00','2026-10-05 11:30+00','2026-10-05',3,0),
		(1,30,5,31,'2026-10-05 12:00+00','2026-10-05 12:30+00','2026-10-05',0,0)`)
	// Pageviews tie sessions to customers; revenue events are kind 1.
	exec(`insert into events(site_id,retention_class,local_day,ts,visitor_id,session_id,kind,profile_id) values
		(1,30,'2026-10-01','2026-10-01 10:01+00',11,1,0,1),
		(1,30,'2026-10-03','2026-10-03 10:01+00',12,2,0,1),
		(1,30,'2026-10-05','2026-10-05 10:01+00',13,3,0,1),
		(1,30,'2026-10-05','2026-10-05 11:01+00',21,4,0,2)`)
	exec(`insert into events(site_id,retention_class,local_day,ts,visitor_id,session_id,kind,profile_id,country,region_id,path_id,source_id,campaign_id,props,revenue_micros,revenue_currency) values
		(1,30,'2026-10-05','2026-10-05 10:05+00',13,3,1,1,'US',0,101,3,0,'{"product":"pro"}',30000000,'USD'),
		(1,30,'2026-10-05','2026-10-05 10:20+00',13,3,1,1,'US',0,0,3,0,'{"product":"basic"}',10000000,'USD'),
		(1,30,'2026-10-05','2026-10-05 11:10+00',21,4,1,2,'DE',0,101,3,0,'{"product":"pro"}',20000000,'USD'),
		(1,30,'2026-10-05','2026-10-05 12:10+00',31,5,1,null,'DE',0,0,0,0,'{"product":"basic"}',5000000,'USD')`)
	if extraEUR {
		exec(`insert into events(site_id,retention_class,local_day,ts,visitor_id,session_id,kind,country,revenue_micros,revenue_currency)
			values (1,30,'2026-10-05','2026-10-05 12:20+00',31,5,1,'DE',1000000,'EUR')`)
	}
	dictionary := dict.New(pool)
	if err := dictionary.Load(ctx); err != nil {
		t.Fatal(err)
	}
	site := sites.Site{ID: 1, PublicID: "revreport001", TZ: time.UTC, Retention: db.Ret30, ProfilesEnabled: true, RevenueCurrency: "USD"}
	engine := query.NewEngine(pool, dictionary, registry{site: site})
	engine.SetTrackingMode(mode)
	return engine
}

func revenueRequest(dimension, attribution string, lookbackDays int) query.RevenueReportRequest {
	return query.RevenueReportRequest{From: day("2026-10-01"), To: day("2026-10-05"), Dimension: dimension, Attribution: attribution, LookbackDays: lookbackDays}
}

func revenueRows(report api.RevenueReport) map[string]float64 {
	rows := map[string]float64{}
	for _, row := range report.Rows {
		rows[row.Key] = row.Revenue
	}
	return rows
}

func assertRows(t *testing.T, name string, report api.RevenueReport, want map[string]float64) {
	t.Helper()
	got := revenueRows(report)
	if len(got) != len(want) {
		t.Fatalf("%s rows = %v, want %v", name, got, want)
	}
	for key, revenue := range want {
		if got[key] != revenue {
			t.Fatalf("%s rows = %v, want %v", name, got, want)
		}
	}
}

func TestRevenueReportTotalsAndBreakdowns(t *testing.T) {
	engine := setupRevenueReportFixture(t, privacy.Identified, false)
	ctx := context.Background()
	report, err := engine.RevenueReport(ctx, 1, revenueRequest("country", "", 0))
	if err != nil {
		t.Fatal(err)
	}
	if report.Orders != 4 || report.Revenue != 65 || report.AverageOrderValue != 16.25 || report.Visitors != 5 || report.RevenuePerVisitor != 13 {
		t.Fatalf("totals = %+v", report)
	}
	if report.UniqueCustomers == nil || *report.UniqueCustomers != 2 || *report.RepeatCustomers != 1 || *report.RepeatRate != 50 {
		t.Fatalf("customers = %v %v %v", report.UniqueCustomers, report.RepeatCustomers, report.RepeatRate)
	}
	assertRows(t, "country", report, map[string]float64{"US": 40, "DE": 25})
	product, _ := engine.RevenueReport(ctx, 1, revenueRequest("product", "", 0))
	assertRows(t, "product", product, map[string]float64{"pro": 50, "basic": 15})
	page, _ := engine.RevenueReport(ctx, 1, revenueRequest("page", "", 0))
	assertRows(t, "page", page, map[string]float64{"/checkout": 50, "": 15})
}

func TestRevenueReportAttributionAcrossDaysThroughCustomer(t *testing.T) {
	engine := setupRevenueReportFixture(t, privacy.Identified, false)
	ctx := context.Background()
	for _, tt := range []struct {
		name, dimension, model string
		lookback               int
		want                   map[string]float64
	}{
		{"event source", "source", "event", 30, map[string]float64{"twitter": 60, "": 5}},
		{"last click", "source", "last_click", 30, map[string]float64{"twitter": 60, "": 5}},
		{"first click reaches google five days back", "source", "first_click", 30, map[string]float64{"google": 40, "twitter": 20, "": 5}},
		{"first click two-day lookback stays on the order's session", "source", "first_click", 2, map[string]float64{"twitter": 60, "": 5}},
		{"campaign first click inside three days", "campaign", "first_click", 3, map[string]float64{"spring": 40, "": 25}},
	} {
		report, err := engine.RevenueReport(ctx, 1, revenueRequest(tt.dimension, tt.model, tt.lookback))
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		assertRows(t, tt.name, report, tt.want)
	}
}

func TestRevenueReportAnonymousModeHasNoCustomers(t *testing.T) {
	engine := setupRevenueReportFixture(t, privacy.Anonymous, false)
	report, err := engine.RevenueReport(context.Background(), 1, revenueRequest("country", "", 0))
	if err != nil {
		t.Fatal(err)
	}
	if report.Orders != 4 || report.UniqueCustomers != nil || report.RepeatRate != nil {
		t.Fatalf("anonymous report = %+v", report)
	}
	if _, err := engine.RevenueCustomers(context.Background(), 1, day("2026-10-01"), day("2026-10-05"), "", 10); err == nil {
		t.Fatal("customers must be refused in anonymous mode")
	}
}

func TestRevenueReportRefusesMixedCurrencyAndBadInput(t *testing.T) {
	engine := setupRevenueReportFixture(t, privacy.Identified, true)
	ctx := context.Background()
	if _, err := engine.RevenueReport(ctx, 1, revenueRequest("country", "", 0)); !errors.Is(err, query.ErrMixedRevenueCurrency) {
		t.Fatalf("err = %v", err)
	}
	for name, req := range map[string]query.RevenueReportRequest{
		"unknown breakdown": revenueRequest("browser", "", 0),
		"unknown model":     revenueRequest("source", "linear", 0),
		"lookback too long": revenueRequest("source", "first_click", 91),
		"inverted range":    {From: day("2026-10-05"), To: day("2026-10-01"), Dimension: "country"},
	} {
		if _, err := engine.RevenueReport(ctx, 1, req); !errors.Is(err, query.ErrRevenueInput) {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
}

func TestRevenueCustomersSearchAndEscaping(t *testing.T) {
	engine := setupRevenueReportFixture(t, privacy.Identified, false)
	ctx := context.Background()
	all, err := engine.RevenueCustomers(ctx, 1, day("2026-10-01"), day("2026-10-05"), "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[0].ExternalId != "cust-a" || all[0].Orders != 2 || all[0].Revenue != 40 || all[1].Revenue != 20 {
		t.Fatalf("customers = %+v", all)
	}
	one, _ := engine.RevenueCustomers(ctx, 1, day("2026-10-01"), day("2026-10-05"), "CUST-B", 10)
	if len(one) != 1 || one[0].ExternalId != "cust-b" {
		t.Fatalf("search = %+v", one)
	}
	none, _ := engine.RevenueCustomers(ctx, 1, day("2026-10-01"), day("2026-10-05"), "%", 10)
	if len(none) != 0 {
		t.Fatalf("a literal percent must not act as a wildcard: %+v", none)
	}
}
