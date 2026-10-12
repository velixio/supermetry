package writer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/closer"
	"supermetry/internal/db"
	"supermetry/internal/privacy"
	"supermetry/internal/session"
	"supermetry/internal/sites"
	"supermetry/internal/testpg"
)

type reliabilityRegistry struct{ site sites.Site }

type reliabilityClock struct{ now time.Time }

func (c reliabilityClock) Now() time.Time { return c.now }

func (r reliabilityRegistry) Site(id int32) (sites.Site, bool) { return r.site, id == r.site.ID }
func (r reliabilityRegistry) ByPublicID(id string) (sites.Site, bool) {
	return r.site, id == r.site.PublicID
}
func (r reliabilityRegistry) All() []sites.Site { return []sites.Site{r.site} }

func TestCopyEventsMarksDayClosedByAnotherEngineDirty(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	day := utcDay(time.Now()).AddDate(0, 0, -1)
	if err := db.EnsurePartitions(ctx, pool, day, day.AddDate(0, 0, 2)); err != nil {
		t.Fatal(err)
	}
	seedReliabilitySession(t, pool, day)
	if _, err := closer.CloseDay(ctx, pool, []int32{1}, day); err != nil {
		t.Fatal(err)
	}

	w := New(pool, nil, 1, time.Second, time.Second)
	w.SetTrackingPolicy(privacy.Anonymous, reliabilityRegistry{site: sites.Site{ID: 1, PublicID: "site", TZ: time.UTC}})
	if err := w.copyEvents(ctx, []Event{reliabilityEvent(day)}); err != nil {
		t.Fatal(err)
	}
	var dirty int
	if err := pool.QueryRow(ctx, `select count(*) from dirty_day where site_id=1 and day=$1`, day).Scan(&dirty); err != nil {
		t.Fatal(err)
	}
	if dirty != 1 {
		t.Fatalf("dirty rows=%d, want 1", dirty)
	}
	if _, err := closer.CloseDay(ctx, pool, []int32{1}, day); err != nil {
		t.Fatal(err)
	}
	var custom int
	if err := pool.QueryRow(ctx, `select count(*) from custom_closed where site_id=1 and day=$1`, day).Scan(&custom); err != nil {
		t.Fatal(err)
	}
	if custom != 1 {
		t.Fatalf("closed custom events=%d, want 1", custom)
	}
}

func TestConcurrentCloseBeforeCopyLeavesDirtyDay(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	day := utcDay(time.Now()).AddDate(0, 0, -1)
	if err := db.EnsurePartitions(ctx, pool, day, day.AddDate(0, 0, 2)); err != nil {
		t.Fatal(err)
	}

	closeTx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer closeTx.Rollback(ctx)
	if _, err := closeTx.Exec(ctx, `select pg_advisory_xact_lock(7469002)`); err != nil {
		t.Fatal(err)
	}

	w := New(pool, nil, 1, time.Second, time.Second)
	w.SetTrackingPolicy(privacy.Anonymous, reliabilityRegistry{site: sites.Site{ID: 1, PublicID: "site", TZ: time.UTC}})
	result := make(chan error, 1)
	go func() { result <- w.copyEvents(ctx, []Event{reliabilityEvent(day)}) }()
	waitForSharedDayCloseLock(t, pool)
	if _, err := closeTx.Exec(ctx, `insert into day_close(site_id,day,status,version) values (1,$1,'done',1)`, day); err != nil {
		t.Fatal(err)
	}
	if err := closeTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	var events, dirty int
	if err := pool.QueryRow(ctx, `select (select count(*) from events), (select count(*) from dirty_day where site_id=1 and day=$1)`, day).Scan(&events, &dirty); err != nil {
		t.Fatal(err)
	}
	if events != 1 || dirty != 1 {
		t.Fatalf("events=%d dirty=%d, want 1/1", events, dirty)
	}
}

func waitForSharedDayCloseLock(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		var waiting bool
		if err := pool.QueryRow(context.Background(), `
select exists(
  select 1 from pg_locks
  where locktype='advisory' and objid=$1 and not granted
)`, dayCloseLockID).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("late COPY did not wait for the exclusive day-close lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestCurrentDayCopyDoesNotTakeSharedDayCloseLock(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	day := utcDay(now)
	if err := db.EnsurePartitions(ctx, pool, day, day.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(7469002)`); err != nil {
		t.Fatal(err)
	}

	w := New(pool, nil, 1, time.Second, time.Second)
	w.SetClock(reliabilityClock{now: now})
	w.SetTrackingPolicy(privacy.Anonymous, reliabilityRegistry{site: sites.Site{ID: 1, PublicID: "site", TZ: time.UTC}})
	result := make(chan error, 1)
	go func() { result <- w.copyEvents(ctx, []Event{reliabilityEvent(day)}) }()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("current-day COPY waited for close advisory lock")
	}
}

func TestConcurrentEventBatchesNeverMoveLastEventBackwards(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	if err := db.EnsurePartitions(ctx, pool, day, day.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
insert into orgs(id,name,slug) overriding system value values(1,'Writer','writer');
insert into sites(id,org_id,public_id,name,timezone,retention_class) overriding system value values(1,1,'writer000001','Writer','UTC',30)`); err != nil {
		t.Fatal(err)
	}

	w := New(pool, nil, 10, time.Hour, time.Hour)
	errors := make(chan error, 2)
	start := make(chan struct{})
	for index, timestamp := range []time.Time{day.Add(time.Hour), day.Add(5 * time.Hour)} {
		go func(index int, timestamp time.Time) {
			<-start
			event := reliabilityEvent(day)
			event.TS = timestamp
			event.SessionID = int64(index + 1)
			event.VisitorID = int64(index + 1)
			errors <- w.copyEvents(ctx, []Event{event})
		}(index, timestamp)
	}
	close(start)
	for range 2 {
		if err := <-errors; err != nil {
			t.Fatal(err)
		}
	}
	var got time.Time
	if err := pool.QueryRow(ctx, `select last_event_at from site_last_event where site_id=1`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if want := day.Add(5 * time.Hour); !got.Equal(want) {
		t.Fatalf("last event=%s, want %s", got, want)
	}
}

func TestOlderLocalDaysUsesEventDayForUnregisteredEastOfUTC(t *testing.T) {
	now := time.Date(2030, time.January, 1, 18, 45, 0, 0, time.UTC)
	eventDay := time.Date(2030, time.January, 1, 0, 0, 0, 0, time.UTC)
	event := reliabilityEvent(eventDay)
	// In UTC+05:45 this event's local day was Jan 1. At now, that site's local
	// day has advanced to Jan 2 even though UTC is still on Jan 1.
	event.TS = time.Date(2030, time.January, 1, 1, 0, 0, 0, time.UTC)

	w := New(nil, nil, 1, time.Second, time.Second)
	got := w.olderLocalDays([]Event{event}, now)
	if len(got) != 1 || got[0] != (siteDay{site: 1, day: eventDay}) {
		t.Fatalf("older local days = %#v, want site 1 day %s", got, eventDay.Format(time.DateOnly))
	}
}

func TestCopyEventsUsesInjectedClockForLateDayLock(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	day := time.Date(2029, time.December, 31, 0, 0, 0, 0, time.UTC)
	if err := db.EnsurePartitions(ctx, pool, day, day.AddDate(0, 0, 2)); err != nil {
		t.Fatal(err)
	}
	closeTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer closeTx.Rollback(ctx)
	if _, err := closeTx.Exec(ctx, `select pg_advisory_xact_lock($1)`, dayCloseLockID); err != nil {
		t.Fatal(err)
	}

	w := New(pool, nil, 1, time.Second, time.Second)
	w.SetClock(reliabilityClock{now: time.Date(2030, time.January, 1, 12, 0, 0, 0, time.UTC)})
	w.SetTrackingPolicy(privacy.Anonymous, reliabilityRegistry{site: sites.Site{ID: 1, PublicID: "site", TZ: time.UTC}})
	copyCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if err := w.copyEvents(copyCtx, []Event{reliabilityEvent(day)}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("copy error = %v, want context deadline while waiting for late-day lock", err)
	}
}

func seedReliabilitySession(t *testing.T, pool *pgxpool.Pool, day time.Time) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
insert into sessions(site_id, retention_class, session_id, visitor_id, start, last, local_day, pageviews, events)
values (1, 30, 99, 7, $1, $1, $2, 1, 1)`, day.Add(time.Hour), day)
	if err != nil {
		t.Fatal(err)
	}
}

func reliabilityEvent(day time.Time) Event {
	return Event{Hit: session.Hit{
		SiteID: 1, Retention: db.Ret30, TS: day.Add(time.Hour), LocalDay: day,
		VisitorID: 7, Kind: 1, NameID: 1, PathID: 1,
	}, SessionID: 99}
}

func utcDay(value time.Time) time.Time {
	value = value.UTC()
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
}
