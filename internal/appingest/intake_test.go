package appingest

import (
	"fmt"
	"testing"
	"time"

	"supermetry/internal/db"
	"supermetry/internal/health"
	"supermetry/internal/sites"
)

var testNow = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

func appSite(mutate func(*sites.Site)) sites.Site {
	s := sites.Site{
		ID: 7, PublicID: "appsite00001", Kind: sites.KindApp, TZ: time.UTC, Retention: db.Ret30,
		App: &sites.AppConfig{MaxReportAgeDays: 7},
	}
	if mutate != nil {
		mutate(&s)
	}
	return s
}

func ev(id string) Event {
	return Event{ID: id, Kind: "error", Level: "error", Occurrences: 1, Fingerprint: "ab", Tags: map[string]string{}, TS: testNow}
}

func drain(in *Intake) []Queued {
	var out []Queued
	for {
		select {
		case q := <-in.ch:
			out = append(out, q)
		default:
			return out
		}
	}
}

func TestIntakeAcceptsAndQueues(t *testing.T) {
	in := NewIntake(Config{})
	got := in.Accept(appSite(nil), "k", testNow, ev("a"))
	if got.Reason != "" {
		t.Fatalf("reason = %q", got.Reason)
	}
	q := drain(in)
	if len(q) != 1 || q[0].SiteID != 7 || q[0].Event.ID != "a" || !q[0].ReceivedAt.Equal(testNow) {
		t.Fatalf("queued = %+v", q)
	}
}

func TestIntakeTooOldUsesTheShorterOfAgeAndRetention(t *testing.T) {
	in := NewIntake(Config{})
	site := appSite(func(s *sites.Site) { s.App.MaxReportAgeDays = 30; s.Retention = db.Ret7 })
	old := ev("old")
	old.TS = testNow.Add(-8 * 24 * time.Hour)
	if got := in.Accept(site, "k", testNow, old); got.Reason != "too_old" {
		t.Fatalf("8-day-old report with 7-day retention: %q", got.Reason)
	}
	recent := ev("recent")
	recent.TS = testNow.Add(-6 * 24 * time.Hour)
	if got := in.Accept(site, "k", testNow, recent); got.Reason != "" {
		t.Fatalf("6-day-old report: %q", got.Reason)
	}
	forever := appSite(func(s *sites.Site) { s.App.MaxReportAgeDays = 30; s.Retention = db.RetForever })
	oldish := ev("oldish")
	oldish.TS = testNow.Add(-20 * 24 * time.Hour)
	if got := in.Accept(forever, "k", testNow, oldish); got.Reason != "" {
		t.Fatalf("forever retention uses the report age: %q", got.Reason)
	}
}

func TestIntakeClampsFutureAndFillsMissingTimestamps(t *testing.T) {
	in := NewIntake(Config{})
	future := ev("f")
	future.TS = testNow.Add(10 * time.Minute)
	missing := ev("m")
	missing.TS = time.Time{}
	near := ev("n")
	near.TS = testNow.Add(4 * time.Minute)
	for _, e := range []Event{future, missing, near} {
		if got := in.Accept(appSite(nil), "k", testNow, e); got.Reason != "" {
			t.Fatalf("%s: %q", e.ID, got.Reason)
		}
	}
	q := drain(in)
	if !q[0].Event.TS.Equal(testNow) || !q[1].Event.TS.Equal(testNow) || !q[2].Event.TS.Equal(testNow.Add(4*time.Minute)) {
		t.Fatalf("timestamps: %v %v %v", q[0].Event.TS, q[1].Event.TS, q[2].Event.TS)
	}
}

func TestIntakeDuplicate(t *testing.T) {
	in := NewIntake(Config{})
	if got := in.Accept(appSite(nil), "k", testNow, ev("a")); got.Reason != "" {
		t.Fatal(got.Reason)
	}
	if got := in.Accept(appSite(nil), "k", testNow.Add(time.Minute), ev("a")); got.Reason != "duplicate" {
		t.Fatalf("second send: %q", got.Reason)
	}
	other := appSite(func(s *sites.Site) { s.ID = 8 })
	if got := in.Accept(other, "k2", testNow, ev("a")); got.Reason != "" {
		t.Fatalf("same id on another app: %q", got.Reason)
	}
	if got := in.Accept(appSite(nil), "k", testNow.Add(DedupTTL+time.Minute), ev("a")); got.Reason != "" {
		t.Fatalf("after the window: %q", got.Reason)
	}
}

func TestIntakeKeyBudget(t *testing.T) {
	in := NewIntake(Config{QueueSize: 2000})
	accepted, limited := 0, 0
	for i := range 650 {
		switch got := in.Accept(appSite(nil), "key-a", testNow, ev(fmt.Sprint("e", i))); got.Reason {
		case "":
			accepted++
		case "rate_limited":
			limited++
		default:
			t.Fatal(got.Reason)
		}
	}
	if accepted != 600 || limited != 50 {
		t.Fatalf("accepted=%d limited=%d", accepted, limited)
	}
	if got := in.Accept(appSite(nil), "key-b", testNow, ev("other-key")); got.Reason != "" {
		t.Fatalf("another key has its own budget: %q", got.Reason)
	}
	if got := in.Accept(appSite(nil), "key-a", testNow.Add(time.Minute), ev("later")); got.Reason != "" {
		t.Fatalf("budget refills: %q", got.Reason)
	}
}

func TestIntakeQueueFullDropsAsRateLimitedAndReleasesTheID(t *testing.T) {
	in := NewIntake(Config{QueueSize: 2})
	for _, id := range []string{"a", "b"} {
		if got := in.Accept(appSite(nil), "k", testNow, ev(id)); got.Reason != "" {
			t.Fatal(got.Reason)
		}
	}
	before := health.AppIngestQueueFull.Load()
	if got := in.Accept(appSite(nil), "k", testNow, ev("c")); got.Reason != "rate_limited" {
		t.Fatalf("full queue: %q", got.Reason)
	}
	if health.AppIngestQueueFull.Load() != before+1 {
		t.Fatal("queue-full counter not incremented")
	}
	drain(in)
	if got := in.Accept(appSite(nil), "k", testNow, ev("c")); got.Reason != "" {
		t.Fatalf("a dropped event must be retryable: %q", got.Reason)
	}
}

func TestIntakeStripsTagsOutsideTheAllowlist(t *testing.T) {
	in := NewIntake(Config{})
	site := appSite(func(s *sites.Site) { s.App.TagAllowlist = []string{"stage"} })
	e := ev("a")
	e.Tags = map[string]string{"stage": "load", "secret": "x", "another": "y"}
	got := in.Accept(site, "k", testNow, e)
	if got.Reason != "" || len(got.Stripped) != 2 || got.Stripped[0] != "another" || got.Stripped[1] != "secret" {
		t.Fatalf("outcome = %+v", got)
	}
	q := drain(in)
	if len(q[0].Event.Tags) != 1 || q[0].Event.Tags["stage"] != "load" {
		t.Fatalf("tags = %v", q[0].Event.Tags)
	}
	open := appSite(nil)
	e2 := ev("b")
	e2.Tags = map[string]string{"anything": "goes"}
	if got := in.Accept(open, "k", testNow, e2); len(got.Stripped) != 0 {
		t.Fatalf("empty allowlist strips nothing: %+v", got)
	}
}

func TestIntakeTagCardinalityCap(t *testing.T) {
	in := NewIntake(Config{QueueSize: 3000})
	for i := range 1002 {
		e := ev(fmt.Sprint("e", i))
		e.Tags = map[string]string{"k": fmt.Sprint("v", i)}
		if got := in.Accept(appSite(nil), fmt.Sprint("key", i), testNow, e); got.Reason != "" {
			t.Fatal(got.Reason)
		}
	}
	q := drain(in)
	if q[999].Event.Tags["k"] != "v999" || q[1000].Event.Tags["k"] != "(other)" || q[1001].Event.Tags["k"] != "(other)" {
		t.Fatalf("tags: %q %q %q", q[999].Event.Tags["k"], q[1000].Event.Tags["k"], q[1001].Event.Tags["k"])
	}
	// A value already counted stays itself; a new UTC day starts afresh.
	again := ev("again")
	again.Tags = map[string]string{"k": "v5"}
	in.Accept(appSite(nil), "kk", testNow, again)
	nextDay := ev("next")
	nextDay.Tags = map[string]string{"k": "brand-new"}
	in.Accept(appSite(nil), "kk2", testNow.Add(24*time.Hour), nextDay)
	q = drain(in)
	if q[0].Event.Tags["k"] != "v5" || q[1].Event.Tags["k"] != "brand-new" {
		t.Fatalf("tags: %v %v", q[0].Event.Tags, q[1].Event.Tags)
	}
}

func TestIntakeEnvironments(t *testing.T) {
	in := NewIntake(Config{})
	site := appSite(func(s *sites.Site) { s.App.Environments = []string{"production", "beta"} })
	for i, env := range []string{"production", "staging", ""} {
		e := ev(fmt.Sprint("e", i))
		e.Environment = env
		in.Accept(site, "k", testNow, e)
	}
	q := drain(in)
	if q[0].Event.Environment != "production" || q[1].Event.Environment != "(other)" || q[2].Event.Environment != "" {
		t.Fatalf("environments: %q %q %q", q[0].Event.Environment, q[1].Event.Environment, q[2].Event.Environment)
	}
}
