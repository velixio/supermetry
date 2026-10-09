package db_test

import (
	"context"
	"testing"
	"time"

	"supermetry/internal/db"
	"supermetry/internal/testpg"
)

func TestHeatmapMigrationAndMonthlyPartitions(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	from := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	if err := db.EnsurePartitions(ctx, pool, from, to); err != nil {
		t.Fatal(err)
	}

	var partitions int
	if err := pool.QueryRow(ctx, `
select count(*)
from pg_inherits
where inhparent = 'heatmap_cells'::regclass`).Scan(&partitions); err != nil {
		t.Fatal(err)
	}
	if partitions != 3 {
		t.Fatalf("heatmap partitions = %d, want 3", partitions)
	}

	if _, err := pool.Exec(ctx, `
insert into heatmap_cells(site_id,day,path_id,device,gx,gy,clicks)
values (1,'2026-10-08',2,0,99,5000,3)`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
insert into heatmap_cells(site_id,day,path_id,device,gx,gy,clicks)
values (1,'2026-10-08',2,3,0,0,1)`); err == nil {
		t.Fatal("invalid device must be rejected")
	}
}
