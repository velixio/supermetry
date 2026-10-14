package realtime_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"supermetry/internal/realtime"
	"supermetry/internal/session"
)

type minuteSource struct {
	minutes []session.MinuteVisitors
	err     error
	siteID  int32
	from    time.Time
	to      time.Time
}

func (s *minuteSource) Minutes(_ context.Context, siteID int32, from, to time.Time) ([]session.MinuteVisitors, error) {
	s.siteID = siteID
	s.from = from
	s.to = to
	return s.minutes, s.err
}

func TestSnapshotUsesThirtyMinuteWindowAndIncludesRankedSources(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	store := session.NewMemory(2, nil)
	store.Touch(session.Hit{SiteID: 1, TS: now.Add(-29 * time.Minute), LocalDay: now, VisitorID: 1, Kind: 0, PathID: 11, Source: 21})
	store.Touch(session.Hit{SiteID: 1, TS: now.Add(-time.Minute), LocalDay: now, VisitorID: 2, Kind: 0, PathID: 11, Source: 22})
	store.Touch(session.Hit{SiteID: 1, TS: now.Add(-31 * time.Minute), LocalDay: now, VisitorID: 3, Kind: 0, PathID: 12, Source: 21})

	visitors, pages, sources := realtime.Snapshot(store, 1, now)
	if visitors != 2 {
		t.Fatalf("visitors=%d, want 2", visitors)
	}
	if len(pages) != 1 || pages[0].Value != "11" || pages[0].Visitors != 2 {
		t.Fatalf("pages=%+v, want [{11 2}]", pages)
	}
	if len(sources) != 2 || sources[0].Value != "21" || sources[0].Visitors != 1 || sources[1].Value != "22" || sources[1].Visitors != 1 {
		t.Fatalf("sources=%+v, want deterministic source ranking", sources)
	}
}

func TestRichSnapshotLiveCountUsesFiveMinutes(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	store := session.NewMemory(2, nil)
	for _, item := range []struct {
		visitor int64
		ago     time.Duration
		path    int32
	}{{1, 2 * time.Minute, 11}, {2, 5 * time.Minute, 11}, {3, 20 * time.Minute, 12}, {4, 31 * time.Minute, 12}} {
		at := now.Add(-item.ago)
		store.Touch(session.Hit{SiteID: 1, TS: at, LocalDay: now, VisitorID: item.visitor, Kind: 0, PathID: item.path})
		store.RecordActivity(session.Activity{SiteID: 1, Timestamp: at, VisitorID: item.visitor, Kind: 0, PathID: item.path})
	}

	snapshot := realtime.RichSnapshot(store, 1, now)
	if snapshot.ActiveVisitors != 2 {
		t.Fatalf("ActiveVisitors=%d, want 2 (activity within the last 5 minutes)", snapshot.ActiveVisitors)
	}
	if snapshot.VisitorsLast30m != 3 {
		t.Fatalf("VisitorsLast30m=%d, want 3", snapshot.VisitorsLast30m)
	}
}

func TestRichSnapshotLiveCountUsesAcceptedActivityNotSessionLifetime(t *testing.T) {
	now := time.Date(2026, 10, 12, 12, 0, 1, 0, time.UTC)
	store := session.NewMemory(2, nil)
	store.Touch(session.Hit{SiteID: 1, TS: now.Add(-6 * time.Minute), LocalDay: now, VisitorID: 1, Kind: 0})
	store.RecordActivity(session.Activity{SiteID: 1, Timestamp: now.Add(-6 * time.Minute), VisitorID: 1, Kind: 0})
	// Engagement extends the session but must not establish accepted-event presence.
	store.Touch(session.Hit{SiteID: 1, TS: now.Add(-time.Minute), LocalDay: now, VisitorID: 1, Kind: 2, JoinOnly: true})
	for _, at := range []time.Duration{-4*time.Minute - 59*time.Second, -2 * time.Minute, -time.Minute} {
		store.Touch(session.Hit{SiteID: 1, TS: now.Add(at), LocalDay: now, VisitorID: 2, Kind: 0})
		store.RecordActivity(session.Activity{SiteID: 1, Timestamp: now.Add(at), VisitorID: 2, Kind: 0})
	}
	store.RecordActivity(session.Activity{SiteID: 1, Timestamp: now.Add(-5 * time.Minute), VisitorID: 3, Kind: 1})
	store.RecordActivity(session.Activity{SiteID: 1, Timestamp: now.Add(-5*time.Minute - time.Second), VisitorID: 4, Kind: 0})

	snapshot := realtime.RichSnapshot(store, 1, now)
	if snapshot.ActiveVisitors != 2 {
		t.Fatalf("ActiveVisitors=%d, want 2 exact distinct accepted-event visitors", snapshot.ActiveVisitors)
	}
	if snapshot.VisitorsLast30m != 4 {
		t.Fatalf("VisitorsLast30m=%d, want 4 distinct accepted-event visitors", snapshot.VisitorsLast30m)
	}
}

// The 30-minute count uses the same definition as the live count: a visitor
// counts when a pageview or custom event arrived in the window. An open tab that
// only sends engagement beacons extends the session but is not counted.
func TestRichSnapshotLast30mIgnoresEngagementOnlyVisitors(t *testing.T) {
	now := time.Date(2026, 10, 12, 12, 0, 1, 0, time.UTC)
	store := session.NewMemory(2, nil)
	// Visitor 1 viewed a page 40 minutes ago and has only sent beacons since.
	store.Touch(session.Hit{SiteID: 1, TS: now.Add(-40 * time.Minute), LocalDay: now, VisitorID: 1, Kind: 0})
	store.RecordActivity(session.Activity{SiteID: 1, Timestamp: now.Add(-40 * time.Minute), VisitorID: 1, Kind: 0})
	for _, ago := range []time.Duration{25, 10, 1} {
		store.Touch(session.Hit{SiteID: 1, TS: now.Add(-ago * time.Minute), LocalDay: now, VisitorID: 1, Kind: 2, JoinOnly: true})
	}
	// Visitor 2 viewed a page 20 minutes ago and has only sent beacons since.
	store.Touch(session.Hit{SiteID: 1, TS: now.Add(-20 * time.Minute), LocalDay: now, VisitorID: 2, Kind: 0})
	store.RecordActivity(session.Activity{SiteID: 1, Timestamp: now.Add(-20 * time.Minute), VisitorID: 2, Kind: 0})
	store.Touch(session.Hit{SiteID: 1, TS: now.Add(-time.Minute), LocalDay: now, VisitorID: 2, Kind: 2, JoinOnly: true})
	// Visitor 3 sent a custom event 3 minutes ago.
	store.Touch(session.Hit{SiteID: 1, TS: now.Add(-3 * time.Minute), LocalDay: now, VisitorID: 3, Kind: 1})
	store.RecordActivity(session.Activity{SiteID: 1, Timestamp: now.Add(-3 * time.Minute), VisitorID: 3, Kind: 1})

	snapshot := realtime.RichSnapshot(store, 1, now)
	if snapshot.VisitorsLast30m != 2 {
		t.Fatalf("VisitorsLast30m=%d, want 2 (visitors 2 and 3; visitor 1 only sent beacons)", snapshot.VisitorsLast30m)
	}
	if snapshot.ActiveVisitors != 1 {
		t.Fatalf("ActiveVisitors=%d, want 1 (visitor 3)", snapshot.ActiveVisitors)
	}
}

func TestRichSnapshotWithMinutesPopulatesRestartedStore(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 45, 0, time.UTC)
	source := &minuteSource{minutes: []session.MinuteVisitors{
		{Minute: time.Date(2026, 10, 8, 11, 31, 0, 0, time.UTC), Visitors: 4},
		{Minute: time.Date(2026, 10, 8, 11, 59, 0, 0, time.UTC), Visitors: 7},
	}}

	snapshot := realtime.RichSnapshotWithMinutes(context.Background(), session.NewMemory(1, nil), source, 42, now)

	if source.siteID != 42 || !source.from.Equal(time.Date(2026, 10, 8, 11, 31, 0, 0, time.UTC)) || !source.to.Equal(time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("Minutes called with site=%d from=%s to=%s", source.siteID, source.from, source.to)
	}
	if got := snapshot.VisitorsPerMinute[0].Visitors; got != 4 {
		t.Fatalf("oldest minute visitors=%d, want 4", got)
	}
	if got := snapshot.VisitorsPerMinute[28].Visitors; got != 7 {
		t.Fatalf("last closed minute visitors=%d, want 7", got)
	}
	if got := snapshot.VisitorsPerMinute[29].Visitors; got != 0 {
		t.Fatalf("open minute visitors=%d, want 0", got)
	}
}

func TestRichSnapshotWithMinutesTakesMaximumForPartialOverlap(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 45, 0, time.UTC)
	store := session.NewMemory(1, nil)
	store.RecordActivity(session.Activity{SiteID: 1, Timestamp: now.Add(-2 * time.Minute), VisitorID: 1, Kind: 0})
	store.RecordActivity(session.Activity{SiteID: 1, Timestamp: now.Add(-2 * time.Minute), VisitorID: 2, Kind: 0})
	store.RecordActivity(session.Activity{SiteID: 1, Timestamp: now.Add(-time.Minute), VisitorID: 3, Kind: 0})
	source := &minuteSource{minutes: []session.MinuteVisitors{
		{Minute: time.Date(2026, 10, 8, 11, 58, 0, 0, time.UTC), Visitors: 1},
		{Minute: time.Date(2026, 10, 8, 11, 59, 0, 0, time.UTC), Visitors: 5},
	}}

	snapshot := realtime.RichSnapshotWithMinutes(context.Background(), store, source, 1, now)

	if got := snapshot.VisitorsPerMinute[27].Visitors; got != 2 {
		t.Fatalf("memory-larger minute visitors=%d, want 2", got)
	}
	if got := snapshot.VisitorsPerMinute[28].Visitors; got != 5 {
		t.Fatalf("stored-larger minute visitors=%d, want 5", got)
	}
}

func TestRichSnapshotWithMinutesIgnoresStoredOpenMinute(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 45, 0, time.UTC)
	store := session.NewMemory(1, nil)
	store.RecordActivity(session.Activity{SiteID: 1, Timestamp: now, VisitorID: 1, Kind: 0})
	source := &minuteSource{minutes: []session.MinuteVisitors{
		{Minute: time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC), Visitors: 9},
	}}

	snapshot := realtime.RichSnapshotWithMinutes(context.Background(), store, source, 1, now)

	if got := snapshot.VisitorsPerMinute[29].Visitors; got != 1 {
		t.Fatalf("open minute visitors=%d, want memory value 1", got)
	}
}

func TestRichSnapshotWithMinutesFallsBackOnSourceError(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 45, 0, time.UTC)
	store := session.NewMemory(1, nil)
	store.RecordActivity(session.Activity{SiteID: 1, Timestamp: now.Add(-time.Minute), VisitorID: 1, Kind: 0})
	source := &minuteSource{err: errors.New("database unavailable")}

	snapshot := realtime.RichSnapshotWithMinutes(context.Background(), store, source, 1, now)

	if got := snapshot.VisitorsPerMinute[28].Visitors; got != 1 {
		t.Fatalf("fallback minute visitors=%d, want memory value 1", got)
	}
}

func TestRichSnapshotWithMinutesNilSourceIsUnchanged(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 45, 0, time.UTC)
	store := session.NewMemory(1, nil)
	store.RecordActivity(session.Activity{SiteID: 1, Timestamp: now.Add(-time.Minute), VisitorID: 1, Kind: 0})

	want := realtime.RichSnapshot(store, 1, now)
	got := realtime.RichSnapshotWithMinutes(context.Background(), store, nil, 1, now)

	if len(got.VisitorsPerMinute) != len(want.VisitorsPerMinute) {
		t.Fatalf("minute count=%d, want %d", len(got.VisitorsPerMinute), len(want.VisitorsPerMinute))
	}
	for i := range want.VisitorsPerMinute {
		if got.VisitorsPerMinute[i] != want.VisitorsPerMinute[i] {
			t.Fatalf("minute %d=%+v, want %+v", i, got.VisitorsPerMinute[i], want.VisitorsPerMinute[i])
		}
	}
}
