// Package obslogs stores application log records (OTLP logs and Sentry log
// items) in the logs table through a bounded, byte-budgeted queue and a
// CopyFrom writer. The write path never reads the database and never runs DDL.
package obslogs

import (
	"encoding/json"
	"time"

	"supermetry/internal/health"
)

// Sources of a Record.
const (
	SourceOTLP   = "otlp"
	SourceSentry = "sentry"
)

// recordOverhead is the fixed per-record cost counted against the queue's byte
// budget, on top of the body and the encoded attributes.
const recordOverhead = 96

// Record is one log record, already scrubbed and capped by its front door.
type Record struct {
	SiteID      int32
	TS          time.Time
	Observed    time.Time
	Severity    int16
	Service     string
	Environment string
	Release     string
	TraceID     []byte // nil or 16 bytes
	SpanID      []byte // nil or 8 bytes
	UserHash    []byte // keyed hash of the user id, never the id
	Body        string
	Truncated   bool
	Attributes  map[string]any
	Source      string
}

// Size is the number of bytes the record counts against the queue budget: the
// body, the encoded attributes and a fixed overhead.
func (r Record) Size() int {
	return len(r.Body) + len(encodeAttributes(r.Attributes)) + recordOverhead
}

func encodeAttributes(attrs map[string]any) []byte {
	if len(attrs) == 0 {
		return []byte("{}")
	}
	raw, err := json.Marshal(attrs)
	if err != nil {
		return []byte("{}")
	}
	return raw
}

// Stats counts drops by reason: log_queue_full, log_out_of_range,
// log_write_failed.
type Stats interface {
	Add(name string, n int)
}

// HealthStats exports log drop counts as health counters. Names it does not
// know are ignored.
type HealthStats struct{}

// Add implements Stats.
func (HealthStats) Add(name string, n int) {
	if n <= 0 {
		return
	}
	switch name {
	case "log_queue_full":
		health.LogQueueFull.Add(uint64(n))
	case "log_out_of_range":
		health.LogOutOfRange.Add(uint64(n))
	case "log_write_failed":
		health.LogWriteFailed.Add(uint64(n))
	}
}
