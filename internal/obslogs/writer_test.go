package obslogs_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/db"
	"supermetry/internal/obslogs"
	"supermetry/internal/testpg"
)

type counts struct {
	mu sync.Mutex
	m  map[string]int
}

func (c *counts) Add(name string, n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]int{}
	}
	c.m[name] += n
}

func (c *counts) get(name string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.m[name]
}

var now = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

func fixture(t *testing.T) *pgxpool.Pool {
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
		values(1,1,'applogs00001','App','UTC',30,'app')`); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureDailyPartitions(ctx, pool, "logs", now.AddDate(0, 0, -1), now.AddDate(0, 0, 3)); err != nil {
		t.Fatal(err)
	}
	return pool
}

func rec(i int, body string) obslogs.Record {
	return obslogs.Record{
		SiteID: 1, TS: now.Add(time.Duration(i) * time.Millisecond), Observed: now, Severity: 9,
		Service: "api", Environment: "prod", Release: "1.0", Body: body,
		Attributes: map[string]any{"http.route": "/pay", "n": i}, Source: obslogs.SourceOTLP,
	}
}

func TestWriterCopiesBatch(t *testing.T) {
	pool := fixture(t)
	st := &counts{}
	w := obslogs.NewWriter(pool, obslogs.WriterConfig{Batch: 2000, Flush: time.Hour, Now: func() time.Time { return now }}, st)
	rs := make([]obslogs.Record, 2500)
	for i := range rs {
		rs[i] = rec(i, "line é "+strings.Repeat("x", i%7))
	}
	rs[0].TraceID = []byte("0123456789abcdef")
	rs[0].SpanID = []byte("01234567")
	if got := w.Enqueue(rs); got != 2500 {
		t.Fatalf("accepted %d", got)
	}
	w.Close() // flushes: 2,000 + 500
	var n int
	var body string
	var attrs string
	var trace []byte
	ctx := context.Background()
	if err := pool.QueryRow(ctx, `select count(*) from logs`).Scan(&n); err != nil || n != 2500 {
		t.Fatalf("rows = %d %v", n, err)
	}
	if err := pool.QueryRow(ctx, `select body, attributes::text, trace_id from logs where ts = $1`, rs[3].TS).Scan(&body, &attrs, &trace); err != nil {
		t.Fatal(err)
	}
	if body != rs[3].Body || attrs != `{"n": 3, "http.route": "/pay"}` || trace != nil {
		t.Fatalf("row = %q %s %v", body, attrs, trace)
	}
	if err := pool.QueryRow(ctx, `select trace_id from logs where ts = $1`, rs[0].TS).Scan(&trace); err != nil || string(trace) != "0123456789abcdef" {
		t.Fatalf("trace id = %q %v", trace, err)
	}
	if st.get("log_write_failed") != 0 || st.get("log_out_of_range") != 0 {
		t.Fatalf("stats %v", st.m)
	}
}

func TestWriterByteBudget(t *testing.T) {
	st := &counts{}
	w := obslogs.NewWriter(nil, obslogs.WriterConfig{MaxBytes: 10_000, Flush: time.Hour}, st)
	big := func() []obslogs.Record { return []obslogs.Record{rec(0, strings.Repeat("a", 4000))} }
	if got := w.Enqueue(big()); got != 1 {
		t.Fatalf("first accepted %d", got)
	}
	if got := w.Enqueue(big()); got != 1 {
		t.Fatalf("second accepted %d", got)
	}
	if got := w.Enqueue(big()); got != 0 {
		t.Fatalf("third accepted %d, want 0", got)
	}
	if st.get("log_queue_full") != 1 {
		t.Fatalf("log_queue_full = %d", st.get("log_queue_full"))
	}
	// Prefix semantics: of three small records only the ones that fit count.
	w2 := obslogs.NewWriter(nil, obslogs.WriterConfig{MaxRecords: 2, Flush: time.Hour}, st)
	if got := w2.Enqueue([]obslogs.Record{rec(0, "a"), rec(1, "b"), rec(2, "c")}); got != 2 {
		t.Fatalf("prefix accepted %d", got)
	}
	if st.get("log_queue_full") != 2 {
		t.Fatalf("log_queue_full = %d", st.get("log_queue_full"))
	}
}

func TestWriterOutOfRange(t *testing.T) {
	pool := fixture(t)
	st := &counts{}
	w := obslogs.NewWriter(pool, obslogs.WriterConfig{Flush: time.Hour, Now: func() time.Time { return now }}, st)
	future := rec(0, "far ahead")
	future.TS = now.AddDate(0, 0, 10)
	if got := w.Enqueue([]obslogs.Record{future, rec(1, "today")}); got != 2 {
		t.Fatalf("accepted %d", got)
	}
	w.Close()
	var n int
	var parts int
	ctx := context.Background()
	if err := pool.QueryRow(ctx, `select count(*) from logs`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("rows = %d %v", n, err)
	}
	if st.get("log_out_of_range") != 1 {
		t.Fatalf("log_out_of_range = %d", st.get("log_out_of_range"))
	}
	// 5 daily partitions (yesterday..+3 days) from the fixture; the write path created none.
	if err := pool.QueryRow(ctx, `select count(*) from pg_inherits where inhparent = 'logs'::regclass`).Scan(&parts); err != nil || parts != 5 {
		t.Fatalf("partitions = %d %v", parts, err)
	}
}

func TestWriterDropsDeletedSite(t *testing.T) {
	pool := fixture(t)
	st := &counts{}
	w := obslogs.NewWriter(pool, obslogs.WriterConfig{Flush: time.Hour, Now: func() time.Time { return now }}, st)
	gone := rec(0, "gone")
	gone.SiteID = 99
	w.Enqueue([]obslogs.Record{gone, rec(1, "kept")})
	w.Close()
	var n int
	if err := pool.QueryRow(context.Background(), `select count(*) from logs`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("rows = %d %v", n, err)
	}
	if st.get("log_write_failed") != 0 {
		t.Fatalf("stats %v", st.m)
	}
}

func TestRecordSize(t *testing.T) {
	r := obslogs.Record{Body: "abcd", Attributes: map[string]any{"a": "b"}}
	// 4 body bytes + {"a":"b"} (9 bytes) + 96 overhead.
	if got := r.Size(); got != 109 {
		t.Fatalf("Size = %d, want 109", got)
	}
	if got := (obslogs.Record{Body: "abcd"}).Size(); got != 102 {
		t.Fatalf("Size without attributes = %d, want 102", got)
	}
}
