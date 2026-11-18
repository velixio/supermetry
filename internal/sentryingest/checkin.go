package sentryingest

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"supermetry/internal/appmonitors"
)

// MaxCheckInBytes bounds one check_in item payload.
const MaxCheckInBytes = 16 << 10

// CheckInQueue takes parsed check-ins for the monitor writer. Enqueue must not
// block and must not read the database; it counts its own drops. A nil queue
// discards check-ins.
type CheckInQueue interface {
	Enqueue(appmonitors.CheckIn) bool
}

type checkInPayload struct {
	CheckInID     string          `json:"check_in_id"`
	MonitorSlug   string          `json:"monitor_slug"`
	Status        string          `json:"status"`
	Duration      *float64        `json:"duration"`
	Release       string          `json:"release"`
	Environment   string          `json:"environment"`
	MonitorConfig *monitorPayload `json:"monitor_config"`
}

type monitorPayload struct {
	Schedule *struct {
		Type  string          `json:"type"`
		Value json.RawMessage `json:"value"`
		Unit  string          `json:"unit"`
	} `json:"schedule"`
	CheckInMargin         *float64 `json:"checkin_margin"`
	MaxRuntime            *float64 `json:"max_runtime"`
	Timezone              string   `json:"timezone"`
	FailureIssueThreshold *int     `json:"failure_issue_threshold"`
	RecoveryThreshold     *int     `json:"recovery_threshold"`
}

// ParseCheckIn reads a Sentry check_in item (the format the SDKs' monitor
// helpers send: check_in_id, monitor_slug, status, duration in seconds,
// release, environment and an optional monitor_config). The result is stamped
// with the site and receive time by the caller. Slugs are lowercased; an
// invalid slug, status or id is an error.
func ParseCheckIn(payload []byte) (appmonitors.CheckIn, error) {
	if len(payload) > MaxCheckInBytes {
		return appmonitors.CheckIn{}, errors.New("check-in too large")
	}
	var p checkInPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return appmonitors.CheckIn{}, fmt.Errorf("check-in json: %w", err)
	}
	id, ok := appmonitors.NormalizeUUID(p.CheckInID)
	if !ok {
		return appmonitors.CheckIn{}, errors.New("check-in has no valid check_in_id")
	}
	slug := strings.ToLower(strings.TrimSpace(p.MonitorSlug))
	if !slugPattern(slug) {
		return appmonitors.CheckIn{}, errors.New("check-in has an invalid monitor_slug")
	}
	switch p.Status {
	case appmonitors.StatusOK, appmonitors.StatusError, appmonitors.StatusInProgress:
	default:
		return appmonitors.CheckIn{}, fmt.Errorf("check-in has an unsupported status %q", p.Status)
	}
	env := strings.TrimSpace(p.Environment)
	if env == "" {
		env = appmonitors.DefaultEnvironment
	}
	if len(env) > 64 {
		return appmonitors.CheckIn{}, errors.New("check-in environment is too long")
	}
	ci := appmonitors.CheckIn{Slug: slug, Environment: env, ID: id, Status: p.Status, Release: truncate(p.Release, 200)}
	if p.Duration != nil && *p.Duration >= 0 && *p.Duration < 1e9 && !math.IsNaN(*p.Duration) {
		ms := int64(*p.Duration * 1000)
		ci.DurationMS = &ms
	}
	if p.MonitorConfig != nil {
		cfg, err := parseMonitorConfig(*p.MonitorConfig)
		if err != nil {
			return appmonitors.CheckIn{}, err
		}
		ci.Config = cfg
	}
	return ci, nil
}

func slugPattern(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "")
}

func parseMonitorConfig(p monitorPayload) (*appmonitors.Config, error) {
	cfg := &appmonitors.Config{TZ: p.Timezone}
	if p.Schedule != nil {
		switch p.Schedule.Type {
		case appmonitors.ScheduleCrontab:
			var expr string
			if err := json.Unmarshal(p.Schedule.Value, &expr); err != nil {
				return nil, errors.New("monitor_config crontab value must be a string")
			}
			cfg.ScheduleType, cfg.Schedule = appmonitors.ScheduleCrontab, expr
		case appmonitors.ScheduleInterval:
			var n float64
			if err := json.Unmarshal(p.Schedule.Value, &n); err != nil || n < 1 || n != math.Trunc(n) || n > 10_000 {
				return nil, errors.New("monitor_config interval value must be a whole number")
			}
			cfg.ScheduleType, cfg.Schedule = appmonitors.ScheduleInterval, appmonitors.IntervalValue(int(n), p.Schedule.Unit)
		default:
			return nil, fmt.Errorf("monitor_config has an unknown schedule type %q", p.Schedule.Type)
		}
	}
	if p.CheckInMargin != nil {
		cfg.MarginMinutes = wholeMinutes(*p.CheckInMargin)
	}
	if p.MaxRuntime != nil {
		cfg.MaxRuntimeMinutes = wholeMinutes(*p.MaxRuntime)
	}
	if p.FailureIssueThreshold != nil {
		cfg.FailureThreshold = *p.FailureIssueThreshold
	}
	if p.RecoveryThreshold != nil {
		cfg.RecoveryThreshold = *p.RecoveryThreshold
	}
	return cfg, nil
}

// wholeMinutes rounds a positive minutes value up to a whole minute.
func wholeMinutes(v float64) int {
	if math.IsNaN(v) || v <= 0 {
		return 0
	}
	return int(math.Min(math.Ceil(v), 1440))
}

// checkInAt is the time a check-in is stamped with: the receive time.
func checkInAt(now time.Time) time.Time { return now.UTC() }
