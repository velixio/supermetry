package sentryingest

import (
	"net/http"
	"strings"
	"supermetry/internal/appingest"
	"supermetry/internal/sites"
	"testing"
	"time"
)

func TestFilterEvent(t *testing.T) {
	const browserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36"
	frames := func(file string) string {
		return `{"exception":{"values":[{"type":"Error","value":"x","stacktrace":{"frames":[{"filename":"` + file + `"},{"filename":"app.js"}]}}]}}`
	}
	cases := []struct {
		name, payload, ua, want string
	}{
		{"clean", `{"message":"hi","request":{"url":"https://app.example.test/a"}}`, browserUA, ""},
		{"localhost", `{"request":{"url":"http://localhost:3000/a"}}`, browserUA, "localhost"},
		{"loopback", `{"request":{"url":"http://127.0.0.1/a"}}`, browserUA, "localhost"},
		{"ipv6 loopback", `{"request":{"url":"http://[::1]:8080/a"}}`, browserUA, "localhost"},
		{"subdomain of localhost", `{"request":{"url":"http://app.localhost/a"}}`, browserUA, "localhost"},
		{"not localhost", `{"request":{"url":"https://notlocalhost.example.test/"}}`, browserUA, ""},
		{"path mentions localhost", `{"request":{"url":"https://app.example.test/localhost"}}`, browserUA, ""},
		{"chrome extension", frames("chrome-extension://abc/content.js"), browserUA, "browser_extension"},
		{"moz extension", frames("moz-extension://abc/content.js"), browserUA, "browser_extension"},
		{"safari extension", frames("safari-extension://abc/content.js"), browserUA, "browser_extension"},
		{"abs_path extension", `{"exception":{"values":[{"stacktrace":{"frames":[{"abs_path":"moz-extension://x/a.js"}]}}]}}`, browserUA, "browser_extension"},
		{"crawler http ua", `{"message":"hi"}`, "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)", "crawler"},
		{"crawler event ua", `{"request":{"headers":{"User-Agent":"Mozilla/5.0 (compatible; bingbot/2.0)"}}}`, browserUA, "crawler"},
		{"crawler event ua pairs", `{"request":{"headers":[["User-Agent","Mozilla/5.0 (compatible; bingbot/2.0)"]]}}`, browserUA, "crawler"},
		{"no ua is fine", `{"message":"hi"}`, "", ""},
		{"sdk ua is fine", `{"message":"hi"}`, "sentry.python/2.1", ""},
		{"malformed keeps event", `not json`, browserUA, ""},
		{"wrong types keep event", `{"request":5,"exception":"x"}`, browserUA, ""},
	}
	for _, c := range cases {
		if got := filterEvent([]byte(c.payload), c.ua, filterSet{true, true, true}); got != c.want {
			t.Errorf("%s: filterEvent = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestLocalhostFilterIsOffUnlessEnabled(t *testing.T) {
	payload := []byte(`{"request":{"url":"http://localhost:3000/a"}}`)
	if got := filterEvent(payload, "", filterSet{true, true, false}); got != "" {
		t.Errorf("localhost filtered while off: %q", got)
	}
	ext := []byte(`{"exception":{"values":[{"stacktrace":{"frames":[{"filename":"chrome-extension://a/b.js"}]}}]}}`)
	if got := filterEvent(ext, "", filterSet{true, true, false}); got != filterExtension {
		t.Errorf("extension filter must stay on: %q", got)
	}
}

func TestFilteredEventIsDroppedCountedAndAnswered200(t *testing.T) {
	e := newEnv(t)
	e.h.WithLocalhostFilter(true)
	env := `{"event_id":"11111111111111111111111111111111"}` + "\n" + `{"type":"event"}` + "\n" +
		`{"message":"hi","request":{"url":"http://localhost:3000/x"}}` + "\n"
	w := e.do(post("/api/42/envelope/", fdQuery, env, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body)
	}
	if e.queued() != 0 {
		t.Error("filtered event was queued")
	}
	if got := e.h.Stats().Snapshot()["filtered"]; got != 1 {
		t.Errorf("filtered = %d", got)
	}
	if len(e.bodies.items) != 0 {
		t.Error("filtered event produced a body")
	}
}

// countingRegistry counts registry lookups, to show that filter settings ride
// on the site snapshot the DSN lookup returns and cost no further lookups.
type countingRegistry struct {
	fakeRegistry
	dsn, site, all int
}

func (c *countingRegistry) ByDSNKey(k string) (sites.Site, sites.DSNKey, bool) {
	c.dsn++
	return c.fakeRegistry.ByDSNKey(k)
}
func (c *countingRegistry) Site(id int32) (sites.Site, bool) {
	c.site++
	return c.fakeRegistry.Site(id)
}
func (c *countingRegistry) All() []sites.Site { c.all++; return nil }

func TestPerSiteFilterToggles(t *testing.T) {
	const ua = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36"
	const bot = "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)"
	ext := `{"message":"hi","exception":{"values":[{"stacktrace":{"frames":[{"filename":"chrome-extension://a/b.js"}]}}]}}`
	local := `{"message":"hi","request":{"url":"http://localhost:3000/x"}}`
	plain := `{"message":"hi"}`
	yes, no := true, false
	cases := []struct {
		name     string
		cfg      sites.AppConfig
		server   bool // server-wide localhost default
		payload  string
		ua       string
		filtered bool
	}{
		{"extensions default on", sites.AppConfig{}, false, ext, ua, true},
		{"extensions off", sites.AppConfig{FilterExtensions: &no}, false, ext, ua, false},
		{"extensions explicit on", sites.AppConfig{FilterExtensions: &yes}, false, ext, ua, true},
		{"crawlers default on", sites.AppConfig{}, false, plain, bot, true},
		{"crawlers off", sites.AppConfig{FilterCrawlers: &no}, false, plain, bot, false},
		{"localhost follows server off", sites.AppConfig{}, false, local, ua, false},
		{"localhost follows server on", sites.AppConfig{}, true, local, ua, true},
		{"localhost site on beats server off", sites.AppConfig{FilterLocalhost: &yes}, false, local, ua, true},
		{"localhost site off beats server on", sites.AppConfig{FilterLocalhost: &no}, true, local, ua, false},
		{"one toggle leaves the others", sites.AppConfig{FilterCrawlers: &no}, false, ext, ua, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := appSite(fdSite, sites.DSNKey{PublicKey: fdKey})
			c.cfg.DSNKeys, c.cfg.MaxReportAgeDays = s.App.DSNKeys, 7
			s.App = &c.cfg
			reg := &countingRegistry{fakeRegistry: newRegistry(s)}
			e := &env{intake: appingest.NewIntake(appingest.Config{}), norm: &stubNormaliser{}, bodies: &bodySink{}}
			now := fdNow
			e.h = NewHandler(reg, e.intake, e.bodies, fixedGeo("DE"), nil, func() time.Time { return now })
			e.h.WithIPLimit(-1).WithLocalhostFilter(c.server)
			body := `{"event_id":"11111111111111111111111111111111"}` + "\n" + `{"type":"event"}` + "\n" + c.payload + "\n"
			hdr := map[string]string{"User-Agent": c.ua}
			if w := e.do(post(fdPath, fdQuery, body, hdr)); w.Code != http.StatusOK {
				t.Fatalf("status %d %s", w.Code, w.Body)
			}
			if got := e.h.Stats().Snapshot()["filtered"] == 1; got != c.filtered {
				t.Errorf("filtered = %v, want %v", got, c.filtered)
			}
			// Lookups are per request, never per event: three events cost what one did.
			perRequest := reg.dsn
			three := `{"event_id":"11111111111111111111111111111111"}` + "\n" +
				strings.Repeat(`{"type":"event"}`+"\n"+c.payload+"\n", 3)
			e.do(post(fdPath, fdQuery, three, hdr))
			if reg.dsn != 2*perRequest || reg.site != 0 || reg.all != 0 {
				t.Errorf("lookups: dsn %d after two requests (one request: %d), site %d, all %d", reg.dsn, perRequest, reg.site, reg.all)
			}
		})
	}
}
