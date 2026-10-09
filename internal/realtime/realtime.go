// Package realtime reports activity from the process-local session store.
package realtime

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"supermetry/internal/session"
)

const window = 30 * time.Minute

// LiveWindow is how recently a visitor must have been active to count as
// "live now", matching the 5 minutes Plausible, Umami and OpenPanel use. The
// charts and breakdowns keep the 30-minute window.
const LiveWindow = 5 * time.Minute

// MinuteSource reads durable visitor counts for closed minute buckets.
type MinuteSource interface {
	Minutes(ctx context.Context, siteID int32, from, to time.Time) ([]session.MinuteVisitors, error)
}

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
	live, _ := store.ActiveSince(siteID, now.Add(-LiveWindow))
	if rich, ok := store.(session.RichRealtimeStore); ok {
		result := rich.RealtimeSnapshot(siteID, now)
		result.VisitorsLast30m = result.ActiveVisitors
		result.ActiveVisitors = live
		return result
	}
	visitors, pages, sources := Snapshot(store, siteID, now)
	result := session.RealtimeSnapshot{ActiveVisitors: live, VisitorsLast30m: visitors, VisitorsPerMinute: emptyMinutes(now)}
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

// RichSnapshotWithMinutes merges durable closed-minute visitor counts into the
// process-local snapshot. The open minute remains process-local because a
// durable row for it may be incomplete.
func RichSnapshotWithMinutes(ctx context.Context, store session.Store, minutes MinuteSource, siteID int32, now time.Time) session.RealtimeSnapshot {
	result := RichSnapshot(store, siteID, now)
	if minutes == nil {
		return result
	}

	current := now.UTC().Truncate(time.Minute)
	from := current.Add(-29 * time.Minute)
	stored, err := minutes.Minutes(ctx, siteID, from, current)
	if err != nil {
		slog.WarnContext(ctx, "load durable realtime minutes", "site_id", siteID, "error", err)
		return result
	}

	merged := emptyMinutes(now)
	mergeMinutes(merged, result.VisitorsPerMinute, from, current, false)
	mergeMinutes(merged, stored, from, current, true)
	result.VisitorsPerMinute = merged
	return result
}

func mergeMinutes(target, values []session.MinuteVisitors, from, current time.Time, closedOnly bool) {
	for _, value := range values {
		minute := value.Minute.UTC().Truncate(time.Minute)
		if minute.Before(from) || minute.After(current) || closedOnly && !minute.Before(current) {
			continue
		}
		index := int(minute.Sub(from) / time.Minute)
		if value.Visitors > target[index].Visitors {
			target[index].Visitors = value.Visitors
		}
	}
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
