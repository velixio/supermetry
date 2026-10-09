// Package core holds the pure, DB-free logic of the Supermetry prototype:
// civil days in a site's time zone, salted visitor hashing, source
// derivation, the sharded session cache and the in-memory rollup
// aggregator used by rollup-stream.
package core

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// SessionTimeout is the idle gap after which a visitor's next event starts a new session (D8).
const SessionTimeout = 30 * time.Minute

// Site is a tracked website.
type Site struct {
	ID   int32          `json:"id"`
	Host string         `json:"host"`
	TZ   string         `json:"timezone"`
	Loc  *time.Location `json:"-"`
}

// Day is a civil (calendar) date, as days since 1970-01-01.
type Day int32

// CivilDay returns the Day for a calendar date.
func CivilDay(y int, m time.Month, d int) Day {
	return Day(time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Unix() / 86400)
}

// DayOf returns the site-local calendar date of instant t.
func DayOf(t time.Time, loc *time.Location) Day {
	y, m, d := t.In(loc).Date()
	return CivilDay(y, m, d)
}

// ParseDay parses YYYY-MM-DD.
func ParseDay(s string) (Day, error) {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return 0, fmt.Errorf("bad date %q: %w", s, err)
	}
	return Day(t.Unix() / 86400), nil
}

// Time returns midnight UTC of the date (the representation used for PG `date`).
func (d Day) Time() time.Time { return time.Unix(int64(d)*86400, 0).UTC() }

func (d Day) String() string { return d.Time().Format("2006-01-02") }

// MarshalText makes Day render as YYYY-MM-DD in JSON.
func (d Day) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

// UnmarshalText parses YYYY-MM-DD.
func (d *Day) UnmarshalText(b []byte) error {
	v, err := ParseDay(string(b))
	*d = v
	return err
}

// Bounds returns the UTC instants [start, end) of local date d in loc.
func (d Day) Bounds(loc *time.Location) (time.Time, time.Time) {
	t := d.Time()
	start := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
	end := time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, loc)
	return start.UTC(), end.UTC()
}

// Event is one stored analytics event. IP and UA are never part of it (P1).
type Event struct {
	SiteID    int32
	Day       Day   // site-local date
	TS        int64 // unix microseconds
	VisitorID int64
	SessionID int64
	Name      string
	Path      string
	// Session dimensions. Before sessionization they hold this event's own
	// values; Cache.Assign overwrites them with the session's values.
	Source, UTMSource, UTMMedium, UTMCampaign string
	Country, Browser, OS, Device              string
	Props                                     []byte // JSON object, nil when empty
}

// IsPageview reports whether the event is a pageview.
func (e *Event) IsPageview() bool { return e.Name == "pageview" }

// Time returns the event timestamp.
func (e *Event) Time() time.Time { return time.UnixMicro(e.TS).UTC() }

// Source derives the traffic source: utm_source when set, otherwise the
// referrer host (lowercased). Self-referrals and unparsable referrers count
// as direct (""). The CONTRACT defines source as "referrer host, or utm_source
// when set".
func Source(ref, utmSource, siteHost string) string {
	if utmSource != "" {
		return utmSource
	}
	if ref == "" {
		return ""
	}
	u, err := url.Parse(ref)
	if err != nil || u.Host == "" {
		return ""
	}
	h := strings.ToLower(u.Hostname())
	if h == strings.ToLower(siteHost) {
		return ""
	}
	return h
}

// NormalizeProps returns compact JSON for a non-empty string map, or nil.
func NormalizeProps(raw json.RawMessage) ([]byte, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	// Plausible's tracker may send props as a JSON-encoded string.
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, err
		}
		raw = json.RawMessage(s)
	}
	if string(raw) == "null" {
		return nil, nil
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("props must be a string map: %w", err)
	}
	if len(m) == 0 {
		return nil, nil
	}
	return json.Marshal(m)
}
