// Package sentryingest accepts Sentry SDK traffic and maps it onto the app
// monitoring model. This file and its siblings normalise.go, privacy.go and
// body.go are the pure part: no I/O, no database.
package sentryingest

import (
	"errors"
	"net/netip"
	"time"
)

// EventContext is what the front door knows about one item besides its
// payload. The envelope reader fills it from the envelope header and the HTTP
// request.
type EventContext struct {
	EventID    string    // envelope header event_id; used when the payload has none
	SentAt     time.Time // envelope header sent_at; zero when absent
	SDKName    string
	SDKVersion string
	InferIP    string     // "never" turns country lookup results off
	ClientIP   netip.Addr // never stored; only here so the front door has one place for it
	UserAgent  string     // raw; parsed to browser and OS, then discarded
	Country    string     // ISO 3166-1 alpha-2 from the geo lookup, or ""
	// HasMinidump is true when the envelope carries an event.minidump
	// attachment for this event (a native crash).
	HasMinidump bool
	// Stats counts parse-time events such as session_no_started; nil is fine.
	Stats *Stats
}

// ClientReportCount is one discarded-events entry of a client report.
type ClientReportCount struct {
	Reason   string
	Category string
	Quantity int64
}

var (
	// ErrInvalid means the payload is not a usable event.
	ErrInvalid = errors.New("sentryingest: invalid payload")
	// ErrTooOld means the event is older than the site accepts.
	ErrTooOld = errors.New("sentryingest: event too old")
)
