// Package session maintains live analytics sessions without database reads on
// the ingest hot path.
package session

import (
	"time"

	"supermetry/internal/db"
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
	UTMSource     int32
	Medium        int32
	Campaign      int32
	Country       string
	Region        int32
	City          int32
	Browser       int32
	OS            int32
	BrowserVer    int32
	OSVer         int32
	Language      int32
	Hostname      int32
	ScreenBucket  int16
	Device        int16
	EngagementMS  int32
	ScrollPct     int16
	// JoinOnly marks a hit that only has meaning inside a live session, such
	// as a background engagement or Web Vitals ping. Touch attaches it to a
	// continuable session, or does nothing and returns a zero Result; it never
	// starts a session.
	JoinOnly bool
}

// Result identifies the session selected for a hit. VisitorID is the
// session's original visitor ID, which intentionally remains stable when a
// session crosses site-local midnight.
type Result struct {
	SessionID int64
	VisitorID int64
	New       bool
}

// ActiveValue is one ranked dimension value in a realtime snapshot.
type ActiveValue struct {
	ID       int32
	Visitors int
}

// RealtimeStore exposes the richer process-local snapshot used by the
// realtime API. Store remains backward compatible for lightweight test
// doubles and alternative implementations.
type RealtimeStore interface {
	ActiveBreakdownsSince(siteID int32, t time.Time) (visitors int, pages, sources []ActiveValue)
}

// ActivityRecorder accepts the privacy-safe portion of a successfully queued
// pageview or custom event. Implementations must not retain VisitorID in feeds.
type ActivityRecorder interface {
	RecordActivity(Activity)
}

// AcceptedActivityStore counts distinct visitors from accepted pageviews and
// custom events at exact timestamps. The upper bound excludes future events
// caused by clock skew while the lower bound is inclusive.
type AcceptedActivityStore interface {
	ActiveVisitorsSince(siteID int32, since, until time.Time) int
}

// RichRealtimeStore exposes the complete process-local realtime snapshot
// without expanding Store.
type RichRealtimeStore interface {
	RealtimeSnapshot(siteID int32, now time.Time) RealtimeSnapshot
}

type Activity struct {
	SiteID    int32
	Timestamp time.Time
	VisitorID int64
	Kind      int16
	PathID    int32
	NameID    int32
	Country   string
	CityID    int32
	Device    int16
	BrowserID int32
	SourceID  int32
}

type MinuteVisitors struct {
	Minute   time.Time
	Visitors int
}

type ActiveLocation struct {
	ID          int32
	CountryCode string
	Visitors    int
}

type ActiveCountry struct {
	CountryCode string
	Visitors    int
}

type ActiveEvent struct {
	NameID int32
	Count  int
}

// RecentActivity deliberately excludes visitor, session, profile, IP and UA
// identifiers. Kind is 0 for pageviews and 1 for custom events.
type RecentActivity struct {
	Kind        int16
	PathID      int32
	NameID      int32
	CountryCode string
	CityID      int32
	Device      int16
	BrowserID   int32
	SourceID    int32
	Timestamp   time.Time
}

type RealtimeSnapshot struct {
	// ActiveVisitors counts visitors seen in the last LiveWindow (5 min);
	// VisitorsLast30m counts the whole realtime window.
	ActiveVisitors    int
	VisitorsLast30m   int
	Pages             []ActiveValue
	Sources           []ActiveValue
	EntryPages        []ActiveValue
	Countries         []ActiveCountry
	Regions           []ActiveLocation
	Cities            []ActiveLocation
	Devices           []ActiveValue
	Browsers          []ActiveValue
	OperatingSystems  []ActiveValue
	VisitorsPerMinute []MinuteVisitors
	Pageviews         int
	Events            []ActiveEvent
	RecentActivity    []RecentActivity
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
	UTMSource    int32
	Medium       int32
	Campaign     int32
	Country      string
	Region       int32
	City         int32
	Browser      int32
	OS           int32
	BrowserVer   int32
	OSVer        int32
	Language     int32
	Hostname     int32
	ScreenBucket int16
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

// Joiner resolves an existing live session without changing analytics
// counters, timestamps, dimensions, or dirty state.
type Joiner interface {
	Join(Hit) Result
}

// ProfileLinker optionally records a hash-only association between a live
// session and an external profile identity.
type ProfileLinker interface {
	LinkProfile(siteID int32, sessionID int64, externalID string) bool
}

// ProfileBlockChecker optionally reports that replay uploads for a live
// session must be suppressed after its linked profile was tombstoned.
type ProfileBlockChecker interface {
	ProfileBlocked(siteID int32, sessionID int64) bool
}
