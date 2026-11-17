package otlpingest

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"supermetry/internal/appingest"
	"supermetry/internal/sites"
	"supermetry/internal/spans"
)

const otherKey = "fedcba9876543210fedcba9876543210"

type registry struct{ list []sites.Site }

func (r registry) ByPublicID(string) (sites.Site, bool) { return sites.Site{}, false }
func (r registry) Site(id int32) (sites.Site, bool) {
	for _, s := range r.list {
		if s.ID == id {
			return s, true
		}
	}
	return sites.Site{}, false
}
func (r registry) All() []sites.Site { return r.list }
func (r registry) ByDSNKey(k string) (sites.Site, sites.DSNKey, bool) {
	for _, s := range r.list {
		for _, dk := range s.App.DSNKeys {
			if dk.PublicKey == k {
				return s, dk, true
			}
		}
	}
	return sites.Site{}, sites.DSNKey{}, false
}

type sink struct {
	mu   sync.Mutex
	got  []spans.Span
	full bool
}

func (s *sink) Enqueue(b []spans.Span) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.full {
		return false
	}
	s.got = append(s.got, b...)
	return true
}

func (s *sink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.got)
}

type stats struct {
	mu sync.Mutex
	m  map[string]int
}

func (s *stats) Add(name string, n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]int{}
	}
	s.m[name] += n
}

type rig struct {
	h      *Handler
	sink   *sink
	intake *appingest.Intake
	stats  *stats
}

func newRig(t *testing.T, budget int) *rig {
	t.Helper()
	a := testSite()
	b := testSite()
	b.ID = 43
	b.App.DSNKeys = []sites.DSNKey{{PublicKey: otherKey}}
	r := &rig{sink: &sink{}, stats: &stats{}, intake: appingest.NewIntake(appingest.Config{})}
	var bud *spans.Budget
	if budget > 0 {
		bud = spans.NewBudget(budget)
	}
	r.h = NewHandler(registry{[]sites.Site{a, b}}, r.intake, r.sink, bud, r.stats, func() time.Time { return testNow })
	return r
}

func (r *rig) post(path string, body []byte, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("Authorization", "Bearer "+testKey)
	for k, v := range hdr {
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	w := httptest.NewRecorder()
	r.h.ServeHTTP(w, req)
	return w
}

const path42 = "/api/42/otlp/v1/traces"

func TestMatches(t *testing.T) {
	h := newRig(t, 0).h
	for p, want := range map[string]bool{
		"/api/42/otlp/v1/traces": true, "/api/42/otlp/v1/traces/": true, "/api/0/otlp/v1/traces": false, "/api/042/otlp/v1/traces": false,
		"/api/42/envelope/": false, "/api/private/otlp/v1/traces": false, "/api/42/otlp/v1/metrics": false,
	} {
		if got := h.Matches(httptest.NewRequest(http.MethodPost, p, nil)); got != want {
			t.Errorf("%s: %v want %v", p, got, want)
		}
	}
}

func TestProtobufAndGzipAndJSONAccepted(t *testing.T) {
	r := newRig(t, 0)
	body := marshalRequest(goHTTP())
	w := r.post(path42, body, nil)
	if w.Code != 200 || w.Body.Len() != 0 || w.Header().Get("Content-Type") != "application/x-protobuf" {
		t.Fatalf("proto: %d %q %q", w.Code, w.Body.String(), w.Header().Get("Content-Type"))
	}
	if r.sink.count() != 3 {
		t.Fatalf("stored %d", r.sink.count())
	}
	for _, s := range r.sink.got {
		if s.SiteID != 42 {
			t.Fatalf("span for site %d", s.SiteID)
		}
	}
	w = r.post(path42, gz(marshalRequest(nodeExpress())), map[string]string{"Content-Encoding": "gzip"})
	if w.Code != 200 || r.sink.count() != 5 {
		t.Fatalf("gzip: %d stored=%d", w.Code, r.sink.count())
	}
	w = r.post(path42, mustRead(t, "python-flask.json"), map[string]string{"Content-Type": "application/json; charset=utf-8", "X-Supermetry-Key": testKey, "Authorization": ""})
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != "{}" || r.sink.count() != 9 {
		t.Fatalf("json: %d %q stored=%d", w.Code, w.Body.String(), r.sink.count())
	}
}

func TestExceptionBecomesAppEvent(t *testing.T) {
	r := newRig(t, 0)
	if w := r.post(path42, marshalRequest(nodeExpress()), nil); w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	select {
	case q := <-r.intake.Queue():
		if q.SiteID != 42 || q.Event.Kind != "error" || q.Event.ErrorType != "TypeError" || len(q.Event.TraceID) != 16 {
			t.Fatalf("queued=%+v", q)
		}
	default:
		t.Fatal("no app event queued")
	}
}

func TestAuthAndIsolation(t *testing.T) {
	r := newRig(t, 0)
	body := marshalRequest(goHTTP())
	cases := map[string]map[string]string{
		"no key":               {"Authorization": ""},
		"unknown key":          {"Authorization": "Bearer 00000000000000000000000000000000"},
		"malformed key":        {"Authorization": "Bearer nope"},
		"other project's key":  {"Authorization": "Bearer " + otherKey},
		"basic scheme":         {"Authorization": "Basic " + testKey},
		"conflicting two keys": {"X-Supermetry-Key": otherKey},
	}
	for name, hdr := range cases {
		w := r.post(path42, body, hdr)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s: %d", name, w.Code)
		}
	}
	if r.sink.count() != 0 {
		t.Fatal("rejected requests stored spans")
	}
	// the other project's key works only on its own path, and its spans carry its site id
	if w := r.post("/api/43/otlp/v1/traces", body, map[string]string{"Authorization": "Bearer " + otherKey}); w.Code != 200 {
		t.Fatalf("own key: %d", w.Code)
	}
	for _, s := range r.sink.got {
		if s.SiteID != 43 {
			t.Fatalf("cross-project span: site %d", s.SiteID)
		}
	}
	if w := r.post("/api/43/otlp/v1/traces", body, nil); w.Code != 401 {
		t.Fatalf("key of 42 on path 43: %d", w.Code)
	}
}

func TestExpiredKeyAndWebSiteRejected(t *testing.T) {
	r := newRig(t, 0)
	past := testNow.Add(-time.Hour)
	site := testSite()
	site.App.DSNKeys = []sites.DSNKey{{PublicKey: testKey, ExpiresAt: &past}}
	r.h.sites = registry{[]sites.Site{site}}
	if w := r.post(path42, marshalRequest(goHTTP()), nil); w.Code != 401 {
		t.Fatalf("expired: %d", w.Code)
	}
	site = testSite()
	site.Kind = sites.KindWeb
	r.h.sites = registry{[]sites.Site{site}}
	if w := r.post(path42, marshalRequest(goHTTP()), nil); w.Code != 401 {
		t.Fatalf("web site: %d", w.Code)
	}
}

func TestDuplicateBodyStoredOnce(t *testing.T) {
	r := newRig(t, 0)
	body := marshalRequest(goHTTP())
	r.post(path42, body, nil)
	if w := r.post(path42, body, nil); w.Code != 200 {
		t.Fatalf("dup status %d", w.Code)
	}
	if r.sink.count() != 3 {
		t.Fatalf("stored %d, want 3", r.sink.count())
	}
	// the same bytes from another project are not a duplicate
	if w := r.post("/api/43/otlp/v1/traces", body, map[string]string{"Authorization": "Bearer " + otherKey}); w.Code != 200 || r.sink.count() != 6 {
		t.Fatalf("other project: %d stored=%d", w.Code, r.sink.count())
	}
}

func TestSizeCaps(t *testing.T) {
	r := newRig(t, 0)
	if w := r.post(path42, make([]byte, MaxCompressedBytes+1), nil); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("4MiB+1: %d", w.Code)
	}
	if w := r.post(path42, make([]byte, MaxCompressedBytes), nil); w.Code == http.StatusRequestEntityTooLarge {
		t.Fatal("exactly 4 MiB must not be too large")
	}
	// A gzip bomb: ~200 MiB of zeros in well under 1 MiB.
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	chunk := make([]byte, 1<<20)
	for i := 0; i < 200; i++ {
		_, _ = zw.Write(chunk)
	}
	_ = zw.Close()
	if buf.Len() > MaxCompressedBytes {
		t.Fatalf("bomb too big: %d", buf.Len())
	}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	w := r.post(path42, buf.Bytes(), map[string]string{"Content-Encoding": "gzip"})
	runtime.ReadMemStats(&after)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("bomb: %d", w.Code)
	}
	if grown := after.TotalAlloc - before.TotalAlloc; grown > 150<<20 {
		t.Fatalf("bomb allocated %d MiB", grown>>20)
	}
	if r.sink.count() != 0 {
		t.Fatal("oversized request stored spans")
	}
}

func TestBadRequests(t *testing.T) {
	r := newRig(t, 0)
	if w := r.post(path42, []byte{0xff, 0xff, 0xff}, nil); w.Code != 400 {
		t.Fatalf("garbage proto: %d", w.Code)
	}
	if w := r.post(path42, []byte("{not json"), map[string]string{"Content-Type": "application/json"}); w.Code != 400 {
		t.Fatalf("garbage json: %d", w.Code)
	}
	if w := r.post(path42, []byte("x"), map[string]string{"Content-Encoding": "gzip"}); w.Code != 400 {
		t.Fatalf("bad gzip: %d", w.Code)
	}
	if w := r.post(path42, []byte("x"), map[string]string{"Content-Encoding": "br"}); w.Code != 415 {
		t.Fatalf("br: %d", w.Code)
	}
	if w := r.post(path42, []byte("x"), map[string]string{"Content-Type": "text/plain"}); w.Code != 415 {
		t.Fatalf("content type: %d", w.Code)
	}
	if w := r.post(path42, []byte("x"), map[string]string{"Content-Type": ""}); w.Code != 415 {
		t.Fatalf("no content type: %d", w.Code)
	}
	req := httptest.NewRequest(http.MethodGet, path42, nil)
	w := httptest.NewRecorder()
	r.h.ServeHTTP(w, req)
	if w.Code != 405 {
		t.Fatalf("GET: %d", w.Code)
	}
}

func TestPartialSuccessCountsRejected(t *testing.T) {
	r := newRig(t, 0)
	good := map[string]any{"traceId": strings.Repeat("ab", 16), "spanId": strings.Repeat("cd", 8), "name": "good", "kind": 2,
		"startTimeUnixNano": testNow.Add(-time.Minute).UnixNano(), "endTimeUnixNano": testNow.Add(-time.Minute + time.Second).UnixNano()}
	bad := map[string]any{}
	for k, v := range good {
		bad[k] = v
	}
	bad["traceId"] = strings.Repeat("a", 300)
	body, _ := json.Marshal(map[string]any{"resourceSpans": []any{map[string]any{"scopeSpans": []any{map[string]any{"spans": []any{bad, good}}}}}})
	w := r.post(path42, body, map[string]string{"Content-Type": "application/json"})
	if w.Code != 200 || r.sink.count() != 1 {
		t.Fatalf("%d stored=%d", w.Code, r.sink.count())
	}
	var resp struct {
		PartialSuccess struct {
			RejectedSpans string `json:"rejectedSpans"`
			ErrorMessage  string `json:"errorMessage"`
		} `json:"partialSuccess"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || resp.PartialSuccess.RejectedSpans != "1" || resp.PartialSuccess.ErrorMessage == "" {
		t.Fatalf("body %s err %v", w.Body.String(), err)
	}
	if r.stats.m["span_invalid"] != 1 {
		t.Fatalf("stats %v", r.stats.m)
	}
}

func TestBudgetAndBackPressure(t *testing.T) {
	r := newRig(t, 2)
	w := r.post(path42, marshalRequest(goHTTP()), nil) // 3 spans, budget 2
	if w.Code != 200 || r.sink.count() != 2 || w.Body.Len() == 0 {
		t.Fatalf("partial: %d stored=%d body=%x", w.Code, r.sink.count(), w.Body.Bytes())
	}
	w = r.post(path42, marshalRequest(nodeExpress()), nil)
	if w.Code != 429 || w.Header().Get("Retry-After") != "60" {
		t.Fatalf("over budget: %d %q", w.Code, w.Header().Get("Retry-After"))
	}
	if r.stats.m["span_rate_limited"] != 3 {
		t.Fatalf("stats %v", r.stats.m)
	}

	r = newRig(t, 0)
	r.sink.full = true
	body := marshalRequest(goHTTP())
	w = r.post(path42, body, nil)
	if w.Code != 503 || w.Header().Get("Retry-After") != "5" {
		t.Fatalf("queue full: %d %q", w.Code, w.Header().Get("Retry-After"))
	}
	r.sink.full = false
	if w = r.post(path42, body, nil); w.Code != 200 || r.sink.count() != 3 {
		t.Fatalf("retry after 503 must store: %d stored=%d", w.Code, r.sink.count())
	}
}

func TestPerformanceDisabled(t *testing.T) {
	r := newRig(t, 0)
	off := false
	site := testSite()
	site.App.PerformanceEnabled = &off
	r.h.sites = registry{[]sites.Site{site}}
	if w := r.post(path42, marshalRequest(goHTTP()), nil); w.Code != 200 || r.sink.count() != 0 {
		t.Fatalf("%d stored=%d", w.Code, r.sink.count())
	}
}

func TestPreflightAndOrigin(t *testing.T) {
	r := newRig(t, 0)
	req := httptest.NewRequest(http.MethodOptions, path42, nil)
	w := httptest.NewRecorder()
	r.h.ServeHTTP(w, req)
	if w.Code != 204 || w.Header().Get("Access-Control-Allow-Origin") != "*" || !strings.Contains(w.Header().Get("Access-Control-Allow-Headers"), "authorization") {
		t.Fatalf("preflight %d %v", w.Code, w.Header())
	}
	site := testSite()
	site.App.DSNKeys = []sites.DSNKey{{PublicKey: testKey, AllowedOrigins: []string{"https://app.example.com"}}}
	r.h.sites = registry{[]sites.Site{site}}
	if w := r.post(path42, marshalRequest(goHTTP()), map[string]string{"Origin": "https://evil.example"}); w.Code != 403 {
		t.Fatalf("bad origin %d", w.Code)
	}
	if w := r.post(path42, marshalRequest(goHTTP()), map[string]string{"Origin": "https://app.example.com"}); w.Code != 200 || w.Header().Get("Access-Control-Allow-Origin") != "https://app.example.com" {
		t.Fatalf("good origin %d %v", w.Code, w.Header())
	}
}

func TestInflightBound(t *testing.T) {
	r := newRig(t, 0)
	for i := 0; i < maxInflight; i++ {
		r.h.inflight <- struct{}{}
	}
	w := r.post(path42, marshalRequest(goHTTP()), nil)
	if w.Code != 503 || w.Header().Get("Retry-After") != "5" {
		t.Fatalf("%d", w.Code)
	}
	// authentication still comes first: a bad key never reaches the semaphore
	if w := r.post(path42, nil, map[string]string{"Authorization": ""}); w.Code != 401 {
		t.Fatalf("%d", w.Code)
	}
}

func TestDigestSetBounds(t *testing.T) {
	s := newDigestSet(2, time.Minute)
	d := func(b byte) [32]byte { return [32]byte{b} }
	if s.seen(d(1), testNow) || !s.seen(d(1), testNow) {
		t.Fatal("first/second")
	}
	s.seen(d(2), testNow)
	s.seen(d(3), testNow) // full: not remembered
	if len(s.entries) != 2 {
		t.Fatalf("entries=%d", len(s.entries))
	}
	if s.seen(d(1), testNow.Add(2*time.Minute)) {
		t.Fatal("expired entry must not count as seen")
	}
	_ = io.Discard
}
