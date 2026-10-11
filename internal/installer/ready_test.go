package installer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

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
