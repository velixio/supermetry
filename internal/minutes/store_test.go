package minutes

import (
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"panel/internal/db"
	"panel/internal/testpg"
)

func TestStore_RecomputeCountsActivityAndIsIdempotent(t *testing.T) {
	pool := newTestStore(t)
	ctx := t.Context()
	start := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	ensureEventPartitions(t, pool, start, start.Add(2*time.Minute))

	events := []testEvent{
		{siteID: 1, at: start.Add(2 * time.Second), visitorID: 10, kind: 0},
		{siteID: 1, at: start.Add(8 * time.Second), visitorID: 10, kind: 1},
		{siteID: 1, at: start.Add(20 * time.Second), visitorID: 20, kind: 1},
		{siteID: 1, at: start.Add(30 * time.Second), visitorID: 30, kind: 2},
		{siteID: 2, at: start.Add(40 * time.Second), visitorID: 10, kind: 0},
		{siteID: 1, at: start.Add(time.Minute), visitorID: 10, kind: 0},
		{siteID: 1, at: start.Add(time.Minute + time.Second), visitorID: 10, kind: 0},
	}
	insertEvents(t, pool, events...)

	store := NewStore(pool)
	for run := 0; run < 2; run++ {
		if err := store.Recompute(ctx, start, start.Add(2*time.Minute)); err != nil {
			t.Fatalf("Recompute run %d: %v", run+1, err)
		}
	}

	type row struct {
		siteID                      int32
		minute                      time.Time
		visitors, pageviews, events int
	}
	rows, err := pool.Query(ctx, `
		select site_id, minute, visitors, pageviews, events
		from visitors_per_minute order by site_id, minute`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []row
	for rows.Next() {
		var item row
		if err := rows.Scan(&item.siteID, &item.minute, &item.visitors, &item.pageviews, &item.events); err != nil {
			t.Fatal(err)
		}
		item.minute = item.minute.UTC()
		got = append(got, item)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := []row{
		{siteID: 1, minute: start, visitors: 2, pageviews: 1, events: 2},
		{siteID: 1, minute: start.Add(time.Minute), visitors: 1, pageviews: 2, events: 0},
		{siteID: 2, minute: start, visitors: 1, pageviews: 1, events: 0},
	}
	if len(got) != len(want) {
		t.Fatalf("rows = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func TestStore_MinutesUsesHalfOpenRangeInAscendingOrder(t *testing.T) {
	pool := newTestStore(t)
	ctx := t.Context()
	start := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx, `
		insert into visitors_per_minute(site_id, minute, visitors, pageviews, events) values
			(1, $1, 9, 0, 0),
			(1, $2, 2, 0, 0),
			(1, $3, 3, 0, 0),
			(1, $4, 8, 0, 0),
			(2, $2, 99, 0, 0)`,
		start.Add(-time.Minute), start, start.Add(time.Minute), start.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}

	got, err := NewStore(pool).Minutes(ctx, 1, start, start.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !got[0].Minute.Equal(start) || got[0].Visitors != 2 ||
		!got[1].Minute.Equal(start.Add(time.Minute)) || got[1].Visitors != 3 {
		t.Fatalf("Minutes = %#v", got)
	}
}

func TestStore_RecomputeDeletesMinuteWhoseEventsWereRemoved(t *testing.T) {
	pool := newTestStore(t)
	ctx := t.Context()
	minute := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	ensureEventPartitions(t, pool, minute, minute.Add(time.Minute))
	insertEvents(t, pool, testEvent{siteID: 1, at: minute, visitorID: 1, kind: 0})
	store := NewStore(pool)
	if err := store.Recompute(ctx, minute, minute.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `delete from events where site_id=1 and ts >= $1 and ts < $2`, minute, minute.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := store.Recompute(ctx, minute, minute.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(ctx, `select count(*) from visitors_per_minute`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("stored rows after raw events removed = %d, want 0", count)
	}
}

func TestStore_JobCatchesUpAcrossGapInChunks(t *testing.T) {
	pool := newTestStore(t)
	start := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	now := start.Add(12 * time.Hour)
	ensureEventPartitions(t, pool, start, now)
	for _, offset := range []time.Duration{0, time.Minute, 2 * time.Minute, 8 * time.Hour, 8*time.Hour + time.Minute, 8*time.Hour + 2*time.Minute} {
		insertEvents(t, pool, testEvent{siteID: 1, at: start.Add(offset), visitorID: int64(offset/time.Minute) + 1, kind: 0})
	}

	store := NewStore(pool)
	store.chunkSize = 2 * time.Minute
	if err := store.Job(func() time.Time { return now }).Run(t.Context()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(t.Context(), `select count(*) from visitors_per_minute`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 6 {
		t.Fatalf("stored rows after chunked catch-up = %d, want 6", count)
	}
}

func TestStore_JobContinuesAfterChunkCap(t *testing.T) {
	pool := newTestStore(t)
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	now := start.Add(30 * time.Hour)
	ensureEventPartitions(t, pool, start, now)
	for _, offset := range []time.Duration{0, 7 * time.Hour, 14 * time.Hour, 21 * time.Hour} {
		insertEvents(t, pool, testEvent{siteID: 1, at: start.Add(offset), visitorID: int64(offset/time.Hour) + 1, kind: 0})
	}

	store := NewStore(pool)
	store.chunkSize = 6 * time.Hour
	store.maxChunks = 2
	job := store.Job(func() time.Time { return now })
	if err := job.Run(t.Context()); err != nil {
		t.Fatal(err)
	}
	var firstCount int
	if err := pool.QueryRow(t.Context(), `select count(*) from visitors_per_minute`).Scan(&firstCount); err != nil {
		t.Fatal(err)
	}
	if firstCount != 2 {
		t.Fatalf("rows after capped run = %d, want 2", firstCount)
	}
	for range 2 {
		if err := job.Run(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	var finalCount int
	if err := pool.QueryRow(t.Context(), `select count(*) from visitors_per_minute`).Scan(&finalCount); err != nil {
		t.Fatal(err)
	}
	if finalCount != 4 {
		t.Fatalf("rows after continuation = %d, want 4", finalCount)
	}
}

func TestStore_JobCorrectsLateEventsAndExcludesOpenMinute(t *testing.T) {
	pool := newTestStore(t)
	now := time.Date(2026, 10, 8, 12, 17, 42, 0, time.UTC)
	closedMinute := now.Truncate(time.Minute).Add(-10 * time.Minute)
	openMinute := now.Truncate(time.Minute)
	ensureEventPartitions(t, pool, closedMinute, openMinute.Add(time.Minute))
	insertEvents(t, pool, testEvent{siteID: 1, at: closedMinute, visitorID: 1, kind: 0})

	job := NewStore(pool).Job(func() time.Time { return now })
	if job.Name != "minute-rollup" || job.Every != time.Minute {
		t.Fatalf("job metadata = %q/%s", job.Name, job.Every)
	}
	if err := job.Run(t.Context()); err != nil {
		t.Fatal(err)
	}
	insertEvents(t, pool,
		testEvent{siteID: 1, at: closedMinute.Add(20 * time.Second), visitorID: 2, kind: 1},
		testEvent{siteID: 1, at: openMinute.Add(3 * time.Second), visitorID: 3, kind: 0},
	)
	if err := job.Run(t.Context()); err != nil {
		t.Fatal(err)
	}

	var visitors int
	if err := pool.QueryRow(t.Context(), `select visitors from visitors_per_minute where site_id=1 and minute=$1`, closedMinute).Scan(&visitors); err != nil {
		t.Fatal(err)
	}
	if visitors != 2 {
		t.Fatalf("closed-minute visitors after late event = %d, want 2", visitors)
	}
	var openRows int
	if err := pool.QueryRow(t.Context(), `select count(*) from visitors_per_minute where minute=$1`, openMinute).Scan(&openRows); err != nil {
		t.Fatal(err)
	}
	if openRows != 0 {
		t.Fatalf("open-minute rows = %d, want 0", openRows)
	}
}

type testEvent struct {
	siteID    int32
	at        time.Time
	visitorID int64
	kind      int16
}

func newTestStore(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, _ := testpg.New(t)
	if err := db.Migrate(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	return pool
}

func ensureEventPartitions(t *testing.T, pool *pgxpool.Pool, from, to time.Time) {
	t.Helper()
	for lower := time.Date(from.UTC().Year(), from.UTC().Month(), 1, 0, 0, 0, 0, time.UTC); lower.Before(to); lower = lower.AddDate(0, 1, 0) {
		upper := lower.AddDate(0, 1, 0)
		name := pgx.Identifier{"events_rf_" + lower.Format("200601")}.Sanitize()
		query := fmt.Sprintf("CREATE TABLE %s PARTITION OF events_rf FOR VALUES FROM ('%s') TO ('%s')", name, lower.Format(time.RFC3339), upper.Format(time.RFC3339))
		if _, err := pool.Exec(t.Context(), query); err != nil {
			t.Fatal(err)
		}
	}
}

func insertEvents(t *testing.T, pool *pgxpool.Pool, events ...testEvent) {
	t.Helper()
	for i, event := range events {
		if _, err := pool.Exec(t.Context(), `
			insert into events(site_id, retention_class, local_day, ts, visitor_id, session_id, kind)
			values ($1, 0, $2, $3, $4, $5, $6)`,
			event.siteID, event.at.UTC().Format(time.DateOnly), event.at, event.visitorID, int64(i+1), event.kind); err != nil {
			t.Fatal(err)
		}
	}
}
