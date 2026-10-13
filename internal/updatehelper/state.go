package updatehelper

import (
	"errors"
	"fmt"
	"time"
)

type State string

const (
	StateRequested        State = "requested"
	StatePreflight        State = "preflight"
	StateDownload         State = "downloading"
	StateArtifactVerified State = "verifying"
	StateStopping         State = "stopping"
	StateBackup           State = "backing_up"
	StateActivating       State = "activating"
	StateRestarting       State = "restarting"
	StateHealthCheck      State = "verifying_health"
	StateComplete         State = "complete"
	StateFailed           State = "failed"
	StateRecoveryRequired State = "recovery_required"
)

var orderedStates = []State{
	StateRequested,
	StatePreflight,
	StateDownload,
	StateArtifactVerified,
	StateStopping,
	StateBackup,
	StateActivating,
	StateRestarting,
	StateHealthCheck,
	StateComplete,
}

var stateRanks = func() map[State]int {
	ranks := make(map[State]int, len(orderedStates))
	for index, state := range orderedStates {
		ranks[state] = index
	}
	return ranks
}()

func (s State) Valid() bool {
	_, ordered := stateRanks[s]
	return ordered || s == StateFailed || s == StateRecoveryRequired
}

func (s State) Terminal() bool {
	return s == StateComplete || s == StateFailed || s == StateRecoveryRequired
}

type Job struct {
	ID            string    `json:"id"`
	TargetVersion string    `json:"target_version"`
	State         State     `json:"state"`
	ErrorCode     string    `json:"error_code,omitempty"`
	CreatedAt     time.Time `json:"created_at,omitempty"`
	UpdatedAt     time.Time `json:"updated_at,omitempty"`
}

func (j Job) Validate() error {
	if err := validateJobID(j.ID); err != nil {
		return err
	}
	if err := validateStableVersion(j.TargetVersion); err != nil {
		return err
	}
	if !j.State.Valid() {
		return fmt.Errorf("invalid state %q", j.State)
	}
	if j.ErrorCode != "" && !errorCodePattern.MatchString(j.ErrorCode) {
		return errors.New("error code has invalid format")
	}
	return nil
}

func (j *Job) Advance(next State) error {
	if j.State.Terminal() {
		return fmt.Errorf("cannot transition terminal state %q", j.State)
	}
	currentRank, currentOrdered := stateRanks[j.State]
	nextRank, nextOrdered := stateRanks[next]
	if currentOrdered && nextOrdered && nextRank == currentRank+1 {
		j.State = next
		return nil
	}
	if next == StateFailed && currentOrdered && currentRank < stateRanks[StateActivating] {
		j.State = next
		return nil
	}
	if next == StateRecoveryRequired && currentOrdered && currentRank >= stateRanks[StateActivating] && currentRank < stateRanks[StateComplete] {
		j.State = next
		return nil
	}
	return fmt.Errorf("invalid state transition %q -> %q", j.State, next)
}

type RecoveryAction string

const (
	RecoveryNone            RecoveryAction = "none"
	RecoveryResume          RecoveryAction = "resume"
	RecoveryFailSafely      RecoveryAction = "fail_safely"
	RecoveryRequireOperator RecoveryAction = "require_operator"
)

func RecoveryFor(state State) RecoveryAction {
	rank, ordered := stateRanks[state]
	if !ordered || state.Terminal() {
		return RecoveryNone
	}
	if rank <= stateRanks[StateArtifactVerified] {
		return RecoveryResume
	}
	if rank < stateRanks[StateActivating] {
		return RecoveryFailSafely
	}
	return RecoveryRequireOperator
}
