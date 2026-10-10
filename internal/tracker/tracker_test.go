package tracker

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
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

func TestFullHandlerServesDifferentTrackerWithSameHeaders(t *testing.T) {
	t.Parallel()

	core := httptest.NewRecorder()
	Handler().ServeHTTP(core, httptest.NewRequest(http.MethodGet, "/js/script.js", nil))
	full := httptest.NewRecorder()
	FullHandler().ServeHTTP(full, httptest.NewRequest(http.MethodGet, "/js/script.full.js", nil))

	if full.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", full.Code, http.StatusOK)
	}
	for _, header := range []string{"Content-Type", "Cache-Control"} {
		if got, want := full.Header().Get(header), core.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	if bytes.Equal(full.Body.Bytes(), core.Body.Bytes()) {
		t.Error("full tracker body equals core tracker body")
	}
}

func TestFullPath(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		path string
		want string
	}{
		{name: "default", path: "/js/script.js", want: "/js/script.full.js"},
		{name: "configured", path: "/x/a.js", want: "/x/a.full.js"},
		{name: "non js suffix", path: "/tracker", want: "/tracker.full.js"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := FullPath(test.path); got != test.want {
				t.Fatalf("FullPath(%q) = %q, want %q", test.path, got, test.want)
			}
		})
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
		`window.supermetry`,
		`options?.revenue`,
		`engagement_ms`,
		`scroll_pct`,
		`function updateScroll()`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("tracker source is missing %q", want)
		}
	}
	fullSource, err := os.ReadFile("tracker.extensions.js")
	if err != nil {
		t.Fatal(err)
	}
	fullText := string(fullSource)
	for _, want := range []string{
		`d.vitals != "false"`,
		`PerformanceObserver`,
		`largest-contentful-paint`,
		`layout-shift`,
		`durationThreshold: 40`,
		`"Web Vitals"`,
	} {
		if !strings.Contains(fullText, want) {
			t.Errorf("full tracker source is missing %q", want)
		}
	}
	if strings.Count(text, "localStorage") == 0 || !strings.Contains(text, `supermetry_ignore`) {
		t.Error("tracker source must use only the supermetry_ignore localStorage opt-out key")
	}
	for _, forbidden := range []string{"sessionStorage", "document.cookie"} {
		if strings.Contains(text, forbidden) || strings.Contains(fullText, forbidden) {
			t.Errorf("tracker source contains forbidden browser API %q", forbidden)
		}
	}
}

func TestTrackerRuntimeContract(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is required to execute the browser tracker contract")
	}
	command := exec.Command("node", "tracker_runtime_test.js")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("tracker runtime contract: %v\n%s", err, output)
	}
}

func TestMinifiedTrackerGzipSize(t *testing.T) {
	t.Parallel()

	response := httptest.NewRecorder()
	Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/js/script.js", nil))

	var compressed bytes.Buffer
	zw, err := gzip.NewWriterLevel(&compressed, gzip.BestCompression)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(zw, response.Body); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	// Required GPC/DNT controls and fail-closed delegation grow the 1004-byte
	// baseline to 1264 bytes. Keep the ceiling at the next 25-byte boundary;
	// replay/heavy features remain outside the core build.
	t.Logf("core tracker gzip: %d bytes (baseline 1004; budget 1275)", compressed.Len())
	if got := compressed.Len(); got > 1275 {
		t.Fatalf("gzipped tracker = %d bytes, want at most 1275-byte privacy budget", got)
	}
}

func TestMinifiedFullTrackerGzipSize(t *testing.T) {
	t.Parallel()

	response := httptest.NewRecorder()
	FullHandler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/js/script.full.js", nil))

	var compressed bytes.Buffer
	zw, err := gzip.NewWriterLevel(&compressed, gzip.BestCompression)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(zw, response.Body); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if got := compressed.Len(); got > 3002+400 {
		t.Fatalf("gzipped full tracker = %d bytes, want at most 400 bytes over 3002-byte baseline", got)
	}
}
