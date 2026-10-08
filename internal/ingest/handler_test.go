package ingest_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/maxmind/mmdbwriter"
	"github.com/maxmind/mmdbwriter/mmdbtype"

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

type allProfilesTombstoned struct{}

func (allProfilesTombstoned) Contains(int32, string) bool { return true }

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
	snapshot := f.h.Sessions.(session.RichRealtimeStore).RealtimeSnapshot(1, f.clock.Now())
	if snapshot.Pageviews != 0 || len(snapshot.RecentActivity) != 0 {
		t.Fatalf("rejected event reached realtime: %+v", snapshot)
	}
}

func TestRealtimeRecordsQueuedPageviewsAndCustomEventsOnly(t *testing.T) {
	f := newHandlerFixture(t, 32, nil)
	for _, body := range []string{
		okBody,
		`{"site":"abc123","name":"engagement","url":"https://example.com/","engagement_ms":1000}`,
		`{"site":"abc123","name":"Signup","url":"https://example.com/"}`,
	} {
		if response := post(t, f.h, body, testUA, "198.51.100.7:5555"); response.Code != http.StatusAccepted {
			t.Fatalf("body=%s status=%d", body, response.Code)
		}
	}
	snapshot := f.h.Sessions.(session.RichRealtimeStore).RealtimeSnapshot(1, f.clock.Now())
	if snapshot.Pageviews != 1 || len(snapshot.Events) != 1 || snapshot.Events[0].Count != 1 || len(snapshot.RecentActivity) != 2 {
		t.Fatalf("realtime snapshot=%+v", snapshot)
	}
}

func TestHeatmapDisabledIsDroppedWithoutRawEvent(t *testing.T) {
	f := newHandlerFixture(t, 32, nil)
	before := health.HeatmapDropped.Load()
	body := `{"site":"abc123","name":"heatmap","url":"https://example.com/","clicks":[[500,120]],"doc_h":2000,"vw":390}`
	rr := post(t, f.h, body, testUA, "198.51.100.7:5555")
	if rr.Code != http.StatusAccepted {
		t.Fatalf("code=%d, want 202", rr.Code)
	}
	f.stop(t)
	var events int
	if err := f.pool.QueryRow(context.Background(), `select count(*) from events`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 0 || health.HeatmapDropped.Load() != before+1 {
		t.Fatalf("events=%d heatmap_dropped_delta=%d", events, health.HeatmapDropped.Load()-before)
	}
}

func TestHeatmapEnabledAggregatesWithoutRawEvent(t *testing.T) {
	f := newHandlerFixture(t, 32, nil)
	setHeatmapsEnabled(t, f, true)
	body := `{"site":"abc123","name":"heatmap","url":"https://example.com/","clicks":[[500,120],[509,139]],"doc_h":2000,"vw":390}`
	rr := post(t, f.h, body, testUA, "198.51.100.7:5555")
	if rr.Code != http.StatusAccepted {
		t.Fatalf("code=%d, want 202", rr.Code)
	}
	f.stop(t)
	var events, cells, clicks int
	if err := f.pool.QueryRow(context.Background(), `
		select (select count(*) from events), count(*), coalesce(sum(clicks),0) from heatmap_cells`).Scan(&events, &cells, &clicks); err != nil {
		t.Fatal(err)
	}
	if events != 0 || cells != 1 || clicks != 2 {
		t.Fatalf("events=%d cells=%d clicks=%d", events, cells, clicks)
	}
}

func TestPrivacySignalsAreDroppedOnlyWhenSiteSettingIsEnabled(t *testing.T) {
	for _, test := range []struct {
		name    string
		header  string
		enabled bool
		want    int
	}{
		{name: "GPC enabled", header: "Sec-GPC", enabled: true, want: 0},
		{name: "DNT enabled", header: "DNT", enabled: true, want: 0},
		{name: "GPC disabled", header: "Sec-GPC", enabled: false, want: 1},
		{name: "DNT disabled", header: "DNT", enabled: false, want: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newHandlerFixture(t, 32, nil)
			setRespectPrivacySignals(t, f, test.enabled)
			before := health.PrivacySignalsDropped.Load()
			droppedBefore := health.IngestDropped.Load()
			req := httptest.NewRequest(http.MethodPost, "/api/event", strings.NewReader(okBody))
			req.Header.Set("User-Agent", testUA)
			req.Header.Set(test.header, "1")
			req.RemoteAddr = "198.51.100.7:5555"
			rr := httptest.NewRecorder()
			f.h.ServeHTTP(rr, req)
			if rr.Code != http.StatusAccepted {
				t.Fatalf("code=%d, want 202", rr.Code)
			}
			f.stop(t)
			var events int
			if err := f.pool.QueryRow(context.Background(), `select count(*) from events`).Scan(&events); err != nil {
				t.Fatal(err)
			}
			if events != test.want {
				t.Fatalf("events=%d, want %d", events, test.want)
			}
			wantDelta := uint64(0)
			if test.enabled {
				wantDelta = 1
			}
			if got := health.PrivacySignalsDropped.Load() - before; got != wantDelta {
				t.Fatalf("privacy signal drop delta=%d, want %d", got, wantDelta)
			}
			if got := health.IngestDropped.Load() - droppedBefore; got != 0 {
				t.Fatalf("ingest_dropped_total delta=%d, want 0 (it counts 503s)", got)
			}
		})
	}
}

func TestHeatmapFromBotIsNotAggregated(t *testing.T) {
	f := newHandlerFixture(t, 32, nil)
	setHeatmapsEnabled(t, f, true)
	before := ingest.Bots[enrich.BotUserAgent].Load()
	body := `{"site":"abc123","name":"heatmap","url":"https://example.com/","clicks":[[500,120]],"doc_h":2000,"vw":390}`
	rr := post(t, f.h, body, "Googlebot/2.1", "66.249.66.1:5555")
	if rr.Code != http.StatusAccepted {
		t.Fatalf("code=%d, want 202", rr.Code)
	}
	f.stop(t)
	var cells int
	if err := f.pool.QueryRow(context.Background(), `select count(*) from heatmap_cells`).Scan(&cells); err != nil {
		t.Fatal(err)
	}
	if cells != 0 || ingest.Bots[enrich.BotUserAgent].Load() != before+1 {
		t.Fatalf("cells=%d bots_delta=%d", cells, ingest.Bots[enrich.BotUserAgent].Load()-before)
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

func TestExcludedCountryCountedAndUnresolvedCountryAccepted(t *testing.T) {
	f := newHandlerFixture(t, 32, nil)
	cityPath := writeCityMMDB(t)
	enricher, err := enrich.NewWithGeo(enrich.GeoPaths{City: cityPath})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = enricher.Close() })
	f.h.Enricher = enricher
	setCountryExclusions(t, f, []string{"US"})
	before := health.ExclusionsFiltered[health.ExclusionCountry].Load()

	if response := post(t, f.h, okBody, testUA, "192.0.2.42:5555"); response.Code != http.StatusAccepted {
		t.Fatalf("excluded country status = %d, want 202", response.Code)
	}
	if response := post(t, f.h, okBody, testUA, "198.51.100.7:5555"); response.Code != http.StatusAccepted {
		t.Fatalf("unresolved country status = %d, want 202", response.Code)
	}
	f.stop(t)

	var events int
	if err := f.pool.QueryRow(context.Background(), `select count(*) from events`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 1 || health.ExclusionsFiltered[health.ExclusionCountry].Load() != before+1 {
		t.Fatalf("events=%d excluded_country_delta=%d, want 1/1", events, health.ExclusionsFiltered[health.ExclusionCountry].Load()-before)
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

func TestCityEnrichmentReachesEventAndSession(t *testing.T) {
	f := newHandlerFixture(t, 32, nil)
	cityPath := writeCityMMDB(t)
	enricher, err := enrich.NewWithGeo(enrich.GeoPaths{City: cityPath})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = enricher.Close() })
	f.h.Enricher = enricher

	if rr := post(t, f.h, okBody, testUA, "192.0.2.42:5555"); rr.Code != http.StatusAccepted {
		t.Fatalf("code=%d, want 202", rr.Code)
	}
	f.stop(t)

	for _, table := range []string{"events", "sessions"} {
		var country, region, city string
		if err := f.pool.QueryRow(context.Background(), `
select row.country, region.value, city.value
from `+table+` row
join dict region on region.kind = $1 and region.id = row.region_id
join dict city on city.kind = $2 and city.id = row.city_id`, dict.Region, dict.City).Scan(&country, &region, &city); err != nil {
			t.Fatal(err)
		}
		if country != "US" || region != "California" || city != "Testville" {
			t.Fatalf("%s geo = %q/%q/%q", table, country, region, city)
		}
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

func TestCustomEventRevenuePersistsExactMicros(t *testing.T) {
	f := newHandlerFixture(t, 32, nil)
	for _, body := range []string{
		`{"site":"abc123","name":"purchase-string","url":"https://example.com/","revenue":{"amount":"12.345678","currency":"USD"}}`,
		`{"site":"abc123","name":"purchase-number","url":"https://example.com/","revenue":{"amount":1000000000000,"currency":"JPY"}}`,
	} {
		if rr := post(t, f.h, body, testUA, "198.51.100.7:5555"); rr.Code != http.StatusAccepted {
			t.Fatalf("code=%d, want 202", rr.Code)
		}
	}
	f.stop(t)

	rows, err := f.pool.Query(context.Background(), `
select revenue_micros, revenue_currency from events order by revenue_micros`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	wantMicros := []int64{12_345_678, 1_000_000_000_000_000_000}
	wantCurrencies := []string{"USD", "JPY"}
	var i int
	for rows.Next() {
		var micros int64
		var currency string
		if err := rows.Scan(&micros, &currency); err != nil {
			t.Fatal(err)
		}
		if micros != wantMicros[i] || currency != wantCurrencies[i] {
			t.Fatalf("row %d revenue=%d/%q, want %d/%q", i, micros, currency, wantMicros[i], wantCurrencies[i])
		}
		i++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if i != len(wantMicros) {
		t.Fatalf("rows=%d, want %d", i, len(wantMicros))
	}
}

func TestInvalidCustomEventRevenueIsDroppedAndCounted(t *testing.T) {
	f := newHandlerFixture(t, 32, nil)
	before := health.IngestRevenueInvalid.Load()
	body := `{"site":"abc123","name":"purchase","url":"https://example.com/","revenue":{"amount":"1.0000001","currency":"usd"}}`
	if rr := post(t, f.h, body, testUA, "198.51.100.7:5555"); rr.Code != http.StatusAccepted {
		t.Fatalf("code=%d, want 202", rr.Code)
	}
	f.stop(t)

	var micros int64
	var currency *string
	if err := f.pool.QueryRow(context.Background(), `select revenue_micros, revenue_currency from events`).Scan(&micros, &currency); err != nil {
		t.Fatal(err)
	}
	if micros != 0 || currency != nil {
		t.Fatalf("revenue=%d/%v, want dropped", micros, currency)
	}
	if got := health.IngestRevenueInvalid.Load(); got != before+1 {
		t.Fatalf("invalid revenue delta=%d, want 1", got-before)
	}
}

func TestPageviewRevenueIsIgnoredWithoutHealthError(t *testing.T) {
	f := newHandlerFixture(t, 32, nil)
	before := health.IngestRevenueInvalid.Load()
	body := `{"site":"abc123","name":"pageview","url":"https://example.com/","revenue":{"amount":"9.99","currency":"USD"}}`
	if rr := post(t, f.h, body, testUA, "198.51.100.7:5555"); rr.Code != http.StatusAccepted {
		t.Fatalf("code=%d, want 202", rr.Code)
	}
	f.stop(t)

	var micros int64
	var currency *string
	if err := f.pool.QueryRow(context.Background(), `select revenue_micros, revenue_currency from events`).Scan(&micros, &currency); err != nil {
		t.Fatal(err)
	}
	if micros != 0 || currency != nil {
		t.Fatalf("pageview revenue=%d/%v, want ignored", micros, currency)
	}
	if got := health.IngestRevenueInvalid.Load(); got != before {
		t.Fatalf("invalid revenue delta=%d, want 0", got-before)
	}
}

func TestDisabledProfilesAreSuppressedAndCounted(t *testing.T) {
	f := newHandlerFixture(t, 32, nil)
	before := health.IngestProfilesSuppressed.Load()
	for _, body := range []string{
		`{"site":"abc123","name":"pageview","url":"https://example.com/profile","profile_id":123}`,
		`{"site":"abc123","name":"pageview","url":"https://example.com/traits","traits":{"nested":{"plan":"pro"}}}`,
	} {
		if rr := post(t, f.h, body, testUA, "198.51.100.7:5555"); rr.Code != http.StatusAccepted {
			t.Fatalf("code=%d, want 202", rr.Code)
		}
	}
	f.stop(t)

	var events, identified, profiles int
	if err := f.pool.QueryRow(context.Background(), `
		select count(*), count(profile_id), (select count(*) from profiles)
		from events`).Scan(&events, &identified, &profiles); err != nil {
		t.Fatal(err)
	}
	if events != 2 || identified != 0 || profiles != 0 {
		t.Fatalf("events=%d identified=%d profiles=%d, want 2/0/0", events, identified, profiles)
	}
	if got := health.IngestProfilesSuppressed.Load(); got != before+2 {
		t.Fatalf("suppressed profile delta=%d, want 2", got-before)
	}
}

func TestEnabledProfilesRejectTraitsOutsideIdentify(t *testing.T) {
	f := newHandlerFixture(t, 32, nil)
	setProfilesEnabled(t, f, true)
	body := `{"site":"abc123","name":"pageview","url":"https://example.com/","profile_id":"customer-1","traits":{"plan":"pro"}}`
	if rr := post(t, f.h, body, testUA, "198.51.100.7:5555"); rr.Code != http.StatusBadRequest {
		t.Fatalf("code=%d, want 400", rr.Code)
	}
}

func TestEnabledIdentifyWithoutTraitsUpsertsAndLinks(t *testing.T) {
	f := newHandlerFixture(t, 32, nil)
	seedProfileSite(t, f.pool)
	setProfilesEnabled(t, f, true)
	body := `{"site":"abc123","name":"identify","url":"https://example.com/","profile_id":"customer-1"}`
	if rr := post(t, f.h, body, testUA, "198.51.100.7:5555"); rr.Code != http.StatusAccepted {
		t.Fatalf("code=%d, want 202", rr.Code)
	}
	f.stop(t)

	var profiles, linked int
	if err := f.pool.QueryRow(context.Background(), `
		select (select count(*) from profiles), count(profile_id) from events`).Scan(&profiles, &linked); err != nil {
		t.Fatal(err)
	}
	if profiles != 1 || linked != 1 {
		t.Fatalf("profiles=%d linked=%d, want 1/1", profiles, linked)
	}
}

func TestTombstonedProfileIsRemovedBeforeQueueing(t *testing.T) {
	f := newHandlerFixture(t, 32, nil)
	seedProfileSite(t, f.pool)
	setProfilesEnabled(t, f, true)
	f.h.ProfileTombstones = allProfilesTombstoned{}
	body := `{"site":"abc123","name":"identify","url":"https://example.com/","profile_id":"customer-1","traits":{"plan":"pro"}}`
	if rr := post(t, f.h, body, testUA, "198.51.100.7:5555"); rr.Code != http.StatusAccepted {
		t.Fatalf("code=%d, want 202", rr.Code)
	}
	f.stop(t)

	var profiles, linked int
	if err := f.pool.QueryRow(context.Background(), `select (select count(*) from profiles), count(profile_id) from events`).Scan(&profiles, &linked); err != nil {
		t.Fatal(err)
	}
	if profiles != 0 || linked != 0 {
		t.Fatalf("profiles=%d linked=%d, want anonymous queued event", profiles, linked)
	}
}

func TestEnabledProfilesUpsertTraitsAndLinkEvents(t *testing.T) {
	f := newHandlerFixture(t, 32, nil)
	seedProfileSite(t, f.pool)
	setProfilesEnabled(t, f, true)
	first := `{"site":"abc123","name":"identify","url":"https://example.com/first","profile_id":"customer-1","traits":{"plan":"free","region":"apac"}}`
	second := `{"site":"abc123","name":"identify","url":"https://example.com/second","profile_id":"customer-1","traits":{"plan":"pro"}}`
	if rr := post(t, f.h, first, testUA, "198.51.100.7:5555"); rr.Code != http.StatusAccepted {
		t.Fatalf("first code=%d, want 202", rr.Code)
	}
	f.clock.now = f.clock.now.Add(time.Minute)
	if rr := post(t, f.h, second, testUA, "198.51.100.7:5555"); rr.Code != http.StatusAccepted {
		t.Fatalf("second code=%d, want 202", rr.Code)
	}
	f.stop(t)

	var profileID int64
	var externalID, traits string
	var firstSeen, lastSeen time.Time
	if err := f.pool.QueryRow(context.Background(), `
		select id, external_id, traits::text, first_seen_at, last_seen_at
		from profiles`).Scan(&profileID, &externalID, &traits, &firstSeen, &lastSeen); err != nil {
		t.Fatal(err)
	}
	if profileID == 0 || externalID != "customer-1" || traits != `{"plan": "pro", "region": "apac"}` {
		t.Fatalf("profile=%d external=%q traits=%s", profileID, externalID, traits)
	}
	if !firstSeen.Equal(fakeNow()) || !lastSeen.Equal(fakeNow().Add(time.Minute)) {
		t.Fatalf("seen range=%s..%s", firstSeen, lastSeen)
	}
	var linked, distinct int
	if err := f.pool.QueryRow(context.Background(), `
		select count(profile_id), count(distinct profile_id) from events`).Scan(&linked, &distinct); err != nil {
		t.Fatal(err)
	}
	if linked != 2 || distinct != 1 {
		t.Fatalf("linked=%d distinct=%d, want 2/1", linked, distinct)
	}
}

func seedProfileSite(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	var orgID int64
	if err := pool.QueryRow(ctx, `insert into orgs(name, slug) values ('Profiles', 'profiles') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		insert into sites(id, org_id, public_id, name, allowed_hosts, timezone, retention_class)
		overriding system value
		values (1, $1, 'profiles0001', 'Profiles', '{example.com}', 'UTC', 30)`, orgID); err != nil {
		t.Fatal(err)
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
	exclusions, err := sites.CompileExclusions(ips, paths, nil)
	if err != nil {
		t.Fatal(err)
	}
	site.Exclusions = exclusions
	fixture.h.Sites = siteRegistry{site: site}
}

func setCountryExclusions(t *testing.T, fixture *handlerFixture, countries []string) {
	t.Helper()
	site, ok := fixture.h.Sites.ByPublicID("abc123")
	if !ok {
		t.Fatal("fixture site missing")
	}
	exclusions, err := sites.CompileExclusions(nil, nil, countries)
	if err != nil {
		t.Fatal(err)
	}
	site.Exclusions = exclusions
	fixture.h.Sites = siteRegistry{site: site}
}

func setProfilesEnabled(t *testing.T, fixture *handlerFixture, enabled bool) {
	t.Helper()
	site, ok := fixture.h.Sites.ByPublicID("abc123")
	if !ok {
		t.Fatal("fixture site missing")
	}
	site.ProfilesEnabled = enabled
	fixture.h.Sites = siteRegistry{site: site}
}

func setHeatmapsEnabled(t *testing.T, fixture *handlerFixture, enabled bool) {
	t.Helper()
	site, ok := fixture.h.Sites.ByPublicID("abc123")
	if !ok {
		t.Fatal("fixture site missing")
	}
	site.HeatmapsEnabled = enabled
	fixture.h.Sites = siteRegistry{site: site}
}

func setRespectPrivacySignals(t *testing.T, fixture *handlerFixture, enabled bool) {
	t.Helper()
	site, ok := fixture.h.Sites.ByPublicID("abc123")
	if !ok {
		t.Fatal("fixture site missing")
	}
	site.RespectPrivacySignals = enabled
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

func writeCityMMDB(t *testing.T) string {
	t.Helper()
	tree, err := mmdbwriter.New(mmdbwriter.Options{
		BuildEpoch:              1_700_000_000,
		DatabaseType:            "GeoLite2-City",
		Description:             map[string]string{"en": "Panel ingest test database"},
		IncludeReservedNetworks: true,
		IPVersion:               4,
		Languages:               []string{"en"},
		RecordSize:              24,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, network, err := net.ParseCIDR("192.0.2.0/24")
	if err != nil {
		t.Fatal(err)
	}
	record := mmdbtype.Map{
		"country": mmdbtype.Map{"iso_code": mmdbtype.String("US")},
		"subdivisions": mmdbtype.Slice{mmdbtype.Map{
			"names": mmdbtype.Map{"en": mmdbtype.String("California")},
		}},
		"city": mmdbtype.Map{"names": mmdbtype.Map{"en": mmdbtype.String("Testville")}},
	}
	if err := tree.Insert(network, record); err != nil {
		t.Fatal(err)
	}
	var data bytes.Buffer
	if _, err := tree.WriteTo(&data); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "GeoLite2-City.mmdb")
	if err := os.WriteFile(path, data.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// A site that has not opted in must not record the tracker's identify calls
// as custom events: they would add one "identify" event per page load.
func TestDisabledProfilesDropIdentifyEvents(t *testing.T) {
	f := newHandlerFixture(t, 32, nil)
	before := health.IngestProfilesSuppressed.Load()
	body := `{"site":"abc123","name":"identify","url":"https://example.com/","profile_id":"customer-1","traits":{"plan":"pro"}}`
	if rr := post(t, f.h, body, testUA, "198.51.100.7:5555"); rr.Code != http.StatusAccepted {
		t.Fatalf("code=%d, want 202", rr.Code)
	}
	f.stop(t)
	var events int
	if err := f.pool.QueryRow(context.Background(), `select count(*) from events`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 0 {
		t.Fatalf("events=%d, want identify dropped", events)
	}
	if got := health.IngestProfilesSuppressed.Load(); got != before+1 {
		t.Fatalf("suppressed delta=%d, want 1", got-before)
	}
}

// Traits merge across identify calls, so the stored object needs the same cap
// as one request or a client could grow it without bound by rotating keys.
func TestEnabledProfilesCapMergedTraits(t *testing.T) {
	f := newHandlerFixture(t, 32, nil)
	seedProfileSite(t, f.pool)
	setProfilesEnabled(t, f, true)
	const siteID = 1 // seedProfileSite
	stored := make([]string, 15)
	for i := range stored {
		stored[i] = fmt.Sprintf(`"old%d":"v"`, i)
	}
	if _, err := f.pool.Exec(context.Background(), `
		insert into profiles(site_id, external_id, traits, first_seen_at, last_seen_at)
		values ($1, 'customer-1', $2::jsonb, $3, $3)`, siteID, "{"+strings.Join(stored, ",")+"}", fakeNow()); err != nil {
		t.Fatal(err)
	}
	for _, prefix := range []string{"a", "b"} {
		traits := make([]string, 15)
		for i := range traits {
			traits[i] = fmt.Sprintf(`"%s%d":"v"`, prefix, i)
		}
		body := `{"site":"abc123","name":"identify","url":"https://example.com/","profile_id":"customer-1","traits":{` + strings.Join(traits, ",") + `}}`
		if rr := post(t, f.h, body, testUA, "198.51.100.7:5555"); rr.Code != http.StatusAccepted {
			t.Fatalf("code=%d, want 202", rr.Code)
		}
	}
	f.stop(t)
	var keys int
	var hasLatest bool
	if err := f.pool.QueryRow(context.Background(), `
		select (select count(*) from jsonb_object_keys(traits)), traits ? 'b14'
		from profiles where external_id='customer-1'`).Scan(&keys, &hasLatest); err != nil {
		t.Fatal(err)
	}
	if keys > 20 || !hasLatest {
		t.Fatalf("traits keys=%d latest=%t, want <= 20 with latest kept", keys, hasLatest)
	}
}
