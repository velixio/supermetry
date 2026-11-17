package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCaptureWritesRawBodyAndSidecar(t *testing.T) {
	dir := t.TempDir()
	var seq atomic.Int64
	h := newCaptureHandler(dir, "test-sdk", &seq)

	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write([]byte(`{"event_id":"abc123","sdk":{"name":"sentry.test","version":"1.2.3"}}` + "\n" + `{"type":"event"}` + "\n{}\n"))
	zw.Close()
	body := buf.Bytes()

	req := httptest.NewRequest(http.MethodPost, "/api/42/envelope/?sentry_key=x", bytes.NewReader(body))
	req.Header.Set("Content-Encoding", "gzip")
	req.Header.Set("Content-Type", "application/x-sentry-envelope")
	req.Header.Set("X-Sentry-Auth", "Sentry sentry_key=0123")
	req.Header.Set("Cookie", "secret=1")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status %d", rr.Code)
	}
	if got := strings.TrimSpace(rr.Body.String()); got != `{"id":"abc123"}` {
		t.Fatalf("body %q", got)
	}
	if rr.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal("missing CORS origin")
	}
	if rr.Header().Get("Access-Control-Expose-Headers") != "X-Sentry-Rate-Limits, Retry-After" {
		t.Fatalf("expose headers %q", rr.Header().Get("Access-Control-Expose-Headers"))
	}

	files, _ := filepath.Glob(filepath.Join(dir, "*.envelope"))
	if len(files) != 1 {
		t.Fatalf("envelope files: %v", files)
	}
	if !strings.HasPrefix(filepath.Base(files[0]), "1-") {
		t.Fatalf("name %s", files[0])
	}
	raw, _ := os.ReadFile(files[0])
	if !bytes.Equal(raw, body) {
		t.Fatal("envelope file is not the raw gzip body")
	}
	side, err := os.ReadFile(strings.TrimSuffix(files[0], ".envelope") + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var sc struct {
		Method, Path, Query string
		Headers             map[string]string
		SDK                 string `json:"sdk"`
	}
	if err := json.Unmarshal(side, &sc); err != nil {
		t.Fatal(err)
	}
	if sc.Headers["Content-Encoding"] != "gzip" || sc.Method != "POST" || sc.SDK != "test-sdk" {
		t.Fatalf("sidecar %+v", sc)
	}
	if _, ok := sc.Headers["Cookie"]; ok {
		t.Fatal("cookie header leaked into sidecar")
	}
}

func TestOptionsPreflight(t *testing.T) {
	var seq atomic.Int64
	h := newCaptureHandler(t.TempDir(), "x", &seq)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodOptions, "/api/42/envelope/", nil))
	if rr.Code != 204 || rr.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("%d %v", rr.Code, rr.Header())
	}
}
