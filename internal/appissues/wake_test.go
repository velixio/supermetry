package appissues_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"supermetry/internal/appissues"
	"supermetry/internal/db"
)

// fakeClock is a settable clock shared by the service and the jobs.
type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time          { return c.t }
func (c *fakeClock) Advance(d time.Duration) { c.t = c.t.Add(d) }

type wakeFixture struct {
	fixture
	clock *fakeClock
	waker *appissues.Waker
}

func newWakeFixture(t *testing.T) wakeFixture {
	t.Helper()
	f := newFixture(t)
	clk := &fakeClock{t: testNow}
	f.svc = appissues.New(f.pool, clk.Now)
	if err := db.EnsureAppEventPartitions(context.Background(), f.pool, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	return wakeFixture{fixture: f, clock: clk, waker: appissues.NewWaker(f.pool, clk.Now)}
}

var eventSeq int

// events inserts n events for the issue at ts; user "" leaves user_hash null.
func (f wakeFixture) events(t *testing.T, issue int64, n int, ts time.Time, user func(i int) string) {
	t.Helper()
	for i := 0; i < n; i++ {
		eventSeq++
		var hash any
		if user != nil {
			if u := user(i); u != "" {
				hash = []byte(u)
			}
		}
		if _, err := f.pool.Exec(context.Background(), `insert into app_events(site_id,ts,received_at,event_id,issue_id,kind,user_hash)
values (7,$1,$1,$2,$3,'error',$4)`, ts, fmt.Sprintf("e%d", eventSeq), issue, hash); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.pool.Exec(context.Background(), `update app_issues set event_count = event_count + $2 where id = $1`, issue, n); err != nil {
		t.Fatal(err)
	}
}

func (f wakeFixture) status(t *testing.T, issue int64) string {
	t.Helper()
	var s string
	if err := f.pool.QueryRow(context.Background(), `select status from app_issues where id=$1`, issue).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func (f wakeFixture) run(t *testing.T) int {
	t.Helper()
	n, err := f.waker.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func (f wakeFixture) snooze(t *testing.T, sn appissues.Snooze) {
	t.Helper()
	if _, err := f.svc.Update(context.Background(), 7, []int64{f.issue}, f.actor, appissues.Change{Snooze: &sn}); err != nil {
		t.Fatal(err)
	}
}

func TestWakeSnoozeUntil(t *testing.T) {
	f := newWakeFixture(t)
	until := testNow.Add(2 * time.Hour)
	f.snooze(t, appissues.Snooze{Until: &until})
	f.clock.Advance(2*time.Hour - time.Second)
	if f.run(t) != 0 || f.status(t, f.issue) != "ignored" {
		t.Fatal("woke before the deadline")
	}
	f.clock.Advance(time.Second)
	if f.run(t) != 1 || f.status(t, f.issue) != "open" {
		t.Fatalf("status = %s, want open at the deadline", f.status(t, f.issue))
	}
	kinds := f.activity(t, f.issue)
	if last := kinds[len(kinds)-1]; last != "status" {
		t.Fatalf("activity = %v, want a final status row", kinds)
	}
	var actor *int64
	var woke string
	if err := f.pool.QueryRow(context.Background(), `select actor_user_id, data->>'woke' from app_issue_activity where issue_id=$1 and kind='status'`, f.issue).Scan(&actor, &woke); err != nil || actor != nil || woke != "until" {
		t.Fatalf("actor=%v woke=%q err=%v, want system and until", actor, woke, err)
	}
	// Snooze state is cleared, and a second run does nothing.
	var until2 *time.Time
	_ = f.pool.QueryRow(context.Background(), `select snooze_until from app_issues where id=$1`, f.issue).Scan(&until2)
	if until2 != nil || f.run(t) != 0 {
		t.Fatal("snooze not cleared or woke twice")
	}
}

func TestWakeSnoozeEvents(t *testing.T) {
	f := newWakeFixture(t)
	f.snooze(t, appissues.Snooze{Events: i64(10)}) // issue has 5 events at snooze time
	f.events(t, f.issue, 9, testNow, nil)
	if f.run(t) != 0 {
		t.Fatal("woke at 9 of 10 more events")
	}
	f.events(t, f.issue, 1, testNow, nil)
	if f.run(t) != 1 || f.status(t, f.issue) != "open" {
		t.Fatalf("status = %s, want open at 10 more events", f.status(t, f.issue))
	}
}

func TestWakeSnoozeUsersCountsDistinctUsersSinceSnooze(t *testing.T) {
	f := newWakeFixture(t)
	// Two users were seen before the snooze: they are the baseline.
	f.events(t, f.issue, 2, testNow.Add(-time.Hour), func(i int) string { return fmt.Sprintf("old%d", i) })
	f.snooze(t, appissues.Snooze{Users: i64(3)})
	// Repeat events from the old users, and many events without a user: no wake.
	f.events(t, f.issue, 4, testNow, func(i int) string { return fmt.Sprintf("old%d", i%2) })
	f.events(t, f.issue, 200, testNow, nil)
	if f.run(t) != 0 || f.status(t, f.issue) != "ignored" {
		t.Fatal("woke on events without users or without new users")
	}
	f.events(t, f.issue, 2, testNow, func(i int) string { return fmt.Sprintf("new%d", i) })
	if f.run(t) != 0 {
		t.Fatal("woke at 2 of 3 new users")
	}
	f.events(t, f.issue, 1, testNow, func(int) string { return "new2" })
	if f.run(t) != 1 || f.status(t, f.issue) != "open" {
		t.Fatalf("status = %s, want open at 3 new users", f.status(t, f.issue))
	}
}

// Review focus 5: "until 100 users" does not wake on userless events.
func TestWakeUsersSnoozeIgnoresUserlessEvents(t *testing.T) {
	f := newWakeFixture(t)
	f.snooze(t, appissues.Snooze{Users: i64(100)})
	f.events(t, f.issue, 500, testNow, nil)
	if f.run(t) != 0 || f.status(t, f.issue) != "ignored" {
		t.Fatalf("status = %s, want ignored: 500 events with user_hash null add no users", f.status(t, f.issue))
	}
}

func TestWakeLeavesPlainIgnoreAndOtherStatusesAlone(t *testing.T) {
	f := newWakeFixture(t)
	if _, err := f.svc.Update(context.Background(), 7, []int64{f.issue}, f.actor, appissues.Change{Status: s("ignored")}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Update(context.Background(), 7, []int64{f.other}, f.actor, appissues.Change{Status: s("resolved")}); err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(1000 * time.Hour)
	f.events(t, f.issue, 50, f.clock.Now(), func(i int) string { return fmt.Sprint(i) })
	if f.run(t) != 0 || f.status(t, f.issue) != "ignored" || f.status(t, f.other) != "resolved" {
		t.Fatal("plain ignore or resolved issue was changed")
	}
}

func TestEscalationRule(t *testing.T) {
	for _, c := range []struct {
		name       string
		last, prev int64
		want       bool
	}{
		{"steady 5x but not above 10", 5, 168, false}, // avg 1/h, 5/h
		{"exactly 10 is not above 10", 10, 0, false},
		{"11 with no history", 11, 0, true},
		{"11 but 5x of avg 3/h is 15", 11, 3 * 168, false},
		{"above 5x and above 10", 16, 3 * 168, true},
		{"exactly 5x avg and above 10", 15, 3 * 168, false}, // must exceed, not equal
	} {
		if got := appissues.Escalates(c.last, c.prev); got != c.want {
			t.Errorf("%s: Escalates(%d, %d) = %v, want %v", c.name, c.last, c.prev, got, c.want)
		}
	}
}

func TestWakeEscalatesArchivedIssue(t *testing.T) {
	f := newWakeFixture(t)
	if _, err := f.svc.Update(context.Background(), 7, []int64{f.issue, f.other}, f.actor, appissues.Change{Archive: b(true)}); err != nil {
		t.Fatal(err)
	}
	now := f.clock.Now()
	// Steady history: 1 event per hour for 7 days on both issues; the last hour
	// of the steady issue has 5 events (5x the average but not above 10).
	for h := 2; h <= 7*24; h++ {
		f.events(t, f.issue, 1, now.Add(-time.Duration(h)*time.Hour).Add(time.Minute), nil)
		f.events(t, f.other, 1, now.Add(-time.Duration(h)*time.Hour).Add(time.Minute), nil)
	}
	f.events(t, f.other, 5, now.Add(-10*time.Minute), nil)
	f.events(t, f.issue, 12, now.Add(-10*time.Minute), nil)
	if f.run(t) != 1 {
		t.Fatal("want exactly one escalation")
	}
	if f.status(t, f.issue) != "open" || f.status(t, f.other) != "archived" {
		t.Fatalf("statuses = %s / %s, want open / archived", f.status(t, f.issue), f.status(t, f.other))
	}
	var archived bool
	_ = f.pool.QueryRow(context.Background(), `select archived_until_escalating from app_issues where id=$1`, f.issue).Scan(&archived)
	kinds := f.activity(t, f.issue)
	if archived || kinds[len(kinds)-1] != "escalated" {
		t.Fatalf("archived=%v activity=%v, want cleared flag and an escalated row", archived, kinds)
	}
}

func TestWakeDoesNotOverrideAUserChange(t *testing.T) {
	f := newWakeFixture(t)
	until := testNow.Add(time.Hour)
	f.snooze(t, appissues.Snooze{Until: &until})
	if _, err := f.svc.Update(context.Background(), 7, []int64{f.issue}, f.actor, appissues.Change{Status: s("resolved")}); err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(2 * time.Hour)
	if f.run(t) != 0 || f.status(t, f.issue) != "resolved" {
		t.Fatalf("status = %s, want resolved kept", f.status(t, f.issue))
	}
}
