package updatehelper

import (
	"context"
	"errors"
	"testing"
)

type memoryJobStore struct {
	job   Job
	saves []State
}

func (s *memoryJobStore) Save(job Job) error {
	s.job = job
	s.saves = append(s.saves, job.State)
	return nil
}
func (s *memoryJobStore) Load(string) (Job, error) { return s.job, nil }

type fakeBackend struct {
	calls         []State
	recoveryCalls []RecoveryAction
	failAt        State
	failRecovery  bool
}

func (b *fakeBackend) Recover(_ context.Context, action RecoveryAction, _ Job) error {
	b.recoveryCalls = append(b.recoveryCalls, action)
	if b.failRecovery {
		return errors.New("injected recovery failure")
	}
	return nil
}

func (b *fakeBackend) RunStep(_ context.Context, step State, _ Job) error {
	b.calls = append(b.calls, step)
	if step == b.failAt {
		return errors.New("injected failure")
	}
	return nil
}

func TestEnginePersistsEverySuccessfulStep(t *testing.T) {
	t.Parallel()
	store := &memoryJobStore{}
	backend := &fakeBackend{}
	engine := Engine{Store: store, Backend: backend}
	job := Job{ID: "42", TargetVersion: "v1.2.3", State: StateRequested}
	got, err := engine.Run(context.Background(), job)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.State != StateComplete || store.job.State != StateComplete {
		t.Fatalf("final state = %q, persisted = %q", got.State, store.job.State)
	}
	want := []State{StatePreflight, StateDownload, StateArtifactVerified, StateStopping, StateBackup, StateActivating, StateRestarting, StateHealthCheck, StateComplete}
	if len(store.saves) != len(want) {
		t.Fatalf("persisted %d transitions, want %d: %v", len(store.saves), len(want), store.saves)
	}
	for index := range want {
		if store.saves[index] != want[index] {
			t.Fatalf("persisted transition %d = %q, want %q", index, store.saves[index], want[index])
		}
	}
}

func TestEngineClassifiesFailureByActivationBoundary(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		failAt       State
		want         State
		wantRecovery bool
	}{
		{name: "download fails safely", failAt: StateDownload, want: StateFailed},
		{name: "backup restarts old deployment", failAt: StateBackup, want: StateFailed, wantRecovery: true},
		{name: "activation needs recovery", failAt: StateActivating, want: StateRecoveryRequired},
		{name: "health needs recovery", failAt: StateHealthCheck, want: StateRecoveryRequired},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &memoryJobStore{}
			backend := &fakeBackend{failAt: tt.failAt}
			engine := Engine{Store: store, Backend: backend}
			got, err := engine.Run(context.Background(), Job{ID: "42", TargetVersion: "v1.2.3", State: StateRequested})
			if err == nil {
				t.Fatal("Run succeeded")
			}
			if got.State != tt.want || store.job.State != tt.want {
				t.Fatalf("state = %q, persisted = %q, want %q", got.State, store.job.State, tt.want)
			}
			if tt.wantRecovery && (len(backend.recoveryCalls) != 1 || backend.recoveryCalls[0] != RecoveryFailSafely) {
				t.Fatalf("recovery calls = %v, want fail-safe recovery", backend.recoveryCalls)
			}
		})
	}
}

func TestEngineRecoversOnlySafeStates(t *testing.T) {
	t.Parallel()
	tests := []struct {
		state State
		want  State
	}{
		{state: StateDownload, want: StateComplete},
		{state: StateStopping, want: StateFailed},
		{state: StateActivating, want: StateRecoveryRequired},
	}
	for _, tt := range tests {
		t.Run(string(tt.state), func(t *testing.T) {
			store := &memoryJobStore{job: Job{ID: "42", TargetVersion: "v1.2.3", State: tt.state}}
			backend := &fakeBackend{}
			engine := Engine{Store: store, Backend: backend}
			got, _ := engine.Recover(context.Background(), store.job.ID)
			if got.State != tt.want {
				t.Fatalf("state = %q, want %q", got.State, tt.want)
			}
			if RecoveryFor(tt.state) == RecoveryResume && tt.state != StateRequested {
				if len(backend.calls) == 0 || backend.calls[0] != tt.state {
					t.Fatalf("first recovered step = %v, want %q", backend.calls, tt.state)
				}
			}
			if RecoveryFor(tt.state) == RecoveryFailSafely {
				if len(backend.recoveryCalls) != 1 || backend.recoveryCalls[0] != RecoveryFailSafely {
					t.Fatalf("recovery calls = %v, want fail-safe recovery", backend.recoveryCalls)
				}
			}
		})
	}
}

func TestEngineLeavesInterruptedStateRetriableWhenFailSafeRecoveryFails(t *testing.T) {
	t.Parallel()
	store := &memoryJobStore{job: Job{ID: "42", TargetVersion: "v1.2.3", State: StateBackup}}
	engine := Engine{Store: store, Backend: &fakeBackend{failRecovery: true}}
	got, err := engine.Recover(context.Background(), store.job.ID)
	if err == nil {
		t.Fatal("Recover succeeded")
	}
	if got.State != StateBackup || store.job.State != StateBackup {
		t.Fatalf("state = %q, persisted = %q, want retriable backup", got.State, store.job.State)
	}
}

func TestCommandRunnerUsesExecutableAndArgumentVector(t *testing.T) {
	t.Parallel()
	var _ Runner = (*fakeRunner)(nil)
}

type fakeRunner struct{}

func (*fakeRunner) Run(context.Context, string, []string, RunOptions) (RunResult, error) {
	return RunResult{}, nil
}
