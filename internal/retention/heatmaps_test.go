package retention_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"supermetry/internal/db"
	"supermetry/internal/retention"
	"supermetry/internal/testpg"
)

func TestRunDropsOnlyHeatmapPartitionsWhollyOlderThanNinetyDays(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
create table heatmap_cells_202605 partition of heatmap_cells for values from ('2026-05-01') to ('2026-06-01');
create table heatmap_cells_202606 partition of heatmap_cells for values from ('2026-06-01') to ('2026-07-01');
create table heatmap_cells_202607 partition of heatmap_cells for values from ('2026-07-01') to ('2026-08-01');
create table heatmap_cells_202608 partition of heatmap_cells for values from ('2026-08-01') to ('2026-09-01');
create table heatmap_cells_202609 partition of heatmap_cells for values from ('2026-09-01') to ('2026-10-01')`); err != nil {
		t.Fatal(err)
	}

	dropped, err := retention.Run(ctx, pool, time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"heatmap_cells_202605", "heatmap_cells_202606"} {
		if !slices.Contains(dropped, want) {
			t.Errorf("dropped = %v, missing %s", dropped, want)
		}
	}
	for _, want := range []string{"heatmap_cells_202607", "heatmap_cells_202608", "heatmap_cells_202609"} {
		if slices.Contains(dropped, want) {
			t.Errorf("dropped = %v, unexpectedly contains %s", dropped, want)
		}
	}
}
