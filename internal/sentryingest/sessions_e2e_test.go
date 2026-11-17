package sentryingest

import (
	"bytes"
	"math"
	"net/http"
	"os"
	"testing"
	"time"

	"supermetry/internal/appingest"
)

// The envelopes in testdata/sessions are written by hand from the public
// Sentry envelope and session protocol documentation, one per SDK shape. They
// carry only example data and are not recorded from any SDK or relay.
//
// The tests here pin what the handler does today (Phase 1: only an init
// update stores a session row, later updates are dropped) next to the
// release-health values Phase 2a must produce once sessions are tracked by
// state.

// sessionsNow is just after the envelopes' sent_at, so no clock skew
// correction applies and the start times stay as written.
var sessionsNow = time.Date(2026, 11, 17, 10, 0, 10, 0, time.UTC)

// todayWant is today's behaviour per fixture. Session rows are what the
// handler queues as kind=session; occurrences are summed over those rows.
type todayWant struct {
	sessionRows   int
	occurrences   int
	crashedOcc    int // rows with ExitReason crashed
	abnormalOcc   int // rows with ExitReason abnormal
	installID     string
	eventKind     string // "" when the envelope has no event
	eventExit     string
	eventErrType  string
	eventPlatform string
	release       string
}

var todaySessions = map[string]todayWant{
	// Browser session, init=true, no duration, no did.
	"javascript": {sessionRows: 1, occurrences: 1, release: "web-app@2.0.0"},
	// sessions aggregates: exited 5, errored 2 | exited 3, crashed 1 -> four
	// rows, 11 sessions.
	"node": {sessionRows: 4, occurrences: 11, crashedOcc: 1, release: "api@2.0.0"},
	// exited 4, errored 1, abnormal 1 -> three rows, 6 sessions.
	"python": {sessionRows: 3, occurrences: 6, abnormalOcc: 1, release: "worker@2.0.0"},
	// Crashed session sent on the next start: init=true, status crashed.
	"electron": {sessionRows: 1, occurrences: 1, crashedOcc: 1, installID: "install-electron-1", release: "desktop@2.0.0"},
	// ANR event plus an abnormal update (init=false). Today the update is
	// dropped: no session row at all.
	"android": {sessionRows: 0, eventKind: "hang", eventExit: "anr", eventErrType: "ApplicationNotResponding", eventPlatform: "android", release: "mobile@2.0.0"},
	// init=true ok, app hang event, then a crashed update (init=false).
	// Today only the init row is stored, with no exit reason: the crashed
	// session counts as healthy. Phase 2a fixes exactly this.
	"cocoa": {sessionRows: 1, occurrences: 1, installID: "install-cocoa-1", eventKind: "hang", eventExit: "app_hang", eventErrType: "App Hanging", eventPlatform: "ios", release: "ios-app@2.0.0"},
}

func TestSessionFixturesThroughHandlerToday(t *testing.T) {
	for name, w := range todaySessions {
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile("testdata/sessions/" + name + ".envelope")
			if err != nil {
				t.Fatal(err)
			}
			e := newEnv(t)
			*e.now = sessionsNow
			if rec := e.do(post(fdPath, fdQuery, string(raw), nil)); rec.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", rec.Code, rec.Body)
			}
			var sessions, events []appingest.Event
			for n := len(e.intake.Queue()); n > 0; n-- {
				q := <-e.intake.Queue()
				if q.Event.Kind == "session" {
					sessions = append(sessions, q.Event)
				} else {
					events = append(events, q.Event)
				}
			}
			var occ, crashed, abnormal int
			for _, s := range sessions {
				occ += s.Occurrences
				switch s.ExitReason {
				case "crashed":
					crashed += s.Occurrences
				case "abnormal":
					abnormal += s.Occurrences
				}
				if s.Release != w.release || s.Environment != "production" {
					t.Errorf("session row release/env = %q/%q", s.Release, s.Environment)
				}
				if s.SessionID != "" && s.InstallID != w.installID {
					t.Errorf("install id = %q, want %q", s.InstallID, w.installID)
				}
				if !s.TS.Before(sessionsNow) {
					t.Errorf("session start %s not taken from the payload", s.TS)
				}
			}
			if len(sessions) != w.sessionRows || occ != w.occurrences || crashed != w.crashedOcc || abnormal != w.abnormalOcc {
				t.Errorf("session rows/occurrences/crashed/abnormal = %d/%d/%d/%d, want %d/%d/%d/%d",
					len(sessions), occ, crashed, abnormal, w.sessionRows, w.occurrences, w.crashedOcc, w.abnormalOcc)
			}
			if w.eventKind == "" {
				if len(events) != 0 {
					t.Fatalf("unexpected events %+v", events)
				}
				return
			}
			if len(events) != 1 {
				t.Fatalf("events = %d, want 1", len(events))
			}
			ev := events[0]
			if ev.Kind != w.eventKind || ev.ExitReason != w.eventExit || ev.ErrorType != w.eventErrType || ev.Platform != w.eventPlatform || ev.Release != w.release {
				t.Errorf("event kind/exit/type/platform/release = %s/%s/%s/%s/%s, want %s/%s/%s/%s/%s",
					ev.Kind, ev.ExitReason, ev.ErrorType, ev.Platform, ev.Release, w.eventKind, w.eventExit, w.eventErrType, w.eventPlatform, w.release)
			}
		})
	}
}

// TestHangClassification pins the ANR and app hang classification of
// normalise.go (classify): mechanism ANR or type ApplicationNotResponding
// gives kind=hang, exit_reason=anr; mechanism AppHang or an "App Hang" type
// gives kind=hang, exit_reason=app_hang. Neither is a crash.
func TestHangClassification(t *testing.T) {
	for _, c := range []struct {
		name, kind, exit string
	}{{"android", "hang", "anr"}, {"cocoa", "hang", "app_hang"}} {
		raw, err := os.ReadFile("testdata/sessions/" + c.name + ".envelope")
		if err != nil {
			t.Fatal(err)
		}
		env, err := ParseEnvelope(bytes.NewReader(raw), Limits{})
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		found := false
		for _, it := range env.Items {
			if it.Type != "event" {
				continue
			}
			found = true
			ev, _, err := Normalise(it.Payload, EventContext{}, testSite(), sessionsNow)
			if err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			if ev.Kind != c.kind || ev.ExitReason != c.exit {
				t.Errorf("%s: kind/exit_reason = %s/%s, want %s/%s", c.name, ev.Kind, ev.ExitReason, c.kind, c.exit)
			}
		}
		if !found {
			t.Errorf("%s: no event item", c.name)
		}
	}
}

// afterWant is the release health Phase 2a must report for each fixture once
// sessions are tracked by state (worst status of a session wins). Every number
// is computed by hand from the envelope, not from the parser:
//
//   - crash-free sessions = 1 - crashed / sessions; abnormal (ANR, app hang,
//     watchdog) is not a crash, as in Sentry, and is reported as its own count.
//   - crash-free users = 1 - crashed users / users; aggregates carry no did, so
//     they add sessions but no users and leave the user rate null.
//
// javascript: 1 session, ok                          -> sessions 1, cfs 1
// node:       5+2 exited/errored, 3 exited, 1 crashed -> sessions 11, crashed 1, errored 2, cfs 10/11
// python:     4 exited, 1 errored, 1 abnormal         -> sessions 6, abnormal 1, errored 1, cfs 1
// electron:   1 session crashed, one install          -> sessions 1, crashed 1, users 1, cfs 0, cfu 0
// android:    1 session abnormal (after an ANR)       -> sessions 1, abnormal 1, users 1, cfs 1, cfu 1
// cocoa:      1 session ok then crashed (one session) -> sessions 1, crashed 1, users 1, cfs 0, cfu 0
type afterWant struct {
	sessions, errored, crashed, abnormal int64
	users, crashedUsers                  int64
	cfs                                  float64
	cfu                                  *float64 // nil: no users, rate is null
}

func ptr(v float64) *float64 { return &v }

var afterSessions = map[string]afterWant{
	"javascript": {sessions: 1, cfs: 1},
	"node":       {sessions: 11, errored: 2, crashed: 1, cfs: 10.0 / 11},
	"python":     {sessions: 6, errored: 1, abnormal: 1, cfs: 1},
	"electron":   {sessions: 1, crashed: 1, users: 1, crashedUsers: 1, cfs: 0, cfu: ptr(0)},
	"android":    {sessions: 1, abnormal: 1, users: 1, cfs: 1, cfu: ptr(1)},
	"cocoa":      {sessions: 1, crashed: 1, users: 1, crashedUsers: 1, cfs: 0, cfu: ptr(0)},
}

// TestSessionExpectationsAreConsistent checks the hand-computed table against
// the formulas, so a typo in either shows up now rather than in Phase 2a.
func TestSessionExpectationsAreConsistent(t *testing.T) {
	if len(afterSessions) != len(todaySessions) {
		t.Fatalf("%d after-2a rows for %d fixtures", len(afterSessions), len(todaySessions))
	}
	for name, w := range afterSessions {
		if got := 1 - float64(w.crashed)/float64(w.sessions); math.Abs(got-w.cfs) > 1e-12 {
			t.Errorf("%s: crash-free sessions formula %v, table %v", name, got, w.cfs)
		}
		if w.users == 0 {
			if w.cfu != nil {
				t.Errorf("%s: no users but a user rate", name)
			}
			continue
		}
		if got := 1 - float64(w.crashedUsers)/float64(w.users); w.cfu == nil || math.Abs(got-*w.cfu) > 1e-12 {
			t.Errorf("%s: crash-free users formula %v, table %v", name, got, w.cfu)
		}
	}
	// The status counters of the aggregate fixtures match what today's parser
	// sees, as occurrences (node: 11, python: 6).
	for name, w := range todaySessions {
		if w.sessionRows > 1 && afterSessions[name].sessions != int64(w.occurrences) {
			t.Errorf("%s: %d sessions after 2a, %d occurrences today", name, afterSessions[name].sessions, w.occurrences)
		}
	}
}

// TestSessionFixturesReleaseHealthAfter2a posts every fixture, rolls the day
// up and compares the release numbers with afterSessions.
func TestSessionFixturesReleaseHealthAfter2a(t *testing.T) {
	t.Skip("needs app_session_daily (Phase 2a Task 2)")
	// Plan Task 6 step 2: for each fixture, post it through the handler on a
	// fresh site, flush the app_sessions writer and the event writer, call
	// approllup.RecomputeDay for 2026-11-17, then read appquery.Releases and
	// compare Sessions, ErroredSessions, AbnormalSessions, Users and the
	// crash-free rates (CrashFreeSessions, CrashFreeUsers) with afterSessions
	// (all fixtures use the same day, the site time zone is UTC).
	_ = afterSessions
}
