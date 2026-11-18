package obsmetrics

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/db"
	"supermetry/internal/sites"
	"supermetry/internal/spanrollup"
)

func f64(v float64) *float64 { return &v }

func TestPercentile(t *testing.T) {
	bounds := []float64{10, 50, 100}
	counts := []int64{5, 10, 4, 1}
	cases := []struct {
		name string
		q    float64
		min  *float64
		max  *float64
		want float64
	}{
		{"p50 interpolates inside (10,50]", 0.50, nil, nil, 30},
		{"p95 reaches the top of (50,100]", 0.95, nil, nil, 100},
		{"p99 overflow without max is the last bound", 0.99, nil, nil, 100},
		{"p99 overflow with max", 0.99, nil, f64(180), 180},
		{"first bucket starts at min", 0.10, f64(2), nil, 2 + (2.0/5)*8}, // rank 2 of 5 in (2,10]
		{"first bucket starts at 0 without min", 0.10, nil, nil, 4},
	}
	for _, c := range cases {
		if got := Percentile(bounds, counts, c.q, c.min, c.max); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
	if got := Percentile(bounds, []int64{0, 0, 0, 0}, 0.5, nil, nil); !math.IsNaN(got) {
		t.Errorf("empty counts: got %v, want NaN", got)
	}
	if got := Percentile(bounds, []int64{1, 2}, 0.5, nil, nil); !math.IsNaN(got) {
		t.Errorf("mismatched counts: got %v, want NaN", got)
	}
}

// rollFixture adds site 8 in Asia/Kathmandu (+05:45) next to site 7 (UTC).
func rollFixture(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := fixture(t)
	if _, err := pool.Exec(context.Background(), `insert into sites(id,org_id,public_id,name,timezone,retention_class,kind) overriding system value
values(8,1,'appsite00008','Kathmandu','Asia/Kathmandu',30,'app')`); err != nil {
		t.Fatal(err)
	}
	return pool
}

func addSeries(t *testing.T, pool *pgxpool.Pool, site int32, name, kind, attrs string, bounds []float64) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(context.Background(), `insert into metric_series(site_id,name,kind,bounds,attrs,attrs_hash,first_seen,last_seen)
values($1,$2,$3,$4,$5::jsonb,sha256(convert_to(gen_random_uuid()::text,'UTF8')),now(),now()) returning id`, site, name, kind, bounds, attrs).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func addValue(t *testing.T, pool *pgxpool.Pool, site int32, series int64, ts time.Time, v float64) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `insert into metric_points(site_id,series_id,ts,value) values($1,$2,$3,$4)`, site, series, ts, v); err != nil {
		t.Fatal(err)
	}
}

func addHist(t *testing.T, pool *pgxpool.Pool, site int32, series int64, ts time.Time, count int64, sum float64, buckets []int64) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `insert into metric_points(site_id,series_id,ts,count,sum,min,max,bucket_counts) values($1,$2,$3,$4,$5,1,180,$6)`,
		site, series, ts, count, sum, buckets); err != nil {
		t.Fatal(err)
	}
}

func wallHour(y int, m time.Month, d, h int) time.Time {
	return time.Date(y, m, d, h, 0, 0, 0, time.UTC)
}

func TestRollupGauge(t *testing.T) {
	pool := rollFixture(t)
	ctx := context.Background()
	id := addSeries(t, pool, 7, "mem", "gauge", `{}`, nil)
	h10 := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	addValue(t, pool, 7, id, h10.Add(5*time.Minute), 4)
	addValue(t, pool, 7, id, h10.Add(20*time.Minute), 8)
	addValue(t, pool, 7, id, h10.Add(50*time.Minute), 6)
	addValue(t, pool, 7, id, h10.Add(-time.Minute), 99) // 09:59 belongs to hour 9
	if err := RecomputeHour(ctx, pool, 7, time.UTC, wallHour(2026, 11, 18, 10)); err != nil {
		t.Fatal(err)
	}
	var samples int64
	var sum, mn, mx, last float64
	if err := pool.QueryRow(ctx, `select samples,value_sum,value_min,value_max,value_last from metric_hourly where series_id=$1 and hour=$2`,
		id, wallHour(2026, 11, 18, 10)).Scan(&samples, &sum, &mn, &mx, &last); err != nil {
		t.Fatal(err)
	}
	if samples != 3 || sum != 18 || mn != 4 || mx != 8 || last != 6 {
		t.Fatalf("hour 10: samples %d sum %v min %v max %v last %v, want 3, 18 (avg 6), 4, 8, 6", samples, sum, mn, mx, last)
	}
}

func TestRollupCounter(t *testing.T) {
	pool := rollFixture(t)
	ctx := context.Background()
	id := addSeries(t, pool, 7, "requests", "counter", `{}`, nil)
	h := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	for i, v := range []float64{30, 125, 15} {
		addValue(t, pool, 7, id, h.Add(time.Duration(i*20+1)*time.Minute), v)
	}
	if err := RecomputeHour(ctx, pool, 7, time.UTC, wallHour(2026, 11, 18, 10)); err != nil {
		t.Fatal(err)
	}
	var sum float64
	if err := pool.QueryRow(ctx, `select value_sum from metric_hourly where series_id=$1`, id).Scan(&sum); err != nil {
		t.Fatal(err)
	}
	if sum != 170 {
		t.Fatalf("counter hour sum = %v, want 170 (rate %v/s)", sum, 170.0/3600)
	}
}

func TestRollupHistogramMerge(t *testing.T) {
	pool := rollFixture(t)
	ctx := context.Background()
	id := addSeries(t, pool, 7, "lat", "histogram", `{}`, []float64{10, 50, 100})
	h := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	addHist(t, pool, 7, id, h.Add(time.Minute), 18, 500, []int64{3, 10, 4, 1})
	addHist(t, pool, 7, id, h.Add(2*time.Minute), 3, 40, []int64{2, 0, 1, 0})
	if err := RecomputeHour(ctx, pool, 7, time.UTC, wallHour(2026, 11, 18, 10)); err != nil {
		t.Fatal(err)
	}
	var count int64
	var buckets []int64
	if err := pool.QueryRow(ctx, `select count, bucket_counts from metric_hourly where series_id=$1`, id).Scan(&count, &buckets); err != nil {
		t.Fatal(err)
	}
	want := []int64{5, 10, 5, 1}
	if count != 21 || len(buckets) != 4 {
		t.Fatalf("count %d buckets %v", count, buckets)
	}
	for i := range want {
		if buckets[i] != want[i] {
			t.Fatalf("buckets %v, want %v", buckets, want)
		}
	}
}

type fakeRegistry struct{ list []sites.Site }

func (f fakeRegistry) ByPublicID(string) (sites.Site, bool) { return sites.Site{}, false }
func (f fakeRegistry) Site(id int32) (sites.Site, bool) {
	for _, s := range f.list {
		if s.ID == id {
			return s, true
		}
	}
	return sites.Site{}, false
}
func (f fakeRegistry) All() []sites.Site { return f.list }

func hourlyRows(t *testing.T, pool *pgxpool.Pool, series int64) (rows int, sum float64) {
	t.Helper()
	if err := pool.QueryRow(context.Background(), `select count(*), coalesce(sum(value_sum),0) from metric_hourly where series_id=$1`, series).Scan(&rows, &sum); err != nil {
		t.Fatal(err)
	}
	return rows, sum
}

func TestRollupIdempotentLate(t *testing.T) {
	pool := rollFixture(t)
	ctx := context.Background()
	id := addSeries(t, pool, 7, "jobs", "counter", `{}`, nil)
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	hourH := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC) // two days earlier
	if err := db.EnsureDailyPartitions(ctx, pool, "metric_points", hourH, hourH.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	addValue(t, pool, 7, id, hourH.Add(10*time.Minute), 5)
	reg := fakeRegistry{list: []sites.Site{{ID: 7, Kind: sites.KindApp, TZ: time.UTC}}}
	r := NewRollup(pool, func() time.Time { return now })

	// First run: catches up the last 24 h only; hour H (2 days old) is not there.
	if err := r.Run(ctx, reg); err != nil {
		t.Fatal(err)
	}
	if rows, _ := hourlyRows(t, pool, id); rows != 0 {
		t.Fatalf("hour H rolled by catch-up: %d rows", rows)
	}
	if err := RecomputeHour(ctx, pool, 7, time.UTC, spanrollup.Wall(hourH)); err != nil {
		t.Fatal(err)
	}
	if _, sum := hourlyRows(t, pool, id); sum != 5 {
		t.Fatalf("sum = %v, want 5", sum)
	}

	// A late point is written two days after its hour; the writer hook marks it.
	addValue(t, pool, 7, id, hourH.Add(40*time.Minute), 7)
	late := 7.0
	r.NoteLate([]Point{{SiteID: 7, TS: hourH.Add(40 * time.Minute), Value: &late}})
	if err := r.Run(ctx, reg); err != nil {
		t.Fatal(err)
	}
	rows, sum := hourlyRows(t, pool, id)
	if rows != 1 || sum != 12 {
		t.Fatalf("after late point: rows %d sum %v, want 1 and 12", rows, sum)
	}
	// Running again changes nothing, with or without a mark.
	r.NoteLate([]Point{{SiteID: 7, TS: hourH.Add(40 * time.Minute), Value: &late}})
	if err := r.Run(ctx, reg); err != nil {
		t.Fatal(err)
	}
	if rows2, sum2 := hourlyRows(t, pool, id); rows2 != rows || sum2 != sum {
		t.Fatalf("second run changed the rollup: %d %v", rows2, sum2)
	}
}

func TestRollupDirtyOverflowRecomputesLastDay(t *testing.T) {
	pool := rollFixture(t)
	ctx := context.Background()
	id := addSeries(t, pool, 7, "jobs", "counter", `{}`, nil)
	now := time.Date(2026, 11, 18, 12, 30, 0, 0, time.UTC)
	addValue(t, pool, 7, id, now.Add(-20*time.Hour), 3)
	reg := fakeRegistry{list: []sites.Site{{ID: 7, Kind: sites.KindApp, TZ: time.UTC}}}
	r := NewRollup(pool, func() time.Time { return now })
	r.booted = true // as after a normal first run
	batch := make([]Point, 0, maxDirty+1)
	v := 1.0
	for i := 0; i <= maxDirty; i++ {
		batch = append(batch, Point{SiteID: 7, TS: now.Add(-30*24*time.Hour - time.Duration(i)*dirtyGrain), Value: &v})
	}
	r.NoteLate(batch)
	if !r.overflow {
		t.Fatal("overflow flag not set")
	}
	if err := r.Run(ctx, reg); err != nil {
		t.Fatal(err)
	}
	if rows, sum := hourlyRows(t, pool, id); rows != 1 || sum != 3 {
		t.Fatalf("after overflow: rows %d sum %v, want the last-24h hour recomputed", rows, sum)
	}
}

func TestRollupKathmanduHourBounds(t *testing.T) {
	pool := rollFixture(t)
	ctx := context.Background()
	loc, err := time.LoadLocation("Asia/Kathmandu")
	if err != nil {
		t.Skip("no tzdata")
	}
	id := addSeries(t, pool, 8, "temp", "gauge", `{}`, nil)
	// Local hour 10:00 in +05:45 is 04:15Z to 05:15Z.
	addValue(t, pool, 8, id, time.Date(2026, 11, 18, 4, 10, 0, 0, time.UTC), 1)   // local 09:55 -> hour 9
	addValue(t, pool, 8, id, time.Date(2026, 11, 18, 4, 20, 0, 0, time.UTC), 2)   // local 10:05
	addValue(t, pool, 8, id, time.Date(2026, 11, 18, 5, 10, 0, 0, time.UTC), 4)   // local 10:55
	addValue(t, pool, 8, id, time.Date(2026, 11, 18, 5, 20, 0, 0, time.UTC), 100) // local 11:05 -> hour 11
	if err := RecomputeHour(ctx, pool, 8, loc, wallHour(2026, 11, 18, 10)); err != nil {
		t.Fatal(err)
	}
	var samples int64
	var sum float64
	if err := pool.QueryRow(ctx, `select samples, value_sum from metric_hourly where series_id=$1 and hour=$2`, id, wallHour(2026, 11, 18, 10)).Scan(&samples, &sum); err != nil {
		t.Fatal(err)
	}
	if samples != 2 || sum != 6 {
		t.Fatalf("local hour 10 in +05:45: samples %d sum %v, want 2 and 6", samples, sum)
	}
}
