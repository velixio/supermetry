//go:build benchtrace

package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type traceKey struct{}
type startedKey struct{}

type queryStart struct {
	started time.Time
	sql     string
	args    []any
}

type capturedQuery struct {
	duration time.Duration
	sql      string
	args     []any
}

var queryCapture struct {
	sync.Mutex
	queries []capturedQuery
}

type requestTrace struct {
	queries atomic.Int64
	nanos   atomic.Int64
}

type queryTracer struct{}

func (queryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, startedKey{}, queryStart{started: time.Now(), sql: data.SQL, args: slices.Clone(data.Args)})
}

func (queryTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryEndData) {
	started, ok := ctx.Value(startedKey{}).(queryStart)
	if !ok {
		return
	}
	duration := time.Since(started.started)
	recordQuery(ctx, float64(duration)/float64(time.Millisecond))
	if ctx.Value(traceKey{}) != nil && isExplainable(started.sql) {
		queryCapture.Lock()
		queryCapture.queries = append(queryCapture.queries, capturedQuery{duration: duration, sql: started.sql, args: started.args})
		queryCapture.Unlock()
	}
}

func configureTrace(config *pgxpool.Config) { config.ConnConfig.Tracer = queryTracer{} }

func recordQuery(ctx context.Context, milliseconds float64) {
	trace, _ := ctx.Value(traceKey{}).(*requestTrace)
	if trace == nil {
		return
	}
	trace.queries.Add(1)
	trace.nanos.Add(int64(milliseconds * float64(time.Millisecond)))
}

type tracedResponseWriter struct {
	http.ResponseWriter
	trace *requestTrace
	wrote bool
}

func (w *tracedResponseWriter) WriteHeader(status int) {
	if w.wrote {
		return
	}
	w.wrote = true
	w.Header().Set("X-Bench-Queries", fmt.Sprint(w.trace.queries.Load()))
	w.Header().Set("X-Bench-DB-Ms", fmt.Sprintf("%.3f", float64(w.trace.nanos.Load())/float64(time.Millisecond)))
	w.ResponseWriter.WriteHeader(status)
}

func (w *tracedResponseWriter) Write(data []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}

func traceMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		trace := new(requestTrace)
		writer := &tracedResponseWriter{ResponseWriter: w, trace: trace}
		next.ServeHTTP(writer, r.WithContext(context.WithValue(r.Context(), traceKey{}, trace)))
		if !writer.wrote {
			writer.WriteHeader(http.StatusOK)
		}
	})
}

func isExplainable(sql string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(sql))
	return strings.HasPrefix(trimmed, "select") || strings.HasPrefix(trimmed, "with")
}

func explainCaptured(ctx context.Context, pool *pgxpool.Pool, count int, outputDir string) error {
	queryCapture.Lock()
	queries := slices.Clone(queryCapture.queries)
	queryCapture.Unlock()
	slices.SortFunc(queries, func(a, b capturedQuery) int {
		if a.duration > b.duration {
			return -1
		}
		if a.duration < b.duration {
			return 1
		}
		return 0
	})
	count = min(count, len(queries))
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return err
	}
	for index, query := range queries[:count] {
		rows, err := pool.Query(ctx, "EXPLAIN (ANALYZE, BUFFERS) "+query.sql, query.args...)
		if err != nil {
			continue
		}
		var lines []string
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				rows.Close()
				return err
			}
			lines = append(lines, line)
		}
		rows.Close()
		content := fmt.Sprintf("-- captured duration: %s\n-- parameter values intentionally omitted\n%s\n", query.duration, strings.Join(lines, "\n"))
		name := filepath.Join(outputDir, fmt.Sprintf("%02d.txt", index+1))
		if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
			return err
		}
	}
	return nil
}
