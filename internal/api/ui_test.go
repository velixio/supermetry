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
	router := api.NewRouter(api.Deps{
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
