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

func render(values []session.ActiveValue) []Item {
	items := make([]Item, len(values))
	for i, value := range values {
		items[i] = Item{Value: strconv.FormatInt(int64(value.ID), 10), Visitors: value.Visitors}
	}
	return items
}
