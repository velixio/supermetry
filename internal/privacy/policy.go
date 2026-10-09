// Package privacy computes value policies and bounded sanitization without I/O.
// Callers enforce these decisions at collection, persistence and read boundaries.
package privacy

import "fmt"

// Mode controls permission to collect identity. Its zero value is anonymous.
// Identified permits identity only when a project also opts in; it says nothing
// about replay, consent, or whether arbitrary payloads contain personal data.
type Mode uint8

const (
	Anonymous Mode = iota
	Identified
)

func (m Mode) String() string {
	switch m {
	case Anonymous:
		return "anonymous"
	case Identified:
		return "identified"
	default:
		return "invalid"
	}
}

// ReplayRules contains concrete collection limits, not optional overrides.
// SampleRate is a percentage, RetentionDays is days, QuotaBytes is bytes.
// Zero is a strict zero cap, never unset or unlimited. A future adapter must
// resolve missing stored/env values before constructing these rules and must
// reject or suppress collection when a zero storage cap cannot be honored.
// These rules do not change the existing stored replay settings schema.
type ReplayRules struct {
	Mask          bool
	SampleRate    int
	RetentionDays int
	QuotaBytes    int64
}

// Tighten combines validated rules monotonically. Order does not matter: a
// masking requirement survives and each numeric limit takes the smaller cap.
func (r ReplayRules) Tighten(other ReplayRules) ReplayRules {
	return ReplayRules{
		Mask:          r.Mask || other.Mask,
		SampleRate:    min(r.SampleRate, other.SampleRate),
		RetentionDays: min(r.RetentionDays, other.RetentionDays),
		QuotaBytes:    min(r.QuotaBytes, other.QuotaBytes),
	}
}

func (r ReplayRules) validate() error {
	switch {
	case r.SampleRate < 0 || r.SampleRate > 100:
		return fmt.Errorf("sample rate must be from 0 to 100")
	case r.RetentionDays < 0 || r.RetentionDays > 365:
		return fmt.Errorf("retention days must be from 0 to 365")
	case r.QuotaBytes < 0:
		return fmt.Errorf("quota bytes must be nonnegative")
	default:
		return nil
	}
}

// Instance sets permission ceilings. ReplayAllowed is independent of Mode.
type Instance struct {
	Mode          Mode
	ReplayAllowed bool
	Replay        ReplayRules
}

// Project is the policy projection of existing sites.Site settings, not a
// replacement site model. ProfilesEnabled maps to Site.ProfilesEnabled and
// ReplayEnabled maps to Site.Replay.Enabled.
type Project struct {
	ProfilesEnabled bool
	ReplayEnabled   bool
	Replay          ReplayRules
}

// Effective describes collection permission and limits after intersection.
// ReplayEnabled expresses permission only; sampling, storage capacity, consent
// and privacy-signal checks can still prevent a recording.
type Effective struct {
	Mode          Mode
	ReplayEnabled bool
	Replay        ReplayRules
}

// Reason explains a disabled gate or an instance rule that tightened a project
// value. Multiple reasons may apply. These are internal codes, not API strings.
type Reason string

const (
	InstanceAnonymous       Reason = "instance_anonymous"
	ProjectProfilesDisabled Reason = "project_profiles_disabled"
	InstanceReplayDisabled  Reason = "instance_replay_disabled"
	ProjectReplayDisabled   Reason = "project_replay_disabled"
	MaskingRequired         Reason = "instance_masking_required"
	SampleRateCapped        Reason = "instance_sample_rate_cap"
	RetentionCapped         Reason = "instance_retention_cap"
	QuotaCapped             Reason = "instance_quota_cap"
)

// Decision preserves configured values alongside the effective policy. Reasons
// include every disabled gate, even if both layers disable the same feature;
// cap reasons appear only when the instance tightens the project's value.
type Decision struct {
	Instance     Instance
	Project      Project
	Effective    Effective
	Restrictions []Reason
}

// Evaluate validates and intersects immutable value inputs. It performs no DB,
// environment, clock or network work. Invalid input returns a zero decision and
// an error; callers must handle the error before using a decision. No production
// caller uses this foundation yet.
func Evaluate(instance Instance, project Project) (Decision, error) {
	if instance.Mode != Anonymous && instance.Mode != Identified {
		return Decision{}, fmt.Errorf("invalid tracking mode: %d", instance.Mode)
	}
	if err := instance.Replay.validate(); err != nil {
		return Decision{}, fmt.Errorf("instance replay: %w", err)
	}
	if err := project.Replay.validate(); err != nil {
		return Decision{}, fmt.Errorf("project replay: %w", err)
	}
	d := Decision{
		Instance: instance, Project: project,
		Effective: Effective{
			ReplayEnabled: instance.ReplayAllowed && project.ReplayEnabled,
			Replay:        instance.Replay.Tighten(project.Replay),
		},
	}
	if instance.Mode == Identified && project.ProfilesEnabled {
		d.Effective.Mode = Identified
	}
	restrict := func(when bool, reason Reason) {
		if when {
			d.Restrictions = append(d.Restrictions, reason)
		}
	}
	restrict(instance.Mode == Anonymous, InstanceAnonymous)
	restrict(!project.ProfilesEnabled, ProjectProfilesDisabled)
	restrict(!instance.ReplayAllowed, InstanceReplayDisabled)
	restrict(!project.ReplayEnabled, ProjectReplayDisabled)
	restrict(d.Effective.Replay.Mask != project.Replay.Mask, MaskingRequired)
	restrict(d.Effective.Replay.SampleRate < project.Replay.SampleRate, SampleRateCapped)
	restrict(d.Effective.Replay.RetentionDays < project.Replay.RetentionDays, RetentionCapped)
	restrict(d.Effective.Replay.QuotaBytes < project.Replay.QuotaBytes, QuotaCapped)
	return d, nil
}
