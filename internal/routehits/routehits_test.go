package routehits

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// copyingMiddleware mimics auth and CSRF middleware that hand the next handler
// a cloned request, so a pattern set downstream never reaches the caller's copy.
func copyingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(r.Context()))
	})
}

func TestNestedMuxRecordsInnerPatternOnly(t *testing.T) {
	file := filepath.Join(t.TempDir(), "hits.txt")
	t.Setenv(Env, file)

	inner := http.NewServeMux()
	inner.HandleFunc("GET /api/private/stats", func(http.ResponseWriter, *http.Request) {})
	inner.HandleFunc("/api/private/", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) })

	outer := http.NewServeMux()
	outer.Handle("/api/private/", copyingMiddleware(Wrap(inner)))
	outer.HandleFunc("GET /healthz", func(http.ResponseWriter, *http.Request) {})
	handler := Wrap(outer)

	for _, target := range []string{"/api/private/stats", "/healthz", "/api/private/missing"} {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, target, nil))
	}

	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSpace(string(data)), "\n")
	want := []string{"GET /api/private/stats", "GET /healthz", "/api/private/"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("recorded %q, want %q", got, want)
	}
}

func TestNoEnvReturnsHandlerUnchanged(t *testing.T) {
	t.Setenv(Env, "")
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	got := Wrap(next)
	if reflect.ValueOf(got).Pointer() != reflect.ValueOf(next).Pointer() {
		t.Fatal("Wrap must return the handler unchanged when recording is off")
	}
}
