package sentryapi

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestRateLimitPerToken(t *testing.T) {
	e := newEnv(t, func(d *Deps) { d.IPPerMin = -1; d.TokenPerMin = 1 }) // burst 20, then 1 per minute
	a := e.svc(t, e.owner, []string{Scope}, nil)
	b := e.svc(t, e.owner, []string{Scope}, nil)
	for i := 0; i < 20; i++ {
		if s, _ := e.json(t, "GET", "/api/0/", a, nil); s != 200 {
			t.Fatalf("request %d = %d", i, s)
		}
	}
	req, _ := http.NewRequest("GET", e.srv.URL+"/api/0/", nil)
	req.Header.Set("Authorization", "Bearer "+a)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("over limit = %d, want 429", resp.StatusCode)
	}
	if n, err := strconv.Atoi(resp.Header.Get("Retry-After")); err != nil || n < 1 {
		t.Fatalf("Retry-After = %q", resp.Header.Get("Retry-After"))
	}
	if s, _ := e.json(t, "GET", "/api/0/", b, nil); s != 200 {
		t.Fatalf("a second token has its own bucket, got %d", s)
	}
}

func TestRateLimitPerIP(t *testing.T) {
	e := newEnv(t, func(d *Deps) { d.IPPerMin = 1; d.TokenPerMin = -1 })
	// Unauthenticated traffic is limited too, so credential guessing is bounded.
	for i := 0; i < 20; i++ {
		if s, _ := e.json(t, "GET", "/api/0/", "", nil); s != 401 {
			t.Fatalf("request %d = %d", i, s)
		}
	}
	if s, _ := e.json(t, "GET", "/api/0/", "", nil); s != http.StatusTooManyRequests {
		t.Fatalf("over limit = %d, want 429", s)
	}
}

func TestRateLimitKeysAreNotRawAddresses(t *testing.T) {
	l := newLimits(1, 1, nil)
	r := httptest.NewRequest("GET", "/api/0/", nil)
	r.RemoteAddr = "203.0.113.9:4000"
	r.Header.Set("Authorization", "Bearer secret-token")
	if _, limited := l.check(r); limited {
		t.Fatal("first request limited")
	}
	if k := l.key("ip", "203.0.113.9"); len(k) != 24 || k == "203.0.113.9" {
		t.Fatalf("key = %q", k)
	}
	if l.key("ip", "x") == newLimits(1, 1, nil).key("ip", "x") {
		t.Fatal("salt must differ per limiter")
	}
}

func TestRateLimitDisabled(t *testing.T) {
	e := newEnv(t, func(d *Deps) { d.IPPerMin = -1; d.TokenPerMin = -1 })
	for i := 0; i < 60; i++ {
		if s, _ := e.json(t, "GET", "/api/0/", "", nil); s != 401 {
			t.Fatalf("request %d = %d", i, s)
		}
	}
}
