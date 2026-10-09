// Package scheduler runs periodic in-process jobs with PostgreSQL advisory-lock
// coordination between Panel instances.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	initialBackoff = time.Second
	maximumBackoff = 5 * time.Minute
	unlockTimeout  = 5 * time.Second
)

// Clock creates tickers used to schedule jobs. It is replaceable in tests.
type Clock interface {
	NewTicker(time.Duration) Ticker
}

// Ticker delivers scheduled times and can be reset or stopped.
type Ticker interface {
	C() <-chan time.Time
	Reset(time.Duration)
	Stop()
}

// Job is a named periodic operation.
type Job struct {
	Name       string
	Every      time.Duration
	RunOnStart bool
	Run        func(ctx context.Context) error
}

// Scheduler runs jobs against a shared PostgreSQL pool.
type Scheduler struct {
	pool  *pgxpool.Pool
	clock Clock
	jobs  []Job
}

// New returns a scheduler for jobs. A nil clock uses the wall clock.
func New(pool *pgxpool.Pool, clk Clock, jobs ...Job) *Scheduler {
	if clk == nil {
		clk = wallClock{}
	}
	return &Scheduler{
		pool:  pool,
		clock: clk,
		jobs:  append([]Job(nil), jobs...),
	}
}

// Run starts one goroutine per job and blocks until ctx is cancelled.
// Advisory locks use a private coordination pool: retaining application-pool
// connections while callbacks acquire from that same pool can exhaust it.
// At most min(number of jobs, application MaxConns) extra connections are used.
// PostgreSQL must have capacity for both pools. Callbacks must honor cancellation.
func (s *Scheduler) Run(ctx context.Context) error {
	if len(s.jobs) == 0 || ctx.Err() != nil {
		<-ctx.Done()
		return nil
	}
	config := s.pool.Config()
	config.MaxConns = min(config.MaxConns, int32(len(s.jobs)))
	config.MinConns, config.MinIdleConns = 0, 0
	if config.ConnConfig.RuntimeParams == nil {
		config.ConnConfig.RuntimeParams = make(map[string]string)
	}
	config.ConnConfig.RuntimeParams["application_name"] += "/scheduler"
	locks, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return fmt.Errorf("open scheduler coordination pool: %w", err)
	}
	// Workers release their lock sessions before this pool is closed. Closing
	// first could let another instance enter a still-running callback.
	defer locks.Close()
	var workers sync.WaitGroup
	for _, job := range s.jobs {
		job := job
		workers.Go(func() {
			s.runJob(ctx, locks, job)
		})
	}

	<-ctx.Done()
	workers.Wait()
	return nil
}

func (s *Scheduler) runJob(ctx context.Context, locks *pgxpool.Pool, job Job) {
	ticker := s.clock.NewTicker(job.Every)
	defer ticker.Stop()

	backoff := initialBackoff
	if job.RunOnStart {
		select {
		case <-ctx.Done():
			return
		default:
		}
		ran, err := s.tryRun(ctx, locks, job)
		if err != nil {
			if ctx.Err() == nil {
				slog.ErrorContext(ctx, "scheduled job failed", "job", job.Name, "error", err)
			}
			ticker.Reset(backoff)
			backoff = min(backoff*2, maximumBackoff)
		} else if ran {
			backoff = initialBackoff
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C():
			ran, err := s.tryRun(ctx, locks, job)
			if err != nil {
				if ctx.Err() == nil {
					slog.ErrorContext(ctx, "scheduled job failed", "job", job.Name, "error", err)
				}
				ticker.Reset(backoff)
				backoff = min(backoff*2, maximumBackoff)
				continue
			}
			if ran {
				backoff = initialBackoff
			}
			ticker.Reset(job.Every)
		}
	}
}

func (s *Scheduler) tryRun(ctx context.Context, locks *pgxpool.Pool, job Job) (ran bool, resultErr error) {
	conn, err := locks.Acquire(ctx)
	if err != nil {
		return false, fmt.Errorf("acquire connection for job %q: %w", job.Name, err)
	}
	defer conn.Release()

	var acquired bool
	if err := conn.QueryRow(ctx, `select pg_try_advisory_lock(hashtext('panel:job:' || $1))`, job.Name).Scan(&acquired); err != nil {
		// The server may have acquired the session lock before a cancellation
		// or transport error hid the result. Never return an uncertain session.
		cleanupCtx, cancel := context.WithTimeout(context.Background(), unlockTimeout)
		defer cancel()
		closeErr := conn.Conn().Close(cleanupCtx)
		return false, errors.Join(fmt.Errorf("lock job %q: %w", job.Name, err), closeErr)
	}
	if !acquired {
		return false, nil
	}

	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), unlockTimeout)
		defer cancel()

		var unlocked bool
		unlockErr := conn.QueryRow(cleanupCtx, `select pg_advisory_unlock(hashtext('panel:job:' || $1))`, job.Name).Scan(&unlocked)
		if unlockErr == nil && unlocked {
			return
		}
		if unlockErr == nil {
			unlockErr = errors.New("advisory lock was not held")
		}
		closeErr := conn.Conn().Close(cleanupCtx)
		resultErr = errors.Join(resultErr, fmt.Errorf("unlock job %q: %w", job.Name, unlockErr), closeErr)
	}()

	if err := job.Run(ctx); err != nil {
		return true, fmt.Errorf("run job %q: %w", job.Name, err)
	}
	return true, nil
}

type wallClock struct{}

func (wallClock) NewTicker(d time.Duration) Ticker {
	return wallTicker{Ticker: time.NewTicker(d)}
}

type wallTicker struct {
	*time.Ticker
}

func (t wallTicker) C() <-chan time.Time { return t.Ticker.C }
