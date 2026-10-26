package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"supermetry/internal/health"
	"supermetry/internal/storageusage"
)

type storageUsageStepperStub struct {
	results   []storageusage.StepResult
	err       error
	errResult storageusage.StepResult
	now       time.Time
	calls     int
	status    storageusage.Status
}

func (s *storageUsageStepperStub) Step(_ context.Context, now time.Time) (storageusage.StepResult, error) {
	s.now = now
	if s.err != nil {
		s.calls++
		return s.errResult, s.err
	}
	result := s.results[min(s.calls, len(s.results)-1)]
	s.calls++
	return result, nil
}

func (s *storageUsageStepperStub) Status(context.Context) (storageusage.Status, error) {
	return s.status, nil
}

func TestStorageUsageJobPublishesMetrics(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	stepper := &storageUsageStepperStub{results: []storageusage.StepResult{{Published: true, Pending: 0}}, status: storageusage.Status{LastCompletedAt: &now}}
	job := storageUsageJob(stepper, partitionClock{now: now})
	if job.Name != "storage-usage" || job.Every != time.Minute || !job.RunOnStart {
		t.Fatalf("job = %+v", job)
	}
	if err := job.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !stepper.now.Equal(now) {
		t.Fatalf("Step now = %v, want %v", stepper.now, now)
	}
	if got := health.StorageUsageLastSuccessUnix.Load(); got != float64(now.Unix()) {
		t.Fatalf("last success = %v, want %v", got, now.Unix())
	}
	if got := health.StorageUsageRunActive.Load(); got != 0 {
		t.Fatalf("run active = %v, want 0", got)
	}
}

func TestStorageUsageJobProcessesSeveralBoundedUnits(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	stepper := &storageUsageStepperStub{results: []storageusage.StepResult{
		{ProcessedKey: "one", Pending: 2},
		{ProcessedKey: "two", Pending: 1},
		{Published: true},
	}}
	if err := storageUsageJob(stepper, partitionClock{now: now}).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if stepper.calls != 3 {
		t.Fatalf("Step calls = %d, want 3", stepper.calls)
	}
}

func TestStorageUsageJobInitializesMetricsFromPersistedStatus(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	completed := now.Add(-time.Hour)
	started := now.Add(-10 * time.Minute)
	stepper := &storageUsageStepperStub{
		results: []storageusage.StepResult{{Idle: true}},
		status:  storageusage.Status{LastCompletedAt: &completed, ActiveStartedAt: &started, Pending: 9},
	}
	if err := storageUsageJob(stepper, partitionClock{now: now}).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := health.StorageUsageLastSuccessUnix.Load(); got != float64(completed.Unix()) {
		t.Fatalf("last success = %v, want %v", got, completed.Unix())
	}
	if got := health.StorageUsagePendingSources.Load(); got != 9 {
		t.Fatalf("pending = %v, want 9", got)
	}
	if got := health.StorageUsageRunDurationSeconds.Load(); got != 600 {
		t.Fatalf("run duration = %v, want 600", got)
	}
}

func TestStorageUsageJobRecordsFailure(t *testing.T) {
	before := health.StorageUsageFailures.Load()
	stepper := &storageUsageStepperStub{results: []storageusage.StepResult{{}}, err: errors.New("scan failed")}
	job := storageUsageJob(stepper, partitionClock{now: time.Now()})
	if err := job.Run(context.Background()); !errors.Is(err, stepper.err) {
		t.Fatalf("Run error = %v, want scan failure", err)
	}
	if got := health.StorageUsageFailures.Load(); got != before+1 {
		t.Fatalf("failures = %d, want %d", got, before+1)
	}
}

func TestStorageUsageJobCountsAndReportsUnitThatExceedsTheBudget(t *testing.T) {
	failuresBefore := health.StorageUsageFailures.Load()
	timeoutsBefore := health.StorageUsageUnitTimeouts.Load()
	stepper := &storageUsageStepperStub{
		err:       fmt.Errorf("scan storage work postgres:events:big: %w", context.DeadlineExceeded),
		errResult: storageusage.StepResult{FailedKey: "postgres:events:big"},
	}
	var logs bytes.Buffer
	job := storageUsageJobWithLogger(stepper, partitionClock{now: time.Now()}, slog.New(slog.NewTextHandler(&logs, nil)))
	for attempt := 1; attempt <= 3; attempt++ {
		if err := job.Run(context.Background()); err != nil {
			t.Fatalf("attempt %d: Run() = %v; a budget overrun is reported through metrics, not as a crash", attempt, err)
		}
		if got := health.StorageUsageStuckAttempts.Load(); got != float64(attempt) {
			t.Fatalf("attempt %d: stuck attempts = %v, want %d", attempt, got, attempt)
		}
	}
	if got := health.StorageUsageFailures.Load(); got != failuresBefore+3 {
		t.Errorf("failures = %d, want %d", got, failuresBefore+3)
	}
	if got := health.StorageUsageUnitTimeouts.Load(); got != timeoutsBefore+3 {
		t.Errorf("unit timeouts = %d, want %d", got, timeoutsBefore+3)
	}
	if !strings.Contains(logs.String(), "postgres:events:big") {
		t.Errorf("log does not name the unit: %s", logs.String())
	}

	// Progress on another unit clears the stuck counter.
	stepper.err, stepper.calls = nil, 0
	stepper.results = []storageusage.StepResult{{ProcessedKey: "other", Pending: 1}, {Idle: true}}
	if err := job.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := health.StorageUsageStuckAttempts.Load(); got != 0 {
		t.Errorf("stuck attempts after progress = %v, want 0", got)
	}
}

func TestStorageUsageJobTreatsBudgetEndAfterProgressAsNormal(t *testing.T) {
	failuresBefore := health.StorageUsageFailures.Load()
	stepper := &budgetThenDeadlineStepper{}
	if err := storageUsageJob(stepper, partitionClock{now: time.Now()}).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := health.StorageUsageFailures.Load(); got != failuresBefore {
		t.Errorf("failures = %d, want unchanged %d: the budget simply ran out after progress", got, failuresBefore)
	}
}

type budgetThenDeadlineStepper struct {
	storageUsageStepperStub
}

func (s *budgetThenDeadlineStepper) Step(context.Context, time.Time) (storageusage.StepResult, error) {
	s.calls++
	if s.calls == 1 {
		return storageusage.StepResult{ProcessedKey: "done", Pending: 5}, nil
	}
	return storageusage.StepResult{FailedKey: "next"}, fmt.Errorf("scan: %w", context.DeadlineExceeded)
}
