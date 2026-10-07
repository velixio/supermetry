package session_test

import (
	"slices"
	"sync"
	"testing"
	"time"

	"panel/internal/db"
	"panel/internal/salts"
	"panel/internal/session"
)

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

func findSnap(t *testing.T, snapshots []session.Snapshot, id int64) session.Snapshot {
	t.Helper()
	for _, snapshot := range snapshots {
		if snapshot.SessionID == id {
			return snapshot
		}
	}
	t.Fatalf("session %d not found in snapshots", id)
	return session.Snapshot{}
}

func TestSessionIdleTimeoutAndMidnight(t *testing.T) {
	m := session.NewMemory(16, realClock{})
	kol, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 10, 7, 18, 20, 0, 0, time.UTC) // 23:50 local
	day0 := salts.LocalDay(t0, kol)
	r1 := m.Touch(session.Hit{SiteID: 1, TS: t0, LocalDay: day0, VisitorID: 111, Kind: 0, PathID: 1})

	// Twenty minutes later is 00:10 on the next local day. The previous-salt
	// ID keeps this hit in the session that began before midnight.
	t1 := t0.Add(20 * time.Minute)
	r2 := m.Touch(session.Hit{
		SiteID:        1,
		TS:            t1,
		LocalDay:      salts.LocalDay(t1, kol),
		VisitorID:     222,
		PrevVisitorID: 111,
		Kind:          0,
		PathID:        2,
	})
	if r2.New || r2.SessionID != r1.SessionID || r2.VisitorID != 111 {
		t.Fatalf("midnight split: first=%+v second=%+v", r1, r2)
	}

	// Thirty-one minutes of idle time starts a new session.
	r3 := m.Touch(session.Hit{
		SiteID:    1,
		TS:        t1.Add(31 * time.Minute),
		LocalDay:  salts.LocalDay(t1, kol),
		VisitorID: 222,
		Kind:      0,
		PathID:    1,
	})
	if !r3.New {
		t.Fatal("idle timeout not applied")
	}

	snapshot := findSnap(t, m.Dirty(100), r1.SessionID)
	if snapshot.Pageviews != 2 || snapshot.LocalDay != day0 || snapshot.Bounce {
		t.Fatalf("bad snapshot: %+v", snapshot)
	}
}

func TestEngagementDoesNotCountAsEvent(t *testing.T) {
	m := session.NewMemory(4, realClock{})
	t0 := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	r := m.Touch(session.Hit{SiteID: 1, TS: t0, LocalDay: t0, VisitorID: 7, Kind: 0, PathID: 1})
	m.Touch(session.Hit{
		SiteID:       1,
		TS:           t0.Add(time.Minute),
		LocalDay:     t0,
		VisitorID:    7,
		Kind:         2,
		PathID:       1,
		EngagementMS: 42000,
		ScrollPct:    80,
	})

	snapshot := findSnap(t, m.Dirty(10), r.SessionID)
	if snapshot.Pageviews != 1 || snapshot.Events != 0 || snapshot.EngagementMS != 42000 {
		t.Fatalf("bad engagement snapshot: %+v", snapshot)
	}
	if snapshot.Last != t0.Add(time.Minute) || snapshot.Entry != 1 || snapshot.Exit != 1 {
		t.Fatalf("engagement changed page metrics or not last activity: %+v", snapshot)
	}
}

func TestSessionMetricsAndFirstHitDimensions(t *testing.T) {
	m := session.NewMemory(4, realClock{})
	t0 := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	first := session.Hit{
		SiteID: 1, Retention: db.Ret30, TS: t0, LocalDay: t0, VisitorID: 9,
		Kind: 1, NameID: 7, Source: 10, Medium: 11, Campaign: 12,
		Country: "DE", Browser: 13, OS: 14, Device: 2,
	}
	r := m.Touch(first)
	m.Touch(session.Hit{
		SiteID: 1, Retention: db.Ret7, TS: t0.Add(time.Minute), LocalDay: t0,
		VisitorID: 9, Kind: 0, PathID: 21, Source: 100, Medium: 101,
		Campaign: 102, Country: "US", Browser: 103, OS: 104, Device: 4,
	})
	m.Touch(session.Hit{SiteID: 1, TS: t0.Add(2 * time.Minute), LocalDay: t0, VisitorID: 9, Kind: 0, PathID: 22})

	snapshot := findSnap(t, m.Dirty(10), r.SessionID)
	if snapshot.Pageviews != 2 || snapshot.Events != 1 || snapshot.Entry != 21 || snapshot.Exit != 22 || snapshot.Bounce {
		t.Fatalf("bad metrics: %+v", snapshot)
	}
	if snapshot.Retention != db.Ret30 || snapshot.Source != 10 || snapshot.Medium != 11 || snapshot.Campaign != 12 ||
		snapshot.Country != "DE" || snapshot.Browser != 13 || snapshot.OS != 14 || snapshot.Device != 2 {
		t.Fatalf("first-hit dimensions changed: %+v", snapshot)
	}
}

func TestDirtyHonorsLimitAndOnlyReturnsChanges(t *testing.T) {
	m := session.NewMemory(1, realClock{})
	t0 := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	first := m.Touch(session.Hit{SiteID: 1, TS: t0, LocalDay: t0, VisitorID: 1, Kind: 0, PathID: 1})
	m.Touch(session.Hit{SiteID: 1, TS: t0.Add(time.Second), LocalDay: t0, VisitorID: 1, Kind: 0, PathID: 2})
	second := m.Touch(session.Hit{SiteID: 1, TS: t0, LocalDay: t0, VisitorID: 2, Kind: 0, PathID: 3})

	a := m.Dirty(1)
	b := m.Dirty(10)
	if len(a) != 1 || len(b) != 1 {
		t.Fatalf("dirty batches have lengths %d and %d, want 1 and 1", len(a), len(b))
	}
	gotIDs := []int64{a[0].SessionID, b[0].SessionID}
	slices.Sort(gotIDs)
	wantIDs := []int64{first.SessionID, second.SessionID}
	slices.Sort(wantIDs)
	if !slices.Equal(gotIDs, wantIDs) {
		t.Fatalf("dirty IDs = %v, want %v", gotIDs, wantIDs)
	}
	if got := m.Dirty(10); len(got) != 0 {
		t.Fatalf("unchanged sessions returned again: %+v", got)
	}

	m.Touch(session.Hit{SiteID: 1, TS: t0.Add(2 * time.Second), LocalDay: t0, VisitorID: 1, Kind: 1})
	if got := m.Dirty(10); len(got) != 1 || got[0].SessionID != first.SessionID || got[0].Events != 1 {
		t.Fatalf("changed session not returned once: %+v", got)
	}
}

func TestRestoreContinuesSessionAndExpireUsesStrictIdleTimeout(t *testing.T) {
	m := session.NewMemory(4, realClock{})
	t0 := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	m.Restore([]session.Snapshot{{
		SiteID: 1, Retention: db.Ret90, SessionID: 500, VisitorID: 77,
		Start: t0, Last: t0.Add(5 * time.Minute), LocalDay: t0,
		Entry: 1, Exit: 2, Pageviews: 2, Source: 3, Country: "US",
	}})
	if dirty := m.Dirty(10); len(dirty) != 0 {
		t.Fatalf("restore marked session dirty: %+v", dirty)
	}

	r := m.Touch(session.Hit{SiteID: 1, TS: t0.Add(10 * time.Minute), LocalDay: t0, VisitorID: 77, Kind: 0, PathID: 4})
	if r.New || r.SessionID != 500 {
		t.Fatalf("restored session not continued: %+v", r)
	}
	snapshot := findSnap(t, m.Dirty(10), 500)
	if snapshot.Pageviews != 3 || snapshot.Entry != 1 || snapshot.Exit != 4 {
		t.Fatalf("continued snapshot wrong: %+v", snapshot)
	}
	if expired := m.Expire(snapshot.Last.Add(30 * time.Minute)); expired != 0 {
		t.Fatalf("expired at exact timeout: %d", expired)
	}
	if expired := m.Expire(snapshot.Last.Add(30*time.Minute + time.Nanosecond)); expired != 1 {
		t.Fatalf("expired after timeout = %d, want 1", expired)
	}
}

func TestRestoreKeepsNewestVisitorState(t *testing.T) {
	m := session.NewMemory(2, realClock{})
	t0 := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	m.Restore([]session.Snapshot{
		{SiteID: 1, SessionID: 2, VisitorID: 8, Start: t0, Last: t0.Add(2 * time.Minute), LocalDay: t0, Pageviews: 2},
		{SiteID: 1, SessionID: 1, VisitorID: 8, Start: t0, Last: t0.Add(time.Minute), LocalDay: t0, Pageviews: 1},
	})
	r := m.Touch(session.Hit{SiteID: 1, TS: t0.Add(3 * time.Minute), LocalDay: t0, VisitorID: 8, Kind: 0, PathID: 9})
	if r.New || r.SessionID != 2 {
		t.Fatalf("older restore replaced newer state: %+v", r)
	}
}

func TestActiveSinceCountsVisitorsAndRanksExitPaths(t *testing.T) {
	m := session.NewMemory(8, realClock{})
	t0 := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	for visitor, path := range map[int64]int32{1: 5, 2: 5, 3: 2} {
		m.Touch(session.Hit{SiteID: 1, TS: t0.Add(time.Duration(visitor) * time.Minute), LocalDay: t0, VisitorID: visitor, Kind: 0, PathID: path})
	}
	m.Touch(session.Hit{SiteID: 1, TS: t0.Add(-time.Minute), LocalDay: t0, VisitorID: 4, Kind: 0, PathID: 1})
	m.Touch(session.Hit{SiteID: 2, TS: t0.Add(4 * time.Minute), LocalDay: t0, VisitorID: 5, Kind: 0, PathID: 9})

	visitors, paths := m.ActiveSince(1, t0)
	if visitors != 3 || !slices.Equal(paths, []int32{5, 2}) {
		t.Fatalf("active visitors=%d paths=%v, want 3 [5 2]", visitors, paths)
	}
}

func TestCountersRemainExactPastCompactRange(t *testing.T) {
	m := session.NewMemory(1, realClock{})
	t0 := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	r := m.Touch(session.Hit{SiteID: 1, TS: t0, LocalDay: t0, VisitorID: 3, Kind: 0, PathID: 1})
	for i := 1; i < 70_000; i++ {
		m.Touch(session.Hit{SiteID: 1, TS: t0.Add(time.Duration(i) * time.Microsecond), LocalDay: t0, VisitorID: 3, Kind: 0, PathID: 1})
	}
	snapshot := findSnap(t, m.Dirty(1), r.SessionID)
	if snapshot.Pageviews != 70_000 {
		t.Fatalf("pageviews = %d, want 70000", snapshot.Pageviews)
	}
}

func TestConcurrentTouchesAreRaceFreeAndExact(t *testing.T) {
	m := session.NewMemory(16, realClock{})
	t0 := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	const touches = 100
	results := make([]session.Result, touches)
	var wg sync.WaitGroup
	for i := range touches {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = m.Touch(session.Hit{SiteID: 1, TS: t0, LocalDay: t0, VisitorID: 10, Kind: 0, PathID: 1})
		}()
	}
	wg.Wait()

	newCount := 0
	for _, result := range results {
		if result.New {
			newCount++
		}
		if result.SessionID != results[0].SessionID {
			t.Fatalf("concurrent touches split sessions: %+v", results)
		}
	}
	if newCount != 1 {
		t.Fatalf("new results = %d, want 1", newCount)
	}
	if snapshot := findSnap(t, m.Dirty(1), results[0].SessionID); snapshot.Pageviews != touches {
		t.Fatalf("pageviews = %d, want %d", snapshot.Pageviews, touches)
	}
}
