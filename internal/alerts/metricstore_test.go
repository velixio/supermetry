package alerts

import (
	"context"
	"fmt"
	"math"
	"testing"
	"time"

	"supermetry/internal/appfixture"
	"supermetry/internal/sites"
	"supermetry/internal/spans"
)

var metricHour = time.Date(2026, 11, 16, 8, 0, 0, 0, time.UTC)

func TestMetricStoreErrorCountFilters(t *testing.T) {
	f := appfixture.New(t)
	store := NewMetricStore(f.Pool, nil, nil)
	for i := 0; i < 12; i++ {
		f.Insert(t, appfixture.Event{ID: fmt.Sprintf("mp%d", i), Kind: "error", Env: "production", Platform: "ios", Release: "9.9.9", At: metricHour.Add(time.Duration(i) * time.Minute)})
	}
	for i := 0; i < 5; i++ {
		f.Insert(t, appfixture.Event{ID: fmt.Sprintf("ms%d", i), Kind: "crash", Env: "staging", Platform: "ios", Release: "9.9.9", At: metricHour.Add(time.Duration(i) * time.Minute)})
	}
	f.Insert(t, appfixture.Event{ID: "msess", Kind: "session", Env: "production", Platform: "ios", Release: "9.9.9", At: metricHour})
	f.Insert(t, appfixture.Event{ID: "mlate", Kind: "error", Env: "production", Platform: "ios", Release: "9.9.9", At: metricHour.Add(time.Hour)})
	ctx := context.Background()
	from, to := metricHour, metricHour.Add(time.Hour)
	for _, tc := range []struct {
		name string
		f    Filters
		want float64
	}{
		{"production", Filters{Environments: []string{"production"}, Releases: []string{"9.9.9"}}, 12},
		{"all environments", Filters{Releases: []string{"9.9.9"}}, 17},
		{"crash kind", Filters{Kinds: []string{"crash"}, Releases: []string{"9.9.9"}}, 5},
		{"other platform", Filters{Platforms: []string{"android"}, Releases: []string{"9.9.9"}}, 0},
	} {
		got, err := store.ErrorCount(ctx, f.SiteID, from, to, tc.f)
		if err != nil || got != tc.want {
			t.Errorf("%s: ErrorCount() = %v, %v, want %v", tc.name, got, err, tc.want)
		}
	}
}

func TestMetricStoreCrashFree(t *testing.T) {
	f := appfixture.New(t)
	store := NewMetricStore(f.Pool, nil, nil)
	ctx := context.Background()
	// 40 sessions: 2 crashed, 1 abnormal, 37 healthy. 10 installs (A..J): A and
	// B had a crashed session, C an abnormal one.
	for i := 0; i < 40; i++ {
		status, install := "exited", string(rune('A'+i%10))
		switch i {
		case 0, 1:
			status = "crashed"
			install = string(rune('A' + i))
		case 2:
			status = "abnormal"
			install = "C"
		}
		if _, err := f.Pool.Exec(ctx, `insert into app_sessions(site_id,started,session_hash,install_hash,release,environment,platform,status,errors,updated_at)
			values($1,$2,$3,$4,'9.9.9','production','ios',$5,0,$2)`, f.SiteID, metricHour.Add(time.Duration(i)*time.Minute), appfixture.Hash(fmt.Sprintf("cf%d", i)), appfixture.Hash(install), status); err != nil {
			t.Fatal(err)
		}
	}
	flt := Filters{Releases: []string{"9.9.9"}}
	ratio, sample, err := store.CrashFree(ctx, f.SiteID, metricHour, metricHour.Add(time.Hour), flt, false)
	if err != nil || sample != 40 || math.Abs(ratio-0.925) > 1e-9 {
		t.Fatalf("sessions: ratio=%v sample=%d err=%v, want 0.925 over 40", ratio, sample, err)
	}
	ratio, sample, err = store.CrashFree(ctx, f.SiteID, metricHour, metricHour.Add(time.Hour), flt, true)
	if err != nil || sample != 10 || math.Abs(ratio-0.7) > 1e-9 {
		t.Fatalf("users: ratio=%v sample=%d err=%v, want 0.7 over 10", ratio, sample, err)
	}
	ratio, sample, err = store.CrashFree(ctx, f.SiteID, metricHour.Add(2*time.Hour), metricHour.Add(3*time.Hour), flt, false)
	if err != nil || sample != 0 || ratio != 1 {
		t.Fatalf("empty window: ratio=%v sample=%d err=%v", ratio, sample, err)
	}
}

func TestPerfStoreSumsClosedSegmentRows(t *testing.T) {
	f := appfixture.New(t)
	ctx := context.Background()
	hour := metricHour
	seed := func(h time.Time, segment bool, name, release string, count, errs int64, buckets []int16, counts []int64, state string) {
		if _, err := f.Pool.Exec(ctx, `insert into span_hourly(site_id,local_hour,is_segment,name,op,group_hash,release,environment,platform,count,errors,sum_us,buckets,bucket_counts)
			values($1,$2,$3,$4,'http.server',0,$5,'production','ios',$6,$7,0,$8,$9)`, f.SiteID, h, segment, name, release, count, errs, buckets, counts); err != nil {
			t.Fatal(err)
		}
		if _, err := f.Pool.Exec(ctx, `insert into span_hour_state(site_id,local_hour,status) values($1,$2,$3) on conflict do nothing`, f.SiteID, h, state); err != nil {
			t.Fatal(err)
		}
	}
	seed(hour, true, "GET /items", "1.0", 60, 1, []int16{10, 20}, []int64{50, 10}, "done")
	seed(hour, true, "GET /items", "2.0", 40, 2, []int16{10, 30}, []int64{30, 10}, "done")
	seed(hour, true, "GET /other", "1.0", 500, 50, []int16{10}, []int64{500}, "done")
	seed(hour, false, "GET /items", "1.0", 999, 0, []int16{10}, []int64{999}, "done")
	seed(hour.Add(time.Hour), true, "GET /items", "1.0", 77, 7, []int16{10}, []int64{77}, "dirty")

	perf := NewPerfStore(f.Pool)
	count, errs, hist, err := perf.Hourly(ctx, f.SiteID, hour, hour.Add(2*time.Hour), "GET /items", Filters{})
	if err != nil || count != 100 || errs != 3 || len(hist) != 3 || hist[10] != 80 || hist[20] != 10 || hist[30] != 10 {
		t.Fatalf("Hourly() = %d, %d, %v, %v, want 100, 3, {10:80 20:10 30:10}", count, errs, hist, err)
	}

	store := NewMetricStore(f.Pool, perf, staticRegistry{site: sites.Site{ID: f.SiteID, TZ: time.UTC}})
	rate, sample, err := store.Perf(ctx, f.SiteID, hour, hour.Add(2*time.Hour), "GET /items", Filters{}, MetricTransactionFailRate)
	if err != nil || sample != 100 || math.Abs(rate-3) > 1e-9 {
		t.Fatalf("failure rate = %v over %d, %v, want 3 over 100", rate, sample, err)
	}
	p95, _, err := store.Perf(ctx, f.SiteID, hour, hour.Add(2*time.Hour), "GET /items", Filters{}, "transaction_p95")
	if want := spans.Estimate(30) / 1000; err != nil || math.Abs(p95-want) > 1e-9 {
		t.Fatalf("p95 = %v, %v, want %v ms (rank 95 of 100 lands in bucket 30)", p95, err, want)
	}
	rel, _, _, err := perf.Hourly(ctx, f.SiteID, hour, hour.Add(time.Hour), "GET /items", Filters{Releases: []string{"2.0"}})
	if err != nil || rel != 40 {
		t.Fatalf("release filter count = %d, %v, want 40", rel, err)
	}
}
