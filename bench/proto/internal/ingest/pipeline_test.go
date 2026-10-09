package ingest

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"supermetry/bench/proto/internal/core"
)

type fakeWriter struct {
	mu       sync.Mutex
	gate     chan struct{} // closed = writes proceed
	events   []*core.Event
	sessions map[int64]core.Session
}

func newFake(open bool) *fakeWriter {
	f := &fakeWriter{gate: make(chan struct{}), sessions: map[int64]core.Session{}}
	if open {
		close(f.gate)
	}
	return f
}

func (f *fakeWriter) CopyEvents(ctx context.Context, evs []*core.Event) error {
	select {
	case <-f.gate:
	case <-ctx.Done():
		return ctx.Err()
	}
	f.mu.Lock()
	f.events = append(f.events, evs...)
	f.mu.Unlock()
	return nil
}

func (f *fakeWriter) UpsertSessions(ctx context.Context, ss []core.Session) error {
	f.mu.Lock()
	for _, s := range ss {
		f.sessions[s.ID()] = s
	}
	f.mu.Unlock()
	return nil
}

func mkEv(vid int64, i int) *core.Event {
	return &core.Event{SiteID: 1, Day: 20000, TS: int64(1_700_000_000_000_000 + i*1_000_000), VisitorID: vid, Name: "pageview", Path: "/"}
}

func TestBackpressureRejectsWithoutBlocking(t *testing.T) {
	f := newFake(false) // writer stalls
	p := New(Config{QueueSize: 4, BatchSize: 1, Writers: 1, FlushEvery: time.Hour}, f, core.NewCache(core.CacheConfig{}))
	if err := p.TrySubmit(mkEv(0, 0)); err != nil {
		t.Fatal(err)
	}
	for p.Stats().QueueLen != 0 { // wait until the stalled batcher holds it
		time.Sleep(time.Millisecond)
	}
	accepted := 1
	var rejectedVID int64
	start := time.Now()
	for i := 1; i < 20; i++ {
		err := p.TrySubmit(mkEv(int64(i%3), i))
		switch {
		case err == nil:
			accepted++
		case errors.Is(err, ErrBackpressure):
			if rejectedVID == 0 {
				rejectedVID = int64(1000 + i)
				if p.TrySubmit(mkEv(rejectedVID, i)) == nil {
					t.Fatal("new visitor accepted on a full queue")
				}
			}
		default:
			t.Fatal(err)
		}
	}
	if el := time.Since(start); el > 500*time.Millisecond {
		t.Fatalf("TrySubmit blocked for %s", el)
	}
	// 1 event held by the stalled batcher + 4 queued.
	if accepted != 5 {
		t.Fatalf("accepted %d, want 5", accepted)
	}
	if st := p.Stats(); st.Rejected != 16 || st.QueueLen != 4 {
		t.Fatalf("stats %+v", st)
	}
	if p.Cache.Len() != 3 {
		t.Fatalf("rejected event created a session: %d live", p.Cache.Len())
	}
	close(f.gate)
	if err := p.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.events) != 5 {
		t.Fatalf("written %d, want 5 (flush on shutdown)", len(f.events))
	}
	var pv int32
	for _, s := range f.sessions {
		pv += s.Pageviews
	}
	if pv != 5 {
		t.Fatalf("session pageviews %d, want 5: rejected events leaked into sessions", pv)
	}
	if err := p.TrySubmit(mkEv(1, 99)); !errors.Is(err, ErrClosed) {
		t.Fatalf("after close: %v", err)
	}
}

func TestCloseDeadlineNeverHangs(t *testing.T) {
	f := newFake(false) // never opens
	p := New(Config{QueueSize: 8, BatchSize: 2, Writers: 1, FlushEvery: time.Millisecond}, f, core.NewCache(core.CacheConfig{}))
	for i := range 5 {
		_ = p.TrySubmit(mkEv(int64(i), i))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := p.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close = %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("Close hung")
	}
	if st := p.Stats(); st.Dropped != 5 || st.Written != 0 {
		t.Fatalf("stats %+v", st)
	}
}

func TestSubmitBlocksUntilSpaceOrCtx(t *testing.T) {
	f := newFake(false)
	p := New(Config{QueueSize: 1, BatchSize: 1, Writers: 1, FlushEvery: time.Hour}, f, core.NewCache(core.CacheConfig{}))
	ctx := context.Background()
	_ = p.Submit(ctx, mkEv(1, 0)) // taken by batcher
	_ = p.Submit(ctx, mkEv(1, 1)) // fills queue
	cctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if err := p.Submit(cctx, mkEv(1, 2)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Submit = %v", err)
	}
	close(f.gate)
	if err := p.Submit(ctx, mkEv(1, 3)); err != nil {
		t.Fatal(err)
	}
	if err := p.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if len(f.events) != 3 {
		t.Fatalf("written %d", len(f.events))
	}
	for _, s := range f.sessions {
		if s.Pageviews != 3 {
			t.Fatalf("session pageviews %d, want 3", s.Pageviews)
		}
	}
}

func TestPeriodicSessionFlushAndEviction(t *testing.T) {
	f := newFake(true)
	var now int64 = 1_700_000_000_000_000
	var mu sync.Mutex
	clock := func() int64 { mu.Lock(); defer mu.Unlock(); return now }
	p := New(Config{SessionFlushEvery: 20 * time.Millisecond, Clock: clock}, f, core.NewCache(core.CacheConfig{}))
	_ = p.TrySubmit(mkEv(7, 0))
	deadline := time.Now().Add(2 * time.Second)
	for {
		f.mu.Lock()
		n := len(f.sessions)
		f.mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("session never flushed")
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	now += (31 * time.Minute).Microseconds()
	mu.Unlock()
	for p.Cache.Len() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("idle session never evicted")
		}
		time.Sleep(5 * time.Millisecond)
	}
	_ = p.Close(context.Background())
}

type slowSessions struct {
	*fakeWriter
	gate chan struct{}
}

func (s slowSessions) UpsertSessions(ctx context.Context, ss []core.Session) error {
	select {
	case <-s.gate:
	case <-ctx.Done():
		return ctx.Err()
	}
	return s.fakeWriter.UpsertSessions(ctx, ss)
}

func TestSessionBacklogBackpressure(t *testing.T) {
	w := slowSessions{newFake(true), make(chan struct{})}
	p := New(Config{QueueSize: 1 << 12, SessionFlushAfter: 10, SessionFlushEvery: time.Hour}, w, core.NewCache(core.CacheConfig{}))
	// HTTP path: rejects once more than 8×SessionFlushAfter events wait for a session flush.
	accepted := 0
	for i := range 200 {
		if p.TrySubmit(mkEv(int64(i), i)) == nil {
			accepted++
		}
	}
	if accepted > 81 || accepted < 80 || p.Stats().Rejected == 0 {
		t.Fatalf("accepted %d with a stalled session flusher", accepted)
	}
	// Bulk path: blocks (here: until ctx) instead of growing memory.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := p.Submit(ctx, mkEv(999, 0)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Submit = %v", err)
	}
	close(w.gate) // flusher catches up: intake resumes
	if err := p.Submit(context.Background(), mkEv(1000, 0)); err != nil {
		t.Fatal(err)
	}
	if err := p.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(w.sessions); n != accepted+1 {
		t.Fatalf("sessions upserted %d, want %d", n, accepted+1)
	}
}
