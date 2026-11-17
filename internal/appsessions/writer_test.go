package appsessions

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/db"
	"supermetry/internal/testpg"
)

var t0 = time.Date(2026, 11, 17, 13, 0, 0, 0, time.UTC)

type counts struct {
	mu sync.Mutex
	m  map[string]uint64
}

func (c *counts) Add(k string, n uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]uint64{}
	}
	c.m[k] += n
}

func (c *counts) get(k string) uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.m[k]
}

func fixture(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into orgs(name,slug) values('Org','org');
insert into sites(id,org_id,public_id,name,timezone,retention_class,kind) overriding system value
values(7,1,'appsite00001','App','UTC',30,'app')`); err != nil {
		t.Fatal(err)
	}
	return pool
}

func upd(status string, errs int) Update {
	return Update{SiteID: 7, SessionHash: []byte("session-hash-1"), InstallHash: []byte("install-hash-1"), Started: t0,
		Release: "r@1", Environment: "prod", Platform: "web", Status: status, Errors: errs}
}

// run enqueues the updates, closes the writer (which flushes) and returns its stats.
func run(t *testing.T, pool *pgxpool.Pool, ups ...Update) *counts {
	t.Helper()
	st := &counts{}
	w := NewWriter(pool, WriterConfig{}, st)
	for _, u := range ups {
		if !w.Enqueue(u) {
			t.Fatal("enqueue refused")
		}
	}
	w.Close()
	return st
}

type row struct {
	Status string
	Errors int
	Dur    *int64
	N      int
}

func rows(t *testing.T, pool *pgxpool.Pool) row {
	t.Helper()
	var r row
	err := pool.QueryRow(context.Background(), `select count(*), coalesce(max(status),''), coalesce(max(errors),0), max(duration_ms) from app_sessions`).Scan(&r.N, &r.Status, &r.Errors, &r.Dur)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRank(t *testing.T) {
	order := []string{"ok", "exited", "errored", "abnormal", "crashed"}
	for i, s := range order {
		if Rank(s) != i {
			t.Fatalf("Rank(%s) = %d", s, Rank(s))
		}
	}
}

func TestWriterWorstStatusWins(t *testing.T) {
	pool := fixture(t)
	run(t, pool, upd("ok", 0), upd("crashed", 1), upd("exited", 1))
	if r := rows(t, pool); r.N != 1 || r.Status != "crashed" {
		t.Fatalf("%+v", r)
	}
}

func TestWriterWorstStatusAcrossFlushes(t *testing.T) {
	pool := fixture(t)
	run(t, pool, upd("crashed", 1))
	run(t, pool, upd("exited", 1)) // a later batch must not downgrade
	if r := rows(t, pool); r.N != 1 || r.Status != "crashed" {
		t.Fatalf("%+v", r)
	}
}

func TestWriterOutOfOrderInit(t *testing.T) {
	pool := fixture(t)
	init := upd("ok", 0)
	init.Init = true
	run(t, pool, upd("crashed", 1))
	run(t, pool, init)
	if r := rows(t, pool); r.N != 1 || r.Status != "crashed" {
		t.Fatalf("%+v", r)
	}
}

func TestWriterErrorsMax(t *testing.T) {
	pool := fixture(t)
	run(t, pool, upd("errored", 1), upd("errored", 3))
	run(t, pool, upd("errored", 2))
	if r := rows(t, pool); r.Errors != 3 {
		t.Fatalf("%+v", r)
	}
}

func TestWriterDurationKeptWhenLaterUpdateHasNone(t *testing.T) {
	pool := fixture(t)
	d := int64(1500)
	u := upd("exited", 0)
	u.DurationMS = &d
	run(t, pool, u)
	run(t, pool, upd("exited", 0))
	if r := rows(t, pool); r.Dur == nil || *r.Dur != 1500 {
		t.Fatalf("%+v", r)
	}
}

func TestWriterSkipsDeletedSite(t *testing.T) {
	pool := fixture(t)
	gone := upd("ok", 0)
	gone.SiteID = 99
	run(t, pool, gone, upd("ok", 0))
	if r := rows(t, pool); r.N != 1 {
		t.Fatalf("%+v", r)
	}
}

func TestWriterQueueFull(t *testing.T) {
	pool := fixture(t)
	st := &counts{}
	w := newWriter(pool, WriterConfig{QueueSize: 1}, st) // loop not started: nothing drains the queue
	if !w.Enqueue(upd("ok", 0)) {
		t.Fatal("first enqueue refused")
	}
	if w.Enqueue(upd("ok", 0)) {
		t.Fatal("second enqueue accepted")
	}
	if st.get("session_queue_full") != 1 {
		t.Fatalf("stat = %d", st.get("session_queue_full"))
	}
	w.start()
	w.Close()
	if r := rows(t, pool); r.N != 1 {
		t.Fatalf("%+v", r)
	}
}

func TestEnqueueAfterCloseRefused(t *testing.T) {
	pool := fixture(t)
	w := NewWriter(pool, WriterConfig{}, &counts{})
	w.Close()
	if w.Enqueue(upd("ok", 0)) {
		t.Fatal("accepted after Close")
	}
}
