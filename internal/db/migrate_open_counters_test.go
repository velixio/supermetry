package db_test

import (
	"context"
	"testing"

	"supermetry/internal/db"
	"supermetry/internal/testpg"
)

func TestOpenCountersMigration(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := pool.QueryRow(ctx, `insert into open_counters(key,last_seen_day) values ('github.com/a','2026-10-14') returning id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into open_counters(key,last_seen_day) values ('github.com/a','2026-10-14')`); err == nil {
		t.Fatal("duplicate key accepted")
	}
	if _, err := pool.Exec(ctx, `insert into open_counters(key,last_seen_day) values ('GitHub.com/A','2026-10-14')`); err == nil {
		t.Fatal("uppercase key accepted")
	}
	if _, err := pool.Exec(ctx, `insert into open_counter_views_daily(counter_id,day,views) values ($1,'2026-10-14',2)`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `delete from open_counters where id=$1`, id); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := pool.QueryRow(ctx, `select count(*) from open_counter_views_daily`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("daily rows after cascade = %d, err %v", left, err)
	}
}
