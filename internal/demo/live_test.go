package demo

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"supermetry/internal/platform/authz"
	"supermetry/internal/platform/store"
	"supermetry/internal/privacy"
	"supermetry/internal/realtime"
	"supermetry/internal/session"
	"supermetry/internal/sites"
	"supermetry/internal/synth"
	"supermetry/internal/writer"
)

type demoClock struct {
	mu  sync.RWMutex
	now time.Time
}

func (c *demoClock) Now() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.now
}

func (c *demoClock) Set(now time.Time) {
	c.mu.Lock()
	c.now = now
	c.mu.Unlock()
}

type liveFixture struct {
	*seedFixture
	clock    *demoClock
	sessions *session.Memory
	stop     context.CancelFunc
	done     chan error
}

func newLiveFixture(t *testing.T, now time.Time) *liveFixture {
	t.Helper()
	seed := newSeedFixture(t)
	clock := &demoClock{now: now}
	sessions := session.NewMemory(4, clock)
	liveWriter := writer.New(seed.data.Pool, sessions, demoQueueSize, 5*time.Millisecond, 5*time.Millisecond)
	liveWriter.SetClock(clock)
	liveWriter.SetTrackingPolicy(privacy.Identified, seed.registry)
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- liveWriter.Run(ctx) }()

	seed.service.deps.Now = clock.Now
	seed.service.deps.Sessions = sessions
	seed.service.deps.Activity = sessions
	seed.service.deps.Writer = liveWriter
	t.Cleanup(func() {
		seed.service.Close()
		stop()
		if err := <-done; err != nil {
			t.Errorf("close live writer: %v", err)
		}
	})
	return &liveFixture{seedFixture: seed, clock: clock, sessions: sessions, stop: stop, done: done}
}

func TestTickShowsRealtime(t *testing.T) {
	day := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	model := synth.NewDemo(synth.DemoConfig{Seed: 123, TZ: time.UTC, Visitors: 40, Anchor: day})
	visits := model.Visits(day, day.AddDate(0, 0, 1))
	if len(visits) == 0 {
		t.Fatal("demo model returned no visits")
	}
	start := visits[len(visits)/2].Start.Add(-15 * time.Minute)
	f := newLiveFixture(t, start)
	site := f.create("UTC")
	if err := f.data.Pool.QueryRow(context.Background(), `update sites set demo_viewed_at=$2 where id=$1 returning id`, site.ID, start).Scan(new(int32)); err != nil {
		t.Fatal(err)
	}

	f.clock.Set(start.Add(15*time.Minute + time.Microsecond))
	if err := f.service.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	now := f.clock.Now()
	snapshot := realtime.RichSnapshot(f.sessions, site.ID, now)
	if snapshot.ActiveVisitors == 0 {
		t.Fatal("realtime active visitors = 0")
	}
	var liveThrough time.Time
	if err := f.data.Pool.QueryRow(context.Background(), `select demo_live_through from sites where id=$1`, site.ID).Scan(&liveThrough); err != nil {
		t.Fatal(err)
	}
	if !liveThrough.Equal(now) {
		t.Fatalf("demo_live_through = %s, want %s", liveThrough, now)
	}
}

func TestTickGapBackfillOnce(t *testing.T) {
	start := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	f := newLiveFixture(t, start)
	site := f.create("UTC")
	ctx := context.Background()
	before := eventCount(t, f, site.ID)

	f.clock.Set(start.Add(50 * time.Hour))
	now := f.clock.Now()
	if _, err := f.data.Pool.Exec(ctx, `update sites set demo_viewed_at=$2 where id=$1`, site.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := f.service.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	wantAdded := generatedHitCount(f.service, site.ID, start, now)
	waitForEventCount(t, f, site.ID, before+wantAdded)
	if err := f.service.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	waitForEventCount(t, f, site.ID, before+wantAdded)

	var closedDays int
	if err := f.data.Pool.QueryRow(ctx, `
		select count(distinct day) from day_close
		where site_id=$1 and day in ('2026-10-10'::date,'2026-10-11'::date) and status='done'`, site.ID).Scan(&closedDays); err != nil {
		t.Fatal(err)
	}
	if closedDays != 2 {
		t.Fatalf("newly closed days = %d, want 2", closedDays)
	}
	var sessions, distinctSessions int
	if err := f.data.Pool.QueryRow(ctx, `
		select count(*),count(distinct session_id) from sessions where site_id=$1`, site.ID).Scan(&sessions, &distinctSessions); err != nil {
		t.Fatal(err)
	}
	if sessions != distinctSessions {
		t.Fatalf("sessions = %d, distinct session ids = %d", sessions, distinctSessions)
	}
}

func TestTickDoesNotDuplicateAfterLiveThroughUpdateFailure(t *testing.T) {
	start := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	f := newLiveFixture(t, start)
	site := f.create("UTC")
	ctx := context.Background()
	before := eventCount(t, f, site.ID)
	now := start.Add(20 * time.Minute)
	if _, err := f.data.Pool.Exec(ctx, `update sites set demo_viewed_at=$2 where id=$1`, site.ID, now); err != nil {
		t.Fatal(err)
	}
	f.clock.Set(now)
	wantErr := errors.New("live cursor unavailable")
	failed := false
	f.service.deps.SetLiveThrough = func(ctx context.Context, siteID int32, at time.Time) error {
		if !failed {
			failed = true
			return wantErr
		}
		return store.SetDemoLiveThrough(ctx, f.data.Pool, siteID, at)
	}
	if err := f.service.Tick(ctx); !errors.Is(err, wantErr) {
		t.Fatalf("first Tick error = %v, want cursor failure", err)
	}
	wantAdded := generatedHitCount(f.service, site.ID, start, now)
	waitForEventCount(t, f, site.ID, before+wantAdded)
	if err := f.service.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	waitForEventCount(t, f, site.ID, before+wantAdded)
}

func TestTickContinuesAfterOneSiteFails(t *testing.T) {
	start := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	f := newLiveFixture(t, start)
	first := f.create("UTC")
	var secondOrg int64
	if err := f.data.Pool.QueryRow(context.Background(), `insert into orgs(name,slug) values ('Second demo','second-demo') returning id`).Scan(&secondOrg); err != nil {
		t.Fatal(err)
	}
	second, err := f.service.Create(context.Background(), authz.Principal{OrgID: secondOrg, Role: authz.RoleOwner}, secondOrg, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	f.service.Wait()
	now := start.Add(20 * time.Minute)
	if _, err := f.data.Pool.Exec(context.Background(), `update sites set demo_viewed_at=$2 where id=any($1)`, []int32{first.ID, second.ID}, now); err != nil {
		t.Fatal(err)
	}
	f.clock.Set(now)
	wantErr := errors.New("first site failed")
	f.service.deps.SetLiveThrough = func(ctx context.Context, siteID int32, at time.Time) error {
		if siteID == first.ID {
			return wantErr
		}
		return store.SetDemoLiveThrough(ctx, f.data.Pool, siteID, at)
	}
	if err := f.service.Tick(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("Tick error = %v, want first-site failure", err)
	}
	var secondThrough time.Time
	if err := f.data.Pool.QueryRow(context.Background(), `select demo_live_through from sites where id=$1`, second.ID).Scan(&secondThrough); err != nil {
		t.Fatal(err)
	}
	if !secondThrough.Equal(now) {
		t.Fatalf("second site live through = %s, want %s", secondThrough, now)
	}
}

func TestTickSkipsUnviewed(t *testing.T) {
	start := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	f := newLiveFixture(t, start)
	site := f.create("UTC")
	ctx := context.Background()
	old := start.Add(-3*time.Hour - time.Nanosecond)
	if _, err := f.data.Pool.Exec(ctx, `update sites set demo_viewed_at=$2 where id=$1`, site.ID, old); err != nil {
		t.Fatal(err)
	}
	before := eventCount(t, f, site.ID)
	f.clock.Set(start.Add(15 * time.Minute))
	if err := f.service.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if got := eventCount(t, f, site.ID); got != before {
		t.Fatalf("events = %d, want unchanged %d", got, before)
	}
	var liveThrough time.Time
	if err := f.data.Pool.QueryRow(ctx, `select demo_live_through from sites where id=$1`, site.ID).Scan(&liveThrough); err != nil {
		t.Fatal(err)
	}
	if !liveThrough.Equal(start) {
		t.Fatalf("demo_live_through = %s, want %s", liveThrough, start)
	}
}

func TestViewedThrottled(t *testing.T) {
	start := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	f := newLiveFixture(t, start)
	site := f.create("UTC")
	f.service.Viewed(site.ID)
	f.clock.Set(start.Add(time.Minute))
	f.service.Viewed(site.ID)

	var viewedAt time.Time
	if err := f.data.Pool.QueryRow(context.Background(), `select demo_viewed_at from sites where id=$1`, site.ID).Scan(&viewedAt); err != nil {
		t.Fatal(err)
	}
	if !viewedAt.Equal(start) {
		t.Fatalf("demo_viewed_at = %s, want first touch %s", viewedAt, start)
	}
	job := f.service.Job()
	if job.Name != "demo-live" || job.Every != 15*time.Second || job.Run == nil {
		t.Fatalf("job = %#v", job)
	}
}

func TestLiveVisitsUseVisitBoundariesForSessions(t *testing.T) {
	start := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	f := newLiveFixture(t, start)
	site := f.create("UTC")
	ctx := context.Background()
	visits := []synth.DemoVisit{
		{Start: start, VisitorKey: 77, Host: "www.demo.example", Hits: []synth.DemoHit{{Kind: 0, Path: "/"}}},
		{Start: start.Add(5 * time.Minute), VisitorKey: 77, Host: "www.demo.example", Hits: []synth.DemoHit{{Kind: 0, Path: "/pricing"}}},
	}
	before := eventCount(t, f, site.ID)
	if err := f.service.writeLive(ctx, mustDemoSite(t, f, site.ID), visits, start.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	waitForEventCount(t, f, site.ID, before+len(visits))
	deadline := time.Now().Add(5 * time.Second)
	for {
		var sessions int
		if err := f.data.Pool.QueryRow(ctx, `select count(distinct session_id) from events where site_id=$1 and ts >= $2`, site.ID, start).Scan(&sessions); err != nil {
			t.Fatal(err)
		}
		if sessions == len(visits) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("live sessions = %d, want %d", sessions, len(visits))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestTickDropsPendingVisitsForDeletedSite(t *testing.T) {
	start := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	f := newLiveFixture(t, start)
	site := f.create("UTC")
	f.service.busyPending[site.ID] = []pendingVisit{{visit: synth.DemoVisit{Start: start}}}
	if _, err := f.data.Pool.Exec(context.Background(), `update sites set deleted_at=now() where id=$1`, site.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.registry.Invalidate(); err != nil {
		t.Fatal(err)
	}
	if err := f.service.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.service.busyPending) != 0 {
		t.Fatalf("busy pending visits after delete = %#v", f.service.busyPending)
	}
}

func TestTickBusyWhileWatched(t *testing.T) {
	tz, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 11, 3, 0, 0, 0, tz)
	f := newLiveFixture(t, now)
	site := f.create("Asia/Kolkata")
	ctx := context.Background()

	f.service.Heartbeat(site.ID)
	if err := f.service.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	assertRealtimeRange(t, f, site.ID, now)
	waitForRecentEvents(t, f, site.ID, now.Add(-5*time.Minute))

	for tick := 1; tick <= 6; tick++ {
		now = now.Add(15 * time.Second)
		f.clock.Set(now)
		if tick%2 == 0 {
			f.service.Heartbeat(site.ID)
		}
		if err := f.service.Tick(ctx); err != nil {
			t.Fatal(err)
		}
		assertRealtimeRange(t, f, site.ID, now)
	}
	waitForNoDuplicateEvents(t, f, site.ID)
}

func TestTickBusyUsesDistinctKeysWithinOneBucket(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	f := newLiveFixture(t, now)
	site := f.create("UTC")
	ctx := context.Background()
	f.service.Heartbeat(site.ID)
	if err := f.service.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	firstKeys := make(map[uint64]struct{})
	for _, pending := range f.service.busyPending[site.ID] {
		firstKeys[pending.visit.VisitorKey] = struct{}{}
	}
	if len(firstKeys) == 0 {
		t.Fatal("first busy tick retained no in-progress visits")
	}
	f.sessions.Expire(now.Add(session.IdleTimeout + time.Hour))

	if err := f.service.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	newVisits := 0
	for _, pending := range f.service.busyPending[site.ID] {
		if pending.visit.Start.Before(now.Add(-15 * time.Second)) {
			continue
		}
		newVisits++
		if _, reused := firstKeys[pending.visit.VisitorKey]; reused {
			t.Fatalf("busy visitor key %d was reused within one scheduler bucket", pending.visit.VisitorKey)
		}
	}
	if newVisits == 0 {
		t.Fatal("second busy tick created no replacement visits")
	}
	waitForNoDuplicateEvents(t, f, site.ID)
}

func TestTickBusyStopsWhenNotWatched(t *testing.T) {
	tz, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 11, 3, 0, 0, 0, tz)
	f := newLiveFixture(t, now)
	site := f.create("Asia/Kolkata")
	ctx := context.Background()
	f.service.Heartbeat(site.ID)
	if err := f.service.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	waitForRecentEvents(t, f, site.ID, now.Add(-5*time.Minute))
	before := busyEventCount(t, f, site.ID)

	now = now.Add(heartbeatTTL + time.Second)
	f.clock.Set(now)
	if err := f.service.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if len(f.service.busyPending[site.ID]) != 0 {
		t.Fatalf("busy pending after heartbeat expiry = %d, want 0", len(f.service.busyPending[site.ID]))
	}
	time.Sleep(20 * time.Millisecond)
	if got := busyEventCount(t, f, site.ID); got != before {
		t.Fatalf("busy events after heartbeat expiry = %d, want unchanged %d", got, before)
	}

	now = now.Add(5*time.Minute + time.Second)
	f.clock.Set(now)
	if err := f.service.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if active := realtime.RichSnapshot(f.sessions, site.ID, now).ActiveVisitors; active >= 10 {
		t.Fatalf("realtime active visitors after watching stopped = %d, want below 10", active)
	}
}

func mustDemoSite(t *testing.T, f *liveFixture, siteID int32) sites.Site {
	t.Helper()
	site, err := f.service.site(siteID)
	if err != nil {
		t.Fatal(err)
	}
	return site
}

func eventCount(t *testing.T, f *liveFixture, siteID int32) int {
	t.Helper()
	var count int
	if err := f.data.Pool.QueryRow(context.Background(), `select count(*) from events where site_id=$1`, siteID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func waitForEventCount(t *testing.T, f *liveFixture, siteID int32, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if got := eventCount(t, f, siteID); got == want {
			return
		} else if got > want || time.Now().After(deadline) {
			t.Fatalf("events = %d, want %d", got, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func waitForRecentEvents(t *testing.T, f *liveFixture, siteID int32, after time.Time) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var count int
		if err := f.data.Pool.QueryRow(context.Background(), `select count(*) from events where site_id=$1 and ts>$2`, siteID, after).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("no recent busy events reached the database")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func waitForNoDuplicateEvents(t *testing.T, f *liveFixture, siteID int32) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var duplicates int
		if err := f.data.Pool.QueryRow(context.Background(), `
			select count(*) from (
				select session_id,ts,kind from events where site_id=$1
				group by session_id,ts,kind having count(*) > 1
			) duplicated`, siteID).Scan(&duplicates); err != nil {
			t.Fatal(err)
		}
		if duplicates == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("duplicate event groups = %d", duplicates)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func busyEventCount(t *testing.T, f *liveFixture, siteID int32) int {
	t.Helper()
	var count int
	if err := f.data.Pool.QueryRow(context.Background(), `
		select count(*) from events where site_id=$1 and visitor_id >= $2`,
		siteID, int64(uint64(1)<<62),
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func assertRealtimeRange(t *testing.T, f *liveFixture, siteID int32, now time.Time) {
	t.Helper()
	active := realtime.RichSnapshot(f.sessions, siteID, now).ActiveVisitors
	if active < 10 || active > 24 {
		t.Fatalf("realtime active visitors = %d, want 10..24", active)
	}
}

func generatedHitCount(service *Service, siteID int32, from, to time.Time) int {
	site, _ := service.deps.Registry.Site(siteID)
	anchor := time.Date(from.In(site.TZ).Year(), from.In(site.TZ).Month(), from.In(site.TZ).Day(), 0, 0, 0, 0, site.TZ)
	model := synth.NewDemo(synth.DemoConfig{Seed: service.deps.Seed, TZ: site.TZ, Visitors: service.deps.Visitors, Anchor: anchor})
	gapEnd := to.Add(-5 * time.Minute)
	count := 0
	for _, visit := range model.Visits(from, gapEnd) {
		count += len(visit.Hits)
	}
	for _, visit := range model.Visits(gapEnd, to) {
		for _, hit := range visit.Hits {
			if !visit.Start.Add(hit.Offset).After(to) {
				count++
			}
		}
	}
	return count
}
