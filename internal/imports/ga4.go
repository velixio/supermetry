package imports

import (
	"context"
	"fmt"
	"hash/maphash"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// maxGA4Rows bounds how many data rows one GA4 file may hold. The duplicate
// detector keeps one 8 byte hash per row, so this also bounds its memory.
var maxGA4Rows = 3_000_000

// ga4Columns maps normalised GA4 column labels (Data API, report CSV exports
// and BigQuery-style names) to the importer's canonical column names.
var ga4Columns = map[string]string{
	"date": "date", "event date": "date", "day": "date",

	"page path and screen class": "page", "page path": "page", "page path + query string": "page",
	"page path + query string and screen class": "page",
	"landing page": "landing", "landing page + query string": "landing",
	"session source / medium": "source", "session source": "source", "first user source": "source",
	"first user source / medium": "source", "source": "source", "source / medium": "source",
	"country": "country", "region": "region", "city": "city",
	"device category": "device", "browser": "browser", "operating system": "os",
	"event name": "event",

	"active users": "users", "total users": "users", "users": "users",
	"sessions": "sessions", "engaged sessions": "engaged",
	"views": "views", "screen page views": "views", "pageviews": "views",
	"event count": "events", "bounce rate": "bounce", "average session duration": "duration",
}

var ga4DimColumns = []struct {
	column string
	dim    int16
}{
	{"page", DimPage}, {"landing", DimEntry}, {"source", DimSource}, {"country", DimCountry},
	{"region", DimRegion}, {"city", DimCity}, {"device", DimDevice}, {"browser", DimBrowser}, {"os", DimOS},
}

// ga4AutomaticEvents are collected for every property and already show up as
// pageviews and sessions, so they are not imported as custom events.
var ga4AutomaticEvents = map[string]bool{
	"page_view": true, "session_start": true, "first_visit": true, "user_engagement": true,
}

func normalizeGA4Label(label string) string {
	label = strings.TrimPrefix(label, "\ufeff")
	label = strings.ToLower(strings.TrimSpace(label))
	label = strings.ReplaceAll(label, "_", " ")
	return strings.Join(strings.Fields(label), " ")
}

// ParseGA4 reads a GA4 report CSV (Data API, Explore or Reports export, or a
// BigQuery-style table export). GA4 dates are already civil days in the
// property's time zone, so no time zone conversion applies.
func ParseGA4(input io.Reader) (Aggregate, ParseStats, error) {
	return parseGA4(context.Background(), input)
}

func parseGA4File(ctx context.Context, path string) (Aggregate, ParseStats, error) {
	file, err := os.Open(path)
	if err != nil {
		return newAggregate(), ParseStats{}, err
	}
	defer file.Close()
	return parseGA4(ctx, file)
}

func parseGA4(ctx context.Context, input io.Reader) (Aggregate, ParseStats, error) {
	result := newAggregate()
	var stats ParseStats
	table, err := newCSVTableComment(contextReader{ctx, input}, &stats, '#')
	if err != nil {
		return result, stats, err
	}
	canonical := make(map[string]int, len(table.header))
	labels := make(map[string]string, len(table.header))
	for label, index := range table.header {
		name, ok := ga4Columns[normalizeGA4Label(label)]
		if !ok {
			continue
		}
		if prior, dup := canonical[name]; dup && prior < index {
			continue
		}
		canonical[name], labels[name] = index, normalizeGA4Label(label)
	}
	table.header = canonical
	if _, ok := canonical["date"]; !ok {
		return result, stats, fmt.Errorf("GA4 file needs a Date column")
	}
	var hasMetric bool
	for _, name := range []string{"users", "sessions", "views", "events"} {
		_, ok := canonical[name]
		hasMetric = hasMetric || ok
	}
	if !hasMetric {
		return result, stats, fmt.Errorf("GA4 file needs at least one of Active users, Sessions, Views or Event count")
	}
	_, hasEvent := canonical["event"]
	_, hasPage := canonical["page"]
	_, hasLanding := canonical["landing"]
	hasDims := hasEvent
	for _, d := range ga4DimColumns {
		if _, ok := canonical[d.column]; ok {
			hasDims = true
		}
	}
	// Sessions count a visit once per page it touched, so a page report's
	// session numbers do not add up to the day total; views always do.
	pageScoped := hasPage || hasLanding

	seed := maphash.MakeSeed()
	seen := make(map[uint64]struct{})
	var rows int
	err = table.each(func(row csvRow) error {
		rows++
		if rows > maxGA4Rows {
			return fatalError{fmt.Errorf("GA4 file has more than %d rows", maxGA4Rows)}
		}
		day, err := parseGA4Day(row.text("date"))
		if err != nil {
			return err
		}
		var m Metrics
		for _, f := range []struct {
			name string
			to   *int64
		}{{"users", &m.Visitors}, {"sessions", &m.Visits}, {"views", &m.Pageviews}, {"events", &m.Events}} {
			if *f.to, err = ga4Count(row, f.name, labels[f.name]); err != nil {
				return err
			}
		}
		if m.Bounces, m.VisitDurationS, err = ga4SessionQuality(row, labels, m.Visits); err != nil {
			return err
		}
		eventName := ""
		if hasEvent {
			if eventName = normalizeGA4Value(0, row.text("event")); eventName == "" {
				return fmt.Errorf("empty event name")
			}
		}
		// Duplicate detection runs only on otherwise valid rows, so a bad row
		// is reported as bad and never hidden as a duplicate.
		fingerprint := maphash.String(seed, strings.Join(row.values, "\x1f"))
		if _, dup := seen[fingerprint]; dup {
			stats.DuplicateRows++
			return errSkipRow
		}
		seen[fingerprint] = struct{}{}

		switch {
		case hasEvent:
			if !ga4AutomaticEvents[eventName] {
				result.addDimension(DimensionKey{day, DimEvent, eventName}, Metrics{Visitors: m.Visitors, Events: m.Events})
			}
		case !hasDims:
			result.addDay(day, m)
		default:
			dayMetrics := Metrics{Pageviews: m.Pageviews}
			if !pageScoped {
				dayMetrics.Visits, dayMetrics.Bounces, dayMetrics.VisitDurationS = m.Visits, m.Bounces, m.VisitDurationS
			}
			if dayMetrics != (Metrics{}) {
				result.addDay(day, dayMetrics)
			}
			for _, d := range ga4DimColumns {
				if _, ok := canonical[d.column]; ok {
					result.addDimension(DimensionKey{day, d.dim, normalizeGA4Value(d.dim, row.text(d.column))}, m)
				}
			}
		}
		return result.checkLimit()
	})
	return result, stats, err
}

func parseGA4Day(value string) (time.Time, error) {
	for _, layout := range []string{"20060102", "2006-01-02"} {
		if day, err := time.Parse(layout, value); err == nil {
			return day, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid date %q", value)
}

// ga4Float parses a GA4 number, tolerating thousands separators.
func ga4Float(value string) (float64, error) {
	parsed, err := strconv.ParseFloat(strings.ReplaceAll(value, ",", ""), 64)
	if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) || parsed < 0 || parsed > 1e15 {
		return 0, fmt.Errorf("invalid number")
	}
	return parsed, nil
}

func ga4Count(row csvRow, name, label string) (int64, error) {
	value := row.text(name)
	if value == "" {
		return 0, nil
	}
	parsed, err := ga4Float(value)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q", label, value)
	}
	return int64(math.Round(parsed)), nil
}

// ga4SessionQuality derives bounced sessions and total visit seconds from the
// rate and average columns GA4 reports.
func ga4SessionQuality(row csvRow, labels map[string]string, sessions int64) (bounces, duration int64, err error) {
	if value := row.text("bounce"); value != "" {
		percent := strings.HasSuffix(value, "%")
		rate, parseErr := ga4Float(strings.TrimSuffix(value, "%"))
		if parseErr != nil {
			return 0, 0, fmt.Errorf("invalid %s %q", labels["bounce"], value)
		}
		if percent || rate > 1 {
			rate /= 100
		}
		bounces = min(sessions, int64(math.Round(float64(sessions)*rate)))
	} else if value := row.text("engaged"); value != "" {
		engaged, parseErr := ga4Float(value)
		if parseErr != nil {
			return 0, 0, fmt.Errorf("invalid %s %q", labels["engaged"], value)
		}
		bounces = max(sessions-int64(math.Round(engaged)), 0)
	}
	if value := row.text("duration"); value != "" {
		average, parseErr := ga4Float(value)
		if parseErr != nil {
			return 0, 0, fmt.Errorf("invalid %s %q", labels["duration"], value)
		}
		duration = int64(math.Round(float64(sessions) * average))
	}
	return bounces, duration, nil
}

// normalizeGA4Value maps one GA4 dimension value to the representation used
// by live data; it returns "" for values that carry no information.
func normalizeGA4Value(dim int16, value string) string {
	value = strings.TrimSpace(value)
	switch strings.ToLower(value) {
	case "", "(not set)", "(other)", "(data not available)":
		return ""
	}
	switch dim {
	case DimPage, DimEntry:
		if !strings.HasPrefix(value, "/") && !strings.Contains(value, "://") {
			value = "/" + value
		}
	case DimSource:
		if source, _, found := strings.Cut(value, " / "); found {
			value = strings.TrimSpace(source)
		}
		if strings.EqualFold(value, "(direct)") {
			return "Direct / None"
		}
		return upperFirst(value)
	case DimCountry:
		if len(value) == 2 {
			return strings.ToUpper(value)
		}
		if code, ok := ga4Countries[strings.ToLower(value)]; ok {
			return code
		}
	case DimDevice:
		if strings.EqualFold(value, "smart tv") {
			return "Smart TV"
		}
		words := strings.Fields(value)
		for i, word := range words {
			words[i] = upperFirst(strings.ToLower(word))
		}
		return strings.Join(words, " ")
	}
	return value
}

func upperFirst(value string) string {
	first, size := utf8.DecodeRuneInString(value)
	if first == utf8.RuneError {
		return value
	}
	return string(unicode.ToUpper(first)) + value[size:]
}
