package appingest

import (
	"context"
	"testing"
	"time"
)

func TestClientReportsUpsertAddAcrossFlushes(t *testing.T) {
	pool, _, _ := writerFixture(t)
	ctx := context.Background()
	c := NewClientReports(pool, 0)
	day := time.Date(2026, 11, 17, 15, 30, 0, 0, time.UTC)
	c.Add(7, day, "sample_rate", "error", 2)
	c.Add(7, day.Add(time.Hour), "sample_rate", "error", 3) // same key, buffered together
	c.Add(7, day, "queue_overflow", "transaction", 1)
	c.Add(99, day, "sample_rate", "error", 5) // deleted site: skipped, must not fail the flush
	if err := c.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	c.Add(7, day, "sample_rate", "error", 4)
	if err := c.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	var q, rows int64
	if err := pool.QueryRow(ctx, `select coalesce(sum(quantity) filter (where reason='sample_rate'),0), count(*) from app_client_reports where site_id=7 and day='2026-11-17'`).Scan(&q, &rows); err != nil {
		t.Fatal(err)
	}
	if q != 9 || rows != 2 {
		t.Fatalf("sample_rate sum = %d (want 9), rows = %d (want 2)", q, rows)
	}
	if len(c.pending) != 0 {
		t.Fatalf("pending left: %v", c.pending)
	}
}

func TestClientReportsKeepCountsWhenFlushFails(t *testing.T) {
	pool, _, _ := writerFixture(t)
	c := NewClientReports(pool, 0)
	c.Add(7, testNow, "ratelimit_backoff", "error", 2)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Flush(ctx); err == nil {
		t.Fatal("flush with a cancelled context must fail")
	}
	if err := c.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	var q int64
	if err := pool.QueryRow(context.Background(), `select quantity from app_client_reports where site_id=7`).Scan(&q); err != nil || q != 2 {
		t.Fatalf("quantity = %d err=%v", q, err)
	}
}
