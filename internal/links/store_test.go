package links

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/db"
	"supermetry/internal/testpg"
)

func newDB(t *testing.T) (*pgxpool.Pool, int32) {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var orgID int64
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Links','links') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	var siteID int32
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class) values ($1,'links0000001','Site','Asia/Kathmandu',30) returning id`, orgID).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	return pool, siteID
}

func TestStoreCRUDAndUniqueSlug(t *testing.T) {
	pool, siteID := newDB(t)
	ctx := context.Background()
	expires := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	limit := int32(10)
	created, err := Create(ctx, pool, Link{SiteID: siteID, Slug: "spring", Name: "Spring", Destination: "https://a.example/x",
		UTM: UTM{Source: "mail", Content: "hero"}, Enabled: true, ExpiresAt: &expires, ClickLimit: &limit})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == 0 || created.UTM.Source != "mail" || created.UTM.Medium != "" || *created.ClickLimit != 10 || !created.ExpiresAt.Equal(expires) {
		t.Fatalf("created = %+v", created)
	}
	if _, err := Create(ctx, pool, Link{SiteID: siteID, Slug: "spring", Name: "Dup", Destination: "https://b.example", Enabled: true}); !errors.Is(err, ErrSlugTaken) {
		t.Fatalf("duplicate slug error = %v", err)
	}
	created.Name, created.Enabled, created.ExpiresAt, created.ClickLimit, created.UTM = "Renamed", false, nil, nil, UTM{}
	updated, err := Update(ctx, pool, created)
	if err != nil || updated.Name != "Renamed" || updated.Enabled || updated.ExpiresAt != nil || updated.ClickLimit != nil || updated.UTM.Source != "" {
		t.Fatalf("updated = %+v, %v", updated, err)
	}
	got, err := BySlug(ctx, pool, "spring")
	if err != nil || got.ID != created.ID {
		t.Fatalf("BySlug = %+v, %v", got, err)
	}
	if _, err := BySlug(ctx, pool, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing slug error = %v", err)
	}
	list, err := List(ctx, pool, siteID)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %v, %v", list, err)
	}
	if err := Delete(ctx, pool, created.ID); err != nil {
		t.Fatal(err)
	}
	if err := Delete(ctx, pool, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete = %v", err)
	}
}

func TestSchemaRejectsUnsafeDestinations(t *testing.T) {
	pool, siteID := newDB(t)
	ctx := context.Background()
	for _, destination := range []string{"javascript:alert(1)", "data:text/html,x", "ftp://a.example", "/relative"} {
		_, err := pool.Exec(ctx, `insert into links(site_id,slug,name,destination) values ($1,'bad-one','x',$2)`, siteID, destination)
		if err == nil {
			t.Errorf("database accepted destination %q", destination)
		}
	}
}

func TestCreateEnforcesPerSiteLimit(t *testing.T) {
	pool, siteID := newDB(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `insert into links(site_id,slug,name,destination) select $1, 'slug' || g, 'n', 'https://a.example' from generate_series(1,$2) g`, siteID, MaxLinksPerSite); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(ctx, pool, Link{SiteID: siteID, Slug: "one-more", Name: "n", Destination: "https://a.example", Enabled: true}); !errors.Is(err, ErrLimit) {
		t.Fatalf("error = %v, want ErrLimit", err)
	}
}

func TestRecorderFlushPersistsBatchesAndStatsMatchFixture(t *testing.T) {
	pool, siteID := newDB(t)
	ctx := context.Background()
	link, err := Create(ctx, pool, Link{SiteID: siteID, Slug: "go", Name: "Go", Destination: "https://a.example", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	recorder := NewRecorder(pool, RecorderOptions{})
	recorder.Views = NewSiteViews()
	recorder.Views.Register(siteID, "spring")
	if _, err := pool.Exec(ctx, `insert into tracking_pixels(site_id,name,tag) values ($1,'Spring','spring')`, siteID); err != nil {
		t.Fatal(err)
	}
	// Hand-computed fixture: 2026-10-10 has 3 clicks, 2026-10-11 has 4, 2026-10-12 has 1.
	for range 2 {
		recorder.Click(link.ID, "2026-10-10", "NP", "news.example", "desktop")
	}
	recorder.Click(link.ID, "2026-10-10", "US", "", "mobile")
	for range 3 {
		recorder.Click(link.ID, "2026-10-11", "NP", "news.example", "mobile")
	}
	recorder.Click(link.ID, "2026-10-11", "", "", "tablet")
	recorder.Click(link.ID, "2026-10-12", "NP", "", "desktop")
	recorder.Impression(siteID, noonOn("2026-10-11"), "spring", Place{Country: "NP"})
	recorder.Impression(siteID, noonOn("2026-10-11"), "spring", Place{Country: "NP"})
	recorder.Impression(siteID, noonOn("2026-10-12"), "", Place{Country: "US"})
	if got := recorder.Pending(link.ID); got != 8 {
		t.Fatalf("pending = %d", got)
	}
	if err := recorder.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if got := recorder.Pending(link.ID); got != 0 {
		t.Fatalf("pending after flush = %d", got)
	}
	// A second flush with new data must add to, not replace, the first.
	recorder.Click(link.ID, "2026-10-12", "NP", "", "desktop")
	if err := recorder.Flush(ctx); err != nil {
		t.Fatal(err)
	}

	stats, err := ClickStats(ctx, pool, link.ID, "2026-10-10", "2026-10-12")
	if err != nil {
		t.Fatal(err)
	}
	if stats.Total != 9 {
		t.Fatalf("total = %d, want 9", stats.Total)
	}
	wantDaily := []DayCount{{"2026-10-10", 3}, {"2026-10-11", 4}, {"2026-10-12", 2}}
	for i, want := range wantDaily {
		if stats.Daily[i] != want {
			t.Fatalf("daily = %v, want %v", stats.Daily, wantDaily)
		}
	}
	if stats.Countries[0] != (Count{"NP", 7}) || len(stats.Countries) != 3 {
		t.Fatalf("countries = %v", stats.Countries)
	}
	if len(stats.Referrers) != 2 || stats.Referrers[0] != (Count{"news.example", 5}) || stats.Referrers[1] != (Count{"", 4}) {
		t.Fatalf("referrers = %v", stats.Referrers)
	}
	devices := map[string]int64{}
	for _, d := range stats.Devices {
		devices[d.Value] = d.Clicks
	}
	if devices["desktop"] != 4 || devices["mobile"] != 4 || devices["tablet"] != 1 {
		t.Fatalf("devices = %v", devices)
	}
	narrow, err := ClickStats(ctx, pool, link.ID, "2026-10-11", "2026-10-11")
	if err != nil || narrow.Total != 4 || len(narrow.Daily) != 1 {
		t.Fatalf("narrow = %+v, %v", narrow, err)
	}
	reloaded, _ := Get(ctx, pool, link.ID)
	if reloaded.Clicks != 9 {
		t.Fatalf("links.clicks = %d, want 9", reloaded.Clicks)
	}
	pixel, err := ImpressionStats(ctx, pool, siteID, "2026-10-10", "2026-10-12", "spring")
	if err != nil || pixel.Total != 2 || len(pixel.Daily) != 1 {
		t.Fatalf("pixel = %+v, %v", pixel, err)
	}
}

func TestFlushSkipsDeletedLinksAndRestoresOnFailure(t *testing.T) {
	pool, siteID := newDB(t)
	ctx := context.Background()
	link, err := Create(ctx, pool, Link{SiteID: siteID, Slug: "gone", Name: "Go", Destination: "https://a.example", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	recorder := NewRecorder(pool, RecorderOptions{})
	recorder.Click(link.ID, "2026-10-10", "NP", "", "desktop")
	if err := Delete(ctx, pool, link.ID); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Flush(ctx); err != nil {
		t.Fatalf("flush for a deleted link must not fail: %v", err)
	}
	var rows int
	_ = pool.QueryRow(ctx, `select count(*) from link_clicks_daily`).Scan(&rows)
	if rows != 0 {
		t.Fatalf("rows for deleted link = %d", rows)
	}

	recorder.Click(link.ID, "2026-10-10", "NP", "", "desktop")
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := recorder.Flush(cancelled); err == nil {
		t.Fatal("flush with a cancelled context should fail")
	}
	if clicks, _ := recorder.snapshot(); len(clicks) != 1 {
		t.Fatalf("failed flush lost the buffered counts: %v", clicks)
	}
}

func TestRecorderBoundsMemoryAndCountsDrops(t *testing.T) {
	recorder := NewRecorder(nil, RecorderOptions{MaxKeys: 3})
	for i := range 10 {
		recorder.Click(1, "2026-10-10", "", string(rune('a'+i)), "desktop")
	}
	recorder.Click(1, "2026-10-10", "", "a", "desktop") // existing key still increments
	clicks, _ := recorder.snapshot()
	if len(clicks) != 3 || recorder.Dropped() != 7 {
		t.Fatalf("keys=%d dropped=%d", len(clicks), recorder.Dropped())
	}
	if clicks[clickKey{1, "2026-10-10", "", "a", "desktop"}] != 2 {
		t.Fatalf("existing key not incremented: %v", clicks)
	}
}

func TestRecorderRunFlushesOnShutdown(t *testing.T) {
	pool, siteID := newDB(t)
	ctx := context.Background()
	link, err := Create(ctx, pool, Link{SiteID: siteID, Slug: "bye", Name: "Go", Destination: "https://a.example", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	recorder := NewRecorder(pool, RecorderOptions{FlushEvery: time.Hour})
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- recorder.Run(runCtx) }()
	recorder.Click(link.ID, "2026-10-10", "NP", "", "desktop")
	stop()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var clicks int64
	_ = pool.QueryRow(ctx, `select clicks from links where id=$1`, link.ID).Scan(&clicks)
	if clicks != 1 {
		t.Fatalf("clicks after shutdown flush = %d", clicks)
	}
}

func TestImpressionSummaryAgainstHandComputedFixture(t *testing.T) {
	pool, siteID := newDB(t)
	ctx := context.Background()
	// today is 2026-10-30: the 7-day window starts 10-24 and the 30-day window 10-01.
	if _, err := pool.Exec(ctx, `insert into pixel_impressions_daily(site_id, day, campaign, impressions) values
		($1,'2026-10-30','a',4), ($1,'2026-10-24','a',3), ($1,'2026-10-23','a',20),
		($1,'2026-10-01','a',7), ($1,'2026-09-30','a',100), ($1,'2026-10-31','a',999)`, siteID); err != nil {
		t.Fatal(err)
	}
	got, err := ImpressionSummaryFor(ctx, pool, siteID, "2026-10-30", "a")
	if err != nil {
		t.Fatal(err)
	}
	if want := (ImpressionSummary{Today: 4, Last7: 7, Last30: 34, Total: 134}); got != want {
		t.Fatalf("summary = %+v, want %+v", got, want)
	}
}

func TestImpressionStatsForOneCampaign(t *testing.T) {
	pool, siteID := newDB(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `insert into tracking_pixels(site_id,name,tag) values ($1,'Spring','spring'),($1,'Summer','summer')`, siteID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into pixel_impressions_daily(site_id, day, campaign, impressions) values
		($1,'2026-10-30','spring',4), ($1,'2026-10-29','spring',3), ($1,'2026-10-30','summer',9),
		($1,'2026-10-01','spring',20)`, siteID); err != nil {
		t.Fatal(err)
	}
	stats, err := ImpressionStats(ctx, pool, siteID, "2026-10-29", "2026-10-30", "spring")
	if err != nil || stats.Total != 7 || len(stats.Daily) != 2 {
		t.Fatalf("spring = %+v, %v", stats, err)
	}
	summary, err := ImpressionSummaryFor(ctx, pool, siteID, "2026-10-30", "spring")
	if err != nil {
		t.Fatal(err)
	}
	if want := (ImpressionSummary{Today: 4, Last7: 7, Last30: 27, Total: 27}); summary != want {
		t.Fatalf("summary = %+v, want %+v", summary, want)
	}
}

func TestSiteViewsReloadsRegisteredTagTotalsAndReconciles(t *testing.T) {
	pool, siteID := newDB(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `insert into tracking_pixels(site_id,name,tag) values ($1,'A','a'),($1,'B','b')`, siteID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into pixel_impressions_daily(site_id, day, campaign, impressions) values
		($1,current_date,'a',4), ($1,current_date,'b',2), ($1,current_date-1,'a',3), ($1,current_date-60,'a',100),
		($1,current_date,'unregistered',9)`, siteID); err != nil {
		t.Fatal(err)
	}
	views := NewSiteViews()
	var today string
	if err := pool.QueryRow(ctx, `select current_date::text`).Scan(&today); err != nil {
		t.Fatal(err)
	}
	views.Register(siteID, "a")
	views.Add(siteID, "a", today, 5) // counted before reload, still unflushed
	views.Add(siteID, "unregistered", today, 20)
	if err := views.Reload(ctx, pool); err != nil {
		t.Fatal(err)
	}
	total, series, ok := views.Snapshot(siteID, "a", today, 3)
	if !ok || total != 107 || series[0] != 0 || series[1] != 3 || series[2] != 5 {
		t.Fatalf("total=%d series=%v ok=%v, want 107 and [0 3 5]", total, series, ok)
	}
	if total, series, ok := views.Snapshot(siteID, "b", today, 1); !ok || total != 2 || series[0] != 2 {
		t.Fatalf("b total=%d series=%v ok=%v", total, series, ok)
	}
	if _, _, ok := views.Snapshot(siteID, "unregistered", today, 1); ok {
		t.Fatal("an arbitrary persisted campaign must not enter the registered cache")
	}
	if _, err := pool.Exec(ctx, `delete from tracking_pixels where site_id=$1 and tag='b'`, siteID); err != nil {
		t.Fatal(err)
	}
	if err := views.Reload(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := views.Snapshot(siteID, "b", today, 1); ok {
		t.Fatal("reload retained a deleted registration")
	}
}

func TestSiteViewsRegisterAndUnregisterBoundAllocation(t *testing.T) {
	views := NewSiteViews()
	views.Add(1, "arbitrary", "2026-10-14", 1)
	if len(views.entries) != 0 {
		t.Fatalf("unregistered add allocated %d entries", len(views.entries))
	}
	views.Register(1, "known")
	views.Add(1, "known", "2026-10-14", 2)
	if total, _, ok := views.Snapshot(1, "known", "2026-10-14", 1); !ok || total != 2 {
		t.Fatalf("registered snapshot total=%d ok=%v", total, ok)
	}
	views.Unregister(1, "known")
	if _, _, ok := views.Snapshot(1, "known", "2026-10-14", 1); ok || len(views.entries) != 0 {
		t.Fatal("unregister retained cache state")
	}
}

// noonOn is a wall-clock time in the site's zone on day.
func noonOn(day string) time.Time {
	at, err := time.Parse(time.DateOnly, day)
	if err != nil {
		panic(err)
	}
	return at.Add(12 * time.Hour)
}

func TestImpressionStatsHourlyForShortRanges(t *testing.T) {
	pool, siteID := newDB(t)
	ctx := context.Background()
	recorder := NewRecorder(pool, RecorderOptions{})
	recorder.Views = NewSiteViews()
	recorder.Views.Register(siteID, "spring")
	if _, err := pool.Exec(ctx, `insert into tracking_pixels(site_id,name,tag) values ($1,'Spring','spring')`, siteID); err != nil {
		t.Fatal(err)
	}
	recorder.Impression(siteID, time.Date(2026, 10, 11, 9, 5, 0, 0, time.UTC), "spring", Place{Country: "NP"})
	recorder.Impression(siteID, time.Date(2026, 10, 11, 9, 59, 59, 0, time.UTC), "spring", Place{Source: SourceGitHub})
	recorder.Impression(siteID, time.Date(2026, 10, 12, 23, 30, 0, 0, time.UTC), "spring", Place{})
	if err := recorder.Flush(ctx); err != nil {
		t.Fatal(err)
	}

	stats, err := ImpressionStats(ctx, pool, siteID, "2026-10-11", "2026-10-12", "spring")
	if err != nil {
		t.Fatal(err)
	}
	want := []HourCount{{Hour: "2026-10-11T09:00", Count: 2}, {Hour: "2026-10-12T23:00", Count: 1}}
	if fmt.Sprint(stats.Hourly) != fmt.Sprint(want) {
		t.Fatalf("hourly = %+v, want %+v", stats.Hourly, want)
	}

	long, err := ImpressionStats(ctx, pool, siteID, "2026-10-01", "2026-10-12", "spring")
	if err != nil {
		t.Fatal(err)
	}
	if long.Hourly == nil || len(long.Hourly) != 0 {
		t.Fatalf("hourly for a 12-day range = %#v, want an empty list", long.Hourly)
	}

	if err := DeleteTrackingPixel(ctx, pool, trackingPixelID(t, pool, siteID, "spring")); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := pool.QueryRow(ctx, `select count(*) from pixel_impressions_hourly where site_id=$1`, siteID).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Fatalf("%d hourly rows survive deleting the pixel", left)
	}
}

func trackingPixelID(t *testing.T, pool *pgxpool.Pool, siteID int32, tag string) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(context.Background(), `select id from tracking_pixels where site_id=$1 and tag=$2`, siteID, tag).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestImpressionStatsBreakDownSourcesCountriesAndCities(t *testing.T) {
	pool, siteID := newDB(t)
	ctx := context.Background()
	recorder := NewRecorder(pool, RecorderOptions{})
	recorder.Views = NewSiteViews()
	recorder.Views.Register(siteID, "readme")
	if _, err := pool.Exec(ctx, `insert into tracking_pixels(site_id,name,tag) values ($1,'README','readme')`, siteID); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		recorder.Impression(siteID, noonOn("2026-10-11"), "readme", Place{Source: SourceGitHub})
	}
	for range 2 {
		recorder.Impression(siteID, noonOn("2026-10-11"), "readme", Place{Country: "NP", City: "Kathmandu", Source: SourceOther})
	}
	recorder.Impression(siteID, noonOn("2026-10-12"), "readme", Place{Country: "NP", City: "Pokhara", Source: SourceOther})
	recorder.Impression(siteID, noonOn("2026-10-12"), "readme", Place{Country: "US", Source: SourceOther})
	recorder.Impression(siteID, noonOn("2026-10-12"), "readme", Place{Source: "whatever"}) // anything but github is other
	if err := recorder.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	stats, err := ImpressionStats(ctx, pool, siteID, "2026-10-11", "2026-10-12", "readme")
	if err != nil {
		t.Fatal(err)
	}
	if stats.Total != 8 {
		t.Fatalf("total = %d, want 8", stats.Total)
	}
	if want := []PlaceCount{{Source: "other", Impressions: 5}, {Source: "github", Impressions: 3}}; fmt.Sprint(stats.Sources) != fmt.Sprint(want) {
		t.Fatalf("sources = %+v, want %+v", stats.Sources, want)
	}
	if want := []PlaceCount{{Country: "NP", Impressions: 3}, {Country: "US", Impressions: 1}}; fmt.Sprint(stats.Countries) != fmt.Sprint(want) {
		t.Fatalf("countries = %+v, want %+v", stats.Countries, want)
	}
	if want := []PlaceCount{{Country: "NP", City: "Kathmandu", Impressions: 2}, {Country: "NP", City: "Pokhara", Impressions: 1}}; fmt.Sprint(stats.Cities) != fmt.Sprint(want) {
		t.Fatalf("cities = %+v, want %+v", stats.Cities, want)
	}
}
