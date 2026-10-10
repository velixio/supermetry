package demo

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"supermetry/internal/alerts"
	"supermetry/internal/api"
	"supermetry/internal/db"
	"supermetry/internal/dict"
	"supermetry/internal/platform/authz"
	platformgoals "supermetry/internal/platform/goals"
	"supermetry/internal/platform/sitesvc"
	"supermetry/internal/platform/store"
	"supermetry/internal/privacy"
	"supermetry/internal/query"
	"supermetry/internal/replay"
	"supermetry/internal/retention"
	"supermetry/internal/testpg"
)

var seedNow = time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)

type seedFixture struct {
	t        *testing.T
	service  *Service
	data     *store.Store
	registry *sitesvc.Registry
	dict     *dict.Dict
	orgID    int64
	owner    authz.Principal
}

func newSeedFixture(t *testing.T) *seedFixture {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var orgID int64
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Demo owner','demo-owner') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	data := &store.Store{Pool: pool}
	registry := sitesvc.NewRegistry(data, 0)
	t.Cleanup(registry.Close)
	dictionary := dict.New(pool)
	if err := dictionary.Load(ctx); err != nil {
		t.Fatal(err)
	}
	owner := authz.Principal{OrgID: orgID, Role: authz.RoleOwner}
	blobs, err := replay.NewDiskStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	index := replay.NewIndexWriter(pool, 5*time.Millisecond, nil)
	indexCtx, stopIndex := context.WithCancel(context.Background())
	indexDone := make(chan error, 1)
	go func() { indexDone <- index.Run(indexCtx) }()
	t.Cleanup(func() {
		stopIndex()
		if err := <-indexDone; err != nil {
			t.Errorf("close replay index: %v", err)
		}
	})
	definitions := platformgoals.New(data, 0)
	t.Cleanup(definitions.Close)
	service := New(Deps{
		Pool: pool, Registry: registry, Dict: dictionary,
		Authz: func(_ context.Context, principal authz.Principal, wantedOrg int64) error {
			if principal.OrgID != wantedOrg || !authz.Can(principal, authz.ActionSiteCreate, 0) {
				return errors.New("forbidden")
			}
			return nil
		},
		Audit: func(context.Context, pgx.Tx, authz.Principal, int64, int32) error { return nil },
		Blobs: blobs, Index: index, Alerts: alerts.New(data, nil, nil), Definitions: definitions,
		Now: func() time.Time { return seedNow }, Seed: 123, Visitors: 40, Window: 3,
	})
	t.Cleanup(service.Close)
	return &seedFixture{t: t, service: service, data: data, registry: registry, dict: dictionary, orgID: orgID, owner: owner}
}

func (f *seedFixture) create(tz string) store.Site {
	f.t.Helper()
	site, err := f.service.Create(context.Background(), f.owner, f.orgID, tz)
	if err != nil {
		f.t.Fatal(err)
	}
	f.service.Wait()
	return site
}

func TestSeedFillsEveryFeature(t *testing.T) {
	f := newSeedFixture(t)
	site := f.create("UTC")
	ctx := context.Background()
	var seededAt *time.Time
	if err := f.data.Pool.QueryRow(ctx, `select demo_seeded_at from sites where id=$1`, site.ID).Scan(&seededAt); err != nil {
		t.Fatal(err)
	}
	if seededAt == nil {
		t.Fatal("demo_seeded_at is null")
	}

	engine := query.NewEngine(f.data.Pool, f.dict, f.registry)
	engine.SetTrackingMode(privacy.Identified)
	for _, dateRange := range []api.DateRange{
		demoRange("2026-10-08", "2026-10-08"),
		demoRange("2026-10-08", "2026-10-10"),
	} {
		assertSeedRange(t, ctx, engine, site.ID, dateRange)
	}
	today := demoRange("2026-10-10", "2026-10-10")
	todaySummary, err := engine.Query(ctx, api.QueryRequest{SiteId: site.ID, DateRange: &today, Metrics: []api.Metric{api.Visitors, api.Visits, api.Pageviews}})
	if err != nil || todaySummary.Totals[string(api.Visits)] <= 0 {
		t.Fatalf("today summary totals = %#v, err=%v", todaySummary.Totals, err)
	}
	for _, dimension := range []string{"page", "source"} {
		dimensions := []string{dimension}
		result, queryErr := engine.Query(ctx, api.QueryRequest{SiteId: site.ID, DateRange: &today, Metrics: []api.Metric{api.Visitors}, Dimensions: &dimensions})
		if queryErr != nil || len(result.Rows) == 0 {
			t.Errorf("today dimension %s rows=%d err=%v", dimension, len(result.Rows), queryErr)
		}
	}
	todayEvents, err := engine.Events(ctx, api.EventExploreRequest{SiteID: site.ID, From: today.From.Time, To: today.To.Time, Limit: 100})
	if err != nil || len(todayEvents.Data) == 0 {
		t.Fatalf("today custom events = %d, err=%v", len(todayEvents.Data), err)
	}

	events, err := engine.Events(ctx, api.EventExploreRequest{SiteID: site.ID, From: demoDay("2026-10-08"), To: demoDay("2026-10-10"), Limit: 100})
	if err != nil || len(events.Data) == 0 {
		t.Fatalf("custom events = %d, err=%v", len(events.Data), err)
	}
	props, err := engine.Properties(ctx, api.PropertyExploreRequest{SiteID: site.ID, From: demoDay("2026-10-08"), To: demoDay("2026-10-10")})
	if err != nil || len(props) == 0 {
		t.Fatalf("properties = %d, err=%v", len(props), err)
	}
	vitals, err := engine.Vitals(ctx, api.VitalsRequest{SiteId: site.ID, DateRange: demoRange("2026-10-08", "2026-10-10")})
	if err != nil || vitals.Overall.Lcp.Samples == 0 || vitals.Overall.Cls.Samples == 0 || vitals.Overall.Inp.Samples == 0 {
		t.Fatalf("vitals = %#v, err=%v", vitals.Overall, err)
	}
	journey, err := engine.Journey(ctx, api.JourneyRequest{SiteId: site.ID, DateRange: demoRange("2026-10-08", "2026-10-10"), Steps: 3, Limit: 10})
	if err != nil || len(journey.Nodes) == 0 {
		t.Fatalf("journey nodes = %d, err=%v", len(journey.Nodes), err)
	}
	retention, err := engine.Retention(ctx, api.RetentionRequest{SiteId: site.ID, DateRange: demoRange("2026-10-08", "2026-10-10"), Interval: api.RetentionDay})
	if err != nil || len(retention.Cohorts) == 0 {
		t.Fatalf("retention cohorts = %d, err=%v", len(retention.Cohorts), err)
	}
	limit := api.Limit(10)
	sessions, err := engine.Sessions(ctx, site.ID, api.ListSessionsParams{From: openapi_types.Date{Time: demoDay("2026-10-08")}, To: openapi_types.Date{Time: demoDay("2026-10-10")}, Limit: &limit}, nil)
	if err != nil || len(sessions.Data) == 0 {
		t.Fatalf("sessions = %d, err=%v", len(sessions.Data), err)
	}
	var profiles int
	if err := f.data.Pool.QueryRow(ctx, `select count(*) from profiles where site_id=$1`, site.ID).Scan(&profiles); err != nil || profiles == 0 {
		t.Fatalf("profiles = %d, err=%v", profiles, err)
	}
}

func TestCreateRetriesSeedFailureAndMarksSiteFailed(t *testing.T) {
	f := newSeedFixture(t)
	wantErr := errors.New("seed step failed")
	var attempts atomic.Int32
	var delays []time.Duration
	f.service.deps.SeedStep = func(context.Context, int32) error {
		attempts.Add(1)
		return wantErr
	}
	f.service.deps.RetryWait = func(_ context.Context, delay time.Duration) error {
		delays = append(delays, delay)
		return nil
	}

	site := f.create("UTC")
	if got := attempts.Load(); got != 4 {
		t.Fatalf("seed attempts = %d, want 4", got)
	}
	if want := []time.Duration{2 * time.Second, 10 * time.Second, 30 * time.Second}; !reflect.DeepEqual(delays, want) {
		t.Fatalf("retry delays = %v, want %v", delays, want)
	}
	var failed bool
	if err := f.data.Pool.QueryRow(context.Background(), `select settings ? 'demo_seed_failed_at' from sites where id=$1`, site.ID).Scan(&failed); err != nil {
		t.Fatal(err)
	}
	if !failed {
		t.Fatal("demo_seed_failed_at was not set")
	}
}

func TestCreateStopsWhenSiteIsDeletedMidSeed(t *testing.T) {
	f := newSeedFixture(t)
	var retryWaits atomic.Int32
	if _, err := f.data.Pool.Exec(context.Background(), `
		create function delete_demo_during_seed() returns trigger language plpgsql as $$
		begin
			update sites set deleted_at=now() where id=new.id;
			insert into site_drop_daily(site_id,day,reason,count) values (new.id,'2099-01-01','privacy_signal',1);
			return null;
		end $$;
		create trigger delete_demo_during_seed
		after update of demo_live_through on sites
		for each row execute function delete_demo_during_seed()`); err != nil {
		t.Fatal(err)
	}
	f.service.deps.RetryWait = func(context.Context, time.Duration) error {
		retryWaits.Add(1)
		return nil
	}

	site, err := f.service.Create(context.Background(), f.owner, f.orgID, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	f.service.Wait()
	var sentinel int
	if err := f.data.Pool.QueryRow(context.Background(), `select count(*) from site_drop_daily where site_id=$1 and day='2099-01-01'`, site.ID).Scan(&sentinel); err != nil {
		t.Fatal(err)
	}
	if got := retryWaits.Load(); got != 0 {
		t.Fatalf("retry waits = %d, want 0", got)
	}
	if sentinel != 1 {
		t.Fatalf("sentinel rows = %d, want 1 (no partial erase)", sentinel)
	}
}

func assertSeedRange(t *testing.T, ctx context.Context, engine *query.Engine, siteID int32, dateRange api.DateRange) {
	t.Helper()
	metrics := []api.Metric{api.Visitors, api.Visits, api.Pageviews}
	response, err := engine.Query(ctx, api.QueryRequest{SiteId: siteID, DateRange: &dateRange, Metrics: metrics})
	if err != nil || response.Totals[string(api.Visits)] <= 0 {
		t.Fatalf("summary totals = %#v, err=%v", response.Totals, err)
	}
	for _, dimension := range []string{
		"time", "page", "entry", "exit", "source", "channel", "utm_source", "utm_medium", "utm_campaign",
		"country", "region", "city", "device", "browser", "browser_version", "os", "os_version", "language", "hostname", "screen_size",
	} {
		dimensions := []string{dimension}
		result, queryErr := engine.Query(ctx, api.QueryRequest{SiteId: siteID, DateRange: &dateRange, Metrics: metrics, Dimensions: &dimensions})
		if queryErr != nil || len(result.Rows) == 0 {
			t.Errorf("dimension %s rows=%d err=%v", dimension, len(result.Rows), queryErr)
		}
	}
	for _, dimension := range []string{"utm_content", "utm_term"} {
		values, suggestionErr := engine.Suggestions(ctx, api.SuggestionExploreRequest{
			SiteID: siteID, Dimension: dimension, From: dateRange.From.Time, To: dateRange.To.Time,
		})
		if suggestionErr != nil || len(values) == 0 {
			t.Errorf("dimension %s suggestions=%d err=%v", dimension, len(values), suggestionErr)
		}
	}
	revenue, err := engine.Query(ctx, api.QueryRequest{SiteId: siteID, DateRange: &dateRange, Metrics: []api.Metric{api.TotalRevenue}})
	if err != nil || revenue.Totals[string(api.TotalRevenue)] <= 0 {
		t.Errorf("revenue totals=%#v err=%v", revenue.Totals, err)
	}
}

func TestSeedKathmandu(t *testing.T) {
	f := newSeedFixture(t)
	site := f.create("Asia/Kathmandu")
	ctx := context.Background()
	rows, err := f.data.Pool.Query(ctx, `select day::text,status from day_close where site_id=$1 order by day`, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var days []string
	for rows.Next() {
		var day, status string
		if err := rows.Scan(&day, &status); err != nil {
			t.Fatal(err)
		}
		if status != "done" {
			t.Fatalf("day %s status = %s", day, status)
		}
		days = append(days, day)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(days) != 2 || days[0] != "2026-10-08" || days[1] != "2026-10-09" {
		t.Fatalf("closed days = %v", days)
	}
	engine := query.NewEngine(f.data.Pool, f.dict, f.registry)
	engine.SetTrackingMode(privacy.Identified)
	dimensions := []string{"time"}
	result, err := engine.Query(ctx, api.QueryRequest{SiteId: site.ID, DateRange: ptrRange(demoRange("2026-10-08", "2026-10-10")), Metrics: []api.Metric{api.Visits}, Dimensions: &dimensions})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Rows) != 3 {
		t.Fatalf("summary day rows = %#v", result.Rows)
	}
	for i, want := range []string{"2026-10-08", "2026-10-09", "2026-10-10"} {
		if got := result.Rows[i]["time"]; got != want {
			t.Errorf("summary day %d = %v, want %s", i, got, want)
		}
	}
}

func TestSeedUsesKolkataLocalDateWhenUTCDateIsBehind(t *testing.T) {
	f := newSeedFixture(t)
	f.service.deps.Now = func() time.Time { return time.Date(2026, 10, 10, 20, 0, 0, 0, time.UTC) }
	site := f.create("Asia/Kolkata")
	ctx := context.Background()

	rows, err := f.data.Pool.Query(ctx, `select day::text from day_close where site_id=$1 and status='done' order by day`, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var closed []string
	for rows.Next() {
		var day string
		if err := rows.Scan(&day); err != nil {
			t.Fatal(err)
		}
		closed = append(closed, day)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if want := []string{"2026-10-09", "2026-10-10"}; !reflect.DeepEqual(closed, want) {
		t.Fatalf("closed days = %v, want %v", closed, want)
	}

	engine := query.NewEngine(f.data.Pool, f.dict, f.registry)
	engine.SetTrackingMode(privacy.Identified)
	dimensions := []string{"time"}
	dateRange := demoRange("2026-10-09", "2026-10-11")
	result, err := engine.Query(ctx, api.QueryRequest{SiteId: site.ID, DateRange: &dateRange, Metrics: []api.Metric{api.Visits}, Dimensions: &dimensions})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Rows) != 3 || result.Rows[2]["time"] != "2026-10-11" {
		t.Fatalf("summary local day rows = %#v, want through 2026-10-11", result.Rows)
	}
}

func TestResumeUnseededReseeds(t *testing.T) {
	f := newSeedFixture(t)
	site := f.create("UTC")
	ctx := context.Background()
	var wantEvents, wantProfiles int64
	if err := f.data.Pool.QueryRow(ctx, `select count(*) from events where site_id=$1`, site.ID).Scan(&wantEvents); err != nil {
		t.Fatal(err)
	}
	if err := f.data.Pool.QueryRow(ctx, `select count(*) from profiles where site_id=$1`, site.ID).Scan(&wantProfiles); err != nil {
		t.Fatal(err)
	}
	if _, err := f.data.Pool.Exec(ctx, `insert into profiles(site_id,external_id,first_seen_at,last_seen_at) values ($1,'stale-profile','2000-01-01','2000-01-01')`, site.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.data.Pool.Exec(ctx, `insert into profile_tombstones(site_id,external_hash,erased_at) values ($1,decode(repeat('ab',32),'hex'),now())`, site.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.data.Pool.Exec(ctx, `update sites set demo_seeded_at=null,demo_live_through=null where id=$1`, site.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.service.ResumeUnseeded(ctx); err != nil {
		t.Fatal(err)
	}
	var events, sessions, distinctSessions int64
	if err := f.data.Pool.QueryRow(ctx, `select count(*),count(distinct session_id) from events where site_id=$1`, site.ID).Scan(&events, &distinctSessions); err != nil {
		t.Fatal(err)
	}
	if err := f.data.Pool.QueryRow(ctx, `select count(*) from sessions where site_id=$1`, site.ID).Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if events != wantEvents || sessions != distinctSessions {
		t.Fatalf("events=%d want=%d sessions=%d distinct=%d", events, wantEvents, sessions, distinctSessions)
	}
	var profiles, stale, tombstones int64
	windowStart := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	if err := f.data.Pool.QueryRow(ctx, `select count(*),count(*) filter (where first_seen_at < $2) from profiles where site_id=$1`, site.ID, windowStart).Scan(&profiles, &stale); err != nil {
		t.Fatal(err)
	}
	if err := f.data.Pool.QueryRow(ctx, `select count(*) from profile_tombstones where site_id=$1`, site.ID).Scan(&tombstones); err != nil {
		t.Fatal(err)
	}
	if profiles != wantProfiles || stale != 0 || tombstones != 0 {
		t.Fatalf("profiles=%d want=%d stale=%d tombstones=%d", profiles, wantProfiles, stale, tombstones)
	}
}

func TestResumeUnseededSkipsRunningSeed(t *testing.T) {
	f := newSeedFixture(t)
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	f.service.deps.SeedStep = func(ctx context.Context, _ int32) error {
		if calls.Add(1) == 1 {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}
	site, err := f.service.Create(context.Background(), f.owner, f.orgID, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if _, err := f.data.Pool.Exec(context.Background(), `insert into site_drop_daily(site_id,day,reason,count) values ($1,'2099-01-01','privacy_signal',1)`, site.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.service.ResumeUnseeded(context.Background()); err != nil {
		t.Fatal(err)
	}
	close(release)
	f.service.Wait()
	var sentinel int
	if err := f.data.Pool.QueryRow(context.Background(), `select count(*) from site_drop_daily where site_id=$1 and day='2099-01-01'`, site.ID).Scan(&sentinel); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || sentinel != 1 {
		t.Fatalf("seed calls=%d sentinel rows=%d, want one seed and no erase", calls.Load(), sentinel)
	}
}

func TestResumeAfterMidSeedCancellationMatchesCleanSeed(t *testing.T) {
	f := newSeedFixture(t)
	site := f.create("UTC")
	ctx := context.Background()
	var wantEvents, wantSessions int
	if err := f.data.Pool.QueryRow(ctx, `select count(*) from events where site_id=$1`, site.ID).Scan(&wantEvents); err != nil {
		t.Fatal(err)
	}
	if err := f.data.Pool.QueryRow(ctx, `select count(*) from sessions where site_id=$1`, site.ID).Scan(&wantSessions); err != nil {
		t.Fatal(err)
	}
	if _, err := f.data.Pool.Exec(ctx, `update sites set demo_seeded_at=null,demo_live_through=null where id=$1`, site.ID); err != nil {
		t.Fatal(err)
	}
	if err := retention.Erase(ctx, f.data.Pool, site.ID, 10_000, 0); err != nil {
		t.Fatal(err)
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	f.service.deps.SeedVisit = func(_ int, total int) {
		if total > 1 {
			cancel()
		}
	}
	if err := f.service.SeedNow(cancelCtx, site.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("interrupted SeedNow error = %v, want context canceled", err)
	}
	f.service.deps.SeedVisit = nil
	if err := f.service.ResumeUnseeded(ctx); err != nil {
		t.Fatal(err)
	}
	var events, sessions int
	if err := f.data.Pool.QueryRow(ctx, `select count(*) from events where site_id=$1`, site.ID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if err := f.data.Pool.QueryRow(ctx, `select count(*) from sessions where site_id=$1`, site.ID).Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if events != wantEvents || sessions != wantSessions {
		t.Fatalf("events=%d want=%d sessions=%d want=%d", events, wantEvents, sessions, wantSessions)
	}
}

func TestCreateTwiceConflicts(t *testing.T) {
	f := newSeedFixture(t)
	_ = f.create("UTC")
	_, err := f.service.Create(context.Background(), f.owner, f.orgID, "UTC")
	if !errors.Is(err, store.ErrDemoExists) {
		t.Fatalf("second Create error = %v, want ErrDemoExists", err)
	}
}

func demoDay(value string) time.Time {
	day, err := time.Parse(time.DateOnly, value)
	if err != nil {
		panic(err)
	}
	return day
}

func demoRange(from, to string) api.DateRange {
	return api.DateRange{From: openapi_types.Date{Time: demoDay(from)}, To: openapi_types.Date{Time: demoDay(to)}}
}

func ptrRange(value api.DateRange) *api.DateRange { return &value }
