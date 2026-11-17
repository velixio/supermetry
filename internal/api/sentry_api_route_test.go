package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSentryAPIMountedAtAPIZero(t *testing.T) {
	var seen []string
	sentry := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		w.WriteHeader(http.StatusTeapot)
	})
	ui := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	router := NewRouter(Deps{SentryAPI: sentry, UI: ui})
	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/0/"},
		{"POST", "/api/0/organizations/acme/chunk-upload/"},
		{"POST", "/api/0/organizations/acme/artifactbundle/assemble/"},
		{"GET", "/api/0/unknown"},
	} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(tc.method, tc.path, nil))
		if response.Code != http.StatusTeapot {
			t.Fatalf("%s %s = %d, want it routed to the Sentry API", tc.method, tc.path, response.Code)
		}
	}
	if len(seen) != 4 {
		t.Fatalf("seen = %v", seen)
	}
	// Without the handler the route is not claimed (UI serves GET, others 404).
	bare := NewRouter(Deps{UI: ui})
	response := httptest.NewRecorder()
	bare.ServeHTTP(response, httptest.NewRequest("POST", "/api/0/organizations/acme/chunk-upload/", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("unmounted POST = %d", response.Code)
	}
}
