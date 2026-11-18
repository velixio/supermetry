package query_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/api"
	"supermetry/internal/closer"
	"supermetry/internal/db"
	"supermetry/internal/dict"
	"supermetry/internal/privacy"
	"supermetry/internal/query"
	"supermetry/internal/sites"
	"supermetry/internal/testpg"
)

// Installs A..D carry stable visitor ids 1001..1004. Dataset from the phase 3
// plan: days 10-06..10-08 are closed, 10-09 is the open day, and D's hits
// arrived late into the closed day 10-06.
func setupAppIdentity(t *testing.T, kind string) (*pgxpool.Pool, *query.Engine) {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsurePartitions(ctx, pool, day("2026-10-05"), day("2026-10-10")); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
insert into dict(kind,id,value) values (0,1,'/home'),(0,2,'/paywall'),(7,20,'purchase'),(7,21,'$app_open');
insert into orgs(id,name,slug) overriding system value values (1,'Apps','apps');
insert into sites(id,org_id,public_id,name,timezone,retention_class) overriding system value
values (1,1,'appidentity1','App identity','UTC',30);
insert into sessions
  (site_id,retention_class,session_id,visitor_id,start,last,local_day,entry_path,exit_path,pageviews,events,engagement_ms,bounce)
values
  (1,30,1,1001,'2026-10-06 10:00Z','2026-10-06 10:01Z','2026-10-06',1,2,2,0,60000,false),
  (1,30,2,1002,'2026-10-06 11:00Z','2026-10-06 11:00Z','2026-10-06',1,1,1,0,0,true),
  (1,30,3,1004,'2026-10-06 18:00Z','2026-10-06 18:02Z','2026-10-06',1,2,2,1,120000,false),
  (1,30,4,1001,'2026-10-07 10:00Z','2026-10-07 10:02Z','2026-10-07',1,1,1,1,0,false),
  (1,30,5,1003,'2026-10-07 12:00Z','2026-10-07 12:02Z','2026-10-07',1,2,2,1,0,false),
  (1,30,6,1001,'2026-10-08 10:00Z','2026-10-08 10:00Z','2026-10-08',1,1,1,0,0,true),
  (1,30,7,1002,'2026-10-08 11:00Z','2026-10-08 11:00Z','2026-10-08',1,1,1,0,0,true),
  (1,30,8,1001,'2026-10-09 09:00Z','2026-10-09 09:00Z','2026-10-09',1,1,1,0,0,true);
insert into events (site_id,retention_class,local_day,ts,visitor_id,session_id,kind,name_id,path_id) values
  (1,30,'2026-10-06','2026-10-06 10:00Z',1001,1,0,0,1),
  (1,30,'2026-10-06','2026-10-06 10:01Z',1001,1,0,0,2),
  (1,30,'2026-10-06','2026-10-06 11:00Z',1002,2,0,0,1),
  (1,30,'2026-10-06','2026-10-06 18:00Z',1004,3,0,0,1),
  (1,30,'2026-10-06','2026-10-06 18:01Z',1004,3,0,0,2),
  (1,30,'2026-10-06','2026-10-06 18:02Z',1004,3,1,20,0),
  (1,30,'2026-10-07','2026-10-07 10:00Z',1001,4,0,0,1),
  (1,30,'2026-10-07','2026-10-07 10:02Z',1001,4,1,20,0),
  (1,30,'2026-10-07','2026-10-07 12:00Z',1003,5,0,0,1),
  (1,30,'2026-10-07','2026-10-07 12:01Z',1003,5,0,0,2),
  (1,30,'2026-10-07','2026-10-07 12:02Z',1003,5,1,20,0),
  (1,30,'2026-10-08','2026-10-08 10:00Z',1001,6,0,0,1),
  (1,30,'2026-10-08','2026-10-08 11:00Z',1002,7,0,0,1),
  (1,30,'2026-10-09','2026-10-09 09:00Z',1001,8,0,0,1)`); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"2026-10-06", "2026-10-07", "2026-10-08"} {
		if _, err := closer.CloseDay(ctx, pool, []int32{1}, day(value)); err != nil {
			t.Fatal(err)
		}
	}
	dictionary := dict.New(pool)
	if err := dictionary.Load(ctx); err != nil {
		t.Fatal(err)
	}
	reg := registry{site: sites.Site{ID: 1, PublicID: "appidentity1", Kind: kind, TZ: time.UTC, Retention: db.Ret30}}
	engine := identifiedEngine(pool, dictionary, reg)
	engine.SetClock(func() time.Time { return time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC) })
	return pool, engine
}

func appFunnelSteps() []api.FunnelStep {
	return []api.FunnelStep{
		{Type: api.Page, Match: "exact", Value: "/home"},
		{Type: api.Page, Match: "exact", Value: "/paywall"},
		{Type: api.Event, Match: "exact", Value: "purchase"},
	}
}

func TestAppRetentionLinksDaysByInstall(t *testing.T) {
	_, engine := setupAppIdentity(t, sites.KindApp)
	got, err := engine.Retention(context.Background(), api.RetentionRequest{
		SiteId: 1, DateRange: dateRange("2026-10-06", "2026-10-09"), Interval: api.RetentionDay,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.IdentityWindow != api.InstallIdentityWindow || got.RetentionMode != api.InstallReturn {
		t.Fatalf("window=%s mode=%s", got.IdentityWindow, got.RetentionMode)
	}
	var sizes []int64
	var retained [][]int64
	for _, c := range got.Cohorts {
		sizes = append(sizes, c.Size)
		retained = append(retained, c.Retained)
	}
	if !reflect.DeepEqual(sizes, []int64{3, 1}) || !reflect.DeepEqual(retained, [][]int64{{3, 1, 2, 1}, {1, 0, 0, 0}}) {
		t.Fatalf("cohorts = %+v", got.Cohorts)
	}
}

func TestAppRetentionIgnoresProfileIdentity(t *testing.T) {
	_, engine := setupAppIdentity(t, sites.KindApp)
	profile := api.ProfileRetentionIdentity
	got, err := engine.Retention(context.Background(), api.RetentionRequest{
		SiteId: 1, DateRange: dateRange("2026-10-06", "2026-10-09"), Interval: api.RetentionDay, Identity: &profile,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.IdentityWindow != api.InstallIdentityWindow || len(got.Cohorts) != 2 {
		t.Fatalf("response = %+v", got)
	}
}

func TestWebRetentionStaysDailyIdentity(t *testing.T) {
	_, engine := setupAppIdentity(t, sites.KindWeb)
	got, err := engine.Retention(context.Background(), api.RetentionRequest{
		SiteId: 1, DateRange: dateRange("2026-10-06", "2026-10-09"), Interval: api.RetentionDay,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.IdentityWindow != api.DailyIdentityWindow || got.RetentionMode != api.WithinDaySessionReturn {
		t.Fatalf("window=%s mode=%s", got.IdentityWindow, got.RetentionMode)
	}
}

func TestAppFunnelCountsInstalls(t *testing.T) {
	_, engine := setupAppIdentity(t, sites.KindApp)
	for _, tc := range []struct {
		countBy api.FunnelRequestCountBy
		want    []int64
	}{
		{api.FunnelCountByVisitors, []int64{4, 3, 2}},
		{api.FunnelCountBySessions, []int64{8, 3, 2}},
	} {
		countBy := tc.countBy
		got, err := engine.Funnel(context.Background(), api.FunnelRequest{
			SiteId: 1, DateRange: dateRange("2026-10-06", "2026-10-09"), Steps: appFunnelSteps(), CountBy: &countBy,
		})
		if err != nil {
			t.Fatal(err)
		}
		if counts := stepCounts(got); !reflect.DeepEqual(counts, tc.want) {
			t.Fatalf("count_by %s = %v, want %v", countBy, counts, tc.want)
		}
	}
}

func TestWebFunnelByVisitorsStaysDailyIdentity(t *testing.T) {
	_, engine := setupAppIdentity(t, sites.KindWeb)
	countBy := api.FunnelCountByVisitors
	got, err := engine.Funnel(context.Background(), api.FunnelRequest{
		SiteId: 1, DateRange: dateRange("2026-10-06", "2026-10-09"), Steps: appFunnelSteps(), CountBy: &countBy,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Daily identities: A counts once per day on /home (4), B twice, C, D.
	if counts := stepCounts(got); counts[0] != 8 {
		t.Fatalf("web daily identities = %v, want 8 home identities", counts)
	}
}

func TestAppJourneyCountsInstalls(t *testing.T) {
	_, engine := setupAppIdentity(t, sites.KindApp)
	start := "/home"
	got, err := engine.Journey(context.Background(), api.JourneyRequest{
		SiteId: 1, DateRange: dateRange("2026-10-06", "2026-10-09"), Steps: 2, Limit: 5, StartPath: &start,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, link := range got.Links {
		if link.FromPath == "/home" && link.ToPath == "/paywall" {
			if link.Visitors != 3 {
				t.Fatalf("home to paywall installs = %d, want 3 (links %+v)", link.Visitors, got.Links)
			}
			return
		}
	}
	t.Fatalf("no /home to /paywall link in %+v", got.Links)
}

func TestAppVisitorsOverRangeRejected(t *testing.T) {
	_, engine := setupAppIdentity(t, sites.KindApp)
	req := queryRequestRange("2026-10-06", "2026-10-09", nil, nil)
	req.Metrics = []api.Metric{api.Visitors}
	_, err := engine.Query(context.Background(), req)
	if err == nil || err.Error() != "visitors over a range is not unique for apps; use /app/product/active-users" {
		t.Fatalf("err = %v", err)
	}
	single := queryRequestRange("2026-10-06", "2026-10-06", nil, nil)
	single.Metrics = []api.Metric{api.Visitors}
	if _, err := engine.Query(context.Background(), single); err != nil {
		t.Fatalf("single day visitors: %v", err)
	}
	pageviews := queryRequestRange("2026-10-06", "2026-10-09", nil, nil)
	pageviews.Metrics = []api.Metric{api.Pageviews}
	if _, err := engine.Query(context.Background(), pageviews); err != nil {
		t.Fatalf("range pageviews: %v", err)
	}
}

func TestWebVisitorsOverRangeStillAllowed(t *testing.T) {
	_, engine := setupAppIdentity(t, sites.KindWeb)
	req := queryRequestRange("2026-10-06", "2026-10-09", nil, nil)
	req.Metrics = []api.Metric{api.Visitors}
	if _, err := engine.Query(context.Background(), req); err != nil {
		t.Fatal(err)
	}
}

func TestAnonymousAppRejectsFunnelAndRetention(t *testing.T) {
	_, engine := setupAppIdentity(t, sites.KindApp)
	engine.SetTrackingMode(privacy.Anonymous)
	if _, err := engine.Funnel(context.Background(), api.FunnelRequest{
		SiteId: 1, DateRange: dateRange("2026-10-06", "2026-10-09"), Steps: appFunnelSteps(),
	}); !errors.Is(err, api.ErrAnonymousHistory) {
		t.Fatalf("funnel err = %v", err)
	}
	if _, err := engine.Retention(context.Background(), api.RetentionRequest{
		SiteId: 1, DateRange: dateRange("2026-10-06", "2026-10-09"), Interval: api.RetentionDay,
	}); !errors.Is(err, api.ErrAnonymousHistory) {
		t.Fatalf("retention err = %v", err)
	}
}
