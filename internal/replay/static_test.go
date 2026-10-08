package replay_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"panel/internal/api"
	"panel/internal/config"
	"panel/internal/replay"
)

func TestReplayJSReturnsEmbeddedBundleAndStableETag(t *testing.T) {
	t.Parallel()

	body, etag := replay.ReplayJS()
	if len(body) == 0 {
		t.Fatal("ReplayJS body is empty")
	}
	if !bytes.Contains(body, []byte("CompressionStream")) {
		t.Fatal("ReplayJS body does not contain the replay compression pipeline")
	}
	if len(etag) != 18 || etag[0] != '"' || etag[len(etag)-1] != '"' {
		t.Fatalf("ReplayJS ETag = %q, want quoted 16-character digest", etag)
	}

	again, againETag := replay.ReplayJS()
	if !bytes.Equal(body, again) || etag != againETag {
		t.Fatal("ReplayJS returned unstable content or ETag")
	}
}

func TestReplayJSRouteIsGuardedByInstanceSwitch(t *testing.T) {
	t.Parallel()

	ui := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	tests := []struct {
		name        string
		enabled     bool
		wantStatus  int
		wantContent string
	}{
		{name: "disabled returns not found before SPA fallback", wantStatus: http.StatusNotFound, wantContent: "text/plain"},
		{name: "enabled serves javascript", enabled: true, wantStatus: http.StatusOK, wantContent: "application/javascript"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			router := api.NewRouter(api.Deps{Config: config.Config{Replay: config.ReplayEnv{Enabled: test.enabled}}, UI: ui})
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/replay.js", nil))
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, test.wantStatus)
			}
			if got := response.Header().Get("Content-Type"); !bytes.Contains([]byte(got), []byte(test.wantContent)) {
				t.Fatalf("Content-Type = %q, want it to contain %q", got, test.wantContent)
			}
		})
	}
}
