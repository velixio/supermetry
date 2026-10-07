package web

import (
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestSPAFallbackAndCaching(t *testing.T) {
	h := Handler()
	tests := []struct {
		name string
		path string
	}{
		{name: "root", path: "/"},
		{name: "site dashboard", path: "/acme/blog"},
		{name: "nested site settings", path: "/acme/blog/settings/goals"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, httptest.NewRequest("GET", tt.path, nil))
			if rr.Code != 200 || !strings.Contains(rr.Header().Get("Content-Type"), "text/html") {
				t.Errorf("%s: %d %s", tt.path, rr.Code, rr.Header().Get("Content-Type"))
			}
			if rr.Header().Get("Cache-Control") != "no-cache" {
				t.Errorf("%s cache: %q", tt.path, rr.Header().Get("Cache-Control"))
			}
		})
	}

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/api/private/me", nil))
	if rr.Code != 404 {
		t.Errorf("api paths must not fall back to index.html: %d", rr.Code)
	}
}

func TestAssetCaching(t *testing.T) {
	h := newHandler(fstest.MapFS{
		"index.html":    {Data: []byte("<main>Panel</main>")},
		"assets/app.js": {Data: []byte("export {}")},
	})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/assets/app.js", nil))

	if rr.Code != 200 {
		t.Fatalf("asset status: got %d, want 200", rr.Code)
	}
	if got := rr.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Errorf("asset cache: got %q", got)
	}
}
