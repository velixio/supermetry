// Package spans stores performance spans: the shared Span shape that the
// Sentry and OTLP front doors normalise into, the log-scale duration
// histogram, the per-project span budget and the bounded CopyFrom writer.
package spans

import "time"

// Span is one stored span. A segment (transaction) has IsSegment set and its
// own SpanID as SegmentID. Spans hold no user identifier, IP, cookie, header
// value, query string or request body.
type Span struct {
	SiteID         int32
	Start          time.Time
	DurationUS     int64
	TraceID        [16]byte
	SpanID         [8]byte
	SegmentID      [8]byte
	ParentID       *[8]byte
	IsSegment      bool
	Name           string
	Op             string
	Description    string
	Status         string
	HTTPStatus     *int16
	Release        string
	Environment    string
	Platform       string
	AppStartType   string // "", "cold" or "warm"
	AppStartMS     *int32
	AppStartBucket *int16 // Bucket(ms*1000)
	Attrs          map[string]string
}

// Stats counts drops by reason.
type Stats interface {
	Add(name string, n int)
}
