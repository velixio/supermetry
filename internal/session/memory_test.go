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
		Country: "DE", Region: 15, City: 16, Browser: 13, OS: 14, Device: 2,
	}
	r := m.Touch(first)
	m.Touch(session.Hit{
		SiteID: 1, Retention: db.Ret7, TS: t0.Add(time.Minute), LocalDay: t0,
		VisitorID: 9, Kind: 0, PathID: 21, Source: 100, Medium: 101,
		Campaign: 102, Country: "US", Region: 105, City: 106, Browser: 103, OS: 104, Device: 4,
	})
	m.Touch(session.Hit{SiteID: 1, TS: t0.Add(2 * time.Minute), LocalDay: t0, VisitorID: 9, Kind: 0, PathID: 22})

	snapshot := findSnap(t, m.Dirty(10), r.SessionID)
	if snapshot.Pageviews != 2 || snapshot.Events != 1 || snapshot.Entry != 21 || snapshot.Exit != 22 || snapshot.Bounce {
		t.Fatalf("bad metrics: %+v", snapshot)
	}
	if snapshot.Retention != db.Ret30 || snapshot.Source != 10 || snapshot.Medium != 11 || snapshot.Campaign != 12 ||
		snapshot.Country != "DE" || snapshot.Region != 15 || snapshot.City != 16 ||
		snapshot.Browser != 13 || snapshot.OS != 14 || snapshot.Device != 2 {
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
		Entry: 1, Exit: 2, Pageviews: 2, Source: 3, Country: "US", Region: 4, City: 5,
	}})
	if dirty := m.Dirty(10); len(dirty) != 0 {
		t.Fatalf("restore marked session dirty: %+v", dirty)
	}

	r := m.Touch(session.Hit{SiteID: 1, TS: t0.Add(10 * time.Minute), LocalDay: t0, VisitorID: 77, Kind: 0, PathID: 4})
	if r.New || r.SessionID != 500 {
		t.Fatalf("restored session not continued: %+v", r)
	}
	snapshot := findSnap(t, m.Dirty(10), 500)
	if snapshot.Pageviews != 3 || snapshot.Entry != 1 || snapshot.Exit != 4 || snapshot.Region != 4 || snapshot.City != 5 {
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

func TestActiveBreakdownsSinceRanksPagesAndSourcesWithCounts(t *testing.T) {
	m := session.NewMemory(8, realClock{})
	t0 := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	for visitor, hit := range map[int64]struct {
		path   int32
		source int32
	}{
		1: {path: 5, source: 8},
		2: {path: 5, source: 7},
		3: {path: 2, source: 8},
	} {
		m.Touch(session.Hit{
			SiteID: 1, TS: t0.Add(time.Duration(visitor) * time.Minute), LocalDay: t0,
			VisitorID: visitor, Kind: 0, PathID: hit.path, Source: hit.source,
		})
	}
	m.Touch(session.Hit{SiteID: 1, TS: t0.Add(-time.Minute), LocalDay: t0, VisitorID: 4, Kind: 0, PathID: 1, Source: 6})
	m.Touch(session.Hit{SiteID: 2, TS: t0.Add(4 * time.Minute), LocalDay: t0, VisitorID: 5, Kind: 0, PathID: 9, Source: 9})

	visitors, pages, sources := m.ActiveBreakdownsSince(1, t0)
	if visitors != 3 {
		t.Fatalf("active visitors=%d, want 3", visitors)
	}
	wantPages := []session.ActiveValue{{ID: 5, Visitors: 2}, {ID: 2, Visitors: 1}}
	wantSources := []session.ActiveValue{{ID: 8, Visitors: 2}, {ID: 7, Visitors: 1}}
	if !slices.Equal(pages, wantPages) || !slices.Equal(sources, wantSources) {
		t.Fatalf("pages=%v sources=%v, want %v and %v", pages, sources, wantPages, wantSources)
	}
}

func TestRealtimeSnapshotRanksSessionsAndKeepsRollingTotalsBeyondFeed(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 30, 0, time.UTC)
	m := session.NewMemory(4, realClock{})
	m.Touch(session.Hit{SiteID: 1, TS: now.Add(-2 * time.Minute), LocalDay: now, VisitorID: 1, Kind: 0, PathID: 10, Source: 20, Country: "US", Region: 30, City: 40, Browser: 50, OS: 60, Device: 0})
	m.Touch(session.Hit{SiteID: 1, TS: now.Add(-time.Minute), LocalDay: now, VisitorID: 1, Kind: 0, PathID: 11})
	m.Touch(session.Hit{SiteID: 1, TS: now.Add(-time.Minute), LocalDay: now, VisitorID: 2, Kind: 0, PathID: 11, Source: 20, Country: "US", Region: 30, City: 41, Browser: 50, OS: 61, Device: 1})

	for i := 0; i < 5; i++ {
		m.RecordActivity(session.Activity{SiteID: 1, Timestamp: now.Add(-20 * time.Second), VisitorID: 9, Kind: 1, NameID: 70})
	}
	for i := 0; i < 120; i++ {
		m.RecordActivity(session.Activity{SiteID: 1, Timestamp: now.Add(-time.Duration(i) * time.Millisecond), VisitorID: int64(i%3 + 1), Kind: 0, PathID: 11, Country: "US", CityID: 41, Device: 0, BrowserID: 50, SourceID: 20})
	}
	m.RecordActivity(session.Activity{SiteID: 1, Timestamp: now.Add(-31 * time.Minute), VisitorID: 99, Kind: 0, PathID: 99})

	got := m.RealtimeSnapshot(1, now)
	if got.ActiveVisitors != 2 || len(got.Pages) != 1 || got.Pages[0] != (session.ActiveValue{ID: 11, Visitors: 2}) {
		t.Fatalf("active/pages = %d/%v", got.ActiveVisitors, got.Pages)
	}
	if len(got.EntryPages) != 2 || got.EntryPages[0].ID != 10 || got.EntryPages[0].Visitors != 1 {
		t.Fatalf("entry pages = %v", got.EntryPages)
	}
	if len(got.Countries) != 1 || got.Countries[0].CountryCode != "US" || got.Countries[0].Visitors != 2 {
		t.Fatalf("countries = %v", got.Countries)
	}
	if len(got.Devices) != 2 || got.Devices[0].ID != 0 || got.Devices[0].Visitors != 1 {
		t.Fatalf("devices = %v", got.Devices)
	}
	if len(got.VisitorsPerMinute) != 30 || got.VisitorsPerMinute[29].Visitors != 4 {
		t.Fatalf("visitors per minute = %v", got.VisitorsPerMinute)
	}
	if got.Pageviews != 120 || len(got.Events) != 1 || got.Events[0] != (session.ActiveEvent{NameID: 70, Count: 5}) {
		t.Fatalf("rolling pageviews/events = %d/%v", got.Pageviews, got.Events)
	}
	if len(got.RecentActivity) != 50 || got.RecentActivity[0].Timestamp.Before(got.RecentActivity[49].Timestamp) {
		t.Fatalf("recent activity length/order = %d/%v", len(got.RecentActivity), got.RecentActivity[:min(2, len(got.RecentActivity))])
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

func TestJoinOnlyHitDoesNotStartSession(t *testing.T) {
	m := session.NewMemory(4, realClock{})
	t0 := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	first := m.Touch(session.Hit{SiteID: 1, TS: t0, LocalDay: t0, VisitorID: 7, Kind: 0, PathID: 1})
	m.Dirty(10)

	// A ping that arrives after the idle timeout belongs to a session that has
	// already ended, so it must not open a new one.
	late := t0.Add(session.IdleTimeout + time.Minute)
	got := m.Touch(session.Hit{SiteID: 1, TS: late, LocalDay: t0, VisitorID: 7, Kind: 2, JoinOnly: true, EngagementMS: 9000})
	if got.SessionID != 0 || got.New {
		t.Fatalf("join-only hit started or joined a session: %+v", got)
	}
	if dirty := m.Dirty(10); len(dirty) != 0 {
		t.Fatalf("join-only hit changed session state: %+v", dirty)
	}
	if visitors, _ := m.ActiveSince(1, late.Add(-5*time.Minute)); visitors != 0 {
		t.Fatalf("join-only hit made the visitor active: %d", visitors)
	}

	// A visitor with no session at all behaves the same way.
	if got := m.Touch(session.Hit{SiteID: 1, TS: t0, LocalDay: t0, VisitorID: 99, Kind: 1, JoinOnly: true}); got.SessionID != 0 {
		t.Fatalf("join-only hit for unknown visitor started a session: %+v", got)
	}

	// The expired session stays expired: a real pageview starts a new one.
	next := m.Touch(session.Hit{SiteID: 1, TS: late, LocalDay: t0, VisitorID: 7, Kind: 0, PathID: 1})
	if !next.New || next.SessionID == first.SessionID {
		t.Fatalf("pageview after idle did not start a new session: %+v", next)
	}
}

func TestJoinOnlyHitJoinsLiveSession(t *testing.T) {
	m := session.NewMemory(4, realClock{})
	t0 := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	first := m.Touch(session.Hit{SiteID: 1, TS: t0, LocalDay: t0, VisitorID: 7, Kind: 0, PathID: 1})
	got := m.Touch(session.Hit{SiteID: 1, TS: t0.Add(time.Minute), LocalDay: t0, VisitorID: 7, Kind: 2, JoinOnly: true, EngagementMS: 5000})
	if got.SessionID != first.SessionID || got.New {
		t.Fatalf("join-only hit did not join the live session: %+v", got)
	}
	snapshot := findSnap(t, m.Dirty(10), first.SessionID)
	if snapshot.EngagementMS != 5000 || snapshot.Pageviews != 1 {
		t.Fatalf("bad snapshot after join: %+v", snapshot)
	}
}
