package query

import (
	"context"
	"fmt"
	"math"
	"reflect"
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

	assertVitalMetric(t, "lcp", response.Overall.Lcp, 3000, 5, .6, .2, .2)
	assertVitalMetric(t, "cls", response.Overall.Cls, 200, 5, .6, .2, .2)
	assertVitalMetric(t, "inp", response.Overall.Inp, 400, 5, .6, .2, .2)
	if len(response.Breakdown) != 2 || response.Breakdown[0].Value != "/" || response.Breakdown[1].Value != "/pricing" {
		t.Fatalf("breakdown = %+v", response.Breakdown)
	}
	assertVitalMetric(t, "page lcp", response.Breakdown[0].Lcp, 1750, 3, 1, 0, 0)

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
	assertVitalMetric(t, "filtered lcp", filtered.Overall.Lcp, 1750, 3, 1, 0, 0)

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
	assertVitalMetric(t, "shared-filter lcp", shared.Overall.Lcp, 1375, 2, 1, 0, 0)
}

func TestEngine_VitalsDecodesDiagnosticBreakdowns(t *testing.T) {
	pool, engine := setupVitals(t, db.Ret30)
	seedVitals(t, pool)
	if err := engine.dict.Load(context.Background()); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		dimension api.VitalsDimension
		want      []string
	}{
		{name: "country", dimension: api.VitalsDimensionCountry, want: []string{"US", "IN"}},
		{name: "browser", dimension: api.VitalsDimensionBrowser, want: []string{"Chrome", "Firefox"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response, err := engine.Vitals(context.Background(), api.VitalsRequest{
				SiteId:    1,
				DateRange: api.DateRange{From: openapi_types.Date{Time: vitalsDay("2026-10-06")}, To: openapi_types.Date{Time: vitalsDay("2026-10-07")}},
				Dimension: &tt.dimension,
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(response.Breakdown) != len(tt.want) {
				t.Fatalf("breakdown = %+v, want values %v", response.Breakdown, tt.want)
			}
			for i, want := range tt.want {
				if response.Breakdown[i].Value != want {
					t.Fatalf("breakdown[%d].value = %q, want %q", i, response.Breakdown[i].Value, want)
				}
			}
		})
	}
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

func TestBuildVitalsSQLUsesSetBasedFilteredEvents(t *testing.T) {
	tests := []struct {
		name   string
		filter api.Filter
	}{
		{name: "property", filter: api.Filter{Dimension: "prop:plan", Operator: api.Is, Values: []string{"pro"}}},
		{name: "event", filter: api.Filter{Dimension: "event", Operator: api.Is, Values: []string{"signup"}}},
		{name: "goal", filter: api.Filter{Dimension: "goal", Operator: api.Is, Values: []string{"signup"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			querySQL, _, err := buildVitalsSQL(normalizedVitals{
				siteID: 1, retentionClass: db.Ret30,
				from: vitalsDay("2026-10-06"), to: vitalsDay("2026-10-07"),
				fromTimestamp: vitalsDay("2026-10-06"), toTimestamp: vitalsDay("2026-10-08"),
				filters: []api.Filter{tt.filter},
			}, nil)
			if err != nil {
				t.Fatal(err)
			}
			compact := strings.NewReplacer(" ", "", "\n", "", "\t", "").Replace(strings.ToLower(querySQL))
			if strings.Contains(compact, "select1fromeventscewherece.site_id=e.site_id") {
				t.Fatalf("SQL contains per-vital correlated event access:\n%s", querySQL)
			}
			for _, fragment := range []string{"matched_filter_", "ce.retention_class=", "ce.local_daybetween", "ce.ts>=", "ce.ts<"} {
				if !strings.Contains(compact, fragment) {
					t.Errorf("SQL missing %q:\n%s", fragment, querySQL)
				}
			}
		})
	}
}

func TestEngine_VitalsFilteredQueriesMatchLegacyAcrossTiers(t *testing.T) {
	pool, engine := setupVitals(t, db.Ret30)
	seedVitals(t, pool)
	if err := engine.dict.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		day    string
		filter api.Filter
	}{
		{name: "closed property", day: "2026-10-06", filter: api.Filter{Dimension: "prop:plan", Operator: api.Is, Values: []string{"pro"}}},
		{name: "closed event", day: "2026-10-06", filter: api.Filter{Dimension: "event", Operator: api.Is, Values: []string{"signup"}}},
		{name: "closed goal", day: "2026-10-06", filter: api.Filter{Dimension: "goal", Operator: api.Is, Values: []string{"signup"}}},
		{name: "closed page", day: "2026-10-06", filter: api.Filter{Dimension: "page", Operator: api.Is, Values: []string{"/"}}},
		{name: "hot property crossing midnight", day: "2026-10-07", filter: api.Filter{Dimension: "prop:plan", Operator: api.Is, Values: []string{"pro"}}},
		{name: "hot event crossing midnight", day: "2026-10-07", filter: api.Filter{Dimension: "event", Operator: api.Is, Values: []string{"signup"}}},
		{name: "hot goal crossing midnight", day: "2026-10-07", filter: api.Filter{Dimension: "goal", Operator: api.Is, Values: []string{"signup"}}},
		{name: "hot page crossing midnight", day: "2026-10-07", filter: api.Filter{Dimension: "page", Operator: api.Is, Values: []string{"/"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filters := []api.Filter{tt.filter}
			req := api.VitalsRequest{
				SiteId: 1, DateRange: api.DateRange{From: openapi_types.Date{Time: vitalsDay(tt.day)}, To: openapi_types.Date{Time: vitalsDay(tt.day)}},
				Filters: &filters,
			}
			normalized, err := normalizeVitals(req, engine.registry.(vitalsRegistry).site, engine.now())
			if err != nil {
				t.Fatal(err)
			}
			legacySQL, legacyArgs, err := buildLegacyVitalsSQL(normalized)
			if err != nil {
				t.Fatal(err)
			}
			legacy, err := scanVitalsMetrics(pool.QueryRow(context.Background(), legacySQL, legacyArgs...))
			if err != nil {
				t.Fatal(err)
			}
			got, err := engine.Vitals(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.Overall, legacy) {
				t.Fatalf("new metrics differ from legacy: new=%+v legacy=%+v", got.Overall, legacy)
			}
		})
	}
}

func TestEngine_VitalsFilteredQueryTiming(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping database timing guard in short mode")
	}
	pool, engine := setupVitals(t, db.Ret30)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `
insert into dict(kind,id,value) values (0,1,'/'),(7,10,'Web Vitals'),(7,11,'signup');
insert into sessions
  (site_id,retention_class,session_id,visitor_id,start,last,local_day,entry_path,exit_path,pageviews,events,engagement_ms,bounce)
select 1,30,n,n,'2026-10-07 10:00Z'::timestamptz + n * interval '1 millisecond',
       '2026-10-07 10:01Z'::timestamptz + n * interval '1 millisecond','2026-10-07',1,1,1,6,0,false
from generate_series(1,3000) n;
insert into events
  (site_id,retention_class,local_day,ts,visitor_id,session_id,kind,name_id,path_id,props)
select 1,30,'2026-10-07','2026-10-07 10:00Z'::timestamptz + n * interval '1 millisecond',n,n,1,10,1,
       '{"lcp":"2000","cls":"100","inp":"200"}'::jsonb
from generate_series(1,3000) n;
insert into events
  (site_id,retention_class,local_day,ts,visitor_id,session_id,kind,name_id,path_id,props)
select 1,30,'2026-10-07','2026-10-07 10:00:10Z'::timestamptz + (n * 10 + x) * interval '1 millisecond',n,n,1,11,1,
       jsonb_build_object('plan',case when n <= 1500 then 'pro' else 'starter' end)
from generate_series(1,3000) n cross join generate_series(1,5) x;
insert into events
  (site_id,retention_class,local_day,ts,visitor_id,session_id,kind,name_id,path_id,props)
select 1,30,'2026-10-07','2026-10-07 10:01Z'::timestamptz + n * interval '1 millisecond',n,n,1,11,1,
       jsonb_build_object('plan',case when n <= 1500 then 'pro' else 'starter' end)
from generate_series(1,2000) n;
analyze events;`)
	if err != nil {
		t.Fatal(err)
	}
	filters := []api.Filter{{Dimension: "prop:plan", Operator: api.Is, Values: []string{"pro"}}}
	req := api.VitalsRequest{
		SiteId: 1, DateRange: api.DateRange{From: openapi_types.Date{Time: vitalsDay("2026-10-07")}, To: openapi_types.Date{Time: vitalsDay("2026-10-07")}}, Filters: &filters,
	}
	if _, err := engine.Vitals(ctx, req); err != nil { // warm caches and prepared plans
		t.Fatal(err)
	}
	started := time.Now()
	got, err := engine.Vitals(ctx, req)
	elapsed := time.Since(started)
	if err != nil {
		t.Fatal(err)
	}
	if got.Overall.Lcp.Samples != 1500 {
		t.Fatalf("LCP samples = %d, want 1500", got.Overall.Lcp.Samples)
	}
	if elapsed >= 100*time.Millisecond {
		t.Fatalf("filtered Web Vitals query took %s, want under 100ms", elapsed)
	}
}

// buildLegacyVitalsSQL preserves the former correlated-filter query so the
// set-based query can be checked against its observable results.
func buildLegacyVitalsSQL(req normalizedVitals) (string, []any, error) {
	builder := &sqlBuilder{}
	direct, session, special := splitVitalsFilters(req.filters)
	conditions, err := filterConditions(builder, direct, vitalsDimensionResolver("e"))
	if err != nil {
		return "", nil, err
	}
	sessionConditions, err := filterConditions(builder, session, vitalsSessionDimensionResolver("s"))
	if err != nil {
		return "", nil, err
	}
	if len(sessionConditions) > 0 {
		conditions = append(conditions, "exists (select 1 from sessions s where s.site_id=e.site_id and s.retention_class=e.retention_class and s.session_id=e.session_id"+andSQL(sessionConditions)+")")
	}
	for _, filter := range special {
		switch {
		case filter.Dimension == "event" || filter.Dimension == "goal":
			filter.Dimension = "event"
			matches, matchErr := filterConditions(builder, []api.Filter{filter}, customResolver("ce"))
			if matchErr != nil {
				return "", nil, matchErr
			}
			conditions = append(conditions, "exists (select 1 from events ce where ce.site_id=e.site_id and ce.retention_class=e.retention_class and ce.session_id=e.session_id and ce.kind=1 and "+matches[0]+")")
		case strings.HasPrefix(filter.Dimension, "prop:"):
			key := strings.TrimPrefix(filter.Dimension, "prop:")
			match, matchErr := textMatch(builder, "ce.props ->> "+builder.arg(key), filter)
			if matchErr != nil {
				return "", nil, matchErr
			}
			conditions = append(conditions, "exists (select 1 from events ce where ce.site_id=e.site_id and ce.retention_class=e.retention_class and ce.session_id=e.session_id and ce.kind=1 and ce.props ? "+builder.arg(key)+" and "+match+")")
		}
	}
	siteArg := builder.arg(req.siteID)
	retentionArg := builder.arg(req.retentionClass)
	fromArg := builder.arg(dateParam(req.from))
	toArg := builder.arg(dateParam(req.to))
	fromTimestampArg := builder.arg(req.fromTimestamp)
	toTimestampArg := builder.arg(req.toTimestamp)
	where := "e.site_id=" + siteArg + " and e.retention_class=" + retentionArg +
		" and e.ts>=" + fromTimestampArg + "::timestamptz and e.ts<" + toTimestampArg + "::timestamptz" +
		" and e.local_day between " + fromArg + "::date and " + toArg + "::date" +
		" and e.kind=1 and e.name_id in (select id from dict where kind=" + fmt.Sprint(dict.EventName) + " and value='" + webVitalsEventName + "')" + andSQL(conditions)
	return vitalsAggregateSQL(where), builder.args, nil
}

func vitalsAggregateSQL(where string) string {
	return `with raw_vitals as (
  select e.props->>'lcp' as lcp_raw,e.props->>'cls' as cls_raw,e.props->>'inp' as inp_raw
  from events e where ` + where + `
), parsed_vitals as (
  select case when length(lcp_raw) between 1 and 5 and lcp_raw ~ '^[0-9]+$' then lcp_raw::int end as lcp,
         case when length(cls_raw) between 1 and 5 and cls_raw ~ '^[0-9]+$' then cls_raw::int end as cls,
         case when length(inp_raw) between 1 and 5 and inp_raw ~ '^[0-9]+$' then inp_raw::int end as inp
  from raw_vitals
), vitals as (
  select case when lcp <= 60000 then lcp end as lcp,
         case when cls <= 10000 then cls end as cls,
         case when inp <= 60000 then inp end as inp
  from parsed_vitals
)
select
  percentile_cont(0.75) within group (order by lcp) filter (where lcp is not null)::float8,
  count(lcp)::int8,
  coalesce(avg((lcp <= 2500)::int) filter (where lcp is not null),0)::float8,
  coalesce(avg((lcp > 2500 and lcp <= 4000)::int) filter (where lcp is not null),0)::float8,
  coalesce(avg((lcp > 4000)::int) filter (where lcp is not null),0)::float8,
  percentile_cont(0.75) within group (order by cls) filter (where cls is not null)::float8,
  count(cls)::int8,
  coalesce(avg((cls <= 100)::int) filter (where cls is not null),0)::float8,
  coalesce(avg((cls > 100 and cls <= 250)::int) filter (where cls is not null),0)::float8,
  coalesce(avg((cls > 250)::int) filter (where cls is not null),0)::float8,
  percentile_cont(0.75) within group (order by inp) filter (where inp is not null)::float8,
  count(inp)::int8,
  coalesce(avg((inp <= 200)::int) filter (where inp is not null),0)::float8,
  coalesce(avg((inp > 200 and inp <= 500)::int) filter (where inp is not null),0)::float8,
  coalesce(avg((inp > 500)::int) filter (where inp is not null),0)::float8
from vitals`
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
	engine := identifiedEngine(pool, dictionary, registry)
	engine.now = func() time.Time { return vitalsDay("2026-10-08") }
	return pool, engine
}

func seedVitals(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
insert into dict(kind,id,value) values
  (0,1,'/'),(0,2,'/pricing'),
  (7,10,'Web Vitals'),(7,11,'signup'),
  (9,20,'Chrome'),(9,21,'Firefox');
insert into day_close(site_id,day,status) values (1,'2026-10-06','done'),(1,'2026-10-07','pending');
insert into sessions
  (site_id,retention_class,session_id,visitor_id,start,last,local_day,entry_path,exit_path,pageviews,events,engagement_ms,bounce)
values
  (1,30,1,1,'2026-10-06 10:00Z','2026-10-06 10:00Z','2026-10-06',1,1,1,1,0,true),
  (1,30,2,2,'2026-10-06 11:00Z','2026-10-06 11:00Z','2026-10-06',2,2,1,1,0,true),
  (1,30,3,3,'2026-10-07 10:00Z','2026-10-07 10:00Z','2026-10-07',2,2,1,0,0,true),
  (1,30,4,4,'2026-10-07 11:00Z','2026-10-07 11:00Z','2026-10-07',2,2,1,0,0,true),
  (1,30,5,5,'2026-10-07 12:00Z','2026-10-07 12:00Z','2026-10-07',2,2,1,0,0,true),
  (1,30,6,6,'2026-10-07 13:00Z','2026-10-07 13:00Z','2026-10-07',2,2,1,0,0,true),
  (1,30,7,7,'2026-10-06 23:59Z','2026-10-07 00:01Z','2026-10-06',1,1,1,1,0,true);
insert into events
  (site_id,retention_class,local_day,ts,visitor_id,session_id,kind,name_id,path_id,country,browser_id,device,props)
values
  (1,30,'2026-10-06','2026-10-06 10:00Z',1,1,1,10,1,'US',20,0,'{"lcp":"1000","cls":"50","inp":"100"}'),
  (1,30,'2026-10-06','2026-10-06 11:00Z',2,2,1,10,1,'US',20,1,'{"lcp":"2000","cls":"100","inp":"200"}'),
  (1,30,'2026-10-07','2026-10-07 10:00Z',3,3,1,10,2,'IN',21,0,'{"lcp":"3000","cls":"200","inp":"400"}'),
  (1,30,'2026-10-07','2026-10-07 11:00Z',4,4,1,10,2,'IN',21,1,'{"lcp":"5000","cls":"300","inp":"600"}'),
	(1,30,'2026-10-07','2026-10-07 12:00Z',5,5,1,10,2,'IN',21,1,'{"lcp":"invalid"}'),
	(1,30,'2026-10-07','2026-10-07 13:00Z',6,6,1,10,2,'IN',21,1,'{"lcp":"999999999999999999999999999999"}'),
	(1,30,'2026-10-06','2026-10-06 10:00:01Z',1,1,1,11,1,'US',0,0,'{"plan":"pro"}'),
	(1,30,'2026-10-06','2026-10-06 11:00:01Z',2,2,1,11,2,'US',0,1,'{"plan":"starter"}'),
	(1,30,'2026-10-06','2026-10-06 23:59:30Z',7,7,1,11,1,'US',0,0,'{"plan":"pro"}'),
	(1,30,'2026-10-07','2026-10-07 00:01:00Z',7,7,1,10,1,'US',20,0,'{"lcp":"1500","cls":"75","inp":"150"}')`)
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
