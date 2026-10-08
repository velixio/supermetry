package api_test

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"

	"panel/internal/api"
	"panel/internal/config"
	"panel/internal/replay"
)

func TestEndpoints404WhenInstanceDisabled(t *testing.T) {
	called := false
	replayHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusTeapot)
	})
	router := api.NewRouter(api.Deps{
		Config: config.Config{Replay: config.ReplayEnv{Enabled: false}},
		Replay: replayHandler,
	})
	for _, target := range []string{"/api/replay/start", "/api/replay/chunk?s=site"} {
		request := httptest.NewRequest(http.MethodPost, target, nil)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("POST %s status = %d, want 404", target, response.Code)
		}
	}
	if called {
		t.Fatal("disabled replay endpoints invoked their handler")
	}
}

func TestReplayCSSRoutePreflightAndPut(t *testing.T) {
	store, err := replay.NewDiskStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	css := []byte("body { color: navy; }")
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	if _, err := zw.Write(css); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(css)
	target := "/api/replay/css/" + hex.EncodeToString(sum[:]) + "?s=public-site"
	cssHandler := replay.NewCSSHandler(store, func(publicID string) (int32, bool) {
		return 42, publicID == "public-site"
	}, make(chan struct{}, 16), nil)
	router := api.NewRouter(api.Deps{
		Config:    config.Config{Replay: config.ReplayEnv{Enabled: true}},
		ReplayCSS: cssHandler,
	})

	preflight := httptest.NewRequest(http.MethodOptions, target, nil)
	preflight.Header.Set("Origin", "https://example.test")
	preflightResponse := httptest.NewRecorder()
	router.ServeHTTP(preflightResponse, preflight)
	if preflightResponse.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want 204", preflightResponse.Code)
	}
	if got := preflightResponse.Header().Get("Access-Control-Allow-Methods"); got != "PUT, OPTIONS" {
		t.Fatalf("preflight methods = %q, want PUT, OPTIONS", got)
	}

	request := httptest.NewRequest(http.MethodPut, target, bytes.NewReader(compressed.Bytes()))
	request.Header.Set("Origin", "https://example.test")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("PUT status = %d, want 201", response.Code)
	}
}
