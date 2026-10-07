package ingest

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"panel/bench/proto/internal/core"
)

func testServer(t *testing.T, w Writer, queue int) (*Server, *Pipeline) {
	t.Helper()
	sites := NewSites([]core.Site{{ID: 1, Host: "a.test", TZ: "UTC", Loc: time.UTC}})
	p := New(Config{QueueSize: queue, BatchSize: 1, Writers: 1, FlushEvery: time.Hour}, w, core.NewCache(core.CacheConfig{}))
	return &Server{Pipe: p, Builder: &Builder{Sites: sites, Salts: core.NewSalts(nil)}, TrustXFF: true}, p
}

func post(h http.Handler, body, ip string) int {
	req := httptest.NewRequest("POST", "/api/event", bytes.NewBufferString(body))
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64; rv:130.0) Gecko/20100101 Firefox/130.0")
	req.Header.Set("X-Forwarded-For", ip)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

func TestHTTPEventAcceptAnd503OnBackpressure(t *testing.T) {
	f := newFake(false) // stalled writer
	s, p := testServer(t, f, 2)
	h := s.Handler()
	body := `{"n":"pageview","u":"https://a.test/pricing?utm_source=nl","d":"a.test","r":"https://www.google.com/"}`
	codes := map[int]int{}
	start := time.Now()
	for i := range 10 {
		codes[post(h, body, "10.0.0."+string(rune('1'+i)))]++
	}
	if time.Since(start) > time.Second {
		t.Fatal("handler blocked under backpressure")
	}
	if codes[202] < 2 || codes[503] == 0 || codes[202]+codes[503] != 10 {
		t.Fatalf("codes %v", codes)
	}
	if c := post(h, `{"n":"pageview","u":"https://unknown.test/","d":"unknown.test"}`, "1.1.1.1"); c != 400 {
		t.Fatalf("unknown site => %d", c)
	}
	if c := post(h, `not json`, "1.1.1.1"); c != 400 {
		t.Fatalf("bad body => %d", c)
	}
	close(f.gate)
	if err := p.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.events) != codes[202] {
		t.Fatalf("written %d, accepted %d", len(f.events), codes[202])
	}
	ev := f.events[0]
	if ev.Path != "/pricing" || ev.Source != "nl" || ev.UTMSource != "nl" || ev.Browser != "" {
		// Builder without Enrich keeps dims empty; utm_source wins over the referrer.
		t.Fatalf("event %+v", ev)
	}
}

func TestHTTPDerivesVisitorAndSessionIDsAndScopesVisitorsBySite(t *testing.T) {
	f := newFake(true)
	sites := NewSites([]core.Site{
		{ID: 1, Host: "a.test", TZ: "UTC", Loc: time.UTC},
		{ID: 2, Host: "b.test", TZ: "UTC", Loc: time.UTC},
	})
	p := New(Config{QueueSize: 4, BatchSize: 1, Writers: 1, FlushEvery: time.Hour}, f, core.NewCache(core.CacheConfig{}))
	salts := core.NewSaltsWithReader(nil, bytes.NewReader(bytes.Repeat([]byte{0x42}, 2*core.SaltLen)))
	s := &Server{Pipe: p, Builder: &Builder{Sites: sites, Salts: salts}, TrustXFF: true}
	h := s.Handler()
	ua := "Mozilla/5.0 (X11; Linux x86_64; rv:130.0) Gecko/20100101 Firefox/130.0"
	bodyA := `{"n":"pageview","u":"https://a.test/","d":"a.test","visitor_id":123,"session_id":456,"flags":32767}`
	bodyB := `{"n":"pageview","u":"https://b.test/","d":"b.test","visitor_id":123,"session_id":456,"flags":32767}`
	if code := post(h, bodyA, "203.0.113.7"); code != http.StatusAccepted {
		t.Fatalf("site a status = %d", code)
	}
	if code := post(h, bodyB, "203.0.113.7"); code != http.StatusAccepted {
		t.Fatalf("site b status = %d", code)
	}
	if err := p.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.events) != 2 {
		t.Fatalf("stored %d events, want 2", len(f.events))
	}
	bySite := map[int32]*core.Event{}
	for _, ev := range f.events {
		bySite[ev.SiteID] = ev
		if ev.VisitorID == 123 || ev.SessionID == 456 || ev.SessionID == 0 {
			t.Fatalf("client-controlled identifiers reached event: %+v", ev)
		}
	}
	if bySite[1] == nil || bySite[2] == nil {
		t.Fatalf("events by site = %v", bySite)
	}
	wantSalt := bytes.Repeat([]byte{0x42}, core.SaltLen)
	if got, want := bySite[1].VisitorID, core.VisitorID(wantSalt, 1, "203.0.113.7", ua); got != want {
		t.Fatalf("site 1 visitor = %d, want server-derived %d", got, want)
	}
	if got, want := bySite[2].VisitorID, core.VisitorID(wantSalt, 2, "203.0.113.7", ua); got != want {
		t.Fatalf("site 2 visitor = %d, want server-derived %d", got, want)
	}
	if bySite[1].VisitorID == bySite[2].VisitorID {
		t.Fatal("same ip+ua correlated across two sites")
	}
}
