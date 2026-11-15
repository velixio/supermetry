package ingest_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"supermetry/internal/api"
	"supermetry/internal/db"
	"supermetry/internal/health"
	"supermetry/internal/privacy"
	"supermetry/internal/query"
)

// Compatibility probe for an external desktop client that posts
// "diagnostic_report" and "identify" events (see the task brief). It only
// observes behavior; it asserts what the current handler is expected to do.

const (
	diagUA     = "VoiceStudio/1.2.3 (linux; x64)"
	diagHost   = "supermetry.com" // fixture site allowed host (abc123)
	diagOrigin = "https://" + diagHost
	diagProf   = "install_0123456789abcdef0123456789abcdef"
)

func diagFrames() string {
	lines := make([]string, 0, 40)
	for i := 0; len(strings.Join(lines, "\n")) < 1700; i++ {
		lines = append(lines, fmt.Sprintf("backend/services/model_manager.py:%d load_%d", 100+i, i))
	}
	return strings.Join(lines, "\n")
}

func diagReport(id string, ts time.Time, frames string) map[string]any {
	return map[string]any{
		"v": 1, "id": id, "ts": ts.UnixMilli(), "site": "abc123", "name": "diagnostic_report",
		"url": "https://" + diagHost + "/diagnostics", "profile_id": diagProf,
		"props": map[string]any{
			"category": "backend_crash", "source": "backend", "stage": "model_load",
			"error_code": "GPU_OOM", "error_type": "RuntimeError", "engine_id": "faster-whisper",
			"app_version": "1.2.3", "build_commit": "abcdef1", "platform": "linux", "arch": "x64",
			"device_family": "cuda", "exit_code": "137", "signal": "SIGKILL", "reason": "oom",
			"frames":      frames,
			"fingerprint": strings.Repeat("ab", 32), "uptime_bucket": "lt_1h", "elapsed_bucket": "lt_1m",
			"memory_bucket": "lt_16gb", "disk_bucket": "lt_32gb", "occurrences": 3, "schema_version": 1,
		},
	}
}

func diagIdentify(id string, ts time.Time) map[string]any {
	return map[string]any{
		"v": 1, "id": id, "ts": ts.UnixMilli(), "site": "abc123", "name": "identify",
		"url": "https://" + diagHost + "/diagnostics", "profile_id": diagProf,
		"traits": map[string]any{"platform": "linux", "app_version": "1.2.3", "profile_kind": "install"},
	}
}

func diagPost(t *testing.T, h http.Handler, body any, origin, ua string) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/public/v1/events", strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	req.Header.Set("User-Agent", ua)
	req.RemoteAddr = "198.51.100.7:5555"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func diagCounterDelta(before map[string]float64) map[string]float64 {
	out := map[string]float64{}
	for k, v := range health.Snapshot() {
		if d := v - before[k]; d != 0 {
			out[k] = d
		}
	}
	return out
}

func diagFixture(t *testing.T, identified, profiles bool) *handlerFixture {
	t.Helper()
	f := newHandlerFixture(t, 64, nil)
	seedProfileSite(t, f.pool)
	if identified {
		f.h.TrackingMode = privacy.Identified
	}
	setProfilesEnabled(t, f, profiles)
	return f
}

func diagProps(t *testing.T, f *handlerFixture) (n int, props map[string]string) {
	t.Helper()
	f.stop(t)
	if err := f.pool.QueryRow(context.Background(), `select count(*) from events where kind <> 0`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		return 0, nil
	}
	if err := f.pool.QueryRow(context.Background(), `select props from events where kind <> 0 order by ts limit 1`).Scan(&props); err != nil {
		t.Fatal(err)
	}
	return n, props
}

// Case 1: fresh ts, identified + profiles enabled.
func TestDiagnosticsCompatFreshReportStoresAllProps(t *testing.T) {
	f := diagFixture(t, true, true)
	frames := diagFrames()
	before := health.Snapshot()
	if rr := diagPost(t, f.h, diagIdentify("a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1", fakeNow()), diagOrigin, diagUA); rr.Code != http.StatusAccepted {
		t.Fatalf("identify status=%d", rr.Code)
	}
	rr := diagPost(t, f.h, diagReport("b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2", fakeNow(), frames), diagOrigin, diagUA)
	t.Logf("fresh: status=%d body=%q len(frames)=%d counters=%v", rr.Code, rr.Body.String(), len(frames), diagCounterDelta(before))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status=%d", rr.Code)
	}
	n, props := diagProps(t, f)
	t.Logf("fresh: custom-event rows=%d props=%d", n, len(props))
	if n != 2 { // identify (stored as custom event) + diagnostic_report
		t.Logf("NOTE: expected 2 custom rows (identify + report), got %d", n)
	}
	var traits string
	var linked int
	_ = f.pool.QueryRow(context.Background(), `select coalesce((select traits::text from profiles limit 1),''), (select count(profile_id) from events)`).Scan(&traits, &linked)
	t.Logf("fresh: profile traits=%s events linked to profile=%d", traits, linked)
	var reportProps map[string]string
	if err := f.pool.QueryRow(context.Background(), `select props from events where props ? 'error_code'`).Scan(&reportProps); err != nil {
		t.Fatalf("no stored diagnostic_report row with props: %v", err)
	}
	if len(reportProps) != 22 {
		t.Errorf("stored %d props, want 22: %v", len(reportProps), reportProps)
	}
	if reportProps["frames"] != frames {
		t.Errorf("frames not intact: got %d bytes want %d", len(reportProps["frames"]), len(frames))
	}
	if reportProps["occurrences"] != "3" || reportProps["schema_version"] != "1" {
		t.Errorf("numeric props: occurrences=%q schema_version=%q", reportProps["occurrences"], reportProps["schema_version"])
	}
}

// Case 2: ts 3 days old and 23h old.
func TestDiagnosticsCompatOldTimestamps(t *testing.T) {
	for _, age := range []time.Duration{-23 * time.Hour, -3 * 24 * time.Hour, -7 * 24 * time.Hour} {
		t.Run(age.String(), func(t *testing.T) {
			f := diagFixture(t, true, true)
			before := health.Snapshot()
			rr := diagPost(t, f.h, diagReport("c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3", fakeNow().Add(age), diagFrames()), diagOrigin, diagUA)
			n, _ := diagProps(t, f)
			t.Logf("age=%s status=%d stored_rows=%d counters=%v", age, rr.Code, n, diagCounterDelta(before))
			if rr.Code != http.StatusAccepted {
				t.Fatalf("status=%d", rr.Code)
			}
		})
	}
}

// Case 3: same id resent.
func TestDiagnosticsCompatDuplicateID(t *testing.T) {
	f := diagFixture(t, true, true)
	before := health.Snapshot()
	body := diagReport("d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4", fakeNow(), diagFrames())
	first := diagPost(t, f.h, body, diagOrigin, diagUA)
	second := diagPost(t, f.h, body, diagOrigin, diagUA)
	n, _ := diagProps(t, f)
	t.Logf("dup: first=%d second=%d stored_rows=%d counters=%v", first.Code, second.Code, n, diagCounterDelta(before))
	if n != 1 {
		t.Fatalf("stored rows=%d, want 1 (deduped)", n)
	}
}

// Case 4: origin, UA, strict origin, site host.
func TestDiagnosticsCompatOriginUAAndHost(t *testing.T) {
	cases := []struct {
		name, origin, ua string
		strict           bool
		urlHost          string
	}{
		{name: "voicestudio UA, matching origin", origin: diagOrigin, ua: diagUA},
		{name: "mismatched origin, strict off", origin: "https://other.example", ua: diagUA},
		{name: "no origin header, strict off", origin: "", ua: diagUA},
		{name: "mismatched origin, strict ON", origin: "https://other.example", ua: diagUA, strict: true},
		{name: "no origin header, strict ON", origin: "", ua: diagUA, strict: true},
		{name: "matching origin, strict ON", origin: diagOrigin, ua: diagUA, strict: true},
		{name: "url host not in allowed_hosts", origin: diagOrigin, ua: diagUA, urlHost: "voicestudio.example"},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := diagFixture(t, true, true)
			if tc.strict {
				setProtectionSettings(t, f, true, nil, nil)
			}
			body := diagReport(fmt.Sprintf("e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5%02d", i), fakeNow(), diagFrames())
			if tc.urlHost != "" {
				body["url"] = "https://" + tc.urlHost + "/diagnostics"
			}
			before := health.Snapshot()
			rr := diagPost(t, f.h, body, tc.origin, tc.ua)
			n, _ := diagProps(t, f)
			t.Logf("%s: status=%d stored_rows=%d counters=%v", tc.name, rr.Code, n, diagCounterDelta(before))
		})
	}
}

func TestDiagnosticsCompatBotFilterUserAgents(t *testing.T) {
	for _, ua := range []string{
		diagUA,
		"VoiceStudio/1.2.3 (win32; x64)",
		"VoiceStudio/1.2.3 (darwin; arm64)",
		"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) VoiceStudio/1.2.3 Chrome/126.0.0.0 Electron/31.0.0 Safari/537.36",
		"Go-http-client/2.0",
		"node",
		"undici",
		"python-requests/2.31",
		"curl/8.5.0",
		"",
	} {
		t.Run(ua, func(t *testing.T) {
			f := diagFixture(t, true, true)
			before := health.Snapshot()
			rr := diagPost(t, f.h, diagReport("f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6", fakeNow(), "x"), diagOrigin, ua)
			n, _ := diagProps(t, f)
			t.Logf("ua=%q status=%d stored_rows=%d counters=%v", ua, rr.Code, n, diagCounterDelta(before))
		})
	}
}

// Case 5: anonymous mode, and identified instance with profiles disabled on the site.
func TestDiagnosticsCompatAnonymousAndProfilesOff(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		identified, profiles bool
	}{
		{"anonymous instance, profiles on", false, true},
		{"identified instance, site profiles off", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := diagFixture(t, tc.identified, tc.profiles)
			before := health.Snapshot()
			idRes := diagPost(t, f.h, diagIdentify("a7a7a7a7a7a7a7a7a7a7a7a7a7a7a7a7", fakeNow()), diagOrigin, diagUA)
			rr := diagPost(t, f.h, diagReport("b8b8b8b8b8b8b8b8b8b8b8b8b8b8b8b8", fakeNow(), diagFrames()), diagOrigin, diagUA)
			n, props := diagProps(t, f)
			var profiles int
			_ = f.pool.QueryRow(context.Background(), `select count(*) from profiles`).Scan(&profiles)
			t.Logf("%s: identify=%d report=%d custom_rows=%d props_stored=%d (%v) profiles=%d counters=%v",
				tc.name, idRes.Code, rr.Code, n, len(props), props != nil, profiles, diagCounterDelta(before))
		})
	}
}

// Case 6: queryability through the query engine behind the explore API
// (GET /sites/{site}/events, /properties, /properties/{key}/values).
func TestDiagnosticsCompatQueryableByErrorCode(t *testing.T) {
	f := diagFixture(t, true, true)
	// The fixture clock is fixed in the past; with 30-day retention the query
	// engine (real clock) would reject the range, so store as retention=forever.
	site, _ := f.h.Sites.ByPublicID("abc123")
	site.Retention = db.RetForever
	f.h.Sites = siteRegistry{site: site}
	f.writer.SetTrackingPolicy(f.h.TrackingMode, f.h.Sites)
	for i, code := range []string{"GPU_OOM", "GPU_OOM", "DISK_FULL"} {
		body := diagReport(fmt.Sprintf("c9c9c9c9c9c9c9c9c9c9c9c9c9c9c9%02d", i), fakeNow(), diagFrames())
		body["props"].(map[string]any)["error_code"] = code
		if rr := diagPost(t, f.h, body, diagOrigin, diagUA); rr.Code != http.StatusAccepted {
			t.Fatalf("status=%d", rr.Code)
		}
	}
	f.stop(t)

	engine := query.NewEngine(f.pool, f.h.Dict, f.h.Sites)
	engine.SetTrackingMode(privacy.Identified)
	day := time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)
	ctx := context.Background()

	keys, err := engine.Properties(ctx, api.PropertyExploreRequest{SiteID: 1, From: day, To: day, Event: "diagnostic_report"})
	if err != nil {
		t.Fatalf("Properties: %v", err)
	}
	t.Logf("property keys for diagnostic_report: %d (%v)", len(keys), keys)
	if len(keys) != 22 {
		t.Errorf("property keys=%d, want 22", len(keys))
	}
	values, err := engine.PropertyValues(ctx, api.PropertyValueExploreRequest{SiteID: 1, Key: "error_code", From: day, To: day, Event: "diagnostic_report"})
	if err != nil {
		t.Fatalf("PropertyValues: %v", err)
	}
	t.Logf("error_code values: %v", values)
	list, err := engine.Events(ctx, api.EventExploreRequest{SiteID: 1, From: day, To: day, Name: "diagnostic_report", Limit: 50,
		Filters: []api.Filter{{Dimension: "prop:error_code", Operator: api.Is, Values: []string{"GPU_OOM"}}}})
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	t.Logf("events filtered prop:error_code=GPU_OOM: total=%d rows=%d", list.Total, len(list.Data))
	if list.Total != 2 {
		t.Errorf("filtered total=%d, want 2", list.Total)
	}
	if len(list.Data) > 0 && list.Data[0].Props != nil {
		t.Logf("first row props=%d frames=%dB", len(*list.Data[0].Props), len((*list.Data[0].Props)["frames"]))
	}
}
