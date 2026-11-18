package retention_test

import (
	"context"
	"testing"
	"time"

	"supermetry/internal/db"
	"supermetry/internal/retention"
)

func TestRunDropsExpiredMetricHourlyPartitions(t *testing.T) {
	pool, now := spansFixture(t, `{}`)
	ctx := context.Background()
	// 90 days plus one day of slack: the cutoff is 2026-08-19 03:00, so a
	// month is dropped once it ended at or before then.
	if err := db.EnsureMonthlyPartitions(ctx, pool, "metric_hourly", time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC), now.AddDate(0, 1, 0)); err != nil {
		t.Fatal(err)
	}
	dropped, err := retention.Run(ctx, pool, now)
	if err != nil {
		t.Fatal(err)
	}
	if !containsPrefix(dropped, "metric_hourly_202605") || !containsPrefix(dropped, "metric_hourly_202607") {
		t.Fatalf("expected old months dropped, got %v", dropped)
	}
	if containsPrefix(dropped, "metric_hourly_202608") || containsPrefix(dropped, "metric_hourly_202611") {
		t.Fatalf("dropped an unexpired month: %v", dropped)
	}
}

func TestEraseRemovesMetricHourly(t *testing.T) {
	pool, now := spansFixture(t, `{}`)
	ctx := context.Background()
	if err := db.EnsureMonthlyPartitions(ctx, pool, "metric_hourly", now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	for _, site := range []int{1, 2} {
		if _, err := pool.Exec(ctx, `insert into metric_hourly(site_id,series_id,hour,samples) values($1::int,$1::bigint,'2026-11-18 01:00',1)`, site); err != nil {
			t.Fatal(err)
		}
	}
	if err := retention.Erase(ctx, pool, 1, 10, 0); err != nil {
		t.Fatal(err)
	}
	var one, two int
	if err := pool.QueryRow(ctx, `select count(*) filter (where site_id=1), count(*) filter (where site_id=2) from metric_hourly`).Scan(&one, &two); err != nil || one != 0 || two != 1 {
		t.Fatalf("after erase: site1=%d site2=%d %v", one, two, err)
	}
}
