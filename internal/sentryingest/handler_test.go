package sentryingest

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"supermetry/internal/appingest"
	"supermetry/internal/sites"
)

// stubNormaliser is the no-op normaliser the handler tests use: it keeps only
// the event id and a fixed shape, so the tests cover the front door, not the
// normaliser.
type stubNormaliser struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (s *stubNormaliser) Normalise(raw []byte, ctx EventContext, site sites.Site, now time.Time) (appingest.Event, appingest.Body, error) {
	hdr := ctx
	s.mu.Lock()
	s.calls++
	n := s.calls
	s.mu.Unlock()
	if s.err != nil {
		return appingest.Event{}, appingest.Body{}, s.err
	}
	var probe struct {
		EventID string `json:"event_id"`
	}
	_ = json.Unmarshal(raw, &probe)
	id := hdr.EventID
	if id == "" {
		id = probe.EventID
	}
	if id == "" {
		id = fmt.Sprintf("generated%024d", n)
	}
	return appingest.Event{ID: id, TS: now, Kind: "error", Level: "error", Occurrences: 1,
		Fingerprint: "ab", Tags: map[string]string{}}, appingest.Body{}, nil
}

type bodySink struct {
	mu    sync.Mutex
	items []appingest.Body
	full  bool
}

func (b *bodySink) Enqueue(v appingest.Body) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.full {
		return false
	}
	b.items = append(b.items, v)
	return true
}

type fixedGeo string

func (g fixedGeo) Country(netip.Addr) string { return string(g) }

type env struct {
	h      *Handler
	intake *appingest.Intake
	norm   *stubNormaliser
	bodies *bodySink
	reg    fakeRegistry
	now    *time.Time
}

func newEnv(t testing.TB, keys ...sites.DSNKey) *env { return newEnvWith(t, false, keys...) }

// newStubEnv uses the stub normaliser, so only the front door is under test.
func newStubEnv(t testing.TB, keys ...sites.DSNKey) *env { return newEnvWith(t, true, keys...) }

func newEnvWith(t testing.TB, stub bool, keys ...sites.DSNKey) *env {
	t.Helper()
	if len(keys) == 0 {
		keys = []sites.DSNKey{{PublicKey: fdKey}}
	}
	reg := newRegistry(appSite(fdSite, keys...), appSite(43, sites.DSNKey{PublicKey: fdOtherKey}))
	now := fdNow
	e := &env{intake: appingest.NewIntake(appingest.Config{}), norm: &stubNormaliser{}, bodies: &bodySink{}, reg: reg, now: &now}
	e.h = NewHandler(reg, e.intake, e.bodies, fixedGeo("DE"), nil, func() time.Time { return *e.now })
	e.h.WithIPLimit(-1) // tests send hundreds of requests from one address
	if stub {
		e.h.WithNormaliser(e.norm.Normalise)
	}
	return e
}

func (e *env) do(r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	return w
}

func (e *env) queued() int {
	n := 0
	for {
		select {
		case <-e.intake.Queue():
			n++
		default:
			return n
		}
	}
}

func post(path, query, body string, headers map[string]string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, path+query, strings.NewReader(body))
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return r
}

const simpleEnvelope = `{"event_id":"11111111111111111111111111111111"}` + "\n" + `{"type":"event"}` + "\n" + `{"message":"hi"}` + "\n"

type sidecar struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Query   string            `json:"query"`
	Headers map[string]string `json:"headers"`
}

// wantEvents is the number of event items each fixture carries, written down
// by hand so the golden test does not just echo the parser.
var wantEvents = map[string]int{
	"android/anr": 1, "android/jvm-crash": 1, "android/ndk-crash": 1, "android/session-update": 0,
	"browser/breadcrumbs": 1, "browser/client-report": 0, "browser/onerror": 1, "browser/session": 0, "browser/unhandledrejection": 1,
	"cocoa/app-hang": 1, "cocoa/crash": 1, "cocoa/session-update": 0, "cocoa/watchdog-termination": 1,
	"electron/main-exception": 1, "electron/native-crash": 1, "electron/renderer-exception": 1,
	"go/captured-exception": 1, "go/recovered-panic": 1,
	"node/captured-message": 1, "node/client-report": 0, "node/session-aggregates": 0, "node/unhandled-exception": 1, "node/unhandled-rejection": 1,
	"transactions/python": 0, "transactions/javascript-static": 0, "transactions/javascript-v11-stream": 0, "transactions/android-app-start": 0, "transactions/cocoa-app-start": 0,
	"checkins/js": 0, "checkins/python": 0, "python/client-report": 0, "python/logging-error": 1, "python/unhandled-exception": 1, "python/unhandled-exception-br": 1, "python/wsgi-sessions": 0,
}

// wantSessionRows are the session rows each fixture adds (one per init, or one
// per status of an aggregate); updates of a running session add nothing.
var wantSessionRows = map[string]int{"browser/session": 1, "node/session-aggregates": 1, "python/wsgi-sessions": 1}

// filteredFixtures were recorded against a local server (127.0.0.1); with the
// opt-in localhost filter on, their events are dropped.
var filteredFixtures = map[string]bool{"browser/breadcrumbs": true, "browser/onerror": true, "browser/unhandledrejection": true}

func TestFixturesAccepted(t *testing.T) { testFixturesAccepted(t, false) }

func TestFixturesWithLocalhostFilterOn(t *testing.T) { testFixturesAccepted(t, true) }

func testFixturesAccepted(t *testing.T, localhostFilter bool) {
	files := fixtureFiles(t)
	seen := map[string]bool{}
	for _, file := range files {
		name := strings.TrimSuffix(strings.TrimPrefix(file, "testdata/"), ".envelope")
		seen[name] = true
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			side, err := os.ReadFile(strings.TrimSuffix(file, ".envelope") + ".json")
			if err != nil {
				t.Fatal(err)
			}
			var sc sidecar
			if err := json.Unmarshal(side, &sc); err != nil {
				t.Fatal(err)
			}
			e := newEnv(t)
			e.h.WithLocalhostFilter(localhostFilter)
			*e.now = time.Date(2026, 11, 17, 14, 0, 0, 0, time.UTC)
			req := httptest.NewRequest(sc.Method, sc.Path+"?"+sc.Query, bytes.NewReader(raw))
			for k, v := range sc.Headers {
				req.Header.Set(k, v)
			}
			if sc.Query == "" && req.Header.Get("X-Sentry-Auth") == "" {
				t.Fatal("fixture carries no credentials")
			}
			if !strings.Contains(sc.Query, "sentry_key") {
				// Fixtures that carry the key in a header only.
				if got := req.Header.Get("X-Sentry-Auth"); !strings.Contains(got, fdKey) {
					t.Fatalf("fixture key is not %s: %q", fdKey, got)
				}
			}
			w := e.do(req)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", w.Code, w.Body)
			}
			if got := w.Header().Get("X-Sentry-Rate-Limits"); got != rejectUnsupported {
				t.Errorf("rate limits header = %q", got)
			}
			want := wantEvents[name] + wantSessionRows[name]
			if localhostFilter && filteredFixtures[name] {
				want = 0
				if got := e.h.Stats().Snapshot()["filtered"]; got != 1 {
					t.Errorf("filtered = %d, want 1", got)
				}
			}
			if got := e.queued(); got != want {
				t.Errorf("stored events = %d, want %d", got, want)
			}
			var reply map[string]string
			if err := json.Unmarshal(w.Body.Bytes(), &reply); err != nil {
				t.Fatalf("reply %q: %v", w.Body, err)
			}
			if wantEvents[name] == 0 {
				if len(reply) != 0 {
					t.Errorf("reply = %v, want {}", reply)
				}
			} else if reply["id"] == "" {
				t.Errorf("reply has no id: %v", reply)
			}
		})
	}
	var missing []string
	for name := range wantEvents {
		if !seen[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("expectations for fixtures that are gone: %v", missing)
	}
	if len(seen) != len(wantEvents) {
		t.Errorf("fixtures without an expected count: have %d, expected %d", len(seen), len(wantEvents))
	}
}

func TestFixtureEventIDIsEchoed(t *testing.T) {
	raw, err := os.ReadFile("testdata/browser/onerror.envelope")
	if err != nil {
		t.Fatal(err)
	}
	e := newEnv(t)
	w := e.do(post(fdPath, fdQuery, string(raw), nil))
	if w.Code != 200 || w.Body.String() != `{"id":"a4a03791b9034ad1a061e0e18ae44d71"}` {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestMethodsRoutesAndErrors(t *testing.T) {
	e := newEnv(t)
	for _, path := range []string{"/api/42/envelope/", "/api/42/envelope"} {
		if w := e.do(post(path, fdQuery, simpleEnvelope, nil)); w.Code != 200 {
			t.Errorf("%s = %d", path, w.Code)
		}
	}
	if w := e.do(httptest.NewRequest(http.MethodGet, fdPath+fdQuery, nil)); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET = %d", w.Code)
	}
	for _, p := range []string{"/api/42/envelope/x", "/api/private/envelope/", "/api/042/envelope/", "/api/0/envelope/", "/api/42/other/", "/api/42"} {
		if _, ok := parseRoute(p); ok {
			t.Errorf("%s matched", p)
		}
	}
	if w := e.do(post(fdPath, fdQuery, "not an envelope", nil)); w.Code != http.StatusBadRequest || w.Header().Get("X-Sentry-Error") == "" {
		t.Errorf("malformed = %d %v", w.Code, w.Header())
	}
	if w := e.do(post(fdPath, fdQuery, simpleEnvelope, map[string]string{"Content-Encoding": "gzip"})); w.Code != http.StatusBadRequest {
		t.Errorf("bad gzip = %d", w.Code)
	}
	if w := e.do(post(fdPath, "?sentry_version=8&sentry_key="+fdKey, simpleEnvelope, nil)); w.Code != http.StatusBadRequest {
		t.Errorf("version 8 = %d", w.Code)
	}
}

func TestAuthFailuresAre401(t *testing.T) {
	e := newEnv(t)
	for name, r := range map[string]*http.Request{
		"no key":        post(fdPath, "", simpleEnvelope, nil),
		"unknown key":   post(fdPath, "?sentry_key=22222222222222222222222222222222", simpleEnvelope, nil),
		"two keys":      post(fdPath, fdQuery, simpleEnvelope, map[string]string{"X-Sentry-Auth": "Sentry sentry_key=" + fdOtherKey}),
		"other project": post("/api/43/envelope/", fdQuery, simpleEnvelope, nil),
		"dsn only, bad": post(fdPath, "", `{"dsn":"https://22222222222222222222222222222222@h/42"}`+"\n", nil),
	} {
		if w := e.do(r); w.Code != http.StatusUnauthorized {
			t.Errorf("%s = %d", name, w.Code)
		}
	}
	if e.queued() != 0 {
		t.Error("an unauthenticated request queued an event")
	}
	// The key may travel only in the envelope header.
	dsnOnly := `{"event_id":"22222222222222222222222222222222","dsn":"https://` + fdKey + `@h/42"}` + "\n{\"type\":\"event\"}\n{}\n"
	if w := e.do(post(fdPath, "", dsnOnly, nil)); w.Code != 200 || e.queued() != 1 {
		t.Errorf("dsn only = %d", w.Code)
	}
}

func TestSizeCaps(t *testing.T) {
	e := newEnv(t)
	huge := bytes.Repeat([]byte("a"), MaxEnvelopeBytes+1)
	if w := e.do(post(fdPath, fdQuery, string(huge), nil)); w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("identity 20 MiB+1 = %d", w.Code)
	}
	var zipped bytes.Buffer
	zipped.Write(compress(t, "gzip", huge))
	if w := e.do(post(fdPath, fdQuery, zipped.String(), map[string]string{"Content-Encoding": "gzip"})); w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("gzip 20 MiB+1 = %d", w.Code)
	}
	// An event over 1 MiB is dropped and counted; the envelope still succeeds.
	big := `{"event_id":"33333333333333333333333333333333"}` + "\n{\"type\":\"event\"}\n{\"x\":\"" + strings.Repeat("a", MaxEventBytes) + "\"}\n"
	if w := e.do(post(fdPath, fdQuery, big, nil)); w.Code != 200 || e.queued() != 0 {
		t.Errorf("big event = %d", w.Code)
	}
	if got := e.h.Stats().Snapshot()["too_large"]; got != 1 {
		t.Errorf("too_large = %d", got)
	}
	// Attachments: oversized and minidump are counted, not stored.
	att := fmt.Sprintf("{}\n{\"type\":\"attachment\",\"length\":%d,\"attachment_type\":\"event.attachment\"}\n%s\n", MaxAttachment+1, strings.Repeat("a", MaxAttachment+1))
	att += "{\"type\":\"attachment\",\"length\":4,\"attachment_type\":\"event.minidump\"}\nMDMP\n{\"type\":\"transaction\"}\n{}\n{\"type\":\"weird type!\"}\n{}\n"
	if w := e.do(post(fdPath, fdQuery, att, nil)); w.Code != 200 {
		t.Errorf("attachments = %d", w.Code)
	}
	snap := e.h.Stats().Snapshot()
	if snap["attachment_too_large"] != 1 || snap["minidump_deferred"] != 1 || snap["item:transaction"] != 1 || snap["item:unknown"] != 1 {
		t.Errorf("stats = %v", snap)
	}
}

func TestCORS(t *testing.T) {
	e := newEnv(t, sites.DSNKey{PublicKey: fdKey, AllowedOrigins: []string{"https://app.example.test"}})
	pre := httptest.NewRequest(http.MethodOptions, fdPath, nil)
	pre.Header.Set("Origin", "https://anything.example.test")
	w := e.do(pre)
	h := w.Header()
	if w.Code != http.StatusNoContent || h.Get("Access-Control-Allow-Origin") != "*" || h.Get("Access-Control-Max-Age") != "3600" ||
		!strings.Contains(h.Get("Access-Control-Allow-Headers"), "x-sentry-auth") || !strings.Contains(h.Get("Access-Control-Expose-Headers"), "x-sentry-rate-limits") {
		t.Fatalf("preflight = %d %v", w.Code, h)
	}
	if w := e.do(httptest.NewRequest(http.MethodOptions, "/api/42/envelope", nil)); w.Code != http.StatusNoContent {
		t.Errorf("preflight without slash = %d", w.Code)
	}
	// A key with allowed origins: listed origin is echoed, others get 403, no Origin passes.
	ok := post(fdPath, fdQuery, simpleEnvelope, map[string]string{"Origin": "https://App.Example.test"})
	if w := e.do(ok); w.Code != 200 || w.Header().Get("Access-Control-Allow-Origin") != "https://App.Example.test" {
		t.Errorf("listed origin = %d %v", w.Code, w.Header())
	}
	bad := post(fdPath, fdQuery, simpleEnvelope, map[string]string{"Origin": "https://evil.example.test"})
	if w := e.do(bad); w.Code != http.StatusForbidden {
		t.Errorf("unlisted origin = %d", w.Code)
	}
	if w := e.do(post(fdPath, fdQuery, strings.Replace(simpleEnvelope, "11111111111111111111111111111111", "44444444444444444444444444444444", 1), nil)); w.Code != 200 {
		t.Errorf("no origin = %d", w.Code)
	}
	// A key without origins allows any origin with "*".
	open := newEnv(t)
	r := post(fdPath, fdQuery, simpleEnvelope, map[string]string{"Origin": "https://evil.example.test"})
	if w := open.do(r); w.Code != 200 || w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("open key = %d %v", w.Code, w.Header())
	}
}

func eventEnvelope(id string) string {
	return `{"event_id":"` + id + `"}` + "\n" + `{"type":"event"}` + "\n" + `{"message":"m"}` + "\n"
}

func TestSpikeProtectionIsPerSite(t *testing.T) {
	e := newEnv(t)
	e.h.WithSpikeLimit(600)
	for i := 0; i < 600; i++ {
		id := fmt.Sprintf("%032d", i)
		if w := e.do(post(fdPath, fdQuery, eventEnvelope(id), nil)); w.Code != 200 {
			t.Fatalf("event %d = %d: %s", i, w.Code, w.Body)
		}
		if i%100 == 99 {
			e.queued() // keep the intake queue from filling
		}
	}
	w := e.do(post(fdPath, fdQuery, eventEnvelope(fmt.Sprintf("%032d", 601)), nil))
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "60" ||
		w.Header().Get("X-Sentry-Rate-Limits") != "60:error;default:project:spike_protection" {
		t.Fatalf("601st = %d %v", w.Code, w.Header())
	}
	other := post("/api/43/envelope/", "?sentry_key="+fdOtherKey, eventEnvelope(fmt.Sprintf("%032d", 7)), nil)
	if w := e.do(other); w.Code != 200 {
		t.Fatalf("other site = %d", w.Code)
	}
	// The window rolls over after a minute.
	*e.now = e.now.Add(61 * time.Second)
	if w := e.do(post(fdPath, fdQuery, eventEnvelope(fmt.Sprintf("%032d", 602)), nil)); w.Code != 200 {
		t.Fatalf("next minute = %d", w.Code)
	}
	// An envelope of several events is admitted whole or not at all.
	multi := "{}\n" + strings.Repeat("{\"type\":\"event\"}\n{}\n", 5)
	small := newEnv(t)
	small.h.WithSpikeLimit(4)
	if w := small.do(post(fdPath, fdQuery, multi, nil)); w.Code != http.StatusTooManyRequests {
		t.Errorf("5 events over a cap of 4 = %d", w.Code)
	}
}

func TestBudgetExhaustedIs429WithHeaders(t *testing.T) {
	reg := newRegistry(appSite(fdSite, sites.DSNKey{PublicKey: fdKey}))
	now := fdNow
	intake := appingest.NewIntake(appingest.Config{KeyBudgetPerMinute: 2})
	h := NewHandler(reg, intake, nil, nil, nil, func() time.Time { return now }).WithNormaliser((&stubNormaliser{}).Normalise)
	codes := []int{}
	var last *httptest.ResponseRecorder
	for i := 0; i < 3; i++ {
		last = httptest.NewRecorder()
		h.ServeHTTP(last, post(fdPath, fdQuery, eventEnvelope(fmt.Sprintf("%032d", i)), nil))
		codes = append(codes, last.Code)
	}
	if fmt.Sprint(codes) != "[200 200 429]" {
		t.Fatalf("codes = %v", codes)
	}
	if last.Header().Get("Retry-After") == "" || !strings.HasPrefix(last.Header().Get("X-Sentry-Rate-Limits"), "60:") {
		t.Errorf("429 headers = %v", last.Header())
	}
	if last.Header().Get("X-Sentry-Rate-Limits") == rejectUnsupported {
		t.Error("429 carries the 2xx reject-all header")
	}
}

func TestFullQueueIs503(t *testing.T) {
	reg := newRegistry(appSite(fdSite, sites.DSNKey{PublicKey: fdKey}))
	intake := appingest.NewIntake(appingest.Config{QueueSize: 1})
	h := NewHandler(reg, intake, nil, nil, nil, func() time.Time { return fdNow }).WithNormaliser((&stubNormaliser{}).Normalise)
	do := func(id string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, post(fdPath, fdQuery, eventEnvelope(id), nil))
		return w
	}
	if w := do(fmt.Sprintf("%032d", 1)); w.Code != 200 {
		t.Fatalf("first = %d", w.Code)
	}
	w := do(fmt.Sprintf("%032d", 2))
	if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") == "" {
		t.Fatalf("full queue = %d %v", w.Code, w.Header())
	}
}

func TestNormaliserFailureAndMissingNormaliser(t *testing.T) {
	e := newStubEnv(t)
	e.norm.err = errors.New("bad event")
	if w := e.do(post(fdPath, fdQuery, simpleEnvelope, nil)); w.Code != 200 || e.queued() != 0 {
		t.Errorf("invalid event = %d", w.Code)
	}
	if e.h.Stats().Snapshot()["event_invalid"] != 1 {
		t.Errorf("stats = %v", e.h.Stats().Snapshot())
	}
	none := newEnv(t)
	none.h.WithNormaliser(nil)
	if w := none.do(post(fdPath, fdQuery, simpleEnvelope, nil)); w.Code != 200 || none.queued() != 0 || none.h.Stats().Snapshot()["event_unsupported"] != 1 {
		t.Errorf("no normaliser = %d %v", w.Code, none.h.Stats().Snapshot())
	}
}

func TestBodyQueueGetsPrivateCopyAndRequestMeta(t *testing.T) {
	e := newEnv(t)
	r := post(fdPath, fdQuery, simpleEnvelope, map[string]string{"User-Agent": "UA/1", "X-Forwarded-For": "203.0.113.9"})
	r.RemoteAddr = "198.51.100.7:1234"
	if w := e.do(r); w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	if len(e.bodies.items) != 1 {
		t.Fatalf("bodies = %d", len(e.bodies.items))
	}
	b := e.bodies.items[0]
	if b.SiteID != fdSite || b.EventID != "11111111111111111111111111111111" || b.Message != "hi" || b.Country != "DE" {
		t.Fatalf("body = %+v", b)
	}
	// A full body queue loses the body, not the event.
	e.bodies.full = true
	r = post(fdPath, fdQuery, strings.Replace(simpleEnvelope, "1111", "2222", 1), nil)
	if w := e.do(r); w.Code != 200 {
		t.Fatalf("full body queue = %d", w.Code)
	}
	if e.h.Stats().Snapshot()["body_queue_full"] != 1 {
		t.Errorf("stats = %v", e.h.Stats().Snapshot())
	}
}

func TestDuplicateEventIsOKAndCountedByIntake(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 2; i++ {
		if w := e.do(post(fdPath, fdQuery, simpleEnvelope, nil)); w.Code != 200 {
			t.Fatalf("send %d = %d", i, w.Code)
		}
	}
	if e.queued() != 1 {
		t.Error("duplicate event queued twice")
	}
}

func TestStoreEndpoint(t *testing.T) {
	e := newEnv(t)
	raw, err := os.ReadFile("testdata/python/unhandled-exception.envelope")
	if err != nil {
		t.Fatal(err)
	}
	// Build the store body from the Python unhandled-exception event payload.
	body, err := decodeBody(post(fdPath, "", string(raw), map[string]string{"Content-Encoding": "gzip"}), MaxEnvelopeBytes)
	if err != nil {
		// The fixture may be identity-encoded; read it as is.
		body = raw
	}
	parsed, err := parseEnvelopeBytes(body, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	var payload []byte
	for _, it := range parsed.Items {
		if it.Type == "event" {
			payload = it.Payload
		}
	}
	if payload == nil {
		t.Fatal("no event item in the python fixture")
	}
	var probe struct {
		EventID string `json:"event_id"`
	}
	if err := json.Unmarshal(payload, &probe); err != nil || probe.EventID == "" {
		t.Fatalf("event payload has no event_id: %v", err)
	}
	w := e.do(post("/api/42/store/", fdQuery, string(payload), map[string]string{"Content-Type": "application/json"}))
	if w.Code != 200 || w.Body.String() != `{"id":"`+probe.EventID+`"}` {
		t.Fatalf("store = %d %s", w.Code, w.Body)
	}
	if e.queued() != 1 {
		t.Error("store event not queued")
	}
	if w := e.do(post("/api/42/store/", "", string(payload), nil)); w.Code != http.StatusUnauthorized {
		t.Errorf("store without key = %d", w.Code)
	}
}

func TestSecurityEndpoint(t *testing.T) {
	legacy := `{"csp-report":{"document-uri":"https://app.example.test/page?token=secret","violated-directive":"script-src-elem 'self'","effective-directive":"script-src-elem","blocked-uri":"https://cdn.evil.example.test/path/x.js?key=abc"}}`
	reportTo := `[{"type":"csp-violation","body":{"effectiveDirective":"img-src","blockedURL":"https://img.example.test/a/b.png","documentURL":"https://app.example.test/"}},{"type":"network-error","body":{}}]`
	e := newEnv(t)
	for name, c := range map[string]struct {
		body, host, directive string
	}{
		"report-uri": {legacy, "cdn.evil.example.test", "script-src-elem"},
		"report-to":  {reportTo, "img.example.test", "img-src"},
	} {
		w := e.do(post("/api/42/security/", fdQuery, c.body, map[string]string{"Content-Type": "application/csp-report"}))
		if w.Code != 200 {
			t.Fatalf("%s = %d %s", name, w.Code, w.Body)
		}
		q := drainQueue(e.intake)
		if len(q) != 1 {
			t.Fatalf("%s: %d events", name, len(q))
		}
		ev := q[0].Event
		title := appingest.IssueTitle(ev.Kind, ev.ErrorType, ev.Frames)
		if ev.ErrorType != "csp" || ev.Kind != "error" || !strings.Contains(title, c.host) || !strings.Contains(title, c.directive) {
			t.Errorf("%s: event = %+v title = %q", name, ev, title)
		}
		for _, secret := range []string{"secret", "key=abc", "/path/", "/a/b.png", "token"} {
			if strings.Contains(title, secret) || strings.Contains(ev.Frames, secret) {
				t.Errorf("%s: %q leaked into %q / %q", name, secret, title, ev.Frames)
			}
		}
		if ev.Fingerprint == "" {
			t.Errorf("%s: no fingerprint", name)
		}
	}
	for name, body := range map[string]string{"not json": "x", "empty object": "{}", "empty array": "[]"} {
		if w := e.do(post("/api/42/security/", fdQuery, body, nil)); w.Code != http.StatusBadRequest {
			t.Errorf("%s = %d", name, w.Code)
		}
	}
	if w := e.do(post("/api/42/security/", "", legacy, nil)); w.Code != http.StatusUnauthorized {
		t.Errorf("security without key = %d", w.Code)
	}
}

func drainQueue(in *appingest.Intake) []appingest.Queued {
	var out []appingest.Queued
	for {
		select {
		case q := <-in.Queue():
			out = append(out, q)
		default:
			return out
		}
	}
}

func TestMatchesRouterContract(t *testing.T) {
	e := newEnv(t)
	for path, want := range map[string]bool{
		"/api/42/envelope/": true, "/api/42/envelope": true, "/api/7/store/": true, "/api/9/security": true,
		"/api/private/sites": false, "/api/public/v1/app-events": false, "/api/42/minidump/": false,
	} {
		if got := e.h.Matches(httptest.NewRequest(http.MethodPost, path, nil)); got != want {
			t.Errorf("Matches(%s) = %v", path, got)
		}
	}
}

var emailPattern = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+\.[A-Za-z]{2,}`)

// TestFixtureBodiesCarryNoPII ingests every fixture and scans the scrubbed
// bodies handed to the body queue for emails, cookies and credentials.
func TestFixtureBodiesCarryNoPII(t *testing.T) {
	stored := 0
	for _, file := range fixtureFiles(t) {
		raw, _ := os.ReadFile(file)
		side, _ := os.ReadFile(strings.TrimSuffix(file, ".envelope") + ".json")
		var sc sidecar
		if err := json.Unmarshal(side, &sc); err != nil {
			t.Fatal(err)
		}
		e := newEnv(t)
		req := httptest.NewRequest(sc.Method, sc.Path+"?"+sc.Query, bytes.NewReader(raw))
		for k, v := range sc.Headers {
			req.Header.Set(k, v)
		}
		if w := e.do(req); w.Code != 200 {
			t.Fatalf("%s = %d", file, w.Code)
		}
		for _, b := range e.bodies.items {
			stored++
			enc, err := json.Marshal(b)
			if err != nil {
				t.Fatal(err)
			}
			text := strings.ToLower(string(enc))
			if m := emailPattern.FindString(string(enc)); m != "" {
				t.Errorf("%s: email-like %q in body", file, m)
			}
			for _, bad := range []string{`"cookie`, `"authorization`, `"email"`, `"ip_address"`, `"username"`} {
				if strings.Contains(text, bad) {
					t.Errorf("%s: %s in body", file, bad)
				}
			}
		}
	}
	if stored < 15 {
		t.Errorf("only %d bodies scanned", stored)
	}
}

func fromIP(r *http.Request, addr string) *http.Request { r.RemoteAddr = addr + ":4000"; return r }

func TestPerIPRequestLimit(t *testing.T) {
	e := newEnv(t)
	e.h.WithIPLimit(600) // burst 120
	codes := map[int]int{}
	var last *httptest.ResponseRecorder
	for i := 0; i < 125; i++ {
		last = e.do(fromIP(post(fdPath, fdQuery, eventEnvelope(fmt.Sprintf("%032d", i)), nil), "198.51.100.1"))
		codes[last.Code]++
		if i%50 == 49 {
			e.queued()
		}
	}
	if codes[200] != 120 || codes[429] != 5 {
		t.Fatalf("codes = %v", codes)
	}
	if last.Header().Get("Retry-After") == "" || !strings.Contains(last.Header().Get("X-Sentry-Rate-Limits"), "rate_limit") {
		t.Errorf("429 headers = %v", last.Header())
	}
	if w := e.do(fromIP(post(fdPath, fdQuery, eventEnvelope(fmt.Sprintf("%032d", 999)), nil), "198.51.100.2")); w.Code != 200 {
		t.Errorf("another IP = %d", w.Code)
	}
	// Tokens refill with time.
	*e.now = e.now.Add(time.Minute)
	if w := e.do(fromIP(post(fdPath, fdQuery, eventEnvelope(fmt.Sprintf("%032d", 998)), nil), "198.51.100.1")); w.Code != 200 {
		t.Errorf("after a minute = %d", w.Code)
	}
}

// blockingBody blocks the first Read until released, holding an in-flight slot.
type blockingBody struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockingBody) Read(p []byte) (int, error) {
	b.once.Do(func() { close(b.started) })
	<-b.release
	return 0, io.EOF
}

func TestInflightCapAnswers503(t *testing.T) {
	e := newEnv(t)
	e.h.WithMaxInflight(1)
	body := &blockingBody{started: make(chan struct{}), release: make(chan struct{})}
	req := httptest.NewRequest(http.MethodPost, fdPath+fdQuery, body)
	done := make(chan int)
	go func() { done <- e.do(req).Code }()
	<-body.started
	w := e.do(post(fdPath, fdQuery, simpleEnvelope, nil))
	if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") != "5" {
		t.Fatalf("second request = %d %v", w.Code, w.Header())
	}
	close(body.release)
	<-done
	// The slot is free again.
	if w := e.do(post(fdPath, fdQuery, simpleEnvelope, nil)); w.Code != 200 {
		t.Fatalf("after release = %d", w.Code)
	}
}

func TestUnauthenticatedBodyIsCappedSmall(t *testing.T) {
	e := newEnv(t)
	big := `{"dsn":"https://` + fdKey + `@h/42"}` + "\n{\"type\":\"attachment\",\"length\":3000000}\n" + strings.Repeat("a", 3000000) + "\n"
	if w := e.do(post(fdPath, "", big, nil)); w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("3 MB dsn-only body = %d", w.Code)
	}
	// The same body with the key in the query is within the authenticated cap.
	if w := e.do(post(fdPath, fdQuery, big, nil)); w.Code != 200 {
		t.Errorf("3 MB keyed body = %d", w.Code)
	}
	// A small dsn-only request still works.
	ok := `{"event_id":"55555555555555555555555555555555","dsn":"https://` + fdKey + `@h/42"}` + "\n{\"type\":\"event\"}\n{}\n"
	if w := e.do(post(fdPath, "", ok, nil)); w.Code != 200 {
		t.Errorf("small dsn-only = %d", w.Code)
	}
	// The cap applies to the compressed size.
	noise := make([]byte, 3<<20)
	_, _ = rand.Read(noise)
	gz := compress(t, "gzip", append([]byte(`{"dsn":"https://`+fdKey+`@h/42"}`+"\n{\"type\":\"attachment\",\"length\":3145728}\n"), noise...))
	if len(gz) <= 2<<20 {
		t.Skip("test noise compressed too well")
	}
	if w := e.do(post(fdPath, "", string(gz), map[string]string{"Content-Encoding": "gzip"})); w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("large compressed dsn-only body = %d", w.Code)
	}
}
