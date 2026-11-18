// Package appmonitors keeps cron monitors and their check-ins: the ingest
// writer that records Sentry check_in items, the per-monitor state machine and
// the job that writes missed and timed-out check-ins.
package appmonitors

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"supermetry/internal/cronexpr"
)

// Check-in and monitor statuses.
const (
	StatusInProgress = "in_progress"
	StatusOK         = "ok"
	StatusError      = "error"
	StatusMissed     = "missed"
	StatusTimeout    = "timeout"
	StatusDisabled   = "disabled"

	DefaultEnvironment = "production"

	// MaxMonitorsPerSite bounds monitors created from check-in configs.
	MaxMonitorsPerSite = 200
)

// Schedule types.
const (
	ScheduleCrontab  = "crontab"
	ScheduleInterval = "interval"
)

// Outcome says whether a transition crossed a threshold.
type Outcome int

const (
	OutcomeNone Outcome = iota
	OutcomeFailure
	OutcomeRecovery
)

// State is the part of a monitor the transition function reads and writes.
type State struct {
	Status   string
	Failures int
	OKs      int
}

// Limits are the monitor's thresholds.
type Limits struct{ FailureThreshold, RecoveryThreshold int }

func isFailure(checkin string) bool {
	return checkin == StatusError || checkin == StatusMissed || checkin == StatusTimeout
}

// Transition applies one final check-in status (ok, error, missed or timeout)
// to a monitor. A failure only changes the monitor status once the number of
// consecutive failures reaches the failure threshold; a failing monitor goes
// back to ok after RecoveryThreshold consecutive ok check-ins. The outcome is
// OutcomeFailure on the check-in that took the monitor from ok to failing and
// OutcomeRecovery on the one that brought it back. Disabled monitors never
// change.
func Transition(s State, l Limits, checkin string) (State, Outcome) {
	if s.Status == StatusDisabled {
		return s, OutcomeNone
	}
	failT, recT := max(l.FailureThreshold, 1), max(l.RecoveryThreshold, 1)
	switch {
	case isFailure(checkin):
		s.Failures++
		s.OKs = 0
		if s.Failures >= failT {
			wasOK := s.Status == StatusOK
			s.Status = checkin
			if wasOK {
				return s, OutcomeFailure
			}
		}
	case checkin == StatusOK:
		if s.Status == StatusOK {
			s.Failures, s.OKs = 0, 0
			return s, OutcomeNone
		}
		s.OKs++
		if s.OKs >= recT {
			s.Status, s.Failures, s.OKs = StatusOK, 0, 0
			return s, OutcomeRecovery
		}
	}
	return s, OutcomeNone
}

// Schedule is a validated monitor schedule.
type Schedule struct {
	Type  string // crontab or interval
	Value string // the cron expression, or "N unit" for an interval
	TZ    string
	cron  cronexpr.Schedule
	loc   *time.Location
	n     int
	unit  string
}

// ParseSchedule validates a schedule. Interval values are "N unit" with unit
// minute, hour, day, week, month or year.
func ParseSchedule(typ, value, tz string) (Schedule, error) {
	if tz == "" {
		tz = "UTC"
	}
	loc, err := time.LoadLocation(tz)
	if err != nil || len(tz) > 64 {
		return Schedule{}, fmt.Errorf("unknown time zone %q", tz)
	}
	s := Schedule{Type: typ, Value: strings.TrimSpace(value), TZ: tz, loc: loc}
	switch typ {
	case ScheduleCrontab:
		if s.cron, err = cronexpr.Parse(s.Value); err != nil {
			return Schedule{}, err
		}
		if s.cron.Next(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), loc).IsZero() {
			return Schedule{}, errors.New("cron expression never matches")
		}
	case ScheduleInterval:
		num, unit, ok := strings.Cut(s.Value, " ")
		n, convErr := strconv.Atoi(num)
		if !ok || convErr != nil || n < 1 || n > 10_000 || cronexpr.NextInterval(time.Unix(0, 0), 1, unit).IsZero() {
			return Schedule{}, fmt.Errorf("invalid interval %q", value)
		}
		s.n, s.unit = n, unit
	default:
		return Schedule{}, fmt.Errorf("unknown schedule type %q", typ)
	}
	return s, nil
}

// IntervalValue formats an interval schedule value.
func IntervalValue(n int, unit string) string { return strconv.Itoa(n) + " " + unit }

// After returns the next expected time for a check-in seen at last. For a
// crontab it is the first match after last; for an interval, last plus the
// interval.
func (s Schedule) After(last time.Time) time.Time {
	if s.Type == ScheduleInterval {
		return cronexpr.NextInterval(last, s.n, s.unit)
	}
	return s.cron.Next(last, s.loc)
}

// CheckIn is one parsed check-in, ready for the writer.
type CheckIn struct {
	SiteID      int32
	Slug        string
	Environment string
	ID          string // canonical lowercase UUID
	Status      string
	DurationMS  *int64
	Release     string
	Config      *Config
	At          time.Time
}

// Config is the optional monitor_config sent with a check-in. Zero numbers mean
// "not set".
type Config struct {
	ScheduleType      string
	Schedule          string
	TZ                string
	MarginMinutes     int
	MaxRuntimeMinutes int
	FailureThreshold  int
	RecoveryThreshold int
}

// NormalizeUUID returns the canonical lowercase form of a UUID given with or
// without dashes, or false.
func NormalizeUUID(s string) (string, bool) {
	s = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), "-", ""))
	if len(s) != 32 {
		return "", false
	}
	if _, err := hex.DecodeString(s); err != nil {
		return "", false
	}
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:], true
}

// NewUUID returns a random (version 4) UUID string.
func NewUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	s := hex.EncodeToString(b[:])
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
}
