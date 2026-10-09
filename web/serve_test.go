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
		"index.html":    {Data: []byte("<main>Supermetry</main>")},
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

func TestFlagCaching(t *testing.T) {
	h := newHandler(fstest.MapFS{
		"index.html":   {Data: []byte("<main>Supermetry</main>")},
		"flags/in.svg": {Data: []byte("<svg></svg>")},
	})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/flags/in.svg", nil))

	if rr.Code != 200 {
		t.Fatalf("flag status: got %d, want 200", rr.Code)
	}
	if got := rr.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Errorf("flag cache: got %q", got)
	}
}

// Browser, OS and device logos live under /icons/; without this they fell
// through to index.html and the dashboard showed text with no logo.
func TestIconsAreServedAsFiles(t *testing.T) {
	h := newHandler(fstest.MapFS{
		"index.html":               {Data: []byte("<main>Supermetry</main>")},
		"icons/browser/chrome.png": {Data: []byte("\x89PNG")},
	})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/icons/browser/chrome.png", nil))
	if rr.Code != 200 || rr.Body.String() != "\x89PNG" {
		t.Fatalf("icon: status %d body %q, want the PNG", rr.Code, rr.Body.String())
	}
	missing := httptest.NewRecorder()
	h.ServeHTTP(missing, httptest.NewRequest("GET", "/icons/browser/nope.png", nil))
	if missing.Code != 404 {
		t.Fatalf("missing icon: status %d, want 404", missing.Code)
	}
}
