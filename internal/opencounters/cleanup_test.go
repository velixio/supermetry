package opencounters

import (
	"context"
	"testing"
	"time"

	"supermetry/internal/db"
	"supermetry/internal/testpg"
)

func TestCleanupDeletesIdleCountersAndOldRows(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var idle, live int64
	if err := pool.QueryRow(ctx, `insert into open_counters(key,last_seen_day) values ('github.com/idle','2025-10-13') returning id`).Scan(&idle); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into open_counters(key,last_seen_day) values ('github.com/live','2026-10-14') returning id`).Scan(&live); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into open_counter_views_daily values ($1,'2025-10-13',1),($2,'2025-09-01',7),($2,'2026-10-14',2)`, idle, live); err != nil {
		t.Fatal(err)
	}
	n, err := Cleanup(ctx, pool, time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC))
	if err != nil || n != 1 {
		t.Fatalf("deleted = %d, %v", n, err)
	}
	var views int64
	if err := pool.QueryRow(ctx, `select coalesce(sum(views),0) from open_counter_views_daily`).Scan(&views); err != nil || views != 2 {
		t.Fatalf("remaining views = %d, err %v", views, err)
	}
}
