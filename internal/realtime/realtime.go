// Package realtime reports activity from the process-local session store.
package realtime

import (
	"strconv"
	"time"

	"panel/internal/session"
)

const window = 30 * time.Minute

// Item is a ranked realtime dimension value.
type Item struct {
	Value    string
	Visitors int
}

// Snapshot returns active visitors and ranked path and source IDs seen during
// the last thirty minutes. IDs are rendered as decimal strings for the API
// layer, which may resolve them to display values in a future dictionary-aware
// adapter.
func Snapshot(store session.Store, siteID int32, now time.Time) (visitors int, topPages, topSources []Item) {
	if store == nil {
		return 0, nil, nil
	}
	if rich, ok := store.(session.RealtimeStore); ok {
		var pages, sources []session.ActiveValue
		visitors, pages, sources = rich.ActiveBreakdownsSince(siteID, now.Add(-window))
		topPages = render(pages)
		topSources = render(sources)
		return visitors, topPages, topSources
	}
	visitors, pathIDs := store.ActiveSince(siteID, now.Add(-window))
	topPages = make([]Item, len(pathIDs))
	for i, pathID := range pathIDs {
		topPages[i] = Item{Value: strconv.FormatInt(int64(pathID), 10)}
	}
	return visitors, topPages, nil
}

// RichSnapshot returns the optional full snapshot, falling back to the legacy
// active visitor/page/source view for alternative Store implementations.
func RichSnapshot(store session.Store, siteID int32, now time.Time) session.RealtimeSnapshot {
	if store == nil {
		return session.RealtimeSnapshot{VisitorsPerMinute: emptyMinutes(now)}
	}
	if rich, ok := store.(session.RichRealtimeStore); ok {
		return rich.RealtimeSnapshot(siteID, now)
	}
	visitors, pages, sources := Snapshot(store, siteID, now)
	result := session.RealtimeSnapshot{ActiveVisitors: visitors, VisitorsPerMinute: emptyMinutes(now)}
	for _, page := range pages {
		id, _ := strconv.ParseInt(page.Value, 10, 32)
		result.Pages = append(result.Pages, session.ActiveValue{ID: int32(id), Visitors: page.Visitors})
	}
	for _, source := range sources {
		id, _ := strconv.ParseInt(source.Value, 10, 32)
		result.Sources = append(result.Sources, session.ActiveValue{ID: int32(id), Visitors: source.Visitors})
	}
	return result
}

func emptyMinutes(now time.Time) []session.MinuteVisitors {
	current := now.UTC().Truncate(time.Minute)
	minutes := make([]session.MinuteVisitors, 30)
	for i := range minutes {
		minutes[i].Minute = current.Add(time.Duration(i-29) * time.Minute)
	}
	return minutes
}

func render(values []session.ActiveValue) []Item {
	items := make([]Item, len(values))
	for i, value := range values {
		items[i] = Item{Value: strconv.FormatInt(int64(value.ID), 10), Visitors: value.Visitors}
	}
	return items
}
