package main

import (
	"context"
	"testing"
	"time"

	"panel/internal/scheduler"
)

type fakeAlertJobProvider struct{}

func (fakeAlertJobProvider) Job() scheduler.Job {
	return scheduler.Job{
		Name:  "alerts",
		Every: 5 * time.Minute,
		Run:   func(context.Context) error { return nil },
	}
}

func TestScheduledJobsRegistersAlertEvaluator(t *testing.T) {
	jobs := scheduledJobs(nil, nil, nil, nil, nil, nil, nil, nil, fakeAlertJobProvider{}, nil, wallClock{})
	for _, job := range jobs {
		if job.Name == "alerts" {
			if job.Every != 5*time.Minute {
				t.Fatalf("alerts interval = %v, want 5m", job.Every)
			}
			return
		}
	}
	t.Fatal("alerts job not registered")
}
