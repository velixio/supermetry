package spans

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/db"
	"supermetry/internal/testpg"
)

type countStats struct {
	mu sync.Mutex
	m  map[string]int
}

func (c *countStats) Add(name string, n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]int{}
	}
	c.m[name] += n
}

func (c *countStats) get(name string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.m[name]
}

var testNow = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

func fixture(t *testing.T, tz string) *pgxpool.Pool {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into orgs(name,slug) values('Org','org')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into sites(id,org_id,public_id,name,timezone,retention_class,kind) overriding system value
values(7,1,'appsite00001','App',$1,30,'app')`, tz); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureDailyPartitions(ctx, pool, "spans", testNow.AddDate(0, 0, -1), testNow.AddDate(0, 0, 3)); err != nil {
		t.Fatal(err)
	}
	return pool
}

func mkSpan(i int, start time.Time) Span {
	s := Span{SiteID: 7, Start: start, DurationUS: int64(1000 + i), Name: "GET /items", Op: "http.server",
		Description: "GET /items", Status: "ok", Release: "1.0", Environment: "prod", Platform: "go", IsSegment: i%2 == 0}
	s.TraceID[0], s.TraceID[15] = byte(i), byte(i>>8)
	s.SpanID[0], s.SpanID[7] = 1, byte(i)
	s.SegmentID = s.SpanID
	return s
}

func newTestWriter(pool *pgxpool.Pool, cfg WriterConfig, st Stats, late func(int32, []time.Time)) *Writer {
	cfg.Now = func() time.Time { return testNow }
	return NewWriter(pool, cfg, st, late)
}

func count(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `select count(*) from spans`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestWriterCopiesSpans(t *testing.T) {
	pool := fixture(t, "UTC")
	w := newTestWriter(pool, WriterConfig{}, &countStats{}, nil)
	batch := make([]Span, 1000)
	for i := range batch {
		batch[i] = mkSpan(i, testNow.Add(-time.Duration(i)*time.Second))
	}
	parent := [8]byte{9, 9, 9, 9, 9, 9, 9, 9}
	status, ms, sb := int16(200), int32(1200), Bucket(1_200_000)
	batch[0].ParentID, batch[0].HTTPStatus = &parent, &status
	batch[0].AppStartType, batch[0].AppStartMS, batch[0].AppStartBucket = "cold", &ms, &sb
	batch[0].Attrs = map[string]string{"http.method": "GET"}
	if !w.Enqueue(batch) {
		t.Fatal("enqueue refused")
	}
	w.flushAll(context.Background())
	if n := count(t, pool); n != 1000 {
		t.Fatalf("rows = %d", n)
	}
	var (
		bucket, hs, sbk       int16
		startMS               int32
		parentID              []byte
		startType, attrs, op  string
		isSeg                 bool
		group                 int64
		dur                   int64
		traceLen, segLen, spL int
	)
	err := pool.QueryRow(context.Background(), `select bucket, http_status, app_start_bucket, app_start_ms, parent_span_id,
		app_start_type, attrs->>'http.method', op, is_segment, group_hash, duration_us,
		octet_length(trace_id), octet_length(segment_id), octet_length(span_id)
		from spans where duration_us = 1000`).Scan(&bucket, &hs, &sbk, &startMS, &parentID, &startType, &attrs, &op, &isSeg, &group, &dur, &traceLen, &segLen, &spL)
	if err != nil {
		t.Fatal(err)
	}
	if bucket != Bucket(1000) || hs != 200 || sbk != sb || startMS != 1200 || len(parentID) != 8 || startType != "cold" ||
		attrs != "GET" || op != "http.server" || !isSeg || group != GroupHash("http.server", "GET /items") || traceLen != 16 || segLen != 8 || spL != 8 {
		t.Fatalf("round trip mismatch: %d %d %d %d %v %q %q", bucket, hs, sbk, startMS, parentID, startType, attrs)
	}
	var nulls int
	if err := pool.QueryRow(context.Background(), `select count(*) from spans where duration_us = 1001 and parent_span_id is null and http_status is null and app_start_type is null and attrs is null`).Scan(&nulls); err != nil || nulls != 1 {
		t.Fatalf("null columns: %d %v", nulls, err)
	}
}

func TestWriterQueueFullAllOrNothing(t *testing.T) {
	pool := fixture(t, "UTC")
	st := &countStats{}
	w := newTestWriter(pool, WriterConfig{QueueSpans: 10}, st, nil)
	batch := make([]Span, 11)
	for i := range batch {
		batch[i] = mkSpan(i, testNow)
	}
	if w.Enqueue(batch) {
		t.Fatal("oversized batch accepted")
	}
	if got := st.get("span_queue_full"); got != 11 {
		t.Fatalf("span_queue_full = %d", got)
	}
	w.flushAll(context.Background())
	if n := count(t, pool); n != 0 {
		t.Fatalf("rows = %d", n)
	}
}

func TestWriterNoPartitionDrops(t *testing.T) {
	pool := fixture(t, "UTC")
	st := &countStats{}
	w := newTestWriter(pool, WriterConfig{}, st, nil)
	var before int
	if err := pool.QueryRow(context.Background(), `select count(*) from pg_partition_tree('spans') where isleaf`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	old := mkSpan(1, testNow.AddDate(0, 0, -10)) // no partition for this day
	ok := mkSpan(2, testNow)
	w.Enqueue([]Span{old, ok})
	w.flushAll(context.Background())
	if n := count(t, pool); n != 1 {
		t.Fatalf("rows = %d, want only the span with a partition", n)
	}
	if got := st.get("span_no_partition"); got != 1 {
		t.Fatalf("span_no_partition = %d", got)
	}
	var parts int
	if err := pool.QueryRow(context.Background(), `select count(*) from pg_partition_tree('spans') where isleaf`).Scan(&parts); err != nil || parts != before {
		t.Fatalf("writer created a partition: %d %v", parts, err)
	}
}

func TestWriterOnLate(t *testing.T) {
	pool := fixture(t, "Asia/Kolkata")
	kolkata, _ := time.LoadLocation("Asia/Kolkata")
	var got map[int32][]time.Time = map[int32][]time.Time{}
	w := newTestWriter(pool, WriterConfig{Location: func(int32) *time.Location { return kolkata }}, &countStats{},
		func(site int32, hours []time.Time) { got[site] = append(got[site], hours...) })
	late := mkSpan(1, time.Date(2026, 11, 18, 7, 10, 0, 0, time.UTC))    // 12:40 IST, now is 17:30 IST
	recent := mkSpan(2, time.Date(2026, 11, 18, 11, 40, 0, 0, time.UTC)) // 17:10 IST, current hour
	prev := mkSpan(3, time.Date(2026, 11, 18, 10, 40, 0, 0, time.UTC))   // 16:10 IST, previous hour
	w.Enqueue([]Span{late, recent, prev})
	w.flushAll(context.Background())
	want := time.Date(2026, 11, 18, 12, 0, 0, 0, kolkata)
	if len(got[7]) != 1 || !got[7][0].Equal(want) {
		t.Fatalf("onLate = %v, want [%v]", got[7], want)
	}
}

func TestWriterShutdownDrains(t *testing.T) {
	pool := fixture(t, "UTC")
	w := newTestWriter(pool, WriterConfig{Flush: time.Hour}, &countStats{}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	batch := []Span{mkSpan(1, testNow), mkSpan(2, testNow)}
	if !w.Enqueue(batch) {
		t.Fatal("enqueue refused")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if n := count(t, pool); n != 2 {
		t.Fatalf("rows = %d", n)
	}
	if w.Enqueue([]Span{mkSpan(3, testNow)}) {
		t.Fatal("enqueue after close accepted")
	}
}

func TestWriterDeletedSiteSkipped(t *testing.T) {
	pool := fixture(t, "UTC")
	w := newTestWriter(pool, WriterConfig{}, &countStats{}, nil)
	gone := mkSpan(1, testNow)
	gone.SiteID = 99
	w.Enqueue([]Span{gone, mkSpan(2, testNow)})
	w.flushAll(context.Background())
	if n := count(t, pool); n != 1 {
		t.Fatalf("rows = %d", n)
	}
}

func TestWriterDropsRepeatedSpansInABatch(t *testing.T) {
	pool := fixture(t, "UTC")
	st := &countStats{}
	w := newTestWriter(pool, WriterConfig{Flush: time.Hour}, st, nil)
	a, b := mkSpan(1, testNow), mkSpan(2, testNow)
	w.Enqueue([]Span{a, b})
	w.Enqueue([]Span{a, b, a})
	w.flushAll(context.Background())
	if n := count(t, pool); n != 2 {
		t.Fatalf("rows = %d, want 2", n)
	}
	if st.get("span_duplicate") != 3 {
		t.Errorf("span_duplicate = %d, want 3", st.get("span_duplicate"))
	}
}
