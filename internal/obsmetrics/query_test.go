package obsmetrics

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func seriesByGroup(t *testing.T, out []Series, key string) map[string]Series {
	t.Helper()
	m := map[string]Series{}
	for _, s := range out {
		m[s.Group[key]] = s
	}
	return m
}

func total(s Series) float64 {
	var total float64
	for _, p := range s.Points {
		if p.Value != nil {
			total += *p.Value
		}
	}
	return total
}

func roll(t *testing.T, pool *pgxpool.Pool, site int32, loc *time.Location, from time.Time, hours int) {
	t.Helper()
	for i := 0; i < hours; i++ {
		if err := RecomputeHour(context.Background(), pool, site, loc, from.Add(time.Duration(i)*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestQueryGroupBy(t *testing.T) {
	pool := rollFixture(t)
	ctx := context.Background()
	day := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	// service api: 1+2, web: 10, worker: 100; two hours.
	for svc, vals := range map[string][]float64{"api": {1, 2}, "web": {10}, "worker": {100}} {
		id := addSeries(t, pool, 7, "http.requests", "counter", fmt.Sprintf(`{"service":%q,"region":"eu"}`, svc), nil)
		for i, v := range vals {
			addValue(t, pool, 7, id, day.Add(time.Duration(i)*time.Hour+10*time.Minute), v)
		}
	}
	// Same name on another site must never leak in.
	other := addSeries(t, pool, 8, "http.requests", "counter", `{"service":"api"}`, nil)
	addValue(t, pool, 8, other, day.Add(10*time.Minute), 5000)
	roll(t, pool, 7, time.UTC, day, 3)
	roll(t, pool, 8, time.UTC, day, 3)

	q := MetricQuery{Name: "http.requests", Agg: "sum", GroupBy: []string{"service"}, From: day, To: day.Add(3 * time.Hour), Interval: "1h"}
	out, err := Query(ctx, pool, 7, time.UTC, q)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 {
		t.Fatalf("groups = %d, want 3: %+v", len(out), out)
	}
	by := seriesByGroup(t, out, "service")
	if total(by["api"]) != 3 || total(by["web"]) != 10 || total(by["worker"]) != 100 {
		t.Fatalf("sums api %v web %v worker %v, want 3, 10, 100", total(by["api"]), total(by["web"]), total(by["worker"]))
	}
	if out[0].Group["service"] != "worker" {
		t.Fatalf("groups are ordered by weight, first = %q", out[0].Group["service"])
	}
	// A filter narrows the series.
	q.Filters = map[string]string{"service": "web"}
	out, err = Query(ctx, pool, 7, time.UTC, q)
	if err != nil || len(out) != 1 || total(out[0]) != 10 {
		t.Fatalf("filtered: %v %+v", err, out)
	}
}

func TestQueryGroupCapOther(t *testing.T) {
	pool := rollFixture(t)
	day := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	want := 0.0
	for i := 1; i <= 60; i++ {
		id := addSeries(t, pool, 7, "jobs", "counter", fmt.Sprintf(`{"service":"s%02d"}`, i), nil)
		addValue(t, pool, 7, id, day.Add(10*time.Minute), float64(i))
		if i <= 10 { // the ten smallest end up in "other"
			want += float64(i)
		}
	}
	roll(t, pool, 7, time.UTC, day, 1)
	out, err := Query(context.Background(), pool, 7, time.UTC, MetricQuery{Name: "jobs", Agg: "sum", GroupBy: []string{"service"}, From: day, To: day.Add(time.Hour), Interval: "1h"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 51 {
		t.Fatalf("groups = %d, want 50 + other", len(out))
	}
	last := out[50]
	if last.Group["service"] != "other" || total(last) != want {
		t.Fatalf("other = %v sum %v, want sum %v (1+...+10)", last.Group, total(last), want)
	}
	if out[0].Group["service"] != "s60" {
		t.Fatalf("first = %v", out[0].Group)
	}
}

func TestQueryDayBucketsInSiteZone(t *testing.T) {
	pool := rollFixture(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `insert into sites(id,org_id,public_id,name,timezone,retention_class,kind) overriding system value
values(9,1,'appsite00009','Kolkata','Asia/Kolkata',30,'app')`); err != nil {
		t.Fatal(err)
	}
	kol, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		t.Skip("no tzdata")
	}
	ktm, _ := time.LoadLocation("Asia/Kathmandu")
	idK := addSeries(t, pool, 9, "orders", "counter", `{}`, nil)
	// 2026-11-17T19:00Z is 00:30 IST on 2026-11-18.
	addValue(t, pool, 9, idK, time.Date(2026, 11, 17, 19, 0, 0, 0, time.UTC), 7)
	addValue(t, pool, 9, idK, time.Date(2026, 11, 17, 18, 0, 0, 0, time.UTC), 3) // 23:30 IST on the 17th
	roll(t, pool, 9, kol, time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC), 48)
	from := time.Date(2026, 11, 16, 18, 30, 0, 0, time.UTC) // 2026-11-17 00:00 IST
	out, err := Query(ctx, pool, 9, kol, MetricQuery{Name: "orders", Agg: "sum", From: from, To: from.Add(48 * time.Hour), Interval: "1d"})
	if err != nil || len(out) != 1 {
		t.Fatalf("%v %+v", err, out)
	}
	pts := out[0].Points
	if len(pts) != 2 || !pts[0].TS.Equal(from) || *pts[0].Value != 3 || !pts[1].TS.Equal(from.Add(24*time.Hour)) || *pts[1].Value != 7 {
		t.Fatalf("Kolkata days = %+v, want 11-17 -> 3 and 11-18 -> 7 starting at local midnight", pts)
	}

	// +05:45: local days begin at 18:15Z.
	idN := addSeries(t, pool, 8, "orders", "counter", `{}`, nil)
	addValue(t, pool, 8, idN, time.Date(2026, 11, 17, 18, 10, 0, 0, time.UTC), 2) // 23:55 NPT on the 17th
	addValue(t, pool, 8, idN, time.Date(2026, 11, 17, 18, 20, 0, 0, time.UTC), 5) // 00:05 NPT on the 18th
	roll(t, pool, 8, ktm, time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC), 48)
	nfrom := time.Date(2026, 11, 16, 18, 15, 0, 0, time.UTC)
	out, err = Query(ctx, pool, 8, ktm, MetricQuery{Name: "orders", Agg: "sum", From: nfrom, To: nfrom.Add(48 * time.Hour), Interval: "1d"})
	if err != nil || len(out) != 1 {
		t.Fatalf("%v %+v", err, out)
	}
	pts = out[0].Points
	if len(pts) != 2 || !pts[0].TS.Equal(nfrom) || *pts[0].Value != 2 || !pts[1].TS.Equal(nfrom.Add(24*time.Hour)) || *pts[1].Value != 5 {
		t.Fatalf("Kathmandu days = %+v, want 11-17 -> 2 and 11-18 -> 5 starting 18:15Z", pts)
	}
}

func TestQueryRawGaugeAndRate(t *testing.T) {
	pool := rollFixture(t)
	ctx := context.Background()
	base := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	g := addSeries(t, pool, 7, "mem", "gauge", `{}`, nil)
	for i, v := range []float64{4, 8, 6} { // 10:01, 10:02 in bucket 10:00; 10:06 in 10:05
		at := []time.Duration{time.Minute, 2 * time.Minute, 6 * time.Minute}[i]
		addValue(t, pool, 7, g, base.Add(at), v)
	}
	c := addSeries(t, pool, 7, "reqs", "counter", `{}`, nil)
	addValue(t, pool, 7, c, base.Add(time.Minute), 30)
	addValue(t, pool, 7, c, base.Add(2*time.Minute), 270)

	out, err := Query(ctx, pool, 7, time.UTC, MetricQuery{Name: "mem", Agg: "avg", From: base, To: base.Add(10 * time.Minute), Interval: "5m"})
	if err != nil || len(out) != 1 || len(out[0].Points) != 2 {
		t.Fatalf("%v %+v", err, out)
	}
	if *out[0].Points[0].Value != 6 || *out[0].Points[1].Value != 6 || !out[0].Points[1].TS.Equal(base.Add(5*time.Minute)) {
		t.Fatalf("avg buckets = %+v", out[0].Points)
	}
	out, err = Query(ctx, pool, 7, time.UTC, MetricQuery{Name: "mem", Agg: "last", From: base, To: base.Add(10 * time.Minute), Interval: "5m"})
	if err != nil || *out[0].Points[0].Value != 8 {
		t.Fatalf("last = %v %v", err, out)
	}
	out, err = Query(ctx, pool, 7, time.UTC, MetricQuery{Name: "reqs", Agg: "rate", From: base, To: base.Add(10 * time.Minute), Interval: "5m"})
	if err != nil || *out[0].Points[0].Value != 1 { // 300 over a 300 s bucket
		t.Fatalf("rate = %v %+v", err, out)
	}
}

func TestQueryHistogramPercentile(t *testing.T) {
	pool := rollFixture(t)
	ctx := context.Background()
	id := addSeries(t, pool, 7, "lat", "histogram", `{}`, []float64{10, 50, 100})
	h := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	addHist(t, pool, 7, id, h.Add(time.Minute), 18, 500, []int64{3, 10, 4, 1})
	addHist(t, pool, 7, id, h.Add(2*time.Minute), 2, 40, []int64{2, 0, 0, 0})
	roll(t, pool, 7, time.UTC, h, 1)
	// buckets sum to [5,10,4,1]: p50 = 30 (hand-computed), min 1, max 180 stored.
	for _, hourly := range []bool{false, true} {
		q := MetricQuery{Name: "lat", Agg: "p50", From: h, To: h.Add(time.Hour), Interval: "5m"}
		if hourly {
			q.Interval = "1h"
		}
		out, err := Query(ctx, pool, 7, time.UTC, q)
		if err != nil || len(out) != 1 {
			t.Fatalf("%v %+v", err, out)
		}
		var got float64
		for _, p := range out[0].Points {
			if p.Value != nil {
				got = *p.Value
			}
		}
		if hourly && got != 30 {
			t.Fatalf("hourly p50 = %v, want 30", got)
		}
		if !hourly && got != 30 {
			t.Fatalf("raw p50 = %v, want 30", got)
		}
	}
	if _, err := Query(ctx, pool, 7, time.UTC, MetricQuery{Name: "lat", Agg: "sum", From: h, To: h.Add(time.Hour), Interval: "1h"}); err != nil {
		t.Fatalf("sum on a histogram: %v", err)
	}
}

func TestQueryValidation(t *testing.T) {
	pool := rollFixture(t)
	g := addSeries(t, pool, 7, "mem", "gauge", `{}`, nil)
	_ = g
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	cases := map[string]MetricQuery{
		"no name":       {Agg: "sum", From: now, To: now.Add(time.Hour), Interval: "1h"},
		"bad agg":       {Name: "mem", Agg: "median", From: now, To: now.Add(time.Hour), Interval: "1h"},
		"bad interval":  {Name: "mem", Agg: "sum", From: now, To: now.Add(time.Hour), Interval: "2h"},
		"3 group keys":  {Name: "mem", Agg: "sum", GroupBy: []string{"a", "b", "c"}, From: now, To: now.Add(time.Hour), Interval: "1h"},
		"reversed":      {Name: "mem", Agg: "sum", From: now, To: now.Add(-time.Hour), Interval: "1h"},
		"too many":      {Name: "mem", Agg: "sum", From: now, To: now.AddDate(0, 0, 100), Interval: "1h"},
		"pct on gauge":  {Name: "mem", Agg: "p95", From: now, To: now.Add(time.Hour), Interval: "1h"},
		"rate on gauge": {Name: "mem", Agg: "rate", From: now, To: now.Add(time.Hour), Interval: "1h"},
	}
	for name, q := range cases {
		if _, err := Query(context.Background(), pool, 7, time.UTC, q); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
}

func TestListMetrics(t *testing.T) {
	pool := rollFixture(t)
	addSeries(t, pool, 7, "a.metric", "gauge", `{"service":"x","env":"p"}`, nil)
	addSeries(t, pool, 7, "a.metric", "gauge", `{"service":"y"}`, nil)
	addSeries(t, pool, 7, "b.metric", "counter", `{}`, nil)
	addSeries(t, pool, 8, "c.metric", "counter", `{}`, nil)
	got, err := ListMetrics(context.Background(), pool, 7)
	if err != nil || len(got) != 2 {
		t.Fatalf("%v %+v", err, got)
	}
	if got[0].Name != "a.metric" || got[0].Series != 2 || len(got[0].AttributeKeys) != 2 || got[0].AttributeKeys[0] != "env" || got[1].Kind != "counter" {
		t.Fatalf("%+v", got)
	}
}
