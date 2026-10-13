package web

import (
	"net/http"
	"net/http/httptest"
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

func TestDevUIHostileTargetsKeepTheConfiguredHost(t *testing.T) {
	h := devUI(stubNext(), "http://localhost:5173/ignored/path")
	for _, target := range []string{"//evil.example", "//evil.example/x", "/\\evil.example", "/%2f%2fevil.example", "///evil.example", "/\\/evil.example"} {
		req := httptest.NewRequest("GET", "/", nil)
		req.URL.Path = strings.ReplaceAll(target, "%2f", "/")
		req.URL.RawPath = ""
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		loc := rr.Header().Get("Location")
		if !strings.HasPrefix(loc, "http://localhost:5173/") || strings.HasPrefix(loc, "http://localhost:5173/ignored") {
			t.Errorf("%q: Location %q", target, loc)
		}
		if strings.Contains(strings.TrimPrefix(loc, "http://localhost:5173"), "//") && strings.HasPrefix(strings.TrimPrefix(loc, "http://localhost:5173"), "//") {
			t.Errorf("%q: Location %q starts with a protocol-relative path", target, loc)
		}
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
