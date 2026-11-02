package api_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"supermetry/internal/api"
	"supermetry/internal/config"
	"supermetry/internal/ingest"
)

type seenRequest struct {
	path, site string
	called     bool
}

func recordingHandler(seen *seenRequest) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.called = true
		seen.path = r.URL.Path
		seen.site, _ = ingest.PathSite(r.Context())
		w.WriteHeader(http.StatusAccepted)
	})
}

func neutralRouter(replayEnabled bool) (http.Handler, *seenRequest, *seenRequest, *seenRequest) {
	events, replay, css := &seenRequest{}, &seenRequest{}, &seenRequest{}
	cfg := config.Config{EventPath: "/api/public/v1/events", AdminAPIKey: "test-key"}
	cfg.Replay.Enabled = replayEnabled
	router := api.NewRouter(api.Deps{
		Config:    cfg,
		Ingest:    recordingHandler(events),
		Replay:    recordingHandler(replay),
		ReplayCSS: recordingHandler(css),
	})
	return router, events, replay, css
}

func TestNeutralPathsReachLegacyHandlers(t *testing.T) {
	router, events, replay, css := neutralRouter(true)
	tests := []struct {
		name, method, target string
		seen                 *seenRequest
		wantPath, wantSite   string
	}{
		{"events", http.MethodPost, "/e/abc123", events, "/api/public/v1/events", "abc123"},
		{"replay start", http.MethodPost, "/e/abc123/s", replay, "/api/replay/start", "abc123"},
		{"replay chunk", http.MethodPost, "/e/abc123/c?s=abc123&q=1", replay, "/api/replay/chunk", "abc123"},
		{"replay css", http.MethodPut, "/e/abc123/k/0123abcd?s=abc123", css, "/api/replay/css/0123abcd", "abc123"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			*tt.seen = seenRequest{}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(tt.method, tt.target, strings.NewReader("{}")))
			if response.Code != http.StatusAccepted || !tt.seen.called {
				t.Fatalf("status %d, called %v", response.Code, tt.seen.called)
			}
			if tt.seen.path != tt.wantPath || tt.seen.site != tt.wantSite {
				t.Fatalf("handler saw path %q site %q, want %q %q", tt.seen.path, tt.seen.site, tt.wantPath, tt.wantSite)
			}
		})
	}
}

func TestNeutralEventsPreflightAllowsCrossOrigin(t *testing.T) {
	router, events, _, _ := neutralRouter(false)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodOptions, "/e/abc123", nil))
	if response.Code != http.StatusNoContent || response.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("preflight status %d, headers %v", response.Code, response.Header())
	}
	if events.called {
		t.Fatal("preflight reached the ingest handler")
	}
}

func TestNeutralPathsRejectMismatchedOrUnknownRoutes(t *testing.T) {
	router, events, replay, css := neutralRouter(true)
	tests := []struct {
		name, method, target string
		want                 int
	}{
		{"chunk site mismatch", http.MethodPost, "/e/abc123/c?s=other", http.StatusBadRequest},
		{"chunk site missing", http.MethodPost, "/e/abc123/c", http.StatusBadRequest},
		{"css site mismatch", http.MethodPut, "/e/abc123/k/0123abcd?s=other", http.StatusBadRequest},
		{"unknown sub-path", http.MethodPost, "/e/abc123/zzz", http.StatusNotFound},
		{"trailing slash", http.MethodPost, "/e/abc123/", http.StatusNotFound},
		{"bad id", http.MethodPost, "/e/a.b", http.StatusNotFound},
		{"events wrong method", http.MethodGet, "/e/abc123", http.StatusMethodNotAllowed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			*events, *replay, *css = seenRequest{}, seenRequest{}, seenRequest{}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(tt.method, tt.target, strings.NewReader("{}")))
			if response.Code != tt.want {
				t.Fatalf("status %d, want %d", response.Code, tt.want)
			}
			if events.called || replay.called || css.called {
				t.Fatal("request reached a handler")
			}
		})
	}
}

func TestNeutralReplayRoutesOffWhenReplayDisabled(t *testing.T) {
	router, _, replay, _ := neutralRouter(false)
	for _, target := range []string{"/e/abc123/s", "/e/abc123/r.js"} {
		method := http.MethodPost
		if strings.HasSuffix(target, ".js") {
			method = http.MethodGet
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(method, target, nil))
		if response.Code != http.StatusNotFound || replay.called {
			t.Fatalf("%s: status %d, called %v", target, response.Code, replay.called)
		}
	}
}

func TestNeutralRecorderServesReplayScript(t *testing.T) {
	router, _, _, _ := neutralRouter(true)
	legacy, neutral := httptest.NewRecorder(), httptest.NewRecorder()
	router.ServeHTTP(legacy, httptest.NewRequest(http.MethodGet, "/replay.js", nil))
	router.ServeHTTP(neutral, httptest.NewRequest(http.MethodGet, "/e/abc123/r.js", nil))
	if neutral.Code != http.StatusOK || neutral.Body.String() != legacy.Body.String() || neutral.Header().Get("ETag") != legacy.Header().Get("ETag") {
		t.Fatalf("neutral recorder status %d, same body %v", neutral.Code, neutral.Body.String() == legacy.Body.String())
	}
}

func TestNeutralPathClassification(t *testing.T) {
	tests := []struct {
		path             string
		intake, recorder bool
	}{
		{"/e/abc123", true, false},
		{"/e/abc123/s", true, false},
		{"/e/abc123/c", true, false},
		{"/e/abc123/k/0123abcd", true, false},
		{"/e/abc123/r.js", false, true},
		{"/e/abc123/zzz", false, false},
		{"/e/", false, false},
		{"/events", false, false},
	}
	for _, tt := range tests {
		intake, recorder := api.NeutralPathKind(tt.path)
		if intake != tt.intake || recorder != tt.recorder {
			t.Errorf("%s: intake %v recorder %v, want %v %v", tt.path, intake, recorder, tt.intake, tt.recorder)
		}
	}
}
