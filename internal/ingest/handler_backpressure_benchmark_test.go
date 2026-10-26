package ingest_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"supermetry/internal/db"
	"supermetry/internal/dict"
	"supermetry/internal/enrich"
	"supermetry/internal/health"
	"supermetry/internal/ingest"
	"supermetry/internal/salts"
	"supermetry/internal/session"
	"supermetry/internal/sites"
	"supermetry/internal/testpg"
	"supermetry/internal/writer"
)

func BenchmarkHandlerCapacity(b *testing.B) {
	for _, saturated := range []bool{false, true} {
		name := "warm_normal"
		if saturated {
			name = "warm_saturated"
		}
		b.Run(name, func(b *testing.B) {
			b.StopTimer()
			pool, _ := testpg.New(b)
			ctx := context.Background()
			if err := db.Migrate(ctx, pool); err != nil {
				b.Fatal(err)
			}
			clk := &fakeClock{now: fakeNow()}
			saltStore := salts.New(pool, clk)
			if err := saltStore.Refresh(ctx); err != nil {
				b.Fatal(err)
			}
			enricher, err := enrich.New("")
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() { _ = enricher.Close() })
			sessions := session.NewMemory(4, clk)
			// No database consumer is needed: all timed events fit. Fixtures, warming
			// and queue allocation are outside the timer; normal never becomes full.
			queueSize := b.N + 1
			if saturated {
				queueSize = 1
			}
			h := &ingest.Handler{Sites: siteRegistry{site: sites.Site{ID: 1, PublicID: "abc123", TZ: time.UTC, Retention: db.Ret30, AllowedHosts: []string{"supermetry.com"}}}, Enricher: enricher, Salts: saltStore, Dict: dict.New(pool), Sessions: sessions, Clock: clk, Writer: writer.New(nil, sessions, queueSize, time.Hour, time.Hour)}
			request := func() int {
				req := httptest.NewRequest(http.MethodPost, "/api/event", strings.NewReader(okBody))
				req.Header.Set("User-Agent", testUA)
				req.RemoteAddr = "198.51.100.7:5555"
				response := httptest.NewRecorder()
				h.ServeHTTP(response, req)
				return response.Code
			}
			if status := request(); status != http.StatusAccepted {
				b.Fatalf("warmup status=%d", status)
			}
			want := http.StatusAccepted
			if saturated {
				want = http.StatusServiceUnavailable
			}
			accepted, dropped, writerDropped := health.IngestAccepted.Load(), health.IngestDropped.Load(), writer.Dropped.Load()
			b.ReportAllocs()
			b.ResetTimer()
			b.StartTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					if status := request(); status != want {
						b.Errorf("status=%d, want %d", status, want)
					}
				}
			})
			b.StopTimer()
			if saturated {
				if health.IngestDropped.Load()-dropped != uint64(b.N) || writer.Dropped.Load()-writerDropped != int64(b.N) || health.IngestAccepted.Load() != accepted {
					b.Fatal("saturated request counters differ")
				}
			} else if health.IngestAccepted.Load()-accepted != uint64(b.N) || health.IngestDropped.Load() != dropped || writer.Dropped.Load() != writerDropped {
				b.Fatal("normal request counters differ")
			}
		})
	}
}
