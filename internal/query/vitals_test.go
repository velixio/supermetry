package query

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"panel/internal/api"
	"panel/internal/db"
	"panel/internal/dict"
	"panel/internal/sites"
	"panel/internal/testpg"
)

type vitalsRegistry struct{ site sites.Site }

func (r vitalsRegistry) ByPublicID(id string) (sites.Site, bool) {
	return r.site, id == r.site.PublicID
}
func (r vitalsRegistry) Site(id int32) (sites.Site, bool) { return r.site, id == r.site.ID }
func (r vitalsRegistry) All() []sites.Site                { return []sites.Site{r.site} }

func TestEngine_VitalsComputesP75RatingsAndBreakdown(t *testing.T) {
	pool, engine := setupVitals(t, db.Ret30)
	seedVitals(t, pool)
	if err := engine.dict.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	dimension := api.VitalsDimensionPage
	response, err := engine.Vitals(context.Background(), api.VitalsRequest{
		SiteId:    1,
		DateRange: api.DateRange{From: openapi_types.Date{Time: vitalsDay("2026-10-06")}, To: openapi_types.Date{Time: vitalsDay("2026-10-07")}},
		Dimension: &dimension,
	})
	if err != nil {
		t.Fatal(err)
	}

	assertVitalMetric(t, "lcp", response.Overall.Lcp, 3500, 4, .5, .25, .25)
	assertVitalMetric(t, "cls", response.Overall.Cls, 225, 4, .5, .25, .25)
	assertVitalMetric(t, "inp", response.Overall.Inp, 450, 4, .5, .25, .25)
	if len(response.Breakdown) != 2 || response.Breakdown[0].Value != "/" || response.Breakdown[1].Value != "/pricing" {
		t.Fatalf("breakdown = %+v", response.Breakdown)
	}
	assertVitalMetric(t, "page lcp", response.Breakdown[0].Lcp, 1750, 2, 1, 0, 0)

	filters := []api.Filter{{Dimension: "country", Operator: api.Is, Values: []string{"US"}}}
	filtered, err := engine.Vitals(context.Background(), api.VitalsRequest{
		SiteId:    1,
		DateRange: api.DateRange{From: openapi_types.Date{Time: vitalsDay("2026-10-06")}, To: openapi_types.Date{Time: vitalsDay("2026-10-07")}},
		Dimension: &dimension,
		Filters:   &filters,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered.Breakdown) != 1 || filtered.Breakdown[0].Value != "/" {
		t.Fatalf("filtered breakdown = %+v", filtered.Breakdown)
	}
	assertVitalMetric(t, "filtered lcp", filtered.Overall.Lcp, 1750, 2, 1, 0, 0)

	sharedFilters := []api.Filter{
		{Dimension: "entry", Operator: api.Is, Values: []string{"/"}},
		{Dimension: "event", Operator: api.Is, Values: []string{"signup"}},
		{Dimension: "prop:plan", Operator: api.Is, Values: []string{"pro"}},
	}
	shared, err := engine.Vitals(context.Background(), api.VitalsRequest{
		SiteId:    1,
		DateRange: api.DateRange{From: openapi_types.Date{Time: vitalsDay("2026-10-06")}, To: openapi_types.Date{Time: vitalsDay("2026-10-07")}},
		Filters:   &sharedFilters,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertVitalMetric(t, "shared-filter lcp", shared.Overall.Lcp, 1000, 1, 1, 0, 0)
}

func TestEngine_VitalsRejectsRangesOutsideRawRetention(t *testing.T) {
	_, engine := setupVitals(t, db.Ret7)
	_, err := engine.Vitals(context.Background(), api.VitalsRequest{
		SiteId: 1,
		DateRange: api.DateRange{
			From: openapi_types.Date{Time: vitalsDay("2026-09-01")},
			To:   openapi_types.Date{Time: vitalsDay("2026-10-07")},
		},
	})
	if err == nil || err.Error() != "Web Vitals are available only within the site's 7-day raw retention window" {
		t.Fatalf("Vitals() error = %v", err)
	}
}

func TestNormalizeVitalsRejectsInvalidRequests(t *testing.T) {
	site := sites.Site{ID: 1, TZ: time.UTC, Retention: db.Ret30}
	invalidDimension := api.VitalsDimension("source")
	revenueFilters := []api.Filter{{Dimension: "revenue_currency", Operator: api.Is, Values: []string{"USD"}}}
	tests := []struct {
		name string
		req  api.VitalsRequest
		want string
	}{
		{name: "missing date range", req: api.VitalsRequest{SiteId: 1}, want: "date_range.from and date_range.to are required"},
		{name: "unknown dimension", req: api.VitalsRequest{SiteId: 1, DateRange: api.DateRange{From: openapi_types.Date{Time: vitalsDay("2026-10-06")}, To: openapi_types.Date{Time: vitalsDay("2026-10-07")}}, Dimension: &invalidDimension}, want: `unknown dimension "source"`},
		{name: "revenue filter", req: api.VitalsRequest{SiteId: 1, DateRange: api.DateRange{From: openapi_types.Date{Time: vitalsDay("2026-10-06")}, To: openapi_types.Date{Time: vitalsDay("2026-10-07")}}, Filters: &revenueFilters}, want: "filter dimension \"revenue_currency\" is not meaningful for Web Vitals"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := normalizeVitals(tt.req, site, vitalsDay("2026-10-08"))
			if err == nil || err.Error() != tt.want {
				t.Fatalf("normalizeVitals() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestBuildVitalsSQLPrunesHotPartitions(t *testing.T) {
	req := normalizedVitals{
		siteID:         1,
		retentionClass: db.Ret30,
		from:           vitalsDay("2026-10-06"),
		to:             vitalsDay("2026-10-07"),
		fromTimestamp:  time.Date(2026, 10, 5, 18, 30, 0, 0, time.UTC),
		toTimestamp:    time.Date(2026, 10, 7, 18, 30, 0, 0, time.UTC),
	}
	querySQL, args, err := buildVitalsSQL(req, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{"e.retention_class=", "e.ts>=", "e.ts<", "e.local_day between"} {
		if !strings.Contains(querySQL, fragment) {
			t.Errorf("SQL missing %q:\n%s", fragment, querySQL)
		}
	}
	if len(args) != 6 {
		t.Fatalf("args = %#v", args)
	}
}

func setupVitals(t *testing.T, retention db.RetentionClass) (*pgxpool.Pool, *Engine) {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsurePartitions(ctx, pool, vitalsDay("2026-10-01"), vitalsDay("2026-10-10")); err != nil {
		t.Fatal(err)
	}
	dictionary := dict.New(pool)
	registry := vitalsRegistry{site: sites.Site{ID: 1, PublicID: "vitals", TZ: time.UTC, Retention: retention}}
	engine := NewEngine(pool, dictionary, registry)
	engine.now = func() time.Time { return vitalsDay("2026-10-08") }
	return pool, engine
}

func seedVitals(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
insert into dict(kind,id,value) values (0,1,'/'),(0,2,'/pricing'),(7,10,'Web Vitals'),(7,11,'signup');
insert into sessions
  (site_id,retention_class,session_id,visitor_id,start,last,local_day,entry_path,exit_path,pageviews,events,engagement_ms,bounce)
values
  (1,30,1,1,'2026-10-06 10:00Z','2026-10-06 10:00Z','2026-10-06',1,1,1,1,0,true),
  (1,30,2,2,'2026-10-06 11:00Z','2026-10-06 11:00Z','2026-10-06',2,2,1,1,0,true),
  (1,30,3,3,'2026-10-07 10:00Z','2026-10-07 10:00Z','2026-10-07',2,2,1,0,0,true),
  (1,30,4,4,'2026-10-07 11:00Z','2026-10-07 11:00Z','2026-10-07',2,2,1,0,0,true),
  (1,30,5,5,'2026-10-07 12:00Z','2026-10-07 12:00Z','2026-10-07',2,2,1,0,0,true),
  (1,30,6,6,'2026-10-07 13:00Z','2026-10-07 13:00Z','2026-10-07',2,2,1,0,0,true);
insert into events
  (site_id,retention_class,local_day,ts,visitor_id,session_id,kind,name_id,path_id,country,browser_id,device,props)
values
  (1,30,'2026-10-06','2026-10-06 10:00Z',1,1,1,10,1,'US',0,0,'{"lcp":"1000","cls":"50","inp":"100"}'),
  (1,30,'2026-10-06','2026-10-06 11:00Z',2,2,1,10,1,'US',0,1,'{"lcp":"2000","cls":"100","inp":"200"}'),
  (1,30,'2026-10-07','2026-10-07 10:00Z',3,3,1,10,2,'IN',0,0,'{"lcp":"3000","cls":"200","inp":"400"}'),
  (1,30,'2026-10-07','2026-10-07 11:00Z',4,4,1,10,2,'IN',0,1,'{"lcp":"5000","cls":"300","inp":"600"}'),
	(1,30,'2026-10-07','2026-10-07 12:00Z',5,5,1,10,2,'IN',0,1,'{"lcp":"invalid"}'),
	(1,30,'2026-10-07','2026-10-07 13:00Z',6,6,1,10,2,'IN',0,1,'{"lcp":"999999999999999999999999999999"}'),
	(1,30,'2026-10-06','2026-10-06 10:00:01Z',1,1,1,11,1,'US',0,0,'{"plan":"pro"}'),
	(1,30,'2026-10-06','2026-10-06 11:00:01Z',2,2,1,11,2,'US',0,1,'{"plan":"starter"}')`)
	if err != nil {
		t.Fatal(err)
	}
}

func assertVitalMetric(t *testing.T, name string, got api.VitalMetric, p75 float64, samples int64, good, needs, poor float64) {
	t.Helper()
	if got.P75 == nil || math.Abs(*got.P75-p75) > 1e-9 || got.Samples != samples || math.Abs(got.Good-good) > 1e-9 || math.Abs(got.NeedsImprovement-needs) > 1e-9 || math.Abs(got.Poor-poor) > 1e-9 {
		t.Fatalf("%s = %+v", name, got)
	}
}

func vitalsDay(value string) time.Time {
	parsed, _ := time.Parse(time.DateOnly, value)
	return parsed
}
