package tracker

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestHandlerServesTracker(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/js/script.js", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/javascript" {
		t.Errorf("Content-Type = %q, want application/javascript", got)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "public, max-age=3600" {
		t.Errorf("Cache-Control = %q, want public, max-age=3600", got)
	}
	if recorder.Body.Len() == 0 {
		t.Error("tracker body is empty")
	}
}

func TestScriptContract(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("tracker.js")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for _, want := range []string{
		`script.dataset`,
		`data.site`,
		`data.api`,
		`/api/public/v1/events`,
		`data-allow-localhost`,
		`navigator.webdriver`,
		`navigator.sendBeacon`,
		`keepalive: true`,
		`history.pushState`,
		`"popstate"`,
		`"pagehide"`,
		`document.addEventListener("visibilitychange"`,
		`window.panel`,
		`engagement_ms`,
		`scroll_pct`,
		`function updateScroll()`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("tracker source is missing %q", want)
		}
	}
	for _, forbidden := range []string{"localStorage", "sessionStorage", "document.cookie"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("tracker source contains forbidden browser API %q", forbidden)
		}
	}
}

func TestMinifiedTrackerGzipSize(t *testing.T) {
	t.Parallel()

	response := httptest.NewRecorder()
	Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/js/script.js", nil))

	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	if _, err := io.Copy(zw, response.Body); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if got := compressed.Len(); got >= 1024 {
		t.Fatalf("gzipped tracker = %d bytes, want under 1024", got)
	}
}
