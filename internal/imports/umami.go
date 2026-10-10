package imports

import (
	"fmt"
	"io"
	"strings"
	"time"
)

type umamiSessionKey struct {
	day time.Time
	id  string
}

type umamiSession struct {
	start, end  time.Time
	firstPageAt time.Time
	lastPageAt  time.Time
	entry, exit string
	pageviews   int64
	source      string
	country     string
	browser     string
	os          string
	device      string
	paths       map[string]*umamiPath
	events      map[string]int64
}

type umamiPath struct {
	pageviews int64
}

// ParseUmami streams rows from a website_event CSV using UTC for callers that
// do not have site metadata.
func ParseUmami(input io.Reader) (Aggregate, ParseStats, error) {
	return ParseUmamiInLocation(input, time.UTC)
}

// ParseUmamiInLocation buckets rows by the site's local civil day. Umami's
// website_event export has session IDs but no stable visitor ID, so distinct
// sessions per local day are also the closest reproducible visitor count.
func ParseUmamiInLocation(input io.Reader, location *time.Location) (Aggregate, ParseStats, error) {
	if location == nil {
		location = time.UTC
	}
	result := newAggregate()
	var stats ParseStats
	table, err := newCSVTable(input, &stats, "session_id", "created_at", "event_type")
	if err != nil {
		return result, stats, err
	}
	sessions := make(map[umamiSessionKey]*umamiSession)
	err = table.each(func(row csvRow) error {
		timestamp, err := parseUmamiTime(row.text("created_at"))
		if err != nil {
			return err
		}
		sessionID := row.text("session_id")
		if sessionID == "" {
			return fmt.Errorf("empty session_id")
		}
		pageview, err := umamiEventKind(row.text("event_type"))
		if err != nil {
			return err
		}
		local := timestamp.In(location)
		day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
		key := umamiSessionKey{day, sessionID}
		session := sessions[key]
		if session == nil {
			if len(sessions) >= maxUmamiSessions {
				return fatalError{fmt.Errorf("import has more than %d sessions", maxUmamiSessions)}
			}
			session = &umamiSession{
				start: timestamp, end: timestamp,
				paths: make(map[string]*umamiPath), events: make(map[string]int64),
			}
			sessions[key] = session
		}
		if timestamp.Before(session.start) {
			session.start = timestamp
		}
		if timestamp.After(session.end) {
			session.end = timestamp
		}
		session.source = firstNonempty(session.source, row.text("referrer_domain"))
		session.country = firstNonempty(session.country, row.text("country"))
		session.browser = firstNonempty(session.browser, row.text("browser"))
		session.os = firstNonempty(session.os, row.text("os"))
		session.device = firstNonempty(session.device, row.text("device"))

		path := row.text("url_path")
		pathStats := session.paths[path]
		if pathStats == nil {
			pathStats = &umamiPath{}
			session.paths[path] = pathStats
		}
		if pageview {
			session.pageviews++
			pathStats.pageviews++
			if session.firstPageAt.IsZero() || timestamp.Before(session.firstPageAt) {
				session.firstPageAt, session.entry = timestamp, path
			}
			if session.lastPageAt.IsZero() || timestamp.After(session.lastPageAt) {
				session.lastPageAt, session.exit = timestamp, path
			}
		} else {
			session.events[row.text("event_name")]++
		}
		return nil
	})
	if err != nil {
		return result, stats, err
	}
	for key, session := range sessions {
		duration := int64(session.end.Sub(session.start) / time.Second)
		metrics := Metrics{Visitors: 1, Visits: 1, Pageviews: session.pageviews, VisitDurationS: duration}
		if session.pageviews == 1 {
			metrics.Bounces = 1
		}
		result.addDay(key.day, metrics)
		for dim, value := range map[int16]string{
			DimSource: session.source, DimCountry: session.country, DimBrowser: session.browser,
			DimOS: session.os, DimDevice: session.device,
		} {
			result.addDimension(DimensionKey{key.day, dim, value}, metrics)
		}
		result.addDimension(DimensionKey{key.day, DimEntry, session.entry}, metrics)
		result.addDimension(DimensionKey{key.day, DimExit, session.exit}, metrics)
		for path, counts := range session.paths {
			pathMetrics := Metrics{Pageviews: counts.pageviews}
			if counts.pageviews > 0 {
				pathMetrics.Visitors = 1
				pathMetrics.Visits = 1
			}
			result.addDimension(DimensionKey{key.day, DimPage, path}, pathMetrics)
		}
		for name, count := range session.events {
			result.addDimension(DimensionKey{key.day, DimEvent, name}, Metrics{Visitors: 1, Visits: 1, Events: count})
		}
		if err := result.checkLimit(); err != nil {
			return result, stats, err
		}
	}
	return result, stats, nil
}

func umamiEventKind(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "pageview", "page_view", "page view":
		return true, nil
	case "2", "event", "custom", "custom_event", "custom event":
		return false, nil
	default:
		return false, fmt.Errorf("invalid event_type %q", value)
	}
}

func parseUmamiTime(value string) (time.Time, error) {
	for _, layout := range []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
	} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid created_at %q", value)
}
