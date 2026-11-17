package spans

import (
	"context"
	"testing"
	"time"

	"supermetry/internal/db"
	"supermetry/internal/testpg"
)

// BenchmarkWriterCopy measures end-to-end writer throughput into Postgres:
// 100,000 spans per iteration, enqueued in transaction-sized batches.
func BenchmarkWriterCopy(b *testing.B) {
	pool, _ := testpg.New(b)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		b.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into orgs(name,slug) values('Org','org')`); err != nil {
		b.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into sites(id,org_id,public_id,name,timezone,retention_class,kind) overriding system value
		values(7,1,'appsite00001','App','UTC',30,'app')`); err != nil {
		b.Fatal(err)
	}
	now := time.Now()
	if err := db.EnsureDailyPartitions(ctx, pool, "spans", now.AddDate(0, 0, -1), now.AddDate(0, 0, 2)); err != nil {
		b.Fatal(err)
	}
	const total = 100_000
	w := NewWriter(pool, WriterConfig{QueueSpans: total, Batch: 5000}, nil, nil)
	status := int16(200)
	template := make([]Span, total)
	for i := range template {
		s := Span{SiteID: 7, Start: now.Add(-time.Duration(i%3600) * time.Second), DurationUS: int64(500 + i%90000),
			Name: "GET /api/items/*", Op: "http.server", Description: "GET /api/items/*", Status: "ok",
			Release: "1.4.2", Environment: "production", Platform: "go", IsSegment: i%10 == 0, HTTPStatus: &status,
			Attrs: map[string]string{"http.method": "GET", "http.status_code": "200"}}
		s.TraceID[0], s.TraceID[1], s.TraceID[2] = byte(i), byte(i>>8), byte(i>>16)
		s.SpanID[0], s.SpanID[1] = byte(i), byte(i>>8)
		s.SegmentID = s.SpanID
		template[i] = s
	}
	b.ReportAllocs()
	b.ResetTimer()
	start := time.Now()
	for n := 0; n < b.N; n++ {
		for off := 0; off < total; off += 100 {
			if !w.Enqueue(append([]Span(nil), template[off:off+100]...)) {
				b.Fatal("queue full")
			}
		}
		w.flushAll(ctx)
	}
	b.ReportMetric(float64(total*b.N)/time.Since(start).Seconds(), "spans/s")
}
