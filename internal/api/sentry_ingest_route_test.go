package api_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"supermetry/internal/api"
	"supermetry/internal/config"
)

type fakeSentry struct{ hits int }

func (f *fakeSentry) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	f.hits++
	w.WriteHeader(http.StatusTeapot)
}
func (f *fakeSentry) Matches(r *http.Request) bool {
	return strings.HasPrefix(r.URL.Path, "/api/42/envelope")
}

func TestSentryIngestIsDispatchedBeforeTheMux(t *testing.T) {
	f := &fakeSentry{}
	router := api.NewRouter(api.Deps{Config: config.Config{AdminAPIKey: "test-key"}, SentryIngest: f})
	for _, path := range []string{"/api/42/envelope/", "/api/42/envelope"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, nil))
		if w.Code != http.StatusTeapot {
			t.Errorf("%s = %d", path, w.Code)
		}
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/private/sites", nil))
	if w.Code == http.StatusTeapot || f.hits != 2 {
		t.Errorf("private route reached the sentry handler: code=%d hits=%d", w.Code, f.hits)
	}
}
