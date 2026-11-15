package appingest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"supermetry/internal/db"
	"supermetry/internal/dropstats"
	"supermetry/internal/platform/appkeys"
	"supermetry/internal/sites"
	"supermetry/internal/testpg"
)

type fakeRegistry struct{ byPublic map[string]sites.Site }

func (f fakeRegistry) ByPublicID(id string) (sites.Site, bool) { s, ok := f.byPublic[id]; return s, ok }
func (f fakeRegistry) Site(id int32) (sites.Site, bool) {
	for _, s := range f.byPublic {
		if s.ID == id {
			return s, true
		}
	}
	return sites.Site{}, false
}
func (f fakeRegistry) All() []sites.Site { return nil }

type handlerEnv struct {
	h     *Handler
	in    *Intake
	key   string
	site  sites.Site
	clock *time.Time
}

func newHandlerEnv(t *testing.T, cfg Config, mutate func(*sites.Site)) *handlerEnv {
	t.Helper()
	plain, _, hash, err := appkeys.New()
	if err != nil {
		t.Fatal(err)
	}
	site := appSite(func(s *sites.Site) {
		s.App.Keys = []sites.AppKey{{Hash: hash}}
		if mutate != nil {
			mutate(s)
		}
	})
	web := sites.Site{ID: 8, PublicID: "websit000001", Kind: sites.KindWeb, TZ: time.UTC}
	now := testNow
	in := NewIntake(cfg)
	reg := fakeRegistry{byPublic: map[string]sites.Site{site.PublicID: site, web.PublicID: web}}
	h := NewHandler(reg, in, nil, func() time.Time { return now })
	return &handlerEnv{h: h, in: in, key: plain, site: site, clock: &now}
}

func (e *handlerEnv) post(body, key, ip string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/public/v1/app-events", strings.NewReader(body))
	if key != "" {
		req.Header.Set("X-Supermetry-Key", key)
	}
	req.RemoteAddr = ip + ":4000"
	rr := httptest.NewRecorder()
	e.h.ServeHTTP(rr, req)
	return rr
}

func oneEvent(id string) string {
	return fmt.Sprintf(`{"id":%q,"kind":"error","ts":%d}`, id, testNow.UnixMilli())
}

func batch(site string, events ...string) string {
	return `{"site":"` + site + `","events":[` + strings.Join(events, ",") + `]}`
}

type reply struct {
	Accepted int `json:"accepted"`
	Dropped  []struct {
		Index  int    `json:"index"`
		Reason string `json:"reason"`
	} `json:"dropped"`
	Warnings []struct {
		Index  int    `json:"index"`
		Reason string `json:"reason"`
		Tag    string `json:"tag"`
	} `json:"warnings"`
}

func decodeReply(t *testing.T, rr *httptest.ResponseRecorder) reply {
	t.Helper()
	var r reply
	if err := json.Unmarshal(rr.Body.Bytes(), &r); err != nil {
		t.Fatalf("reply %q: %v", rr.Body.String(), err)
	}
	return r
}

func TestHandlerAcceptsAValidBatch(t *testing.T) {
	e := newHandlerEnv(t, Config{}, nil)
	rr := e.post(batch(e.site.PublicID, oneEvent("a"), oneEvent("b")), e.key, "198.51.100.1")
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status %d: %s", rr.Code, rr.Body)
	}
	r := decodeReply(t, rr)
	if r.Accepted != 2 || len(r.Dropped) != 0 || len(r.Warnings) != 0 {
		t.Fatalf("reply = %+v", r)
	}
	if !strings.Contains(rr.Body.String(), `"dropped":[]`) || !strings.Contains(rr.Body.String(), `"warnings":[]`) {
		t.Fatalf("empty lists must be arrays: %s", rr.Body)
	}
	if rr.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal("POST response needs the CORS origin header")
	}
	if got := len(drain(e.in)); got != 2 {
		t.Fatalf("queued %d", got)
	}
}

func TestHandlerRequestLevelChecks(t *testing.T) {
	e := newHandlerEnv(t, Config{}, nil)
	good := batch(e.site.PublicID, oneEvent("a"))
	twentyOne := make([]string, 21)
	for i := range twentyOne {
		twentyOne[i] = oneEvent(fmt.Sprint("x", i))
	}
	// A well-formed key that matches no stored key.
	other, _, _, _ := appkeys.New()
	cases := []struct {
		name   string
		method string
		body   string
		key    string
		want   int
	}{
		{"get is a bad method", http.MethodGet, good, e.key, 400},
		{"not json", http.MethodPost, `{nope`, e.key, 400},
		{"trailing data", http.MethodPost, good + good, e.key, 400},
		{"no events field", http.MethodPost, `{"site":"` + e.site.PublicID + `"}`, e.key, 400},
		{"empty events", http.MethodPost, batch(e.site.PublicID), e.key, 400},
		{"too many events", http.MethodPost, batch(e.site.PublicID, twentyOne...), e.key, 400},
		{"site not a string", http.MethodPost, `{"site":5,"events":[{}]}`, e.key, 400},
		{"missing key", http.MethodPost, good, "", 401},
		{"malformed key", http.MethodPost, good, "smk_short", 401},
		{"unknown key", http.MethodPost, good, other, 401},
		{"unknown site", http.MethodPost, batch("nosuchsite01", oneEvent("a")), e.key, 401},
		{"web site", http.MethodPost, batch("websit000001", oneEvent("a")), e.key, 401},
		{"ok", http.MethodPost, good, e.key, 202},
	}
	var bodies []string
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(c.method, "/api/public/v1/app-events", strings.NewReader(c.body))
			if c.key != "" {
				req.Header.Set("X-Supermetry-Key", c.key)
			}
			req.RemoteAddr = "198.51.100.9:1"
			rr := httptest.NewRecorder()
			e.h.ServeHTTP(rr, req)
			if rr.Code != c.want {
				t.Fatalf("status %d, want %d (%s)", rr.Code, c.want, rr.Body)
			}
			if rr.Code == 401 {
				bodies = append(bodies, rr.Body.String())
			}
		})
	}
	for _, b := range bodies[1:] {
		if b != bodies[0] {
			t.Fatalf("every 401 must look the same: %q vs %q", b, bodies[0])
		}
	}
}

func TestHandlerRejectsAnExpiredKey(t *testing.T) {
	plain, _, hash, _ := appkeys.New()
	e := newHandlerEnv(t, Config{}, func(s *sites.Site) {
		s.App.Keys = []sites.AppKey{{Hash: hash, ExpiresAt: testNow.Add(-time.Second)}}
	})
	if rr := e.post(batch(e.site.PublicID, oneEvent("a")), plain, "198.51.100.1"); rr.Code != 401 {
		t.Fatalf("status %d", rr.Code)
	}
}

func TestHandlerBodyLimit(t *testing.T) {
	e := newHandlerEnv(t, Config{}, nil)
	pad := strings.Repeat("p", 700)
	events := make([]string, 20)
	for i := range events {
		events[i] = fmt.Sprintf(`{"id":"load-%d","kind":"error","frames":%q,"ts":%d}`, i, "a.py:1 "+pad, testNow.UnixMilli())
	}
	build := func(size int) string {
		base := batch(e.site.PublicID, events...)
		fill := size - len(base) - len(`,"pad":""`)
		return base[:len(base)-1] + `,"pad":"` + strings.Repeat("x", fill) + `"}`
	}
	exact := build(16 << 10)
	if len(exact) != 16<<10 {
		t.Fatalf("test body is %d bytes", len(exact))
	}
	rr := e.post(exact, e.key, "198.51.100.2")
	if rr.Code != 202 || decodeReply(t, rr).Accepted != 20 {
		t.Fatalf("a 16 KB batch of 20 events: %d %s", rr.Code, rr.Body)
	}
	if rr := e.post(build(16<<10+1), e.key, "198.51.100.2"); rr.Code != 400 {
		t.Fatalf("16 KB + 1: %d", rr.Code)
	}
}

func TestHandlerRateLimitsPerClientIP(t *testing.T) {
	e := newHandlerEnv(t, Config{}, nil)
	for i := range 60 {
		if rr := e.post(batch(e.site.PublicID, oneEvent(fmt.Sprint("e", i))), e.key, "198.51.100.3"); rr.Code != 202 {
			t.Fatalf("request %d: %d", i, rr.Code)
		}
	}
	rr := e.post(batch(e.site.PublicID, oneEvent("over")), e.key, "198.51.100.3")
	if rr.Code != 429 || rr.Header().Get("Retry-After") == "" {
		t.Fatalf("61st request: %d", rr.Code)
	}
	if rr := e.post(batch(e.site.PublicID, oneEvent("elsewhere")), e.key, "198.51.100.4"); rr.Code != 202 {
		t.Fatalf("another address: %d", rr.Code)
	}
	*e.clock = e.clock.Add(2 * time.Second)
	if rr := e.post(batch(e.site.PublicID, oneEvent("later")), e.key, "198.51.100.3"); rr.Code != 202 {
		t.Fatalf("after refill: %d", rr.Code)
	}
}

func TestHandlerCORSPreflight(t *testing.T) {
	e := newHandlerEnv(t, Config{}, nil)
	req := httptest.NewRequest(http.MethodOptions, "/api/public/v1/app-events", nil)
	req.Header.Set("Origin", "https://anywhere.example")
	rr := httptest.NewRecorder()
	e.h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status %d", rr.Code)
	}
	h := rr.Header()
	if h.Get("Access-Control-Allow-Origin") != "*" || h.Get("Access-Control-Allow-Methods") != "POST, OPTIONS" ||
		h.Get("Access-Control-Allow-Headers") != "Content-Type, X-Supermetry-Key" {
		t.Fatalf("headers = %v", h)
	}
}

func TestHandlerReplyListsDropsWarningsAndFeedsDropStats(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into orgs(name,slug) values('Org','org');
insert into sites(id,org_id,public_id,name,timezone,retention_class,kind) overriding system value
values(7,1,'appsite00001','App','UTC',30,'app')`); err != nil {
		t.Fatal(err)
	}
	recorder := dropstats.New(pool, dropstats.Options{MaxSites: 10})
	e := newHandlerEnv(t, Config{DropStats: recorder, KeyBudgetPerMinute: 2}, func(s *sites.Site) {
		s.App.TagAllowlist = []string{"stage"}
	})
	tooOld := fmt.Sprintf(`{"id":"old","kind":"error","ts":%d}`, testNow.Add(-8*24*time.Hour).UnixMilli())
	body := batch(e.site.PublicID,
		`{"id":"ok","kind":"error","tags":{"stage":"load","secret":"x"}}`, // 0 accepted, one tag stripped
		`{"id":"bad","kind":"explode"}`,                                   // 1 invalid
		tooOld,                                                            // 2 too_old
		`{"id":"ok","kind":"error"}`,                                      // 3 duplicate
		`"not an object"`,                                                 // 4 invalid
		oneEvent("e5"),                                                    // 5 accepted (2nd token)
		oneEvent("e6"),                                                    // 6 rate_limited
	)
	rr := e.post(body, e.key, "198.51.100.5")
	if rr.Code != 202 {
		t.Fatalf("status %d: %s", rr.Code, rr.Body)
	}
	r := decodeReply(t, rr)
	want := map[int]string{1: "invalid", 2: "too_old", 3: "duplicate", 4: "invalid", 6: "rate_limited"}
	if r.Accepted != 2 || len(r.Dropped) != len(want) {
		t.Fatalf("reply = %+v", r)
	}
	for _, d := range r.Dropped {
		if want[d.Index] != d.Reason {
			t.Errorf("index %d: %q, want %q", d.Index, d.Reason, want[d.Index])
		}
	}
	if len(r.Warnings) != 1 || r.Warnings[0].Index != 0 || r.Warnings[0].Reason != "tag_not_allowed" || r.Warnings[0].Tag != "secret" {
		t.Fatalf("warnings = %+v", r.Warnings)
	}
	if err := recorder.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err := recorder.Read(ctx, []dropstats.SiteDay{{SiteID: 7, Today: testNow}})
	if err != nil {
		t.Fatal(err)
	}
	got := rows[0].Today
	if got[dropstats.AppInvalid] != 2 || got[dropstats.TooOld] != 1 || got[dropstats.AppDuplicate] != 1 || got[dropstats.AppRateLimited] != 1 {
		t.Fatalf("dropstats today = invalid %d, too_old %d, duplicate %d, rate_limited %d",
			got[dropstats.AppInvalid], got[dropstats.TooOld], got[dropstats.AppDuplicate], got[dropstats.AppRateLimited])
	}
}
