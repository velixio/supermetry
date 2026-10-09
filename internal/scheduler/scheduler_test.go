package scheduler

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"supermetry/internal/testpg"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestJobsRunSkipOnLockAndRetry(t *testing.T) {
	pool, _ := testpg.New(t)
	clk := newFakeClock()

	var runs atomic.Int32
	var attempts atomic.Int32
	s := New(pool, clk,
		Job{
			Name:  "ok",
			Every: time.Minute,
			Run: func(context.Context) error {
				runs.Add(1)
				return nil
			},
		},
		Job{
			Name:  "flaky",
			Every: time.Minute,
			Run: func(context.Context) error {
				if attempts.Add(1) < 3 {
					return errors.New("boom")
				}
				return nil
			},
		},
	)

	ctx, cancel := context.WithCancel(context.Background())
	done := runScheduler(s, ctx)
	eventually(t, func() bool { return clk.activeTickers() == 2 })

	clk.Advance(time.Minute)
	eventually(t, func() bool { return runs.Load() == 1 && attempts.Load() == 1 })
	eventually(t, func() bool { return clk.hasTickerIn(time.Second) })

	clk.Advance(time.Second - time.Nanosecond)
	if got := attempts.Load(); got != 1 {
		t.Fatalf("attempts before first retry = %d, want 1", got)
	}
	clk.Advance(time.Nanosecond)
	eventually(t, func() bool { return attempts.Load() == 2 })
	eventually(t, func() bool { return clk.hasTickerIn(2 * time.Second) })

	clk.Advance(2*time.Second - time.Nanosecond)
	if got := attempts.Load(); got != 2 {
		t.Fatalf("attempts before second retry = %d, want 2", got)
	}
	clk.Advance(time.Nanosecond)
	eventually(t, func() bool { return attempts.Load() == 3 })
	eventually(t, func() bool { return clk.hasTickerIn(time.Minute) })

	clk.Advance(2 * time.Minute)
	eventually(t, func() bool { return runs.Load() >= 3 && attempts.Load() >= 4 })
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run after cancellation: %v", err)
	}

	held := lockJob(t, pool, "ok")
	defer held()

	var lockedRuns atomic.Int32
	s2 := New(pool, clk, Job{
		Name:  "ok",
		Every: time.Minute,
		Run: func(context.Context) error {
			lockedRuns.Add(1)
			return nil
		},
	})
	ctx2, cancel2 := context.WithCancel(context.Background())
	done2 := runScheduler(s2, ctx2)
	eventually(t, func() bool { return clk.activeTickers() == 1 })
	clk.Advance(2 * time.Minute)
	time.Sleep(50 * time.Millisecond)
	if got := lockedRuns.Load(); got != 0 {
		t.Fatalf("job runs while advisory lock held = %d, want 0", got)
	}
	cancel2()
	if err := <-done2; err != nil {
		t.Fatalf("second Run after cancellation: %v", err)
	}
}

func TestJobRunsImmediatelyWhenRequested(t *testing.T) {
	pool, _ := testpg.New(t)
	clk := newFakeClock()
	var runs atomic.Int32
	s := New(pool, clk, Job{
		Name:       "startup",
		Every:      time.Hour,
		RunOnStart: true,
		Run: func(context.Context) error {
			runs.Add(1)
			return nil
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := runScheduler(s, ctx)

	eventually(t, func() bool { return runs.Load() == 1 })
	clk.Advance(time.Hour - time.Nanosecond)
	if got := runs.Load(); got != 1 {
		t.Fatalf("runs before interval = %d, want 1", got)
	}
	clk.Advance(time.Nanosecond)
	eventually(t, func() bool { return runs.Load() == 2 })
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run after cancellation: %v", err)
	}
}

func runScheduler(s *Scheduler, ctx context.Context) <-chan error {
	done := make(chan error, 1)
	go func() {
		done <- s.Run(ctx)
	}()
	return done
}

func lockJob(t *testing.T, pool *pgxpool.Pool, name string) func() {
	t.Helper()
	ctx := context.Background()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire lock connection: %v", err)
	}

	var acquired bool
	if err := conn.QueryRow(ctx, `select pg_try_advisory_lock(hashtext('supermetry:job:' || $1))`, name).Scan(&acquired); err != nil {
		conn.Release()
		t.Fatalf("lock job %q: %v", name, err)
	}
	if !acquired {
		conn.Release()
		t.Fatalf("lock job %q: already locked", name)
	}

	return func() {
		var unlocked bool
		if err := conn.QueryRow(context.Background(), `select pg_advisory_unlock(hashtext('supermetry:job:' || $1))`, name).Scan(&unlocked); err != nil {
			t.Errorf("unlock job %q: %v", name, err)
		} else if !unlocked {
			t.Errorf("unlock job %q: lock was not held", name)
		}
		conn.Release()
	}
}

func eventually(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("condition was not met before timeout")
		}
		time.Sleep(time.Millisecond)
	}
}

type fakeClock struct {
	mu      sync.Mutex
	now     time.Time
	tickers map[*fakeTicker]struct{}
}

func newFakeClock() *fakeClock {
	return &fakeClock{
		now:     time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC),
		tickers: make(map[*fakeTicker]struct{}),
	}
}

func (c *fakeClock) NewTicker(d time.Duration) Ticker {
	c.mu.Lock()
	defer c.mu.Unlock()
	ticker := &fakeTicker{
		clock: c,
		ch:    make(chan time.Time, 16),
		next:  c.now.Add(d),
		every: d,
	}
	c.tickers[ticker] = struct{}{}
	return ticker
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	for ticker := range c.tickers {
		for !ticker.stopped && !ticker.next.After(c.now) {
			ticker.ch <- ticker.next
			ticker.next = ticker.next.Add(ticker.every)
		}
	}
	c.mu.Unlock()
}

func (c *fakeClock) activeTickers() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.tickers)
}

func (c *fakeClock) hasTickerIn(d time.Duration) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for ticker := range c.tickers {
		if ticker.next.Sub(c.now) == d {
			return true
		}
	}
	return false
}

type fakeTicker struct {
	clock   *fakeClock
	ch      chan time.Time
	next    time.Time
	every   time.Duration
	stopped bool
}

func (t *fakeTicker) C() <-chan time.Time { return t.ch }

func (t *fakeTicker) Reset(d time.Duration) {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	t.every = d
	t.next = t.clock.now.Add(d)
}

func (t *fakeTicker) Stop() {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	if t.stopped {
		return
	}
	t.stopped = true
	delete(t.clock.tickers, t)
}
