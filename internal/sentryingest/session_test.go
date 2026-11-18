package sentryingest

import (
	"errors"
	"strings"
	"testing"
	"time"

	"supermetry/internal/sites"
)

func sessionFixtures(t *testing.T) map[string]fixtureItem {
	out := map[string]fixtureItem{}
	for _, f := range loadFixtures(t) {
		for _, it := range f.Items {
			if it.Type == "session" || it.Type == "sessions" || it.Type == "client_report" {
				out[f.Name] = it
			}
		}
	}
	return out
}

func TestParseSessionAggregates(t *testing.T) {
	rows, _, err := ParseSession([]byte(`{"aggregates":[{"started":"2026-11-17T13:10:00.000Z","exited":3,"errored":1}],"attrs":{"release":"r@1","environment":"prod"}}`), EventContext{}, testSite(), testNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows %+v", rows)
	}
	if rows[0].Occurrences != 3 || rows[0].ExitReason != "" || rows[1].Occurrences != 1 || rows[1].ExitReason != "errored" {
		t.Fatalf("%+v", rows)
	}
	for _, r := range rows {
		if r.Kind != "session" || r.Release != "r@1" || r.Environment != "prod" || len(r.ID) != 32 || r.SessionID != "" || r.TS.Hour() != 13 {
			t.Fatalf("%+v", r)
		}
	}
	if rows[0].ID == rows[1].ID {
		t.Fatal("rows of one aggregate need distinct ids")
	}
}

func TestParseSessionAggregateCrashedSetsExitReason(t *testing.T) {
	rows, _, err := ParseSession([]byte(`{"aggregates":[{"started":"2026-11-17T13:10:00Z","exited":3,"errored":1,"crashed":2,"abnormal":1}]}`), EventContext{}, testSite(), testNow)
	if err != nil || len(rows) != 4 {
		t.Fatalf("%v %+v", err, rows)
	}
	got := map[string]int{}
	for _, r := range rows {
		got[r.ExitReason] += r.Occurrences
	}
	if got["crashed"] != 2 || got["abnormal"] != 1 || got["errored"] != 1 || got[""] != 3 {
		t.Fatalf("%v", got)
	}
}

func TestParseSessionAggregatesZeroAndShapes(t *testing.T) {
	rows, _, err := ParseSession([]byte(`{"aggregates":[{"started":"2026-11-17T13:10:00Z","exited":0},null,{"started":"2026-11-17T13:11:00Z","exited":2}]}`), EventContext{}, testSite(), testNow)
	if err != nil || len(rows) != 1 || rows[0].Occurrences != 2 {
		t.Fatalf("%v %+v", err, rows)
	}
	rows, _, err = ParseSession([]byte(`{"aggregates":[]}`), EventContext{}, testSite(), testNow)
	if err != nil || len(rows) != 0 {
		t.Fatalf("%v %+v", err, rows)
	}
	a, _, _ := ParseSession([]byte(`{"aggregates":[{"started":"2026-11-17T13:10:00Z","exited":2}]}`), EventContext{}, testSite(), testNow)
	b, _, _ := ParseSession([]byte(`{"aggregates":[{"started":"2026-11-17T13:10:00Z","exited":2}]}`), EventContext{}, testSite(), testNow)
	c, _, _ := ParseSession([]byte(`{"aggregates":[{"started":"2026-11-17T13:11:00Z","exited":2}]}`), EventContext{}, testSite(), testNow)
	if a[0].ID != b[0].ID || a[0].ID == c[0].ID {
		t.Fatal("ids must be stable for a retry and differ for different payloads")
	}
}

func TestParseSessionInit(t *testing.T) {
	rows, _, err := ParseSession([]byte(`{"sid":"f4f879bbd40149b586a1c19480b043e2","did":"user-1","init":true,"started":"2026-11-17T13:11:41.685Z","timestamp":"2026-11-17T13:11:41.685Z","status":"ok","errors":0,"attrs":{"release":"fixture@1.0.0","environment":"fixture","ip_address":"{{auto}}","user_agent":"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 Chrome/150.0 Safari/537.36"}}`),
		EventContext{SDKName: "sentry.javascript.browser"}, testSite(), testNow)
	if err != nil || len(rows) != 1 {
		t.Fatalf("%v %+v", err, rows)
	}
	r := rows[0]
	if r.SessionID != "f4f879bbd40149b586a1c19480b043e2" || r.InstallID != "user-1" || r.Platform != "web" || r.ExitReason != "" || r.Occurrences != 1 {
		t.Fatalf("%+v", r)
	}
	// the same session crashing at init
	rows, _, _ = ParseSession([]byte(`{"sid":"s1","init":true,"started":"2026-11-17T13:11:41Z","status":"crashed"}`), EventContext{}, testSite(), testNow)
	if len(rows) != 1 || rows[0].ExitReason != "crashed" {
		t.Fatalf("%+v", rows)
	}
}

func TestParseSessionUpdateNotInit(t *testing.T) {
	rows, ups, err := ParseSession([]byte(`{"sid":"s1","did":"u1","init":false,"started":"2026-11-17T13:11:41Z","timestamp":"2026-11-17T13:20:00Z","status":"crashed","errors":2,"duration":12.5,"attrs":{"release":"r@1","environment":"prod"}}`), EventContext{}, testSite(), testNow)
	if err != nil || len(rows) != 0 || len(ups) != 1 {
		t.Fatalf("%v %+v %+v", err, rows, ups)
	}
	u := ups[0]
	if u.SiteID != 42 || u.Status != "crashed" || u.Errors != 2 || u.Init || u.Release != "r@1" || u.Environment != "prod" ||
		u.DurationMS == nil || *u.DurationMS != 12500 || u.Started.Hour() != 13 || u.Started.Minute() != 11 {
		t.Fatalf("%+v", u)
	}
	if _, _, err := ParseSession([]byte(`{"init":true}`), EventContext{}, testSite(), testNow); !errors.Is(err, ErrInvalid) {
		t.Fatalf("init without sid: %v", err)
	}
	if _, _, err := ParseSession([]byte(`nope`), EventContext{}, testSite(), testNow); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}

func TestParseSessionInitEmitsEventAndUpdate(t *testing.T) {
	rows, ups, err := ParseSession([]byte(`{"sid":"s1","init":true,"started":"2026-11-17T13:11:41Z","status":"ok"}`), EventContext{}, testSite(), testNow)
	if err != nil || len(rows) != 1 || len(ups) != 1 || !ups[0].Init || ups[0].Status != "ok" || ups[0].InstallHash != nil {
		t.Fatalf("%v %+v %+v", err, rows, ups)
	}
}

func TestParseSessionStatusNormalised(t *testing.T) {
	for in, want := range map[string]string{"ok": "ok", "exited": "exited", "errored": "errored", "abnormal": "abnormal", "crashed": "crashed", "unhandled": "crashed", "weird": "ok", "": "ok"} {
		_, ups, err := ParseSession([]byte(`{"sid":"s1","started":"2026-11-17T13:11:41Z","status":"`+in+`"}`), EventContext{}, testSite(), testNow)
		if err != nil || len(ups) != 1 || ups[0].Status != want {
			t.Errorf("%q: %v %+v", in, err, ups)
		}
	}
}

func TestParseSessionHashesSid(t *testing.T) {
	site := testSite()
	_, a, _ := ParseSession([]byte(`{"sid":"sid-123","did":"did-9","started":"2026-11-17T13:11:41Z","status":"ok"}`), EventContext{}, site, testNow)
	_, b, _ := ParseSession([]byte(`{"sid":"sid-123","did":"did-9","started":"2026-11-17T13:11:41Z","status":"exited"}`), EventContext{}, site, testNow)
	_, c, _ := ParseSession([]byte(`{"sid":"sid-124","started":"2026-11-17T13:11:41Z","status":"ok"}`), EventContext{}, site, testNow)
	if len(a) != 1 || len(b) != 1 || len(c) != 1 {
		t.Fatal("updates missing")
	}
	if len(a[0].SessionHash) != 32 || string(a[0].SessionHash) == "sid-123" || strings.Contains(string(a[0].SessionHash), "sid-123") {
		t.Fatalf("session hash %x", a[0].SessionHash)
	}
	if string(a[0].SessionHash) != string(b[0].SessionHash) || string(a[0].SessionHash) == string(c[0].SessionHash) {
		t.Fatal("same sid must hash the same, different sid differently")
	}
	if string(a[0].InstallHash) != string(HashUser(site.App.HashSecret, "did-9")) || len(a[0].InstallHash) != 32 {
		t.Fatalf("install hash %x", a[0].InstallHash)
	}
	if c[0].InstallHash != nil {
		t.Fatal("no did, no install hash")
	}
	// The session hash is the MAC the event writer applies to session ids.
	if string(a[0].SessionHash) != string(HashUser(site.App.HashSecret, "sid-123")) {
		t.Fatal("session hash differs from the writer's MAC")
	}
}

func TestParseSessionNoSecretNoUpdate(t *testing.T) {
	site := sites.Site{ID: 42, Kind: sites.KindApp, App: &sites.AppConfig{MaxReportAgeDays: 30}}
	rows, ups, err := ParseSession([]byte(`{"sid":"s1","init":true,"started":"2026-11-17T13:11:41Z"}`), EventContext{}, site, testNow)
	if err != nil || len(rows) != 1 || len(ups) != 0 {
		t.Fatalf("%v %+v %+v", err, rows, ups)
	}
}

func TestParseSessionsAggregateNoUpdates(t *testing.T) {
	rows, ups, err := ParseSession([]byte(`{"aggregates":[{"started":"2026-11-17T13:10:00Z","exited":3,"crashed":1}]}`), EventContext{}, testSite(), testNow)
	if err != nil || len(rows) != 2 || len(ups) != 0 {
		t.Fatalf("%v %+v %+v", err, rows, ups)
	}
}

func TestParseSessionFutureStartClamped(t *testing.T) {
	rows, _, _ := ParseSession([]byte(`{"sid":"s1","init":true,"started":"2026-11-18T13:11:41Z"}`), EventContext{}, testSite(), testNow)
	if !rows[0].TS.Equal(testNow) {
		t.Fatalf("%s", rows[0].TS)
	}
}

func TestParseSessionFixtures(t *testing.T) {
	fx := sessionFixtures(t)
	want := map[string][2]int{ // rows, total occurrences
		"node/session-aggregates": {1, 3},
		"python/wsgi-sessions":    {1, 3},
		"browser/session":         {1, 1},
		"android/session-update":  {0, 0},
		"cocoa/session-update":    {0, 0},
	}
	for name, w := range want {
		it, ok := fx[name]
		if !ok {
			t.Fatalf("missing fixture %s", name)
		}
		rows, _, err := ParseSession(it.Payload, EventContext{SentAt: time.Time{}}, testSite(), testNow)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		total := 0
		for _, r := range rows {
			total += r.Occurrences
			if strings.Contains(r.InstallID+r.Release, "{{") {
				t.Errorf("%s: placeholder leaked", name)
			}
		}
		if len(rows) != w[0] || total != w[1] {
			t.Errorf("%s: %d rows / %d occurrences, want %v", name, len(rows), total, w)
		}
	}
}

func TestParseClientReport(t *testing.T) {
	got, err := ParseClientReport([]byte(`{"timestamp":1794921108.676,"discarded_events":[{"reason":"before_send","category":"error","quantity":2},{"reason":"before_send","category":"error","quantity":1},{"reason":"queue_overflow","category":"transaction","quantity":5},{"reason":"x","category":"error","quantity":0},{"reason":"","category":"error","quantity":3},null]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != (ClientReportCount{"before_send", "error", 3}) || got[1] != (ClientReportCount{"queue_overflow", "transaction", 5}) {
		t.Fatalf("%+v", got)
	}
	got, err = ParseClientReport([]byte(`{"discarded_events":[{"reason":"a b\n","category":"error","quantity":1}]}`))
	if err != nil || got[0].Reason != "a_b" {
		t.Fatalf("%v %+v", err, got)
	}
	got, err = ParseClientReport([]byte(`{}`))
	if err != nil || len(got) != 0 {
		t.Fatalf("%v %+v", err, got)
	}
	if _, err := ParseClientReport([]byte(`[`)); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}

func TestParseClientReportFixtures(t *testing.T) {
	n := 0
	for name, it := range sessionFixtures(t) {
		if !strings.HasSuffix(name, "client-report") {
			continue
		}
		n++
		got, err := ParseClientReport(it.Payload)
		if err != nil || len(got) != 1 || got[0] != (ClientReportCount{"before_send", "error", 1}) {
			t.Errorf("%s: %v %+v", name, err, got)
		}
	}
	if n != 3 {
		t.Fatalf("%d client report fixtures", n)
	}
}

func FuzzParseSession(f *testing.F) {
	for _, it := range []string{`{"aggregates":[{"started":"x","exited":-1,"crashed":9999999999}]}`, `{"sid":1,"init":true}`, `{"aggregates":{"a":1}}`} {
		f.Add([]byte(it))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		rows, _, err := ParseSession(raw, EventContext{}, testSite(), testNow)
		if err != nil {
			return
		}
		for _, r := range rows {
			if r.Occurrences < 1 || r.Occurrences > 1_000_000 || r.ID == "" {
				t.Fatalf("%+v", r)
			}
		}
		_, _ = ParseClientReport(raw)
	})
}

func TestParseSessionSkewedClockSameKey(t *testing.T) {
	// A device whose clock is hours ahead: started is far in the future, and
	// each envelope's sent_at skew differs by a few seconds.
	payload := []byte(`{"sid":"s1","started":"2026-11-17T20:10:05Z","status":"ok"}`)
	site := testSite()
	var got [2]time.Time
	for i, skew := range []time.Duration{2 * time.Hour, 2*time.Hour + 3*time.Second} {
		_, ups, err := ParseSession(payload, EventContext{SentAt: testNow.Add(-skew)}, site, testNow)
		if err != nil || len(ups) != 1 {
			t.Fatalf("%v %+v", err, ups)
		}
		got[i] = ups[0].Started
	}
	if !got[0].Equal(got[1]) {
		t.Fatalf("started differs: %s vs %s", got[0], got[1])
	}
	if got[0].Second() != 0 {
		t.Fatalf("corrected start not truncated to the minute: %s", got[0])
	}
}

func TestParseSessionPlausibleStartedUsedRaw(t *testing.T) {
	_, ups, _ := ParseSession([]byte(`{"sid":"s1","started":"2026-11-17T13:11:41.685Z","status":"ok"}`), EventContext{SentAt: testNow.Add(-time.Hour)}, testSite(), testNow)
	if len(ups) != 1 || !ups[0].Started.Equal(time.Date(2026, 11, 17, 13, 11, 41, 685000000, time.UTC)) {
		t.Fatalf("%+v", ups)
	}
}

func TestParseSessionMissingStartedNoUpdate(t *testing.T) {
	stats := &Stats{}
	for _, p := range []string{
		`{"sid":"s1","init":false,"timestamp":"2026-11-17T13:20:00Z","status":"exited"}`,
		`{"sid":"s1","init":false,"started":"garbage","status":"exited"}`,
	} {
		rows, ups, err := ParseSession([]byte(p), EventContext{Stats: stats}, testSite(), testNow)
		if err != nil || len(ups) != 0 || len(rows) != 0 {
			t.Fatalf("%s: %v %+v %+v", p, err, rows, ups)
		}
	}
	if stats.Snapshot()["session_no_started"] != 2 {
		t.Fatalf("%v", stats.Snapshot())
	}
	// The v1 init row is unchanged.
	rows, ups, _ := ParseSession([]byte(`{"sid":"s1","init":true,"timestamp":"2026-11-17T13:20:00Z","status":"ok"}`), EventContext{Stats: stats}, testSite(), testNow)
	if len(rows) != 1 || len(ups) != 0 {
		t.Fatalf("%+v %+v", rows, ups)
	}
}
