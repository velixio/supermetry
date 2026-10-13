package updatehelper

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type RunOptions struct {
	Dir string
	Env []string
}

type RunResult struct {
	ExitCode int
	Stdout   []byte
	Stderr   []byte
}

// Runner deliberately separates the executable from its argument vector. A
// backend must never pass either through a shell.
type Runner interface {
	Run(ctx context.Context, executable string, args []string, options RunOptions) (RunResult, error)
}

// Backend implements one fixed deployment mode. Each call must be idempotent
// for states that RecoveryFor classifies as resumable.
type Backend interface {
	RunStep(ctx context.Context, step State, job Job) error
	Recover(ctx context.Context, action RecoveryAction, job Job) error
}

type Engine struct {
	Store   JobStore
	Backend Backend
	Now     func() time.Time
}

func (e Engine) Run(ctx context.Context, job Job) (Job, error) {
	if e.Store == nil || e.Backend == nil {
		return job, errors.New("update engine is not configured")
	}
	if err := job.Validate(); err != nil {
		return job, fmt.Errorf("validate job: %w", err)
	}
	startRank, ordered := stateRanks[job.State]
	if !ordered || job.State.Terminal() {
		return job, fmt.Errorf("cannot run job in state %q", job.State)
	}
	return e.runFollowingSteps(ctx, job, startRank+1)
}

func (e Engine) runFollowingSteps(ctx context.Context, job Job, startIndex int) (Job, error) {
	for index := startIndex; index < len(orderedStates); index++ {
		next := orderedStates[index]
		if err := job.Advance(next); err != nil {
			return job, err
		}
		e.stamp(&job)
		if err := e.Store.Save(job); err != nil {
			return job, fmt.Errorf("persist %s: %w", next, err)
		}
		if next == StateComplete {
			return job, nil
		}
		if err := e.Backend.RunStep(ctx, next, job); err != nil {
			return e.failStep(ctx, job, next, err)
		}
	}
	return job, nil
}

func (e Engine) failStep(ctx context.Context, job Job, step State, stepErr error) (Job, error) {
	failedState := StateFailed
	if stateRanks[step] >= stateRanks[StateActivating] {
		failedState = StateRecoveryRequired
	} else if stateRanks[step] >= stateRanks[StateStopping] {
		if recoveryErr := e.Backend.Recover(ctx, RecoveryFailSafely, job); recoveryErr != nil {
			return job, errors.Join(fmt.Errorf("run %s: %w", step, stepErr), fmt.Errorf("recover old deployment: %w", recoveryErr))
		}
	}
	if transitionErr := job.Advance(failedState); transitionErr != nil {
		return job, errors.Join(stepErr, transitionErr)
	}
	job.ErrorCode = "step_failed"
	e.stamp(&job)
	if persistErr := e.Store.Save(job); persistErr != nil {
		return job, errors.Join(fmt.Errorf("run %s: %w", step, stepErr), fmt.Errorf("persist failure: %w", persistErr))
	}
	return job, fmt.Errorf("run %s: %w", step, stepErr)
}

func (e Engine) Recover(ctx context.Context, jobID string) (Job, error) {
	if e.Store == nil {
		return Job{}, errors.New("update engine is not configured")
	}
	job, err := e.Store.Load(jobID)
	if err != nil {
		return Job{}, err
	}
	switch RecoveryFor(job.State) {
	case RecoveryResume:
		if e.Backend == nil {
			return job, errors.New("update backend is not configured")
		}
		if job.State == StateRequested {
			return e.Run(ctx, job)
		}
		// A persisted non-terminal state records that its operation was about
		// to start. Safe pre-activation operations are idempotent and must be
		// rerun; advancing first would silently skip interrupted work.
		if err := e.Backend.RunStep(ctx, job.State, job); err != nil {
			return e.failStep(ctx, job, job.State, err)
		}
		return e.runFollowingSteps(ctx, job, stateRanks[job.State]+1)
	case RecoveryFailSafely:
		if e.Backend == nil {
			return job, errors.New("update backend is not configured")
		}
		if err := e.Backend.Recover(ctx, RecoveryFailSafely, job); err != nil {
			return job, fmt.Errorf("recover old deployment: %w", err)
		}
		if err := job.Advance(StateFailed); err != nil {
			return job, err
		}
		job.ErrorCode = "interrupted_before_activation"
	case RecoveryRequireOperator:
		if err := job.Advance(StateRecoveryRequired); err != nil {
			return job, err
		}
		job.ErrorCode = "interrupted_during_activation"
	case RecoveryNone:
		return job, nil
	default:
		return job, fmt.Errorf("unknown recovery action for state %q", job.State)
	}
	e.stamp(&job)
	if err := e.Store.Save(job); err != nil {
		return job, err
	}
	return job, errors.New(job.ErrorCode)
}

func (e Engine) stamp(job *Job) {
	now := time.Now().UTC()
	if e.Now != nil {
		now = e.Now().UTC()
	}
	if job.CreatedAt.IsZero() {
		job.CreatedAt = now
	}
	job.UpdatedAt = now
}
