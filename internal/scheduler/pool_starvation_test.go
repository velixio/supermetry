package scheduler

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"panel/internal/testpg"
)

func tinyPool(t *testing.T, original *pgxpool.Pool, size int32) *pgxpool.Pool {
	t.Helper()
	config := original.Config()
	config.MaxConns = size
	config.MinConns = 0
	config.MinIdleConns = 0
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func waitScheduler(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("scheduler did not stop")
	}
}

// Both callbacks must hold their application connection before either releases
// it. With advisory locks in that same two-slot pool, neither can get that far.
func TestConcurrentJobsProgressWithTinyApplicationPool(t *testing.T) {
	for _, size := range []int32{1, 2} {
		t.Run(fmt.Sprintf("slots_%d", size), func(t *testing.T) {
			original, schema := testpg.New(t)
			pool := tinyPool(t, original, size)
			if _, err := pool.Exec(context.Background(), "create table scheduler_probe (value int); insert into scheduler_probe values(1)"); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var entered, acquired atomic.Int32
			enteredAll, acquiredAll := make(chan struct{}), make(chan struct{})
			complete := make(chan error, int(size))
			jobs := make([]Job, 0, size)
			for i := int32(0); i < size; i++ {
				jobs = append(jobs, Job{Name: fmt.Sprintf("%s-progress-%d", schema, i), Every: time.Hour, RunOnStart: true, Run: func(ctx context.Context) error {
					if entered.Add(1) == size {
						close(enteredAll)
					}
					select {
					case <-enteredAll:
					case <-ctx.Done():
						return ctx.Err()
					}
					conn, err := pool.Acquire(ctx)
					if err != nil {
						complete <- err
						return err
					}
					defer conn.Release()
					if acquired.Add(1) == size {
						close(acquiredAll)
					}
					select {
					case <-acquiredAll:
					case <-ctx.Done():
						return ctx.Err()
					}
					var n int
					err = conn.QueryRow(ctx, "select value from scheduler_probe").Scan(&n)
					if err == nil && n != 1 {
						err = fmt.Errorf("probe=%d", n)
					}
					complete <- err
					return err
				}})
			}
			done := runScheduler(New(pool, nil, jobs...), ctx)
			t.Cleanup(func() { cancel(); waitScheduler(t, done) })
			for i := int32(0); i < size; i++ {
				select {
				case err := <-complete:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(3 * time.Second):
					t.Fatalf("jobs made no progress: entered=%d acquired=%d", entered.Load(), acquired.Load())
				}
			}
		})
	}
}

func TestCoordinationPoolCancellationClosesSessionsAndPreservesApplicationPool(t *testing.T) {
	original, schema := testpg.New(t)
	config := original.Config()
	config.MaxConns = 1
	config.MinConns = 1
	config.MinIdleConns = 1
	inherited := config.AfterConnect
	lockPID := make(chan uint32, 4)
	config.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
		if err := inherited(ctx, c); err != nil {
			return err
		}
		if strings.HasSuffix(c.Config().RuntimeParams["application_name"], "/scheduler") {
			lockPID <- c.PgConn().PID()
		}
		return nil
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	for cycle := 0; cycle < 3; cycle++ {
		ctx, cancel := context.WithCancel(context.Background())
		entered := make(chan struct{}, 2)
		job := func(ctx context.Context) error { entered <- struct{}{}; <-ctx.Done(); return ctx.Err() }
		s := New(pool, nil, Job{Name: schema + "-a", Every: time.Hour, RunOnStart: true, Run: job}, Job{Name: schema + "-b", Every: time.Hour, RunOnStart: true, Run: job})
		done := runScheduler(s, ctx)
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			cancel()
			waitScheduler(t, done)
			t.Fatal("callback did not enter")
		}
		var pid uint32
		select {
		case pid = <-lockPID:
		case <-time.After(3 * time.Second):
			cancel()
			waitScheduler(t, done)
			t.Fatal("coordination connection hook not inherited")
		}
		cancel()
		waitScheduler(t, done)
		select {
		case <-entered:
			t.Fatal("another callback started during cancellation")
		default:
		}
		eventually(t, func() bool {
			var exists bool
			err := original.QueryRow(context.Background(), "select exists(select 1 from pg_stat_activity where pid=$1)", pid).Scan(&exists)
			return err == nil && !exists
		})
		if err := pool.Ping(context.Background()); err != nil {
			t.Fatalf("application pool was closed: %v", err)
		}
		release := lockJob(t, original, schema+"-a")
		release()
		release = lockJob(t, original, schema+"-b")
		release()
	}
}

func TestIndependentSchedulersExcludeSameJobAndReleaseAfterErrors(t *testing.T) {
	pool, schema := testpg.New(t)
	firstLocks, secondLocks := tinyPool(t, pool, 1), tinyPool(t, pool, 1)
	first, second := New(pool, nil), New(pool, nil)
	entered, release := make(chan struct{}), make(chan struct{})
	finished := make(chan error, 1)
	name := schema + "-exclusive"
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go func() {
		_, err := first.tryRun(ctx, firstLocks, Job{Name: name, Run: func(ctx context.Context) error {
			close(entered)
			select {
			case <-release:
				return errors.New("job failed")
			case <-ctx.Done():
				return ctx.Err()
			}
		}})
		finished <- err
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("first job did not enter")
	}
	called := false
	job := Job{Name: name, Run: func(context.Context) error { called = true; return nil }}
	if ran, err := second.tryRun(ctx, secondLocks, job); err != nil || ran || called {
		t.Fatalf("duplicate job: ran=%v called=%v err=%v", ran, called, err)
	}
	close(release)
	if err := <-finished; err == nil || !strings.Contains(err.Error(), "job failed") {
		t.Fatalf("job error=%v", err)
	}
	for range 3 {
		if ran, err := second.tryRun(ctx, secondLocks, job); err != nil || !ran {
			t.Fatalf("lock not released: ran=%v err=%v", ran, err)
		}
	}
	firstLocks.Close()
	called = false
	if ran, err := first.tryRun(ctx, firstLocks, job); err == nil || ran || called {
		t.Fatalf("acquire error ran=%v called=%v err=%v", ran, called, err)
	}
}

func TestBrokenCoordinationSessionIsDiscarded(t *testing.T) {
	pool, schema := testpg.New(t)
	config := pool.Config()
	config.MaxConns = 1
	config.MinConns = 0
	config.MinIdleConns = 0
	config.ConnConfig.RuntimeParams["application_name"] = schema + "-locks"
	locks, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer locks.Close()
	s := New(pool, nil)
	job := Job{Name: schema + "-broken", Run: func(ctx context.Context) error {
		var terminated bool
		return pool.QueryRow(ctx, "select pg_terminate_backend(pid) from pg_stat_activity where application_name=$1", schema+"-locks").Scan(&terminated)
	}}
	if ran, err := s.tryRun(context.Background(), locks, job); !ran || err == nil || !strings.Contains(err.Error(), "unlock job") {
		t.Fatalf("broken session ran=%v err=%v", ran, err)
	}
	job.Run = func(ctx context.Context) error { return pool.Ping(ctx) }
	if ran, err := s.tryRun(context.Background(), locks, job); !ran || err != nil {
		t.Fatalf("replacement lock session ran=%v err=%v", ran, err)
	}
}

// Cancellation can obscure whether the server acquired its session lock. The
// scheduler must discard that session even when no callback was started.
func TestFailedLockQueryDiscardsUncertainSession(t *testing.T) {
	pool, schema := testpg.New(t)
	config := pool.Config()
	config.MaxConns = 1
	config.MinConns = 0
	config.MinIdleConns = 0
	config.ConnConfig.Tracer = cancelLockQuery{}
	connected := make(chan uint32, 3)
	inherited := config.AfterConnect
	config.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
		if err := inherited(ctx, c); err != nil {
			return err
		}
		connected <- c.PgConn().PID()
		return nil
	}
	locks, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer locks.Close()
	called := false
	s := New(pool, nil)
	for range 3 {
		if ran, err := s.tryRun(context.Background(), locks, Job{Name: schema + "-uncertain", Run: func(context.Context) error { called = true; return nil }}); ran || err == nil || called {
			t.Fatalf("failed lock ran=%v err=%v called=%v", ran, err, called)
		}
		var pid uint32
		select {
		case pid = <-connected:
		case <-time.After(3 * time.Second):
			t.Fatal("failed lock session reused")
		}
		eventually(t, func() bool {
			var exists bool
			err := pool.QueryRow(context.Background(), "select exists(select 1 from pg_stat_activity where pid=$1)", pid).Scan(&exists)
			return err == nil && !exists
		})
	}
}

type cancelLockQuery struct{}

func (cancelLockQuery) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.Contains(data.SQL, "pg_try_advisory_lock") {
		ctx, cancel := context.WithCancel(ctx)
		cancel()
		return ctx
	}
	return ctx
}
func (cancelLockQuery) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestMoreDueJobsThanCoordinationSlotsAllProgress(t *testing.T) {
	original, schema := testpg.New(t)
	pool := tinyPool(t, original, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	const count = 5
	completed := make(chan error, count)
	jobs := make([]Job, 0, count)
	for i := 0; i < count; i++ {
		jobs = append(jobs, Job{Name: fmt.Sprintf("%s-saturated-%d", schema, i), Every: time.Hour, RunOnStart: true, Run: func(ctx context.Context) error { err := pool.Ping(ctx); completed <- err; return err }})
	}
	done := runScheduler(New(pool, nil, jobs...), ctx)
	t.Cleanup(func() { cancel(); waitScheduler(t, done) })
	for range count {
		select {
		case err := <-completed:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("queued job did not make progress")
		}
	}
}

func TestCoordinationStartupFailureClosesConnectionsAndStops(t *testing.T) {
	original, _ := testpg.New(t)
	config := original.Config()
	config.MaxConns = 1
	config.MinConns = 0
	config.MinIdleConns = 0
	failed := make(chan uint32, 1)
	inherited := config.AfterConnect
	config.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
		if err := inherited(ctx, c); err != nil {
			return err
		}
		if strings.HasSuffix(c.Config().RuntimeParams["application_name"], "/scheduler") {
			failed <- c.PgConn().PID()
			return errors.New("coordination setup rejected")
		}
		return nil
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var called atomic.Bool
	s := New(pool, nil, Job{Name: "startup-failure", Every: time.Hour, RunOnStart: true, Run: func(context.Context) error { called.Store(true); return nil }})
	done := runScheduler(s, ctx)
	var pid uint32
	select {
	case pid = <-failed:
	case <-time.After(3 * time.Second):
		cancel()
		waitScheduler(t, done)
		t.Fatal("startup failure was not exercised")
	}
	cancel()
	waitScheduler(t, done)
	if called.Load() {
		t.Fatal("callback ran without a lock connection")
	}
	eventually(t, func() bool {
		var exists bool
		err := original.QueryRow(context.Background(), "select exists(select 1 from pg_stat_activity where pid=$1)", pid).Scan(&exists)
		return err == nil && !exists
	})
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatalf("caller pool unavailable after scheduler failure: %v", err)
	}
}
