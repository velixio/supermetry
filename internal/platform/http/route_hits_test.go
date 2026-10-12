package platformhttp

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRouteHitRecorder(t *testing.T) {
	tests := []struct {
		name     string
		enabled  bool
		testName string
		want     string
	}{
		{name: "disabled writes nothing"},
		{name: "enabled records matched pattern", enabled: true, want: "GET /widgets/{widget}\n"},
		{name: "enabled includes available test name", enabled: true, testName: "TestWidget/found", want: "GET /widgets/{widget}\tTestWidget/found\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hits := filepath.Join(t.TempDir(), "hits")
			if tt.enabled {
				t.Setenv(routeHitsEnv, hits)
			} else {
				t.Setenv(routeHitsEnv, "")
			}

			mux := http.NewServeMux()
			mux.HandleFunc("GET /widgets/{widget}", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			})
			handler := withRouteHitRecorder(mux)
			request := httptest.NewRequest(http.MethodGet, "/widgets/42?secret=discarded", nil)
			request.Header.Set("User-Agent", "must-not-be-recorded")
			if tt.testName != "" {
				request = WithRouteTestName(request, tt.testName)
			}
			handler.ServeHTTP(httptest.NewRecorder(), request)

			got, err := os.ReadFile(hits)
			if !tt.enabled {
				if !os.IsNotExist(err) {
					t.Fatalf("disabled recorder created hit file: err=%v contents=%q", err, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("read hits: %v", err)
			}
			if string(got) != tt.want {
				t.Fatalf("hits = %q, want %q", got, tt.want)
			}
			for _, private := range []string{"42", "secret", "must-not-be-recorded"} {
				if strings.Contains(string(got), private) {
					t.Errorf("hit record contains request data %q", private)
				}
			}
		})
	}
}

func TestRouteHitRecorderReturnsOriginalHandlerWhenDisabled(t *testing.T) {
	t.Setenv(routeHitsEnv, "")
	next := &identityHandler{}
	if got := withRouteHitRecorder(next); got != next {
		t.Fatal("disabled recorder installed a middleware")
	}
}

type identityHandler struct{}

func (*identityHandler) ServeHTTP(http.ResponseWriter, *http.Request) {}

func TestRouteHitRecorderAppendsOneLinePerRequest(t *testing.T) {
	hits := filepath.Join(t.TempDir(), "hits")
	t.Setenv(routeHitsEnv, hits)
	if err := os.WriteFile(hits, []byte("GET /existing\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /widgets/{widget}", func(http.ResponseWriter, *http.Request) {})
	handler := withRouteHitRecorder(mux)
	for range 2 {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/widgets/42", nil))
	}
	got, err := os.ReadFile(hits)
	if err != nil {
		t.Fatal(err)
	}
	want := "GET /existing\nGET /widgets/{widget}\nGET /widgets/{widget}\n"
	if string(got) != want {
		t.Fatalf("hits = %q, want %q", got, want)
	}
}

func TestNestedRouteHitRecordersWriteOnce(t *testing.T) {
	hits := filepath.Join(t.TempDir(), "hits")
	t.Setenv(routeHitsEnv, hits)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /widgets/{widget}", func(http.ResponseWriter, *http.Request) {})
	handler := withRouteHitRecorder(withRouteHitRecorder(mux))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/widgets/42", nil))

	got, err := os.ReadFile(hits)
	if err != nil {
		t.Fatal(err)
	}
	if want := "GET /widgets/{widget}\n"; string(got) != want {
		t.Fatalf("hits = %q, want %q", got, want)
	}
}
