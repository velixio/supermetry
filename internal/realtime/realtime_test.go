package realtime_test

import (
	"testing"
	"time"

	"panel/internal/realtime"
	"panel/internal/session"
)

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
