package main

import (
	"context"
	"errors"
	"time"

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
			for range storageUsageMaxSteps {
				result, err := stepper.Step(workCtx, now)
				if err != nil {
					if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
						break
					}
					health.StorageUsageFailures.Add(1)
					return err
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
