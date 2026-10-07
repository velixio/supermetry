package core

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)

func ev(vid int64, at time.Duration, name, path, source string) *Event {
	return &Event{
		SiteID: 1, Day: DayOf(t0, time.UTC), TS: t0.Add(at).UnixMicro(), VisitorID: vid,
		Name: name, Path: path, Source: source, Country: "DE", Browser: "Chrome", OS: "Windows", Device: "desktop",
	}
}

func assign(t *testing.T, c *Cache, e *Event) *Event {
	t.Helper()
	if !c.Assign(e, nil) {
		t.Fatal("assign rejected")
	}
	return e
}

func collectByID(c *Cache, now int64) map[int64]Session {
	out := map[int64]Session{}
	for _, s := range c.Collect(now, false) {
		out[s.ID()] = s
	}
	return out
}

func TestSessionGapRule(t *testing.T) {
	c := NewCache(CacheConfig{})
	a := assign(t, c, ev(1, 0, "pageview", "/", "google.com"))
	b := assign(t, c, ev(1, 25*time.Minute, "pageview", "/pricing", ""))
	if a.SessionID != b.SessionID {
		t.Fatal("25 min gap must stay in session")
	}
	e30 := assign(t, c, ev(1, 55*time.Minute, "pageview", "/x", "")) // exactly 30 min gap
	if e30.SessionID != a.SessionID {
		t.Fatal("30 min gap (== timeout) must stay in session")
	}
	d := assign(t, c, ev(1, 55*time.Minute+30*time.Minute+time.Second, "pageview", "/y", "bing.com"))
	if d.SessionID == a.SessionID {
		t.Fatal(">30 min gap must start a new session")
	}
	o := assign(t, c, ev(2, 26*time.Minute, "pageview", "/", ""))
	if o.SessionID == a.SessionID || o.SessionID == d.SessionID {
		t.Fatal("other visitor shares session")
	}
}

func TestSessionDimsCopiedFromFirstEvent(t *testing.T) {
	c := NewCache(CacheConfig{})
	a := ev(1, 0, "pageview", "/", "google.com")
	a.UTMCampaign = "summer"
	assign(t, c, a)
	b := ev(1, time.Minute, "pageview", "/p", "a.test-internal")
	b.Country = "XX"
	assign(t, c, b)
	if b.Source != "google.com" || b.UTMCampaign != "summer" || b.Country != "DE" {
		t.Fatalf("dims not copied from session: %+v", b)
	}
}

func TestSessionMetrics(t *testing.T) {
	c := NewCache(CacheConfig{})
	// Session A: custom first, then 2 pageviews: not a bounce, entry = first pageview.
	assign(t, c, ev(1, 0, "signup", "/", ""))
	assign(t, c, ev(1, 2*time.Minute, "pageview", "/a", ""))
	last := assign(t, c, ev(1, 10*time.Minute, "pageview", "/b", ""))
	// Session B: one pageview: bounce, duration 0.
	b := assign(t, c, ev(2, 0, "pageview", "/only", ""))
	// Session C: one pageview + one custom event: not a bounce.
	cc := assign(t, c, ev(3, 0, "pageview", "/c", ""))
	assign(t, c, ev(3, 90*time.Second, "download", "/c", ""))

	got := collectByID(c, t0.Add(11*time.Minute).UnixMicro())
	sa, sb, sc := got[last.SessionID], got[b.SessionID], got[cc.SessionID]
	if sa.Pageviews != 2 || sa.Custom != 1 || sa.Entry() != "/a" || sa.Exit() != "/b" || sa.Bounce() || sa.DurationMs() != 600_000 {
		t.Errorf("session A wrong: %+v", sa)
	}
	if sb.Pageviews != 1 || sb.Custom != 0 || !sb.Bounce() || sb.DurationMs() != 0 || sb.Entry() != "/only" || sb.Exit() != "/only" {
		t.Errorf("session B wrong: %+v", sb)
	}
	if sc.Bounce() || sc.DurationMs() != 90_000 || sc.Exit() != "/c" {
		t.Errorf("session C wrong: %+v", sc)
	}
	if len(got) != 3 {
		t.Errorf("collected %d sessions", len(got))
	}
	// Nothing changed since: second collect is empty.
	if n := len(c.Collect(0, false)); n != 0 {
		t.Errorf("second collect returned %d", n)
	}
}

func TestSessionCommitRejectLeavesStateUnchanged(t *testing.T) {
	c := NewCache(CacheConfig{})
	a := assign(t, c, ev(1, 0, "pageview", "/", ""))
	rej := ev(1, time.Minute, "pageview", "/nope", "")
	if c.Assign(rej, func(*Event) bool { return false }) {
		t.Fatal("expected reject")
	}
	rejNew := ev(9, time.Minute, "pageview", "/nope", "")
	if c.Assign(rejNew, func(*Event) bool { return false }) {
		t.Fatal("expected reject")
	}
	got := collectByID(c, 0)
	if s := got[a.SessionID]; len(got) != 1 || s.Pageviews != 1 || s.Exit() != "/" {
		t.Fatalf("rejected events changed state: %+v", got)
	}
}

func TestSessionEvictionAndReplacement(t *testing.T) {
	var retired []Session
	c := NewCache(CacheConfig{OnRetire: func(s *Session) { retired = append(retired, *s) }})
	a := assign(t, c, ev(1, 0, "pageview", "/", ""))
	// Replacement: the expired session is retired and still collected once.
	b := assign(t, c, ev(1, 2*time.Hour, "pageview", "/2", ""))
	if len(retired) != 1 || retired[0].ID() != a.SessionID {
		t.Fatalf("replacement not retired: %+v", retired)
	}
	got := collectByID(c, t0.Add(2*time.Hour).UnixMicro())
	if _, ok := got[a.SessionID]; !ok {
		t.Fatal("replaced dirty session lost before flush")
	}
	if c.Len() != 1 {
		t.Fatalf("len %d", c.Len())
	}
	// Idle eviction relative to "now".
	c.Collect(t0.Add(2*time.Hour+31*time.Minute).UnixMicro(), true)
	if c.Len() != 0 || len(retired) != 2 || retired[1].ID() != b.SessionID {
		t.Fatalf("idle session not evicted: len=%d retired=%d", c.Len(), len(retired))
	}
}

func TestSessionRealtimeActive(t *testing.T) {
	c := NewCache(CacheConfig{})
	assign(t, c, ev(1, 0, "pageview", "/", ""))
	assign(t, c, ev(2, 4*time.Minute, "pageview", "/", ""))
	assign(t, c, ev(3, 9*time.Minute, "pageview", "/", ""))
	other := ev(4, 9*time.Minute, "pageview", "/", "")
	other.SiteID = 2
	assign(t, c, other)
	now := t0.Add(10 * time.Minute)
	if n := c.Active(1, now.Add(-5*time.Minute).UnixMicro()); n != 1 {
		t.Fatalf("active = %d, want 1", n)
	}
	if n := c.Active(1, now.Add(-6*time.Minute).UnixMicro()); n != 2 {
		t.Fatalf("active = %d, want 2", n)
	}
	if w := c.Watermark(); w != t0.Add(9*time.Minute).UnixMicro() {
		t.Fatalf("watermark %d", w)
	}
}

func TestSessionTrackPaths(t *testing.T) {
	var retired []Session
	c := NewCache(CacheConfig{TrackPaths: true, OnRetire: func(s *Session) { retired = append(retired, *s) }})
	assign(t, c, ev(1, 0, "pageview", "/a", ""))
	assign(t, c, ev(1, time.Minute, "pageview", "/b", ""))
	assign(t, c, ev(1, 2*time.Minute, "pageview", "/a", ""))
	c.RetireAll()
	if len(retired) != 1 || len(retired[0].Paths()) != 2 {
		t.Fatalf("paths: %+v", retired)
	}
}

func TestRestoreOpenSessionContinuesWithoutNewVisit(t *testing.T) {
	start := t0.UnixMicro()
	restored := RestoreSession(SessionState{
		SiteID: 1, VisitorID: 77, Day: DayOf(t0, time.UTC), Start: start,
		Last: start + (5 * time.Minute).Microseconds(), Pageviews: 2,
		Entry: "/", Exit: "/pricing", Source: "google", Country: "US",
		Browser: "Firefox", OS: "Linux", Device: "desktop",
	})
	c := NewCache(CacheConfig{})
	if !c.Restore(restored) {
		t.Fatal("restore rejected")
	}
	next := ev(77, 10*time.Minute, "pageview", "/done", "")
	if !c.Assign(next, nil) {
		t.Fatal("assign rejected")
	}
	if next.SessionID != restored.ID() {
		t.Fatalf("session id = %d, want restored %d", next.SessionID, restored.ID())
	}
	got := c.Collect(t0.Add(10*time.Minute).UnixMicro(), false)
	if len(got) != 1 || got[0].Pageviews != 3 || got[0].Entry() != "/" || got[0].Exit() != "/done" {
		t.Fatalf("continued session = %+v", got)
	}
}

func TestRebuildSessionUsesEveryDurableEvent(t *testing.T) {
	start := t0.UnixMicro()
	// The event that originally established the session ID was still buffered
	// at the crash; the first surviving durable event has a later timestamp.
	sid := sessionID(91, start-time.Minute.Microseconds())
	events := []Event{
		{SiteID: 1, Day: DayOf(t0, time.UTC), TS: start, VisitorID: 91, SessionID: sid,
			Name: "signup", Path: "/before", Source: "google", Country: "US", Browser: "Firefox", OS: "Linux", Device: "desktop"},
		{SiteID: 1, Day: DayOf(t0, time.UTC), TS: start + time.Minute.Microseconds(), VisitorID: 91, SessionID: sid,
			Name: "pageview", Path: "/entry", Source: "google", Country: "US", Browser: "Firefox", OS: "Linux", Device: "desktop"},
		{SiteID: 1, Day: DayOf(t0, time.UTC), TS: start + 4*time.Minute.Microseconds(), VisitorID: 91, SessionID: sid,
			Name: "pageview", Path: "/exit", Source: "google", Country: "US", Browser: "Firefox", OS: "Linux", Device: "desktop"},
	}

	got, err := RebuildSession(events)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID() != sid || got.Start != start || got.Last != start+4*time.Minute.Microseconds() {
		t.Fatalf("identity/times = id %d start %d last %d", got.ID(), got.Start, got.Last)
	}
	if got.Pageviews != 2 || got.Custom != 1 || got.Entry() != "/entry" || got.Exit() != "/exit" || got.Bounce() {
		t.Fatalf("rebuilt metrics = %+v entry=%q exit=%q", got, got.Entry(), got.Exit())
	}
	if got.Source() != "google" || got.Country() != "US" || got.Browser() != "Firefox" || got.OS() != "Linux" || got.Device() != "desktop" {
		t.Fatalf("rebuilt dimensions = %+v", got)
	}
}
