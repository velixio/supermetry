package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"panel/internal/api"
	"panel/internal/config"
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
