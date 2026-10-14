package opencounters

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"supermetry/internal/pixels"
)

func TestHandlerDisabled(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, nil, false)
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/c/github.com/a.svg", nil))
	if res.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", res.Code)
	}
}

func TestHandlerCountsOnlyProxyImages(t *testing.T) {
	store := newMemoryStore()
	recorder := NewRecorder(store, 50, 500)
	h := &Handler{
		Recorder: recorder, Proxy: pixels.NewProxyLimiter(),
		Blocklist: map[string]bool{"github.com/blocked": true}, Branded: true,
		Now: func() time.Time { return time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC) },
	}
	mux := http.NewServeMux()
	Register(mux, h, true)
	request := func(path, ua string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Header.Set("User-Agent", ua)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	for _, path := range []string{"/c/github.com/a.svg", "/c/github.com/a.gif"} {
		w := request(path, "github-camo")
		if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store, max-age=0" {
			t.Fatalf("%s response = %d, cache %q", path, w.Code, w.Header().Get("Cache-Control"))
		}
	}
	request("/c/github.com/a.svg", "curl/8")
	request("/c/github.com/blocked.svg", "github-camo")
	if w := request("/c/github.com/bad_owner.svg", "github-camo"); w.Code != http.StatusNotFound {
		t.Fatalf("invalid key status = %d", w.Code)
	}
	if err := recorder.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.rows) != 1 || store.rows[ViewKey{Key: "github.com/a", Day: "2026-10-14"}] != 2 {
		t.Fatalf("stored rows = %#v", store.rows)
	}
	w := request("/c/github.com/a.svg", "curl/8")
	body, _ := io.ReadAll(w.Result().Body)
	if !strings.Contains(string(body), "views: 2") {
		t.Fatalf("badge does not show total: %s", body)
	}
}
