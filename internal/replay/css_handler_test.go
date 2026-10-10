package replay

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"supermetry/internal/ingest"
)

func TestCSSPutRejectsHashMismatch(t *testing.T) {
	store, err := NewDiskStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handler := newTestCSSHandler(store, make(chan struct{}, 1), nil)

	request := httptest.NewRequest(http.MethodPut, "/api/replay/css/"+cssHash([]byte("different"))+"?s=public-site", bytes.NewReader(gzipCSS(t, []byte("body{}"))))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}

func TestCSSPutIdempotent(t *testing.T) {
	store, err := NewDiskStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handler := newTestCSSHandler(store, make(chan struct{}, 1), nil)
	css := []byte("body { color: rebeccapurple; }")
	compressed := gzipCSS(t, css)
	url := "/api/replay/css/" + cssHash(css) + "?s=public-site"

	for index, want := range []int{http.StatusCreated, http.StatusOK} {
		request := httptest.NewRequest(http.MethodPut, url, bytes.NewReader(compressed))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("request %d status = %d, want %d", index+1, response.Code, want)
		}
	}

	stored, err := store.Open(t.Context(), CSSKey(42, cssHash(css)))
	if err != nil {
		t.Fatal(err)
	}
	defer stored.Close()
	var got bytes.Buffer
	if _, err := got.ReadFrom(stored); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Bytes(), compressed) {
		t.Fatal("stored CSS does not preserve the uploaded gzip bytes")
	}
}

func TestCSSPutRejectsCompressedBodyOverLimit(t *testing.T) {
	store, err := NewDiskStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handler := newTestCSSHandler(store, make(chan struct{}, 1), nil)
	css := make([]byte, 2*1024*1024)
	if _, err := rand.Read(css); err != nil {
		t.Fatal(err)
	}
	compressed := gzipCSS(t, css)
	if len(compressed) <= 1024*1024 {
		t.Fatalf("fixture compressed to %d bytes, want over 1 MiB", len(compressed))
	}

	request := httptest.NewRequest(http.MethodPut, "/api/replay/css/"+cssHash(css)+"?s=public-site", bytes.NewReader(compressed))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestCSSPutRejectsDecompressionBomb(t *testing.T) {
	store, err := NewDiskStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handler := newTestCSSHandler(store, make(chan struct{}, 1), nil)
	css := make([]byte, 4*1024*1024+1)
	compressed := gzipCSS(t, css)
	if len(compressed) >= 1024*1024 {
		t.Fatalf("fixture compressed to %d bytes, want under 1 MiB", len(compressed))
	}

	request := httptest.NewRequest(http.MethodPut, "/api/replay/css/"+cssHash(css)+"?s=public-site", bytes.NewReader(compressed))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestCSSPutRejectsWhenSemaphoreIsFull(t *testing.T) {
	store, err := NewDiskStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sem := make(chan struct{}, 1)
	sem <- struct{}{}
	var drops atomic.Int32
	handler := newTestCSSHandler(store, sem, func() { drops.Add(1) })
	css := []byte("body{}")

	request := httptest.NewRequest(http.MethodPut, "/api/replay/css/"+cssHash(css)+"?s=public-site", bytes.NewReader(gzipCSS(t, css)))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	if got := drops.Load(); got != 1 {
		t.Fatalf("onDrop calls = %d, want 1", got)
	}
}

func TestCSSPutRejectsWhenRateLimited(t *testing.T) {
	store, err := NewDiskStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var drops atomic.Int32
	handler := newTestCSSHandler(store, make(chan struct{}, 1), func() { drops.Add(1) },
		WithCSSLimiter(denyRateLimiter{}, nil))
	css := []byte("body{}")
	request := httptest.NewRequest(http.MethodPut, "/api/replay/css/"+cssHash(css)+"?s=public-site", bytes.NewReader(gzipCSS(t, css)))
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusTooManyRequests)
	}
	if got := drops.Load(); got != 1 {
		t.Fatalf("onDrop calls = %d, want 1", got)
	}
}

func TestCSSPutRejectsWhenDiskGuardTripped(t *testing.T) {
	store, err := NewDiskStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var drops atomic.Int32
	handler := newTestCSSHandler(store, make(chan struct{}, 1), func() { drops.Add(1) },
		WithCSSDiskGuard(func() bool { return true }))
	css := []byte("body{}")
	request := httptest.NewRequest(http.MethodPut, "/api/replay/css/"+cssHash(css)+"?s=public-site", bytes.NewReader(gzipCSS(t, css)))
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	if got := drops.Load(); got != 1 {
		t.Fatalf("onDrop calls = %d, want 1", got)
	}
}

func TestCSSPutEnforcesPerSiteCountCap(t *testing.T) {
	store, err := NewDiskStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var drops atomic.Int32
	handler := newTestCSSHandler(store, make(chan struct{}, 1), func() { drops.Add(1) }, WithCSSLimits(1, 1<<20))
	first := []byte("body{color:red}")
	second := []byte("body{color:blue}")
	for _, css := range [][]byte{first, second} {
		request := httptest.NewRequest(http.MethodPut, "/api/replay/css/"+cssHash(css)+"?s=public-site", bytes.NewReader(gzipCSS(t, css)))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if cssHash(css) == cssHash(first) && response.Code != http.StatusCreated {
			t.Fatalf("first status = %d, want %d", response.Code, http.StatusCreated)
		}
		if cssHash(css) == cssHash(second) && response.Code != http.StatusServiceUnavailable {
			t.Fatalf("second status = %d, want %d", response.Code, http.StatusServiceUnavailable)
		}
	}
	if got := drops.Load(); got != 1 {
		t.Fatalf("onDrop calls = %d, want 1", got)
	}
}

func TestCSSPutEnforcesPerSiteByteCap(t *testing.T) {
	store, err := NewDiskStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first := []byte("body{color:red}")
	firstCompressed := gzipCSS(t, first)
	handler := newTestCSSHandler(store, make(chan struct{}, 1), nil, WithCSSLimits(10, int64(len(firstCompressed))))
	firstRequest := httptest.NewRequest(http.MethodPut, "/api/replay/css/"+cssHash(first)+"?s=public-site", bytes.NewReader(firstCompressed))
	firstResponse := httptest.NewRecorder()
	handler.ServeHTTP(firstResponse, firstRequest)
	if firstResponse.Code != http.StatusCreated {
		t.Fatalf("first status = %d, want %d", firstResponse.Code, http.StatusCreated)
	}

	second := []byte("body{color:blue}")
	secondRequest := httptest.NewRequest(http.MethodPut, "/api/replay/css/"+cssHash(second)+"?s=public-site", bytes.NewReader(gzipCSS(t, second)))
	secondResponse := httptest.NewRecorder()
	handler.ServeHTTP(secondResponse, secondRequest)
	if secondResponse.Code != http.StatusServiceUnavailable {
		t.Fatalf("second status = %d, want %d", secondResponse.Code, http.StatusServiceUnavailable)
	}
}

type denyRateLimiter struct{}

func (denyRateLimiter) Allow(string) bool { return false }

var _ ingest.RateLimiter = denyRateLimiter{}

func newTestCSSHandler(store BlobStore, sem chan struct{}, onDrop func(), options ...CSSHandlerOption) http.Handler {
	return NewCSSHandler(store, func(publicID string) (int32, bool) {
		return 42, publicID == "public-site"
	}, sem, onDrop, options...)
}

func gzipCSS(t *testing.T, css []byte) []byte {
	t.Helper()
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(css); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return compressed.Bytes()
}

func cssHash(css []byte) string {
	sum := sha256.Sum256(css)
	return hex.EncodeToString(sum[:])
}
