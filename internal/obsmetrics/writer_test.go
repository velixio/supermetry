package obsmetrics

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/db"
	"supermetry/internal/testpg"
)

func fixture(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into orgs(name,slug) values('Org','org');
insert into sites(id,org_id,public_id,name,timezone,retention_class,kind) overriding system value
values(7,1,'appsite00007','App','UTC',30,'app')`); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureDailyPartitions(ctx, pool, "metric_points", testNow.AddDate(0, 0, -2), testNow.AddDate(0, 0, 2)); err != nil {
		t.Fatal(err)
	}
	return pool
}

func newWriter(t *testing.T, pool *pgxpool.Pool, cfg WriterConfig, stats Stats) *Writer {
	t.Helper()
	reg, err := LoadRegistry(context.Background(), pool, cfg.SeriesCap)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Now = func() time.Time { return testNow }
	return NewWriter(pool, reg, cfg, stats)
}

func gaugePoint(name string, i int, v float64) Point {
	k := SeriesKey{Name: name, Kind: "gauge", Attrs: map[string]string{"i": strconv.Itoa(i)}}
	return Point{SiteID: 7, SeriesKey: k, Hash: k.Hash(), TS: testNow.Add(-time.Duration(i) * time.Second), Value: &v}
}

func count(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestMetricsWriter(t *testing.T) {
	pool := fixture(t)
	ctx := context.Background()
	w := newWriter(t, pool, WriterConfig{}, nil)
	var batch []Point
	for i := 0; i < 1000; i++ {
		batch = append(batch, gaugePoint("m", i%10, float64(i)))
	}
	if dropped, ok := w.Enqueue(batch); !ok || dropped != 0 {
		t.Fatalf("enqueue: dropped %d ok %v", dropped, ok)
	}
	w.Flush(ctx)
	if n := count(t, pool, `select count(*) from metric_series where site_id=7`); n != 10 {
		t.Fatalf("series = %d, want 10", n)
	}
	if n := count(t, pool, `select count(*) from metric_points where site_id=7`); n != 1000 {
		t.Fatalf("points = %d, want 1000", n)
	}
	var idBefore int64
	_ = pool.QueryRow(ctx, `select id from metric_series where attrs->>'i'='3'`).Scan(&idBefore)

	// A restart loads the registry and reuses the ids.
	w2 := newWriter(t, pool, WriterConfig{}, nil)
	if _, ok := w2.Enqueue([]Point{gaugePoint("m", 3, 1)}); !ok {
		t.Fatal("enqueue after restart")
	}
	w2.Flush(ctx)
	if n := count(t, pool, `select count(*) from metric_series where site_id=7`); n != 10 {
		t.Fatalf("series after restart = %d, want 10", n)
	}
	if n := count(t, pool, `select count(*) from metric_points where series_id=$1`, idBefore); n != 101 {
		t.Fatalf("points of series = %d, want 101", n)
	}
}

func TestWriterCumulativeBaselineThenDelta(t *testing.T) {
	pool := fixture(t)
	w := newWriter(t, pool, WriterConfig{}, nil)
	k := SeriesKey{Name: "requests", Kind: "counter"}
	cum := func(at time.Time, v float64) Point {
		return Point{SiteID: 7, SeriesKey: k, Hash: k.Hash(), TS: at, Cumulative: true, Start: s1, Value: &v}
	}
	// The server just restarted: the writer's delta cache is empty, so the
	// first reading (a huge running total) must not become a spike.
	if _, ok := w.Enqueue([]Point{cum(testNow.Add(-2*time.Minute), 1_000_000), cum(testNow.Add(-time.Minute), 1_000_030)}); !ok {
		t.Fatal("enqueue")
	}
	w.Flush(context.Background())
	var n int
	var v float64
	if err := pool.QueryRow(context.Background(), `select count(*), coalesce(max(value),0) from metric_points where site_id=7`).Scan(&n, &v); err != nil {
		t.Fatal(err)
	}
	if n != 1 || v != 30 {
		t.Fatalf("rows=%d max=%v, want 1 row with delta 30", n, v)
	}
}

func TestWriterHistogramDeltaAndBoundsChange(t *testing.T) {
	pool := fixture(t)
	w := newWriter(t, pool, WriterConfig{}, nil)
	hp := func(at time.Time, bounds []float64, count int64, sumV float64, b ...int64) Point {
		k := SeriesKey{Name: "lat", Kind: "histogram", Bounds: bounds}
		return Point{SiteID: 7, SeriesKey: k, Hash: k.Hash(), TS: at, Cumulative: true, Start: s1, Count: &count, Sum: &sumV, Buckets: b}
	}
	b3 := []float64{10, 50, 100}
	w.Enqueue([]Point{
		hp(testNow.Add(-2*time.Minute), b3, 7, 90, 2, 4, 1, 0),
		hp(testNow.Add(-time.Minute), b3, 25, 600, 5, 14, 5, 1),
		// New bounds: a new series whose first reading is only a baseline.
		hp(testNow.Add(-90*time.Second), []float64{10, 100}, 3, 30, 1, 1, 1),
		hp(testNow.Add(-time.Minute), []float64{10, 100}, 5, 50, 2, 2, 1),
	})
	w.Flush(context.Background())
	if n := count(t, pool, `select count(*) from metric_series where name='lat'`); n != 2 {
		t.Fatalf("series = %d, want 2 (changed bounds are a new series)", n)
	}
	var c int64
	var s float64
	var b []int64
	if err := pool.QueryRow(context.Background(), `select count, sum, bucket_counts from metric_points p join metric_series s on s.id=p.series_id
		where cardinality(s.bounds)=3`).Scan(&c, &s, &b); err != nil {
		t.Fatal(err)
	}
	if c != 18 || s != 510 || len(b) != 4 || b[0] != 3 || b[1] != 10 || b[2] != 4 || b[3] != 1 {
		t.Fatalf("delta = %d %v %v", c, s, b)
	}
}

func TestWriterSeriesCapAndQueueFull(t *testing.T) {
	pool := fixture(t)
	st := &countStats{}
	w := newWriter(t, pool, WriterConfig{SeriesCap: 3, QueuePoints: 6}, st)
	var batch []Point
	for i := 0; i < 4; i++ {
		batch = append(batch, gaugePoint("m", i, 1))
	}
	dropped, ok := w.Enqueue(batch)
	if !ok || dropped != 1 || st.m["metric_series_limit"] != 1 {
		t.Fatalf("dropped %d ok %v stats %v", dropped, ok, st.m)
	}
	if dropped, ok := w.Enqueue([]Point{gaugePoint("m", 0, 2)}); !ok || dropped != 0 {
		t.Fatal("a known series must still be admitted at the cap")
	}
	// 4 queued of 6: five more do not fit, and nothing is queued.
	var more []Point
	for i := 0; i < 5; i++ {
		more = append(more, gaugePoint("m", 0, 3))
	}
	if _, ok := w.Enqueue(more); ok || st.m["metric_queue_full"] != 5 {
		t.Fatalf("queue full: ok=%v stats %v", ok, st.m)
	}
	w.Flush(context.Background())
	if n := count(t, pool, `select count(*) from metric_points`); n != 3+1 {
		t.Fatalf("points = %d, want 4", n)
	}
}

func TestWriterDropsPointsWithoutPartition(t *testing.T) {
	pool := fixture(t)
	st := &countStats{}
	w := newWriter(t, pool, WriterConfig{}, st)
	p := gaugePoint("m", 0, 1)
	p.TS = testNow.AddDate(0, 0, -10)
	w.Enqueue([]Point{p, gaugePoint("m", 0, 2)})
	w.Flush(context.Background())
	if st.m["metric_no_partition"] != 1 || count(t, pool, `select count(*) from metric_points`) != 1 {
		t.Fatalf("stats %v", st.m)
	}
}
