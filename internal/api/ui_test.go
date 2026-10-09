package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"panel/internal/api"
	"panel/internal/config"
	"panel/internal/ingest"
)

func TestUIServesNonAPIPathsOnly(t *testing.T) {
	ui := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(299) })
	router := identifiedRouter(api.Deps{
		Config:      config.Config{EventPath: "/api/public/v1/events", TrackerPath: "/js/script.js"},
		Query:       queryStub{},
		RateLimiter: ingest.NewTokenBuckets(0, 30),
		UI:          ui,
	})
	for path, wantUI := range map[string]bool{"/": true, "/login": true, "/acme/blog?period=30d": true, "/healthz": false, "/api/public/openapi.json": false, "/api/private/me": false} {
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
		if (rr.Code == 299) != wantUI {
			t.Errorf("%s: status %d, UI served = %v, want %v", path, rr.Code, rr.Code == 299, wantUI)
		}
	}
}

func TestTrackerUsesConfiguredPathAndHeaders(t *testing.T) {
	t.Parallel()

	router := identifiedRouter(api.Deps{
		Config:      config.Config{TrackerPath: "/custom/tracker.js"},
		RateLimiter: ingest.NewTokenBuckets(0, 30),
	})

	for _, test := range []struct {
		name       string
		path       string
		wantStatus int
	}{
		{name: "configured path", path: "/custom/tracker.js", wantStatus: http.StatusOK},
		{name: "configured full path", path: "/custom/tracker.full.js", wantStatus: http.StatusOK},
		{name: "default path is not mounted", path: "/js/script.js", wantStatus: http.StatusNotFound},
		{name: "default full path is not mounted", path: "/js/script.full.js", wantStatus: http.StatusNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, test.path, nil))
			if recorder.Code != test.wantStatus {
				t.Fatalf("GET %s status = %d, want %d", test.path, recorder.Code, test.wantStatus)
			}
			if test.wantStatus == http.StatusOK {
				if got := recorder.Header().Get("Content-Type"); got != "application/javascript" {
					t.Errorf("Content-Type = %q, want application/javascript", got)
				}
				if got := recorder.Header().Get("Cache-Control"); got != "public, max-age=3600" {
					t.Errorf("Cache-Control = %q, want public, max-age=3600", got)
				}
			}
		})
	}
}
