package tracker_test

import (
	"bytes"
	"compress/gzip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSourceImplementsTrackerContract(t *testing.T) {
	source, err := os.ReadFile("tracker.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(source)

	required := []string{
		`send("pageview")`,
		`history.pushState`,
		`history.replaceState`,
		`addEventListener("popstate"`,
		`window.panel`,
		`.track=send`,
		`n:name`,
		`u:location.href`,
		`d:location.hostname`,
		`r:document.referrer`,
		`p:props`,
		`"/api/event"`,
		`navigator.sendBeacon`,
		`new Blob`,
		`type:"text/plain"`,
		`fetch(`,
		`keepalive:true`,
	}
	for _, token := range required {
		if !strings.Contains(js, token) {
			t.Errorf("tracker source is missing required token %q", token)
		}
	}

	assertNoStorageAPIs(t, "tracker.js", js)
}

func TestMinifiedBuildIsCurrentAndPrivate(t *testing.T) {
	want, err := os.ReadFile("tracker.min.js")
	if err != nil {
		t.Fatal(err)
	}

	tempDir := t.TempDir()
	first := generateMinified(t, filepath.Join(tempDir, "first.js"))
	second := generateMinified(t, filepath.Join(tempDir, "second.js"))
	if !bytes.Equal(first, second) {
		t.Fatal("minifier output is not deterministic")
	}
	got := first
	if !bytes.Equal(got, want) {
		t.Fatal("tracker.min.js is stale; run: go run ./cmd/minify -in tracker.js -out tracker.min.js")
	}
	for _, token := range []string{
		`send("pageview")`, `history.pushState`, `history.replaceState`,
		`"popstate"`, `window.panel`, `.track=send`, `navigator.sendBeacon`,
		`new Blob`, `type:"text/plain"`, `fetch(`, `keepalive:true`,
	} {
		if !strings.Contains(string(got), token) {
			t.Errorf("minified tracker is missing required token %q", token)
		}
	}
	assertNoStorageAPIs(t, "tracker.min.js", string(got))
}

func generateMinified(t *testing.T, outputPath string) []byte {
	t.Helper()
	cmd := exec.Command("go", "run", "./cmd/minify", "-in", "tracker.js", "-out", outputPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate minified tracker: %v\n%s", err, output)
	}
	got, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestMinifiedTrackerFitsOneKiBGzip(t *testing.T) {
	minified, err := os.ReadFile("tracker.min.js")
	if err != nil {
		t.Fatal(err)
	}

	var compressed bytes.Buffer
	zw, err := gzip.NewWriterLevel(&compressed, gzip.BestCompression)
	if err != nil {
		t.Fatal(err)
	}
	zw.Header.ModTime = time.Unix(0, 0)
	zw.Header.OS = 255
	if _, err := zw.Write(minified); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	t.Logf("tracker.min.js: %d bytes, deterministic gzip -9: %d bytes", len(minified), compressed.Len())
	if compressed.Len() >= 1024 {
		t.Fatalf("compressed tracker is %d bytes; want less than 1024", compressed.Len())
	}
}

func assertNoStorageAPIs(t *testing.T, name, js string) {
	t.Helper()
	for _, banned := range []string{"cookie", "localStorage", "sessionStorage"} {
		if strings.Contains(js, banned) {
			t.Errorf("%s contains banned browser API %q", name, banned)
		}
	}
}
