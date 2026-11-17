package db

import (
	"context"
	"testing"
	"time"

	"supermetry/internal/testpg"
)

func TestEnsureDailyPartitionsIsIdempotentAndDaily(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 11, 18, 5, 0, 0, 0, time.UTC)
	for i := 0; i < 2; i++ {
		if err := EnsureDailyPartitions(ctx, pool, "spans", from, from.AddDate(0, 0, 3)); err != nil {
			t.Fatal(err)
		}
	}
	var names []string
	rows, err := pool.Query(ctx, `select c.relname from pg_partition_tree('spans') p join pg_class c on c.oid=p.relid where p.isleaf order by 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		names = append(names, n)
	}
	// EnsurePartitions from Migrate may add days; the three requested exist.
	want := map[string]bool{"spans_20261118": false, "spans_20261119": false, "spans_20261120": false}
	for _, n := range names {
		if _, ok := want[n]; ok {
			want[n] = true
		}
	}
	for n, ok := range want {
		if !ok {
			t.Errorf("missing partition %s in %v", n, names)
		}
	}
	if _, err := pool.Exec(ctx, `insert into spans(site_id,start_ts,duration_us,bucket,trace_id,span_id,segment_id,is_segment,name)
		values(1,'2026-11-19T23:59:59Z',1,0,decode(repeat('01',16),'hex'),decode(repeat('02',8),'hex'),decode(repeat('02',8),'hex'),true,'x')`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pool.QueryRow(ctx, `select count(*) from spans_20261119`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("row not in daily partition: %d %v", n, err)
	}
}
