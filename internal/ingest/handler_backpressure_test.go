package ingest_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"panel/internal/dict"
	"panel/internal/enrich"
	"panel/internal/health"
	"panel/internal/ingest"
	"panel/internal/session"
	"panel/internal/writer"
)

type preflightSessionSpy struct {
	session.Store
	touches     int
	beforeTouch func()
}

func (s *preflightSessionSpy) Touch(hit session.Hit) session.Result {
	s.touches++
	if s.beforeTouch != nil {
		s.beforeTouch()
	}
	return s.Store.Touch(hit)
}

func TestFullEventQueuePreservesIntakeOrdering(t *testing.T) {
	const heatmap = `{"site":"abc123","name":"heatmap","url":"https://example.com/","doc_h":2000,"vw":1000,"clicks":[[100,100]]}`
	for _, tt := range []struct {
		name, body, ua, header, remote string
		setup                          func(*testing.T, *handlerFixture)
		want                           int
		dropped                        int64
		heatmapAccepted                uint64
	}{
		{name: "ordinary valid event", body: okBody, want: 503, dropped: 1},
		{name: "bot overload precedes bot suppression", body: okBody, ua: "Googlebot/2.1", want: 503, dropped: 1},
		{name: "stale ping overload precedes stale suppression", body: `{"site":"abc123","name":"engagement","url":"https://example.com/","engagement_ms":9000}`, want: 503, dropped: 1},
		{name: "stopped empty writer", body: okBody, want: 503, dropped: 1, setup: func(t *testing.T, f *handlerFixture) { f.h.Writer = f.writer; f.stop(t) }},
		{name: "invalid JSON", body: `{`, want: 400},
		{name: "invalid host", body: `{"site":"abc123","name":"pageview","url":"https://other.test/"}`, want: 400},
		{name: "invalid remote address", body: okBody, remote: "invalid", want: 400},
		{name: "DNT", body: okBody, header: "DNT", want: 202},
		{name: "GPC", body: okBody, header: "Sec-GPC", want: 202},
		{name: "suppressed identity", body: `{"site":"abc123","name":"identify","url":"https://example.com/","profile_id":"synthetic-person"}`, want: 202},
		{name: "excluded IP", body: okBody, want: 202, setup: func(t *testing.T, f *handlerFixture) { setExclusions(t, f, []string{"198.51.100.7"}, nil) }},
		{name: "excluded path", body: okBody, want: 202, setup: func(t *testing.T, f *handlerFixture) { setExclusions(t, f, nil, []string{"/"}) }},
		{name: "excluded country", body: okBody, remote: "192.0.2.42:5555", want: 202, setup: func(t *testing.T, f *handlerFixture) {
			enricher, err := enrich.NewWithGeo(enrich.GeoPaths{City: writeCityMMDB(t)})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = enricher.Close() })
			f.h.Enricher = enricher
			setCountryExclusions(t, f, []string{"US"})
		}},
		{name: "rate limit", body: okBody, want: 429, setup: func(_ *testing.T, f *handlerFixture) { f.h.Limiter = denyLimiter{} }},
		{name: "disabled heatmap", body: heatmap, want: 202},
		{name: "independent heatmap capacity", body: heatmap, want: 202, heatmapAccepted: 1, setup: func(t *testing.T, f *handlerFixture) { primeIndependentHeatmap(t, f) }},
		{name: "heatmap own queue full", body: heatmap, want: 503, dropped: 1, setup: func(t *testing.T, f *handlerFixture) {
			primeIndependentHeatmap(t, f)
			if !f.h.Writer.EnqueueHeatmap(writer.HeatmapEvent{}) {
				t.Fatal("fill heatmap queue")
			}
		}},
		{name: "heatmap privacy signal", body: heatmap, header: "Sec-GPC", want: 202, setup: func(t *testing.T, f *handlerFixture) {
			setHeatmapsEnabled(t, f, true)
			setRespectPrivacySignals(t, f, false)
		}},
		{name: "heatmap rate limit", body: heatmap, want: 429, setup: func(t *testing.T, f *handlerFixture) { setHeatmapsEnabled(t, f, true); f.h.Limiter = denyLimiter{} }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newHandlerFixture(t, 32, nil)
			f.h.Writer = writer.New(nil, f.h.Sessions, 1, time.Hour, time.Hour, nil)
			if !f.h.Writer.Enqueue(writer.Event{}) {
				t.Fatal("fill event queue")
			}
			if tt.setup != nil {
				tt.setup(t, f)
			}
			spy := &preflightSessionSpy{Store: f.h.Sessions}
			f.h.Sessions = spy
			var dictBefore, dictAfter int
			if err := f.pool.QueryRow(context.Background(), `select count(*) from dict`).Scan(&dictBefore); err != nil {
				t.Fatal(err)
			}
			writerDrops, ingestDrops, accepted := writer.Dropped.Load(), health.IngestDropped.Load(), health.IngestAccepted.Load()
			bots, stale := ingest.Bots[enrich.BotUserAgent].Load(), health.IngestStalePings.Load()
			req := httptest.NewRequest(http.MethodPost, "/api/event", strings.NewReader(tt.body))
			ua := tt.ua
			if ua == "" {
				ua = testUA
			}
			req.Header.Set("User-Agent", ua)
			req.RemoteAddr = tt.remote
			if req.RemoteAddr == "" {
				req.RemoteAddr = "198.51.100.7:5555"
			}
			if tt.header != "" {
				req.Header.Set(tt.header, "1")
			}
			response := httptest.NewRecorder()
			f.h.ServeHTTP(response, req)
			if response.Code != tt.want {
				t.Fatalf("status=%d want=%d", response.Code, tt.want)
			}
			if tt.want == 503 && response.Header().Get("Retry-After") != "1" {
				t.Fatal("missing retry header")
			}
			if writer.Dropped.Load()-writerDrops != tt.dropped || health.IngestDropped.Load()-ingestDrops != uint64(tt.dropped) || health.IngestAccepted.Load()-accepted != tt.heatmapAccepted {
				t.Fatal("request accounting differs")
			}
			if spy.touches != 0 || ingest.Bots[enrich.BotUserAgent].Load() != bots || health.IngestStalePings.Load() != stale {
				t.Fatal("known-full request reached event enrichment/session handling")
			}
			if err := f.pool.QueryRow(context.Background(), `select count(*) from dict`).Scan(&dictAfter); err != nil {
				t.Fatal(err)
			}
			if dictBefore != dictAfter {
				t.Fatalf("dictionary mutated: before=%d after=%d", dictBefore, dictAfter)
			}
			snapshot := spy.Store.(session.RichRealtimeStore).RealtimeSnapshot(1, f.clock.Now())
			if snapshot.ActiveVisitors != 0 || snapshot.Pageviews != 0 || len(snapshot.RecentActivity) != 0 {
				t.Fatalf("rejected event changed realtime: %+v", snapshot)
			}
		})
	}
}

func TestHandlerReservationPreventsSessionMutationRace(t *testing.T) {
	f := newHandlerFixture(t, 32, nil)
	f.stop(t)
	w := writer.New(f.pool, f.h.Sessions, 1, time.Hour, time.Hour, noneClosed{})
	f.h.Writer = w
	original := f.h.Sessions
	var competingAccepted bool
	spy := &preflightSessionSpy{Store: original, beforeTouch: func() {
		competingAccepted = w.Enqueue(writer.Event{})
	}}
	f.h.Sessions = spy
	runCtx, cancel := context.WithCancel(context.Background())
	stopped := make(chan error, 1)
	go func() { stopped <- w.Run(runCtx) }()
	drops, healthDrops, accepted := writer.Dropped.Load(), health.IngestDropped.Load(), health.IngestAccepted.Load()
	response := post(t, f.h, okBody, testUA, "198.51.100.7:5555")
	if response.Code != http.StatusAccepted || response.Header().Get("Retry-After") != "" || spy.touches != 1 {
		t.Fatalf("status=%d retry=%q touches=%d", response.Code, response.Header().Get("Retry-After"), spy.touches)
	}
	if competingAccepted {
		t.Fatal("competing producer took reserved capacity")
	}
	if writer.Dropped.Load() != drops+1 || health.IngestDropped.Load() != healthDrops || health.IngestAccepted.Load() != accepted+1 {
		t.Fatal("reservation accounting differs")
	}
	cancel()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(12 * time.Second):
		t.Fatal("reserved writer did not stop")
	}
	var sessions, events int
	if err := f.pool.QueryRow(context.Background(), `select count(*) from sessions`).Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(context.Background(), `select count(*) from events`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if sessions != 1 || events != 1 {
		t.Fatalf("accepted event persisted sessions=%d events=%d", sessions, events)
	}
}

func primeIndependentHeatmap(t *testing.T, f *handlerFixture) {
	t.Helper()
	setHeatmapsEnabled(t, f, true)
	// Heatmaps legitimately intern their sanitized path independently of event
	// capacity. Only these fixtures are warmed; rejected event paths stay cold.
	path := f.h.PathPolicy.SanitizePath("https://example.com/").Path
	if _, err := f.h.Dict.IDs(context.Background(), dict.Path, []string{path}); err != nil {
		t.Fatal(err)
	}
}
