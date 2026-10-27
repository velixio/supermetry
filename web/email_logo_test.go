package web

import (
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"testing/fstest"
)

func TestEmailLogoIsServedAndNotShadowedBySPA(t *testing.T) {
	h := newHandler(fstest.MapFS{
		"index.html":     {Data: []byte("<main>Supermetry</main>")},
		"email-logo.png": {Data: []byte("\x89PNG")},
	})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/email-logo.png", nil))
	if rr.Code != 200 || !strings.HasPrefix(rr.Header().Get("Content-Type"), "image/png") {
		t.Fatalf("email logo: %d %q", rr.Code, rr.Header().Get("Content-Type"))
	}
	if strings.Contains(rr.Body.String(), "<main>") {
		t.Fatal("email logo was shadowed by the SPA index")
	}
	if rr.Header().Get("Cache-Control") == "no-cache" || rr.Header().Get("Set-Cookie") != "" {
		t.Fatalf("unexpected headers: %v", rr.Header())
	}

	// A missing logo is a 404, not index.html served as an image.
	missing := newHandler(fstest.MapFS{"index.html": {Data: []byte("<main>x</main>")}})
	rr = httptest.NewRecorder()
	missing.ServeHTTP(rr, httptest.NewRequest("GET", "/email-logo.png", nil))
	if rr.Code != 404 {
		t.Fatalf("missing logo: %d", rr.Code)
	}
}

// The logo source file must exist in web/public so the build ships it.
func TestEmailLogoSourceExists(t *testing.T) {
	for _, name := range []string{"public/email-logo.png", "public/email-logo-light.png"} {
		info, err := os.Stat(name)
		if err != nil || info.Size() == 0 || info.Size() > 60*1024 {
			t.Fatalf("%s: %v %v", name, info, err)
		}
	}
}
