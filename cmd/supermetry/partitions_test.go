package main

import (
	"context"
	"testing"
	"time"

	"supermetry/internal/config"
	"supermetry/internal/db"
	"supermetry/internal/testpg"
)

type partitionClock struct{ now time.Time }

func (c partitionClock) Now() time.Time { return c.now }

func TestPartitionsJobCoversTwoPreviousUTCDays(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	jobs := scheduledJobs(pool, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, config.ReplayEnv{}, "", nil, nil, partitionClock{now: now})
	for _, job := range jobs {
		if job.Name != "partitions" {
			continue
		}
		if err := job.Run(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `
insert into events(site_id, retention_class, ts, visitor_id, session_id, kind)
values (1, 30, $1, 1, 1, 0)`, now.AddDate(0, 0, -2)); err != nil {
			t.Fatalf("two-day-old event did not route to a partition: %v", err)
		}
		return
	}
	t.Fatal("partitions job not registered")
}
