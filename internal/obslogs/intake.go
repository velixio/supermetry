package obslogs

import (
	"strings"
	"time"
)

// Intake limits shared by the OTLP and Sentry log front doors.
const (
	// MaxAge is how old a record's timestamp may be.
	MaxAge = 7 * 24 * time.Hour
	// MaxFuture is how far ahead of the receive time it may be.
	MaxFuture = 10 * time.Minute
	// MaxRequestRecords is the most log records one request may carry.
	MaxRequestRecords = 10_000
	// MaxSentryItemLogs is the most logs one Sentry log item may carry.
	MaxSentryItemLogs = 1_000
)

// InWindow reports whether ts is acceptable at receive time now.
func InWindow(ts, now time.Time) bool {
	return !ts.Before(now.Add(-MaxAge)) && !ts.After(now.Add(MaxFuture))
}

// SeverityFromText maps a severity text or Sentry level to the first OTLP
// severity number of its band (trace 1, debug 5, info 9, warn 13, error 17,
// fatal 21), or 0 when the text is not one of them.
func SeverityFromText(s string) int16 {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "trace":
		return 1
	case "debug":
		return 5
	case "info", "information":
		return 9
	case "warn", "warning":
		return 13
	case "error", "err":
		return 17
	case "fatal", "critical", "crit":
		return 21
	}
	return 0
}
