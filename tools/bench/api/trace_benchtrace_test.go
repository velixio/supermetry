//go:build benchtrace

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/testpg"
)

func TestBenchTraceHeaders(t *testing.T) {
	t.Parallel()
	handler := traceMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recordQuery(r.Context(), 2)
		w.WriteHeader(http.StatusNoContent)
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if got := recorder.Header().Get("X-Bench-Queries"); got != "1" {
		t.Fatalf("X-Bench-Queries = %q, want 1", got)
	}
	if got, err := strconv.ParseFloat(recorder.Header().Get("X-Bench-DB-Ms"), 64); err != nil || got <= 0 {
		t.Fatalf("X-Bench-DB-Ms = %q, want positive number", recorder.Header().Get("X-Bench-DB-Ms"))
	}
}

func TestBenchTraceRealDatabase(t *testing.T) {
	base, _ := testpg.New(t)
	config := base.Config().Copy()
	configureTrace(config)
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open traced pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatalf("warm traced pool: %v", err)
	}

	handler := traceMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var one int
		if err := pool.QueryRow(r.Context(), "select 1").Scan(&one); err != nil {
			t.Errorf("query: %v", err)
			http.Error(w, "query failed", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if got := recorder.Header().Get("X-Bench-Queries"); got != "1" {
		t.Fatalf("X-Bench-Queries = %q, want 1", got)
	}
}
