//go:build !embedui

package web

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func stubNext() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
}

func TestDevUIRedirectsPathAndQuery(t *testing.T) {
	h := devUI(stubNext(), "http://localhost:5173")
	for _, method := range []string{"GET", "HEAD"} {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(method, "/app/acme/blog?range=7d&x=a%20b", nil))
		if rr.Code != http.StatusFound {
			t.Fatalf("%s: status %d", method, rr.Code)
		}
		if got, want := rr.Header().Get("Location"), "http://localhost:5173/app/acme/blog?range=7d&x=a%20b"; got != want {
			t.Errorf("%s: Location %q, want %q", method, got, want)
		}
		if rr.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: Cache-Control %q", method, rr.Header().Get("Cache-Control"))
		}
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if rr.Header().Get("Location") != "http://localhost:5173/" {
		t.Errorf("root Location %q", rr.Header().Get("Location"))
	}
}

func TestDevUILeavesAPIAndOtherMethodsAlone(t *testing.T) {
	h := devUI(stubNext(), "http://localhost:5173")
	for _, tc := range [][2]string{{"GET", "/api"}, {"GET", "/api/private/me"}, {"POST", "/app"}} {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(tc[0], tc[1], nil))
		if rr.Code != http.StatusTeapot {
			t.Errorf("%s %s: status %d, want pass-through", tc[0], tc[1], rr.Code)
		}
	}
}

// rawRequest parses a request exactly as the server would from the wire.
func rawRequest(t *testing.T, line string, header string) *http.Request {
	t.Helper()
	r, err := http.ReadRequest(bufio.NewReader(strings.NewReader(line + " HTTP/1.1\r\nHost: localhost:8787\r\n" + header + "\r\n")))
	if err != nil {
		t.Fatalf("%q: %v", line, err)
	}
	return r
}

func TestDevUIHostileTargetsKeepTheConfiguredHost(t *testing.T) {
	h := devUI(stubNext(), "http://localhost:5173/ignored/path")
	targets := []string{
		"/", "//evil.example", "//evil.example/x", "/%5Cevil.example", "/%2f%2fevil.example", "///evil.example",
		"/..//evil.example", "/%0d%0aSet-Cookie:x=1", "/a?b=%0d%0aX:1", "/@evil.example", "/a?next=//evil.example",
		"http://evil.example/x", "http://evil.example//evil2.example", "/" + strings.Repeat("a", 8192),
		"/a%2fb", "/%2e%2e/%2e%2e/evil.example",
	}
	for _, target := range targets {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, rawRequest(t, "GET "+target, ""))
		loc := rr.Header().Get("Location")
		if rr.Code != http.StatusFound {
			t.Errorf("%.40q: status %d", target, rr.Code)
			continue
		}
		if strings.ContainsAny(loc, "\r\n") {
			t.Errorf("%.40q: Location has CR/LF: %q", target, loc)
		}
		u, err := url.Parse(loc)
		if err != nil || u.Scheme != "http" || u.Host != "localhost:5173" || u.User != nil {
			t.Errorf("%.40q: Location %.80q resolves to host %q (err %v)", target, loc, u.Host, err)
		}
		if strings.HasPrefix(strings.TrimPrefix(loc, "http://localhost:5173"), "//") {
			t.Errorf("%.40q: Location %.80q has a protocol-relative path", target, loc)
		}
	}
}

func TestDevUIPreservesEscapedPathAndQuery(t *testing.T) {
	h := devUI(stubNext(), "http://localhost:5173")
	for target, want := range map[string]string{
		"/a%2fb":                  "http://localhost:5173/a%2fb",
		"/a/b?next=//evil":        "http://localhost:5173/a/b?next=//evil",
		"/x%20y?q=1&r=%2F":        "http://localhost:5173/x%20y?q=1&r=%2F",
		"http://evil.example/p?q": "http://localhost:5173/p?q",
	} {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, rawRequest(t, "GET "+target, ""))
		if got := rr.Header().Get("Location"); got != want {
			t.Errorf("%s: Location %q, want %q", target, got, want)
		}
	}
}

func TestDevUISkipsRequestsFromTheViteProxy(t *testing.T) {
	h := devUI(stubNext(), "http://localhost:5173")
	for _, target := range []string{"/l/", "/l/a/b", "/px/", "/js/x.js", "/app"} {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, rawRequest(t, "GET "+target, devProxyHeader+": 1\r\n"))
		if rr.Code != http.StatusTeapot || rr.Header().Get("Location") != "" {
			t.Errorf("%s: status %d Location %q; proxied requests must not be redirected", target, rr.Code, rr.Header().Get("Location"))
		}
	}
}

func TestHandlerWiring(t *testing.T) {
	t.Setenv(DevUIEnv, "http://localhost:5173")
	h := Handler()
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/app/x?y=1", nil))
	if rr.Code != http.StatusFound || rr.Header().Get("Location") != "http://localhost:5173/app/x?y=1" {
		t.Errorf("/app/x: %d %q", rr.Code, rr.Header().Get("Location"))
	}
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/api/x", nil))
	if rr.Code != http.StatusNotFound {
		t.Errorf("/api/x: %d, want the normal 404", rr.Code)
	}
	// A proxied request for a path nobody owns gets the stub page, not a redirect loop.
	rr = httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/l/", nil)
	req.Header.Set(devProxyHeader, "1")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || rr.Header().Get("Location") != "" {
		t.Errorf("proxied /l/: %d %q", rr.Code, rr.Header().Get("Location"))
	}
}

// The legacy /invite?token= link is rewritten by the UI handler to /invite/<token>
// (the SPA only understands the path form). The dev redirect must not bypass that.
func TestLegacyInviteLinkStillRewritesInDev(t *testing.T) {
	t.Setenv(DevUIEnv, "http://localhost:5173")
	h := Handler()
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/invite?token=abc", nil))
	if rr.Code != http.StatusFound || rr.Header().Get("Location") != "/invite/abc" {
		t.Fatalf("first hop: %d %q", rr.Code, rr.Header().Get("Location"))
	}
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/invite/abc", nil))
	if rr.Header().Get("Location") != "http://localhost:5173/invite/abc" {
		t.Errorf("second hop: %q", rr.Header().Get("Location"))
	}
}

func TestDevUIIgnoresUserinfoValue(t *testing.T) {
	rr := httptest.NewRecorder()
	devUI(stubNext(), "http://u@evil.example").ServeHTTP(rr, httptest.NewRequest("GET", "/app", nil))
	if rr.Code != http.StatusTeapot {
		t.Errorf("status %d, want the stub handler", rr.Code)
	}
}

func TestDevUIIgnoresInvalidValues(t *testing.T) {
	for _, raw := range []string{"", "localhost:5173", "ftp://x", "http://", "javascript:alert(1)", "//evil.example", "http://a b"} {
		h := devUI(stubNext(), raw)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest("GET", "/app", nil))
		if rr.Code != http.StatusTeapot {
			t.Errorf("%q: status %d, want the stub handler", raw, rr.Code)
		}
	}
}

func TestStubPageMentionsDevUI(t *testing.T) {
	if got := string(stubIndex("")); strings.Contains(got, "make dev") {
		t.Errorf("stub without a dev UI URL changed: %s", got)
	}
	got := string(stubIndex("http://localhost:5173"))
	if !strings.Contains(got, `href="http://localhost:5173"`) || !strings.Contains(got, "In make dev the UI is at") {
		t.Errorf("stub page: %s", got)
	}
	if strings.Contains(string(stubIndex("not a url")), "make dev") {
		t.Errorf("invalid URL leaked into the page")
	}
}
