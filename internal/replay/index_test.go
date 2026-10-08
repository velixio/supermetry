package replay

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"panel/internal/db"
	"panel/internal/testpg"
)

func TestIndexWriterCoalesces(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	if err := db.EnsurePartitions(ctx, pool, day, day.AddDate(0, 1, 0)); err != nil {
		t.Fatal(err)
	}

	writer := NewIndexWriter(pool, time.Hour, func() { t.Error("unexpected drop") })
	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- writer.Run(runCtx) }()
	profileID := int64(88)
	for i := 0; i < 100; i++ {
		writer.Add(IndexUpdate{
			SiteID: 1, SessionID: 7, Day: day, At: day.Add(time.Duration(i) * time.Second),
			Bytes: 10, Page: i%2 == 0, Trigger: "error", ProfileID: &profileID,
		})
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	var startedAt, lastSeenAt time.Time
	var bytes, pages int64
	var trigger, blobKey string
	var gotProfileID int64
	err := pool.QueryRow(ctx, `select started_at, last_seen_at, bytes, pages, trigger, profile_id, blob_key
from replay_sessions where site_id = 1 and session_id = 7`).Scan(
		&startedAt, &lastSeenAt, &bytes, &pages, &trigger, &gotProfileID, &blobKey,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !startedAt.Equal(day) || !lastSeenAt.Equal(day.Add(99*time.Second)) {
		t.Fatalf("time range = %s..%s, want %s..%s", startedAt, lastSeenAt, day, day.Add(99*time.Second))
	}
	if bytes != 1000 || pages != 50 {
		t.Fatalf("totals = %d bytes/%d pages, want 1000/50", bytes, pages)
	}
	if trigger != "error" || gotProfileID != profileID {
		t.Fatalf("metadata = trigger %q/profile %d", trigger, gotProfileID)
	}
	if want := SessionKey(1, day, 7); blobKey != want {
		t.Fatalf("blob_key = %q, want %q", blobKey, want)
	}
}

func TestIndexWriterFlushesOnShutdown(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	if err := db.EnsurePartitions(ctx, pool, day, day.AddDate(0, 1, 0)); err != nil {
		t.Fatal(err)
	}

	writer := NewIndexWriter(pool, time.Hour, func() { t.Error("unexpected drop") })
	writer.Add(IndexUpdate{SiteID: 2, SessionID: 9, Day: day, At: day, Bytes: 12, Page: true})
	runCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := writer.Run(runCtx); err != nil {
		t.Fatal(err)
	}
	var bytes int64
	if err := pool.QueryRow(ctx, `select bytes from replay_sessions where site_id = 2 and session_id = 9`).Scan(&bytes); err != nil {
		t.Fatal(err)
	}
	if bytes != 12 {
		t.Fatalf("bytes = %d, want 12", bytes)
	}
}

func TestIndexWriterAddDropsWhenFull(t *testing.T) {
	var dropped atomic.Int64
	writer := NewIndexWriter(nil, time.Hour, func() { dropped.Add(1) })
	for i := 0; i < cap(writer.ch); i++ {
		writer.Add(IndexUpdate{SessionID: int64(i)})
	}

	done := make(chan struct{})
	go func() {
		writer.Add(IndexUpdate{SessionID: -1})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Add blocked on a full buffer")
	}
	if got := dropped.Load(); got != 1 {
		t.Fatalf("onDrop calls = %d, want 1", got)
	}
}
