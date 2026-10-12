package installer

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCheckSetupRequired(t *testing.T) {
	t.Parallel()
	for _, required := range []bool{true, false} {
		required := required
		t.Run(fmt.Sprintf("required_%t", required), func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/private/setup/status" {
					t.Errorf("path = %q", r.URL.Path)
				}
				fmt.Fprintf(w, `{"setup_required":%t}`, required)
			}))
			defer server.Close()
			got, err := CheckSetupRequired(context.Background(), server.URL)
			if err != nil {
				t.Fatal(err)
			}
			if got != required {
				t.Fatalf("CheckSetupRequired() = %t, want %t", got, required)
			}
		})
	}
}

func TestCheckSetupRequiredHonorsContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := CheckSetupRequired(ctx, "http://127.0.0.1:1"); err == nil {
		t.Fatal("CheckSetupRequired() error = nil, want canceled request")
	}
}

func TestWaitReady(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/readyz" {
			t.Errorf("path = %q, want /readyz", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	if err := WaitReady(context.Background(), server.URL, time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestWaitReadyHonorsContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := WaitReady(ctx, "http://127.0.0.1:1", time.Minute); err != context.Canceled {
		t.Fatalf("WaitReady() error = %v, want context.Canceled", err)
	}
}
