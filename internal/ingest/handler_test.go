package ingest_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"panel/internal/db"
	"panel/internal/dict"
	"panel/internal/enrich"
	"panel/internal/health"
	"panel/internal/ingest"
	"panel/internal/salts"
	"panel/internal/session"
	"panel/internal/sites"
	"panel/internal/testpg"
	"panel/internal/writer"
)

const (
	okBody = `{"site":"abc123","name":"pageview","url":"https://example.com/"}`
	testUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/120.0 Safari/537.36"
)

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

type noneClosed struct{}

func (noneClosed) IsClosed(int32, time.Time) bool { return false }

type handlerFixture struct {
	h       *ingest.Handler
	pool    *pgxpool.Pool
	writer  *writer.Writer
	clock   *fakeClock
	cancel  context.CancelFunc
	stopped <-chan error
}

type denyLimiter struct{}

func (denyLimiter) Allow(string) bool { return false }

type recordingLimiter struct{ keys []string }

func (l *recordingLimiter) Allow(key string) bool {
	l.keys = append(l.keys, key)
	return true
}

type siteRegistry struct{ site sites.Site }

func (r siteRegistry) ByPublicID(id string) (sites.Site, bool) {
	return r.site, id == r.site.PublicID
}

func (r siteRegistry) Site(id int32) (sites.Site, bool) { return r.site, id == r.site.ID }
func (r siteRegistry) All() []sites.Site                { return []sites.Site{r.site} }

func TestHostilePayloads(t *testing.T) {
	f := newHandlerFixture(t, 32, nil)
	big := strings.Repeat("a", 9*1024)
	props := map[string]string{}
	for i := 0; i < 31; i++ {
		props[fmt.Sprint("k", i)] = "v"
	}
	propsJSON, err := json.Marshal(map[string]any{
		"site": "abc123", "name": "x", "url": "https://example.com/", "props": props,
	})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		body string
		want int
	}{
		{name: "too big", body: `{"site":"abc123","name":"pageview","url":"https://example.com/` + big + `"}`, want: http.StatusBadRequest},
		{name: "31 props", body: string(propsJSON), want: http.StatusBadRequest},
		{name: "bad utf8", body: "{\"site\":\"abc123\",\"name\":\"\xff\",\"url\":\"https://example.com/\"}", want: http.StatusBadRequest},
		{name: "long url", body: `{"site":"abc123","name":"pageview","url":"https://example.com/` + strings.Repeat("p", 8000) + `"}`, want: http.StatusBadRequest},
		{name: "host not allowed", body: `{"site":"abc123","name":"pageview","url":"https://evil.test/"}`, want: http.StatusBadRequest},
		{name: "unknown site", body: `{"site":"nope","name":"pageview","url":"https://example.com/"}`, want: http.StatusBadRequest},
		{name: "ok and client ids ignored", body: `{"site":"abc123","name":"pageview","url":"https://example.com/","visitor_id":"123","session_id":"456"}`, want: http.StatusAccepted},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			rr := post(t, f.h, tt.body, testUA, "198.51.100.7:5555")
			if rr.Code != tt.want {
				t.Fatalf("code=%d, want %d; body=%q", rr.Code, tt.want, rr.Body.String())
			}
			if strings.Contains(rr.Body.String(), "198.51.100.7") || strings.Contains(rr.Body.String(), testUA) {
				t.Fatal("response leaks request identity")
			}
		})
	}

	f.stop(t)
	var events int
	var visitorID, sessionID int64
	if err := f.pool.QueryRow(context.Background(), `select count(*), min(visitor_id), min(session_id) from events`).Scan(&events, &visitorID, &sessionID); err != nil {
		t.Fatal(err)
	}
	if events != 1 || visitorID == 123 || sessionID == 456 {
		t.Fatalf("events=%d visitor_id=%d session_id=%d", events, visitorID, sessionID)
	}
}

func TestBackpressureReturns503Fast(t *testing.T) {
	f := newHandlerFixture(t, 0, nil)
	start := time.Now()
	rr := post(t, f.h, okBody, testUA, "198.51.100.7:5555")
	if elapsed := time.Since(start); rr.Code != http.StatusServiceUnavailable || elapsed > 20*time.Millisecond {
		t.Fatalf("code=%d took %v", rr.Code, elapsed)
	}
	if rr.Header().Get("Retry-After") == "" {
		t.Fatal("503 response has no Retry-After header")
	}
}

func TestBotsCountedNotStored(t *testing.T) {
	f := newHandlerFixture(t, 32, nil)
	before := ingest.Bots[enrich.BotUserAgent].Load()
	rr := post(t, f.h, okBody, "Googlebot/2.1", "66.249.66.1:5555")
	if rr.Code != http.StatusAccepted {
		t.Fatalf("code=%d, want 202", rr.Code)
	}
	f.stop(t)
	var events int
	if err := f.pool.QueryRow(context.Background(), `select count(*) from events`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 0 || ingest.Bots[enrich.BotUserAgent].Load() != before+1 {
		t.Fatalf("events=%d bots=%d", events, ingest.Bots[enrich.BotUserAgent].Load()-before)
	}
}

func TestExcludedIPCountedNotStored(t *testing.T) {
	f := newHandlerFixture(t, 32, nil)
	setExclusions(t, f, []string{"203.0.113.7"}, nil)
	limiter := &recordingLimiter{}
	f.h.Limiter = limiter
	before := health.ExclusionsFiltered[health.ExclusionIP].Load()

	rr := post(t, f.h, okBody, testUA, "[::ffff:203.0.113.7]:5555")
	if rr.Code != http.StatusAccepted {
		t.Fatalf("code=%d, want 202", rr.Code)
	}
	if len(limiter.keys) != 0 {
		t.Fatalf("excluded request reached limiter: %v", limiter.keys)
	}
	f.stop(t)
	var events int
	if err := f.pool.QueryRow(context.Background(), `select count(*) from events`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 0 || health.ExclusionsFiltered[health.ExclusionIP].Load() != before+1 {
		t.Fatalf("events=%d excluded_ip_delta=%d", events, health.ExclusionsFiltered[health.ExclusionIP].Load()-before)
	}
}

func TestExcludedTrafficDoesNotLogClientIP(t *testing.T) {
	f := newHandlerFixture(t, 32, nil)
	setExclusions(t, f, []string{"10.0.0.0/8"}, nil)
	f.h.Diag = true
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	rr := post(t, f.h, okBody, testUA, "10.1.2.3:5555")
	if rr.Code != http.StatusAccepted {
		t.Fatalf("code=%d, want 202", rr.Code)
	}
	if logs.Len() != 0 {
		t.Fatalf("excluded request was logged: %s", logs.String())
	}
}

func TestExcludedPathCountedNotStored(t *testing.T) {
	f := newHandlerFixture(t, 32, nil)
	setExclusions(t, f, nil, []string{"/admin/*"})
	before := health.ExclusionsFiltered[health.ExclusionPath].Load()
	body := `{"site":"abc123","name":"pageview","url":"https://example.com/admin/users?source=test"}`

	rr := post(t, f.h, body, testUA, "198.51.100.7:5555")
	if rr.Code != http.StatusAccepted {
		t.Fatalf("code=%d, want 202", rr.Code)
	}
	f.stop(t)
	var events int
	if err := f.pool.QueryRow(context.Background(), `select count(*) from events`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 0 || health.ExclusionsFiltered[health.ExclusionPath].Load() != before+1 {
		t.Fatalf("events=%d excluded_path_delta=%d", events, health.ExclusionsFiltered[health.ExclusionPath].Load()-before)
	}
}

func TestExclusionNonMatchPasses(t *testing.T) {
	f := newHandlerFixture(t, 32, nil)
	setExclusions(t, f, []string{"203.0.113.0/24"}, []string{"/admin/*"})
	body := `{"site":"abc123","name":"pageview","url":"https://example.com/admin/users/7"}`

	rr := post(t, f.h, body, testUA, "198.51.100.7:5555")
	if rr.Code != http.StatusAccepted {
		t.Fatalf("code=%d, want 202", rr.Code)
	}
	f.stop(t)
	var events int
	if err := f.pool.QueryRow(context.Background(), `select count(*) from events`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 1 {
		t.Fatalf("events=%d, want 1", events)
	}
}

func TestRateLimitReturns429WithoutIdentityLeak(t *testing.T) {
	f := newHandlerFixture(t, 32, nil)
	f.h.Limiter = denyLimiter{}
	rr := post(t, f.h, okBody, testUA, "198.51.100.7:5555")
	if rr.Code != http.StatusTooManyRequests || rr.Header().Get("Retry-After") == "" {
		t.Fatalf("code=%d retry-after=%q", rr.Code, rr.Header().Get("Retry-After"))
	}
	if strings.Contains(rr.Body.String(), "198.51.100.7") || strings.Contains(rr.Body.String(), testUA) {
		t.Fatal("response leaks request identity")
	}
}

func TestRateLimitKeysDoNotContainRawIP(t *testing.T) {
	f := newHandlerFixture(t, 32, nil)
	limiter := &recordingLimiter{}
	f.h.Limiter = limiter
	rr := post(t, f.h, okBody, testUA, "198.51.100.7:5555")
	if rr.Code != http.StatusAccepted {
		t.Fatalf("code=%d, want 202", rr.Code)
	}
	if len(limiter.keys) != 2 || limiter.keys[0] != "site:1" || !strings.HasPrefix(limiter.keys[1], "ip:") {
		t.Fatalf("rate-limit keys=%v", limiter.keys)
	}
	if strings.Contains(strings.Join(limiter.keys, " "), "198.51.100.7") {
		t.Fatalf("rate-limit keys contain raw IP: %v", limiter.keys)
	}
}

func TestPreviousSaltContinuesSessionAcrossLocalMidnight(t *testing.T) {
	f := newHandlerFixture(t, 32, nil)
	f.clock.now = time.Date(2026, 10, 7, 23, 50, 0, 0, time.UTC)
	if rr := post(t, f.h, okBody, testUA, "198.51.100.7:5555"); rr.Code != http.StatusAccepted {
		t.Fatalf("first event code=%d", rr.Code)
	}
	f.clock.now = f.clock.now.Add(20 * time.Minute)
	if rr := post(t, f.h, okBody, testUA, "198.51.100.7:5555"); rr.Code != http.StatusAccepted {
		t.Fatalf("second event code=%d", rr.Code)
	}
	f.stop(t)

	var sessions, pageviews int
	var localDay time.Time
	if err := f.pool.QueryRow(context.Background(), `select count(*), sum(pageviews), min(local_day) from sessions`).Scan(&sessions, &pageviews, &localDay); err != nil {
		t.Fatal(err)
	}
	if sessions != 1 || pageviews != 2 || localDay.Format(time.DateOnly) != "2026-10-07" {
		t.Fatalf("sessions=%d pageviews=%d local_day=%s", sessions, pageviews, localDay)
	}
}

func TestTrustedProxyControlsForwardedClientIP(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}
	f := newHandlerFixture(t, 32, trusted)
	req := httptest.NewRequest(http.MethodPost, "/api/event", strings.NewReader(okBody))
	req.Header.Set("User-Agent", testUA)
	req.Header.Set("X-Forwarded-For", "203.0.113.9, 192.0.2.12")
	req.RemoteAddr = "192.0.2.10:5555"
	rr := httptest.NewRecorder()
	f.h.ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("code=%d, want 202", rr.Code)
	}
	f.stop(t)

	day := salts.LocalDay(fakeNow(), time.UTC)
	salt, ok := f.h.Salts.For(day)
	if !ok {
		t.Fatal("test salt missing")
	}
	want := salts.VisitorID(salt, 1, "203.0.113.9", testUA)
	var got int64
	if err := f.pool.QueryRow(context.Background(), `select visitor_id from events`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("visitor_id=%d, want forwarded-client id %d", got, want)
	}
}

func TestTokenBuckets(t *testing.T) {
	limiter := ingest.NewTokenBuckets(0, 2)
	if !limiter.Allow("site:1") || !limiter.Allow("site:1") {
		t.Fatal("initial burst was rejected")
	}
	if limiter.Allow("site:1") {
		t.Fatal("request beyond burst was allowed")
	}
	if !limiter.Allow("site:2") {
		t.Fatal("one key exhausted another key's bucket")
	}
}

func TestParsePayloadFieldLimits(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "trailing JSON", body: okBody + `{}`},
		{name: "long prop key", body: `{"site":"abc123","name":"x","url":"https://example.com/","props":{"` + strings.Repeat("k", 301) + `":"v"}}`},
		{name: "long prop value", body: `{"site":"abc123","name":"x","url":"https://example.com/","props":{"k":"` + strings.Repeat("v", 2001) + `"}}`},
		{name: "negative engagement", body: `{"site":"abc123","name":"engagement","url":"https://example.com/","engagement_ms":-1}`},
		{name: "scroll over 100", body: `{"site":"abc123","name":"engagement","url":"https://example.com/","scroll_pct":101}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ingest.ParsePayload([]byte(tt.body)); err == nil {
				t.Fatal("payload accepted")
			}
		})
	}
}

func newHandlerFixture(t *testing.T, queueSize int, trusted []netip.Prefix) *handlerFixture {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	day := fakeNow().UTC().Truncate(24 * time.Hour)
	if err := db.EnsurePartitions(ctx, pool, day.AddDate(0, 0, -1), day.AddDate(0, 0, 2)); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "sites.json")
	if err := os.WriteFile(path, []byte(`[{"id":1,"public_id":"abc123","timezone":"UTC","retention_days":30,"allowed_hosts":["example.com"]}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	registry, err := sites.LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	enricher, err := enrich.New("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = enricher.Close() })
	clk := &fakeClock{now: fakeNow()}
	saltStore := salts.New(pool, clk)
	if err := saltStore.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	dictionary := dict.New(pool)
	primeDictionary(t, dictionary)
	sessions := session.NewMemory(4, clk)
	w := writer.New(pool, sessions, queueSize, time.Hour, time.Hour, noneClosed{})
	runCtx, cancel := context.WithCancel(context.Background())
	stopped := make(chan error, 1)
	go func() { stopped <- w.Run(runCtx) }()

	h := &ingest.Handler{
		Sites: registry, Enricher: enricher, Salts: saltStore, Dict: dictionary,
		Sessions: sessions, Writer: w, Limiter: ingest.NewTokenBuckets(1000, 1000),
		TrustedProxies: trusted, Clock: clk,
	}
	f := &handlerFixture{h: h, pool: pool, writer: w, clock: clk, cancel: cancel, stopped: stopped}
	t.Cleanup(func() { f.stop(t) })
	return f
}

func (f *handlerFixture) stop(t *testing.T) {
	t.Helper()
	if f.cancel == nil {
		return
	}
	f.cancel()
	f.cancel = nil
	select {
	case err := <-f.stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(12 * time.Second):
		t.Fatal("writer did not stop")
	}
}

func primeDictionary(t *testing.T, dictionary *dict.Dict) {
	t.Helper()
	ctx := context.Background()
	for kind, values := range map[dict.Kind][]string{
		dict.Path: {"/"}, dict.Browser: {"Chrome"}, dict.OS: {"Windows"},
	} {
		if _, err := dictionary.IDs(ctx, kind, values); err != nil {
			t.Fatal(err)
		}
	}
}

func setExclusions(t *testing.T, fixture *handlerFixture, ips, paths []string) {
	t.Helper()
	site, ok := fixture.h.Sites.ByPublicID("abc123")
	if !ok {
		t.Fatal("fixture site missing")
	}
	exclusions, err := sites.CompileExclusions(ips, paths)
	if err != nil {
		t.Fatal(err)
	}
	site.Exclusions = exclusions
	fixture.h.Sites = siteRegistry{site: site}
}

func post(t *testing.T, h http.Handler, body, ua, remote string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/event", strings.NewReader(body))
	req.Header.Set("User-Agent", ua)
	req.RemoteAddr = remote
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func fakeNow() time.Time {
	return time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
}
