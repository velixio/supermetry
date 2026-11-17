package sentryingest

import (
	"net/http"
	"testing"
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
		if got := filterEvent([]byte(c.payload), c.ua, true); got != c.want {
			t.Errorf("%s: filterEvent = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestLocalhostFilterIsOffUnlessEnabled(t *testing.T) {
	payload := []byte(`{"request":{"url":"http://localhost:3000/a"}}`)
	if got := filterEvent(payload, "", false); got != "" {
		t.Errorf("localhost filtered while off: %q", got)
	}
	ext := []byte(`{"exception":{"values":[{"stacktrace":{"frames":[{"filename":"chrome-extension://a/b.js"}]}}]}}`)
	if got := filterEvent(ext, "", false); got != filterExtension {
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
