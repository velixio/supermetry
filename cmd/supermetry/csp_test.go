package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"supermetry/internal/config"
)

func TestApplyCSPExtra(t *testing.T) {
	base := "default-src 'self'; img-src 'self' data:; frame-ancestors 'none'"
	got := applyCSPExtra(base, "img-src https://cdn.example; connect-src https://api.example; script-src https://evil.example; frame-ancestors *")
	want := "default-src 'self'; img-src 'self' data: https://cdn.example; frame-ancestors 'none'; connect-src https://api.example"
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	if applyCSPExtra(base, "") != base || applyCSPExtra("", "img-src x") != "" {
		t.Fatal("empty inputs must be a no-op")
	}
}

func TestCSPModes(t *testing.T) {
	for mode, hdr := range map[string]string{"enforce": "Content-Security-Policy", "": "Content-Security-Policy", "report-only": "Content-Security-Policy-Report-Only"} {
		h := appSecurityHeaders(config.Config{CSPMode: mode}, http.NotFoundHandler())
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
		if rec.Header().Get(hdr) == "" {
			t.Errorf("mode %q: %s missing", mode, hdr)
		}
		if mode == "report-only" && rec.Header().Get("Content-Security-Policy") != "" {
			t.Error("report-only must not enforce")
		}
	}
	rec := httptest.NewRecorder()
	appSecurityHeaders(config.Config{CSPMode: "off"}, http.NotFoundHandler()).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Header().Get("Content-Security-Policy") != "" || rec.Header().Get("X-Content-Type-Options") == "" {
		t.Error("off drops CSP only")
	}
}
