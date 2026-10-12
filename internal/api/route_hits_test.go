package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"supermetry/internal/config"
)

func TestRouterRecordsOuterRouteHit(t *testing.T) {
	hits := filepath.Join(t.TempDir(), "hits")
	t.Setenv("SUPERMETRY_ROUTE_HITS", hits)
	handler := NewRouter(Deps{Config: config.Config{}})
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/healthz", nil))

	got, err := os.ReadFile(hits)
	if err != nil {
		t.Fatalf("read hits: %v", err)
	}
	if string(got) != "GET /healthz\n" {
		t.Fatalf("hits = %q, want %q", got, "GET /healthz\\n")
	}
}
