// Package session maintains live analytics sessions without database reads on
// the ingest hot path.
package session

import (
	"time"

	"panel/internal/db"
)

// IdleTimeout is the maximum gap between hits in one session.
const IdleTimeout = 30 * time.Minute

// Clock supplies the current time.
type Clock interface {
	Now() time.Time
}

// Hit is the session-relevant part of an accepted analytics event. VisitorID
// and PrevVisitorID are already privacy-preserving hashes; this type must never
// contain an IP address or user-agent string.
type Hit struct {
	SiteID        int32
	Retention     db.RetentionClass
	TS            time.Time
	LocalDay      time.Time
	VisitorID     int64
	PrevVisitorID int64
	Kind          int16
	PathID        int32
	NameID        int32
	Source        int32
	Medium        int32
	Campaign      int32
	Country       string
	Region        int32
	City          int32
	Browser       int32
	OS            int32
	Device        int16
	EngagementMS  int32
	ScrollPct     int16
}

// Result identifies the session selected for a hit. VisitorID is the
// session's original visitor ID, which intentionally remains stable when a
// session crosses site-local midnight.
type Result struct {
	SessionID int64
	VisitorID int64
	New       bool
}

// Snapshot is the durable state needed to upsert or restore an open session.
type Snapshot struct {
	SiteID       int32
	Retention    db.RetentionClass
	SessionID    int64
	VisitorID    int64
	Start        time.Time
	Last         time.Time
	LocalDay     time.Time
	Entry        int32
	Exit         int32
	Pageviews    int32
	Events       int32
	EngagementMS int32
	Bounce       bool
	Source       int32
	Medium       int32
	Campaign     int32
	Country      string
	Region       int32
	City         int32
	Browser      int32
	OS           int32
	Device       int16
}

// Store is the live-session boundary used by intake, persistence,
// rehydration, and realtime reporting.
type Store interface {
	Touch(Hit) Result
	Dirty(max int) []Snapshot
	ActiveSince(siteID int32, t time.Time) (visitors int, topPaths []int32)
	Restore([]Snapshot)
	Expire(now time.Time) int
}
