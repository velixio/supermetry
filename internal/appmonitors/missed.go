package appmonitors

import (
	"context"
	"log/slog"
	"time"

	"supermetry/internal/scheduler"
)

// JobName is the scheduler job that writes missed and timed-out check-ins.
const JobName = "app-monitors"

// Job returns the app-monitors job: every minute it writes `missed` for
// monitors whose next slot plus margin has passed with no check-in and
// `timeout` for in-progress check-ins older than the max runtime. The
// scheduler holds an advisory lock around it like every job.
func Job(store *Store) scheduler.Job {
	return scheduler.Job{
		Name:  JobName,
		Every: time.Minute,
		Run: func(ctx context.Context) error {
			missed, timeouts, err := store.Sweep(ctx, store.now())
			if missed+timeouts > 0 {
				slog.Info("cron monitors swept", "missed", missed, "timeouts", timeouts)
			}
			return err
		},
	}
}
