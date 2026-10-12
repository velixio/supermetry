package writer_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/db"
	"supermetry/internal/privacy"
	"supermetry/internal/session"
	"supermetry/internal/sites"
	"supermetry/internal/testpg"
	"supermetry/internal/writer"
)

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

type revenueRegistry struct{ site sites.Site }

func (r revenueRegistry) Site(id int32) (sites.Site, bool) { return r.site, r.site.ID == id }
func (r revenueRegistry) ByPublicID(id string) (sites.Site, bool) {
	return r.site, r.site.PublicID == id
}
func (r revenueRegistry) All() []sites.Site { return []sites.Site{r.site} }

func TestEnqueueNeverBlocksAndReportsFull(t *testing.T) {
	w := writer.New(nil, session.NewMemory(1, realClock{}), 2, time.Hour, time.Hour)
	if !w.Enqueue(writer.Event{}) || !w.Enqueue(writer.Event{}) {
		t.Fatal("first two events must fit")
	}
	done := make(chan bool)
	go func() { done <- w.Enqueue(writer.Event{}) }()
	select {
	case ok := <-done:
		if ok {
			t.Fatal("third event must be rejected")
		}
	case <-time.After(50 * time.Millisecond):
		t.Fatal("Enqueue blocked")
	}
}

func TestZeroCapacityAlwaysReportsFull(t *testing.T) {
	w := writer.New(nil, session.NewMemory(1, realClock{}), 0, time.Hour, time.Hour)
	if w.Enqueue(writer.Event{}) {
		t.Fatal("zero-capacity writer accepted an event")
	}
}

func TestFlushOnShutdownAndLateEventMarksDirty(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	if err := db.EnsurePartitions(ctx, pool, day.AddDate(0, 0, -1), day.AddDate(0, 0, 2)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into day_close(site_id,day,status,version) values (1,$1,'done',1)`, day); err != nil {
		t.Fatal(err)
	}

	runCtx, cancel := context.WithCancel(context.Background())
	w := writer.New(pool, session.NewMemory(1, realClock{}), 100, time.Hour, time.Hour)
	w.SetTrackingPolicy(privacy.Anonymous, revenueRegistry{site: sites.Site{ID: 1, RevenueCurrency: "USD", RevenueCurrencyState: sites.RevenueCurrencyConfigured}})
	stopped := runWriter(w, runCtx)
	if !w.Enqueue(writer.Event{
		Hit: session.Hit{
			SiteID: 1, Retention: db.Ret30, TS: day.Add(13 * time.Hour), LocalDay: day,
			VisitorID: 1, Kind: 0, PathID: 1,
		},
		SessionID: 9, RevenueMicros: 9_990_000, RevenueCurrency: "USD",
	}) {
		t.Fatal("event rejected")
	}
	cancel()
	waitStopped(t, stopped)

	var events, dirty int
	var revenueMicros int64
	var revenueCurrency string
	if err := pool.QueryRow(context.Background(), `
		select (select count(*) from events),
		       (select count(*) from dirty_day where site_id = 1 and day = $1),
		       revenue_micros, revenue_currency
		from events`, day).Scan(&events, &dirty, &revenueMicros, &revenueCurrency); err != nil {
		t.Fatal(err)
	}
	if events != 1 || dirty != 1 || revenueMicros != 9_990_000 || revenueCurrency != "USD" {
		t.Fatalf("events=%d dirty=%d revenue=%d/%q", events, dirty, revenueMicros, revenueCurrency)
	}
}

func TestCopyEventsUpsertsLatestEventOncePerSite(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	if err := db.EnsurePartitions(ctx, pool, day, day.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	seedWriterSites(t, pool, 1, 2)

	w := writer.New(pool, nil, 10, time.Hour, time.Hour)
	events := []writer.Event{
		testEventForSite(day.Add(4*time.Hour), 1, 1),
		testEventForSite(day.Add(2*time.Hour), 1, 2),
		testEventForSite(day.Add(3*time.Hour), 2, 3),
	}
	if !w.Enqueue(events[0]) || !w.Enqueue(events[1]) || !w.Enqueue(events[2]) {
		t.Fatal("events rejected")
	}
	runCtx, cancel := context.WithCancel(ctx)
	stopped := runWriter(w, runCtx)
	cancel()
	waitStopped(t, stopped)

	assertLastEvent(t, pool, 1, day.Add(4*time.Hour))
	assertLastEvent(t, pool, 2, day.Add(3*time.Hour))

	w = writer.New(pool, nil, 10, time.Hour, time.Hour)
	if !w.Enqueue(testEventForSite(day.Add(time.Hour), 1, 4)) {
		t.Fatal("older event rejected")
	}
	runCtx, cancel = context.WithCancel(ctx)
	stopped = runWriter(w, runCtx)
	cancel()
	waitStopped(t, stopped)
	assertLastEvent(t, pool, 1, day.Add(4*time.Hour))
}

func seedWriterSites(t *testing.T, pool *pgxpool.Pool, ids ...int32) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `insert into orgs(id,name,slug) overriding system value values(1,'Writer','writer') on conflict do nothing`); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if _, err := pool.Exec(ctx, `insert into sites(id,org_id,public_id,name,timezone,retention_class) overriding system value values($1,1,$2,$2,'UTC',30)`, id, fmt.Sprintf("writer%06d", id)); err != nil {
			t.Fatal(err)
		}
	}
}

func testEventForSite(timestamp time.Time, siteID int32, sequence int) writer.Event {
	event := testEvent(timestamp.Truncate(24*time.Hour), sequence)
	event.SiteID = siteID
	event.TS = timestamp
	return event
}

func assertLastEvent(t *testing.T, pool *pgxpool.Pool, siteID int32, want time.Time) {
	t.Helper()
	var got time.Time
	if err := pool.QueryRow(context.Background(), `select last_event_at from site_last_event where site_id=$1`, siteID).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Equal(want) {
		t.Fatalf("site %d last event=%s, want %s", siteID, got, want)
	}
}

func TestPGOutageThenRecoveryNoDoubleCount(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	day := time.Now().UTC().Truncate(24 * time.Hour)
	if err := db.EnsurePartitions(ctx, pool, day.AddDate(0, 0, -1), day.AddDate(0, 0, 2)); err != nil {
		t.Fatal(err)
	}
	bad, err := pgxpool.New(ctx, "postgres://nobody@127.0.0.1:1/x?connect_timeout=1")
	if err != nil {
		t.Fatal(err)
	}
	defer bad.Close()

	runCtx, cancel := context.WithCancel(context.Background())
	w := writer.New(bad, session.NewMemory(1, realClock{}), 10, 10*time.Millisecond, time.Hour)
	stopped := runWriter(w, runCtx)
	for i := range 10 {
		if !w.Enqueue(testEvent(day, i)) {
			t.Fatalf("event %d rejected before capacity was reached", i)
		}
	}
	time.Sleep(100 * time.Millisecond)
	if w.Enqueue(testEvent(day, 99)) {
		t.Fatal("event must be rejected while all capacity is uncommitted")
	}

	w.SetPool(pool)
	eventually(t, func() bool {
		var count int
		return pool.QueryRow(ctx, `select count(*) from events`).Scan(&count) == nil && count == 10
	})
	cancel()
	waitStopped(t, stopped)

	var count int
	if err := pool.QueryRow(ctx, `select count(*) from events`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 10 {
		t.Fatalf("events=%d, want 10", count)
	}
}

func TestDirtySessionsAreUpsertedOnShutdown(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	if err := db.EnsurePartitions(ctx, pool, day.AddDate(0, 0, -1), day.AddDate(0, 0, 2)); err != nil {
		t.Fatal(err)
	}

	sessions := session.NewMemory(1, realClock{})
	hit := session.Hit{
		SiteID: 1, Retention: db.Ret30, TS: day.Add(13 * time.Hour), LocalDay: day,
		VisitorID: 7, Kind: 0, PathID: 42, Country: "IN", Region: 51, City: 52,
	}
	result := sessions.Touch(hit)
	runCtx, cancel := context.WithCancel(context.Background())
	w := writer.New(pool, sessions, 10, time.Hour, time.Hour)
	stopped := runWriter(w, runCtx)
	cancel()
	waitStopped(t, stopped)

	var sessionID int64
	var pageviews int
	var bounce bool
	var regionID, cityID int32
	if err := pool.QueryRow(ctx, `select session_id, pageviews, bounce, region_id, city_id from sessions`).Scan(&sessionID, &pageviews, &bounce, &regionID, &cityID); err != nil {
		t.Fatal(err)
	}
	if sessionID != result.SessionID || pageviews != 1 || !bounce || regionID != 51 || cityID != 52 {
		t.Fatalf("session_id=%d pageviews=%d bounce=%v region=%d city=%d", sessionID, pageviews, bounce, regionID, cityID)
	}
}

func testEvent(day time.Time, i int) writer.Event {
	return writer.Event{
		Hit: session.Hit{
			SiteID: 1, Retention: db.Ret30, TS: day.Add(time.Duration(i) * time.Second), LocalDay: day,
			VisitorID: int64(i + 1), Kind: 0, PathID: int32(i + 1), Country: "IN", Region: 51, City: 52,
		},
		SessionID: int64(i + 1),
		Props:     map[string]string{"index": fmt.Sprint(i)},
	}
}

func runWriter(w *writer.Writer, ctx context.Context) <-chan error {
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	return done
}

func waitStopped(t *testing.T, stopped <-chan error) {
	t.Helper()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("writer stopped with error: %v", err)
		}
	case <-time.After(12 * time.Second):
		t.Fatal("writer did not stop")
	}
}

func eventually(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("condition was not met before deadline")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
