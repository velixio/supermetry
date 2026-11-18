package sentryingest

import (
	"os"
	"strings"
	"sync"
	"testing"

	"supermetry/internal/appmonitors"
)

type checkInSink struct {
	mu    sync.Mutex
	items []appmonitors.CheckIn
}

func (s *checkInSink) Enqueue(ci appmonitors.CheckIn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = append(s.items, ci)
	return true
}

func readCheckIn(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/checkins/" + name + ".envelope")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	return []byte(lines[2]) // header, item header, payload
}

// The fixtures are hand-built from the documented SDK payload shape (the JS
// SDK's crontab monitor config and the Python SDK's interval one).
func TestParseCheckInFixtures(t *testing.T) {
	js, err := ParseCheckIn(readCheckIn(t, "js"))
	if err != nil {
		t.Fatal(err)
	}
	if js.Slug != "nightly-export" || js.ID != "0f1e2d3c-4b5a-4978-8796-a5b4c3d2e1f0" || js.Status != appmonitors.StatusInProgress ||
		js.Environment != "production" || js.Release != "api@1.4.0" || js.DurationMS != nil {
		t.Fatalf("js check-in: %+v", js)
	}
	want := appmonitors.Config{ScheduleType: "crontab", Schedule: "*/5 * * * *", TZ: "Europe/Berlin", MarginMinutes: 2, MaxRuntimeMinutes: 10, FailureThreshold: 2, RecoveryThreshold: 1}
	if js.Config == nil || *js.Config != want {
		t.Fatalf("js config: %+v", js.Config)
	}

	py, err := ParseCheckIn(readCheckIn(t, "python"))
	if err != nil {
		t.Fatal(err)
	}
	if py.Slug != "sync_users" || py.Status != appmonitors.StatusOK || py.Environment != "staging" || py.DurationMS == nil || *py.DurationMS != 12500 {
		t.Fatalf("python check-in: %+v", py)
	}
	want = appmonitors.Config{ScheduleType: "interval", Schedule: "2 hour", MarginMinutes: 5, MaxRuntimeMinutes: 30}
	if py.Config == nil || *py.Config != want {
		t.Fatalf("python config: %+v", py.Config)
	}
}

func TestParseCheckInRejects(t *testing.T) {
	const id = `"check_in_id":"0f1e2d3c4b5a49788796a5b4c3d2e1f0"`
	cases := map[string]string{
		"no id":           `{"monitor_slug":"a","status":"ok"}`,
		"bad id":          `{"check_in_id":"nope","monitor_slug":"a","status":"ok"}`,
		"bad slug":        `{` + id + `,"monitor_slug":"a b","status":"ok"}`,
		"no slug":         `{` + id + `,"status":"ok"}`,
		"bad status":      `{` + id + `,"monitor_slug":"a","status":"missed"}`,
		"bad schedule":    `{` + id + `,"monitor_slug":"a","status":"ok","monitor_config":{"schedule":{"type":"weekly","value":1}}}`,
		"fractional ival": `{` + id + `,"monitor_slug":"a","status":"ok","monitor_config":{"schedule":{"type":"interval","value":1.5,"unit":"hour"}}}`,
		"not json":        `[`,
	}
	for name, payload := range cases {
		if _, err := ParseCheckIn([]byte(payload)); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func checkInEnv(t *testing.T, name string) (*env, *checkInSink) {
	t.Helper()
	e := newStubEnv(t)
	sink := &checkInSink{}
	e.h.WithCheckIns(sink)
	raw, err := os.ReadFile("testdata/checkins/" + name + ".envelope")
	if err != nil {
		t.Fatal(err)
	}
	if w := e.do(post(fdPath, fdQuery, string(raw), nil)); w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	} else if limits := w.Header().Get("X-Sentry-Rate-Limits"); strings.Contains(limits, "check_in") {
		t.Fatalf("check_in must not be rejected any more: %q", limits)
	}
	return e, sink
}

func TestHandlerQueuesCheckIns(t *testing.T) {
	for _, name := range []string{"js", "python"} {
		e, sink := checkInEnv(t, name)
		if len(sink.items) != 1 || sink.items[0].SiteID != fdSite || !sink.items[0].At.Equal(fdNow.UTC()) {
			t.Fatalf("%s: queued %+v", name, sink.items)
		}
		if e.queued() != 0 {
			t.Fatalf("%s: a check-in must not become an event", name)
		}
		if got := e.h.Stats().Snapshot()["checkin_queued"]; got != 1 {
			t.Fatalf("%s: checkin_queued = %d", name, got)
		}
	}
}

func TestHandlerCountsInvalidAndDiscardedCheckIns(t *testing.T) {
	e := newStubEnv(t)
	body := `{"event_id":"cccccccccccccccccccccccccccccccc"}` + "\n" + `{"type":"check_in"}` + "\n" + `{"monitor_slug":"x"}` + "\n"
	if w := e.do(post(fdPath, fdQuery, body, nil)); w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	if got := e.h.Stats().Snapshot()["checkin_invalid"]; got != 1 {
		t.Fatalf("checkin_invalid = %d", got)
	}
	raw, _ := os.ReadFile("testdata/checkins/python.envelope")
	e.do(post(fdPath, fdQuery, string(raw), nil)) // no queue configured
	if got := e.h.Stats().Snapshot()["checkin_discarded"]; got != 1 {
		t.Fatalf("checkin_discarded = %d", got)
	}
}
