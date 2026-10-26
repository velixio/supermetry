package main

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"supermetry/internal/health"
	"supermetry/internal/scheduler"
	"supermetry/internal/storageusage"
)

type storageUsageStepper interface {
	Step(context.Context, time.Time) (storageusage.StepResult, error)
	Status(context.Context) (storageusage.Status, error)
}

const (
	storageUsageMaxSteps  = 20
	storageUsageJobBudget = 45 * time.Second
)

func storageUsageJob(stepper storageUsageStepper, clk clock) scheduler.Job {
	return storageUsageJobWithLogger(stepper, clk, slog.Default())
}

// isUnitTimeout reports a scan that ran out of time: the job budget or the
// per-statement timeout.
func isUnitTimeout(err error) bool {
	var pgErr *pgconn.PgError
	return errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &pgErr) && pgErr.Code == "57014")
}

func storageUsageJobWithLogger(stepper storageUsageStepper, clk clock, log *slog.Logger) scheduler.Job {
	var stuckKey string
	var stuckAttempts int
	return scheduler.Job{
		Name:       "storage-usage",
		Every:      time.Minute,
		RunOnStart: true,
		Run: func(ctx context.Context) error {
			now := clk.Now()
			if status, err := stepper.Status(ctx); err == nil {
				setStorageUsageMetrics(status, now)
			}
			workCtx, cancel := context.WithTimeout(ctx, storageUsageJobBudget)
			defer cancel()
			progressed := false
			for range storageUsageMaxSteps {
				result, err := stepper.Step(workCtx, now)
				if err != nil {
					timedOut := isUnitTimeout(err) && ctx.Err() == nil
					if timedOut && progressed {
						// The budget ran out after earlier units finished; the
						// next run resumes here. Not a failure.
						break
					}
					health.StorageUsageFailures.Add(1)
					if timedOut {
						health.StorageUsageUnitTimeouts.Add(1)
					}
					if result.FailedKey != "" && result.FailedKey == stuckKey {
						stuckAttempts++
					} else {
						stuckKey, stuckAttempts = result.FailedKey, 1
					}
					health.StorageUsageStuckAttempts.Store(float64(stuckAttempts))
					log.Warn("storage usage unit failed", "unit", result.FailedKey, "attempts", stuckAttempts, "budget_exceeded", timedOut, "error", err)
					if timedOut {
						// Retried next minute; the stuck gauge and counters
						// make a unit that can never finish visible.
						return nil
					}
					return err
				}
				progressed = progressed || result.ProcessedKey != "" || result.Published
				if progressed {
					stuckKey, stuckAttempts = "", 0
					health.StorageUsageStuckAttempts.Store(0)
				}
				if result.Idle || result.Published {
					break
				}
			}
			status, err := stepper.Status(ctx)
			if err != nil {
				health.StorageUsageFailures.Add(1)
				return err
			}
			setStorageUsageMetrics(status, now)
			return nil
		},
	}
}

func setStorageUsageMetrics(status storageusage.Status, now time.Time) {
	if status.LastCompletedAt == nil {
		health.StorageUsageLastSuccessUnix.Store(0)
	} else {
		health.StorageUsageLastSuccessUnix.Store(float64(status.LastCompletedAt.Unix()))
	}
	health.StorageUsagePendingSources.Store(float64(status.Pending))
	if status.ActiveStartedAt == nil {
		health.StorageUsageRunActive.Store(0)
		health.StorageUsageRunDurationSeconds.Store(0)
		return
	}
	health.StorageUsageRunActive.Store(1)
	duration := now.Sub(*status.ActiveStartedAt).Seconds()
	if duration < 0 {
		duration = 0
	}
	health.StorageUsageRunDurationSeconds.Store(duration)
}
