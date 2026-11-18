package appingest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/appattach"
	"supermetry/internal/health"
)

func testBody(site int32, id string) Body {
	return Body{SiteID: site, TS: testNow.Add(-time.Minute), EventID: id, Level: "error", Message: "boom " + id}
}

func countBodies(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `select count(*) from app_event_bodies`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestBodyWriterStoresBodyWithSize(t *testing.T) {
	pool, _, _ := writerFixture(t)
	w := NewBodyWriter(pool, BodyWriterConfig{})
	if !w.Enqueue(testBody(7, "e1")) {
		t.Fatal("enqueue refused")
	}
	batch := []bodyRow{<-w.ch}
	if err := w.copyRows(context.Background(), batch); err != nil {
		t.Fatal(err)
	}
	var size int
	var level string
	if err := pool.QueryRow(context.Background(), `select size, body->>'level' from app_event_bodies where site_id=7 and event_id='e1'`).Scan(&size, &level); err != nil {
		t.Fatal(err)
	}
	if size != len(batch[0].json) || size == 0 || level != "error" {
		t.Fatalf("size=%d level=%q", size, level)
	}
}

func TestBodyWriterSkipsDeletedSites(t *testing.T) {
	pool, _, _ := writerFixture(t)
	w := NewBodyWriter(pool, BodyWriterConfig{})
	w.Enqueue(testBody(99, "gone"))
	w.Enqueue(testBody(7, "ok"))
	if err := w.copyRows(context.Background(), []bodyRow{<-w.ch, <-w.ch}); err != nil {
		t.Fatal(err)
	}
	if n := countBodies(t, pool); n != 1 {
		t.Fatalf("rows = %d, want 1", n)
	}
}

func TestBodyWriterRunFlushesOnSizeIntervalAndShutdown(t *testing.T) {
	pool, _, _ := writerFixture(t)
	w := NewBodyWriter(pool, BodyWriterConfig{BatchSize: 3, FlushEvery: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	for i := range 3 {
		w.Enqueue(testBody(7, fmt.Sprint("size", i)))
	}
	waitFor(t, func() bool { return countBodies(t, pool) == 3 })
	w.Enqueue(testBody(7, "tail1"))
	w.Enqueue(testBody(7, "tail2"))
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if n := countBodies(t, pool); n != 5 {
		t.Fatalf("rows after shutdown = %d, want 5", n)
	}
	if w.Enqueue(testBody(7, "late")) {
		t.Fatal("enqueue after shutdown must be refused")
	}

	w2 := NewBodyWriter(pool, BodyWriterConfig{BatchSize: 500, FlushEvery: 50 * time.Millisecond})
	ctx2, cancel2 := context.WithCancel(context.Background())
	done2 := make(chan error, 1)
	go func() { done2 <- w2.Run(ctx2) }()
	w2.Enqueue(testBody(7, "tick"))
	waitFor(t, func() bool { return countBodies(t, pool) == 6 })
	cancel2()
	<-done2
}

func TestBodyWriterFullQueueDropsAndCounts(t *testing.T) {
	w := NewBodyWriter(nil, BodyWriterConfig{QueueSize: 2})
	before := health.AppBodyQueueFull.Load()
	got := []bool{w.Enqueue(testBody(7, "a")), w.Enqueue(testBody(7, "b")), w.Enqueue(testBody(7, "c"))}
	if !got[0] || !got[1] || got[2] {
		t.Fatalf("enqueue results = %v", got)
	}
	if d := health.AppBodyQueueFull.Load() - before; d != 1 {
		t.Fatalf("queue-full count = %d", d)
	}
}

func TestBodyWriterRetriesOnceThenDropsAndCounts(t *testing.T) {
	w := NewBodyWriter(nil, BodyWriterConfig{})
	w.retryDelay = time.Millisecond
	var mu sync.Mutex
	calls := 0
	w.copyRows = func(context.Context, []bodyRow) error {
		mu.Lock()
		defer mu.Unlock()
		calls++
		return errors.New("db down")
	}
	before := health.AppBodyWriteFailed.Load()
	w.flushRetry(context.Background(), []bodyRow{{}, {}})
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
	if d := health.AppBodyWriteFailed.Load() - before; d != 2 {
		t.Fatalf("write-failed count = %d", d)
	}
}

func TestSiteDeletionCascadesBodiesAndClientReports(t *testing.T) {
	pool, _, _ := writerFixture(t)
	ctx := context.Background()
	w := NewBodyWriter(pool, BodyWriterConfig{})
	w.Enqueue(testBody(7, "x"))
	if err := w.copyRows(ctx, []bodyRow{<-w.ch}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into app_client_reports values (7,'2026-11-17','queue_overflow','error',3)`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `delete from sites where id = 7`); err != nil {
		t.Fatal(err)
	}
	var b, r int
	if err := pool.QueryRow(ctx, `select (select count(*) from app_event_bodies), (select count(*) from app_client_reports)`).Scan(&b, &r); err != nil || b != 0 || r != 0 {
		t.Fatalf("left after site delete: bodies=%d reports=%d err=%v", b, r, err)
	}
}

func bodyWithFile(site int32, id string, size int) Body {
	b := testBody(site, id)
	b.Attachments = []AttachmentMeta{{Name: "log.txt", ContentType: "text/plain", Size: size, Stored: true}}
	b.Files = []appattach.File{{Idx: 0, Name: "log.txt", ContentType: "text/plain", Data: bytes.Repeat([]byte("a"), size)}}
	return b
}

func TestBodyWriterStoresAttachmentsWithBodyAndSkipsDeletedSites(t *testing.T) {
	pool, _, _ := writerFixture(t)
	ctx := context.Background()
	w := NewBodyWriter(pool, BodyWriterConfig{})
	w.Enqueue(bodyWithFile(7, "att", 10))
	w.Enqueue(bodyWithFile(99, "gone", 10))
	if err := w.copyRows(ctx, []bodyRow{<-w.ch, <-w.ch}); err != nil {
		t.Fatal(err)
	}
	var n, size int
	var ct string
	if err := pool.QueryRow(ctx, `select count(*), coalesce(max(size),0), coalesce(max(content_type),'') from app_event_attachments`).Scan(&n, &size, &ct); err != nil {
		t.Fatal(err)
	}
	if n != 1 || size != 10 || ct != "text/plain" {
		t.Fatalf("attachments n=%d size=%d ct=%q", n, size, ct)
	}
}

func TestBodyWriterQueuedAttachmentBytesAreBounded(t *testing.T) {
	w := NewBodyWriter(nil, BodyWriterConfig{AttachmentBytes: 100})
	w.copyRows = func(context.Context, []bodyRow) error { return nil }
	before := health.AppAttachmentDropped.Load()
	if !w.Enqueue(bodyWithFile(7, "a", 60)) || !w.Enqueue(bodyWithFile(7, "b", 60)) {
		t.Fatal("a body must be kept even when its attachments are not")
	}
	first, second := <-w.ch, <-w.ch
	if len(first.files) != 1 || len(second.files) != 0 {
		t.Fatalf("files kept = %d, %d", len(first.files), len(second.files))
	}
	if strings.Contains(string(first.json), `"stored":false`) || !strings.Contains(string(second.json), `"stored":false`) {
		t.Fatalf("stored flags: %s / %s", first.json, second.json)
	}
	if d := health.AppAttachmentDropped.Load() - before; d != 1 {
		t.Fatalf("dropped = %d", d)
	}
	w.flushRetry(context.Background(), []bodyRow{first, second})
	if got := w.queuedBytes.Load(); got != 0 {
		t.Fatalf("budget not released: %d", got)
	}
}
