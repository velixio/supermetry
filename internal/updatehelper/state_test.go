package updatehelper

import "testing"

func TestJobAdvanceAllowsOnlyExplicitMonotonicTransitions(t *testing.T) {
	t.Parallel()

	job := Job{ID: "42", TargetVersion: "v1.2.3", State: StateRequested}
	for _, next := range []State{StatePreflight, StateDownload, StateArtifactVerified, StateStopping, StateBackup, StateActivating, StateRestarting, StateHealthCheck, StateComplete} {
		if err := job.Advance(next); err != nil {
			t.Fatalf("advance to %q: %v", next, err)
		}
	}
	if err := job.Advance(StateBackup); err == nil {
		t.Fatal("accepted backwards transition")
	}
	if err := job.Advance(StateFailed); err == nil {
		t.Fatal("accepted transition from terminal state")
	}
}

func TestJobAdvanceRejectsSkippedAndRepeatedTransitions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		from State
		to   State
	}{
		{name: "skip", from: StateRequested, to: StateDownload},
		{name: "repeat", from: StatePreflight, to: StatePreflight},
		{name: "backward", from: StateDownload, to: StatePreflight},
		{name: "fail after activation", from: StateRestarting, to: StateFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job := Job{State: tt.from}
			if err := job.Advance(tt.to); err == nil {
				t.Fatalf("accepted transition %q -> %q", tt.from, tt.to)
			}
		})
	}
}

func TestJobAdvanceAllowsFailureAndRecoveryRequiredAtSafeBoundaries(t *testing.T) {
	t.Parallel()

	beforeActivation := Job{State: StateBackup}
	if err := beforeActivation.Advance(StateFailed); err != nil {
		t.Fatalf("pre-activation failure: %v", err)
	}
	afterActivation := Job{State: StateActivating}
	if err := afterActivation.Advance(StateRecoveryRequired); err != nil {
		t.Fatalf("activation recovery: %v", err)
	}
	if err := (&Job{State: StateRequested}).Advance(StateRecoveryRequired); err == nil {
		t.Fatal("accepted recovery-required before activation")
	}
}

func TestRecoveryAction(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		state State
		want  RecoveryAction
	}{
		{name: "requested", state: StateRequested, want: RecoveryResume},
		{name: "verified", state: StateArtifactVerified, want: RecoveryResume},
		{name: "stopping", state: StateStopping, want: RecoveryFailSafely},
		{name: "backup", state: StateBackup, want: RecoveryFailSafely},
		{name: "activating", state: StateActivating, want: RecoveryRequireOperator},
		{name: "health", state: StateHealthCheck, want: RecoveryRequireOperator},
		{name: "complete", state: StateComplete, want: RecoveryNone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RecoveryFor(tt.state); got != tt.want {
				t.Fatalf("RecoveryFor(%q) = %q, want %q", tt.state, got, tt.want)
			}
		})
	}
}
