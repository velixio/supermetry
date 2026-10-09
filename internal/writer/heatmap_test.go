package writer

import (
	"context"
	"reflect"
	"testing"
	"time"

	"supermetry/internal/db"
	"supermetry/internal/testpg"
)

func TestAggregateHeatmapsUsesDeviceAndGridCells(t *testing.T) {
	day := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	events := []HeatmapEvent{
		{SiteID: 7, LocalDay: day, PathID: 11, ViewportWidth: 390, Clicks: [][2]int{{0, 0}, {9, 19}, {10, 20}, {1000, 100000}}},
		{SiteID: 7, LocalDay: day, PathID: 11, ViewportWidth: 767, Clicks: [][2]int{{0, 0}}},
		{SiteID: 7, LocalDay: day, PathID: 11, ViewportWidth: 768, Clicks: [][2]int{{10, 20}}},
		{SiteID: 7, LocalDay: day, PathID: 11, ViewportWidth: 1024, Clicks: [][2]int{{10, 20}}},
	}

	got := aggregateHeatmaps(events)
	want := map[heatmapCell]int32{
		{siteID: 7, day: day, pathID: 11, device: DeviceMobile, gx: 0, gy: 0}:     3,
		{siteID: 7, day: day, pathID: 11, device: DeviceMobile, gx: 1, gy: 1}:     1,
		{siteID: 7, day: day, pathID: 11, device: DeviceMobile, gx: 99, gy: 5000}: 1,
		{siteID: 7, day: day, pathID: 11, device: DeviceTablet, gx: 1, gy: 1}:     1,
		{siteID: 7, day: day, pathID: 11, device: DeviceDesktop, gx: 1, gy: 1}:    1,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("aggregateHeatmaps() = %#v, want %#v", got, want)
	}
}

func TestRunHeatmapsFlushesAggregatesOnShutdown(t *testing.T) {
	day := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	w := New(nil, nil, 4, time.Hour, time.Hour, nil)
	if !w.EnqueueHeatmap(HeatmapEvent{SiteID: 1, LocalDay: day, PathID: 2, ViewportWidth: 820, Clicks: [][2]int{{100, 40}, {100, 40}}}) {
		t.Fatal("heatmap rejected")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var flushed map[heatmapCell]int32
	err := w.runHeatmaps(ctx, func(_ context.Context, cells map[heatmapCell]int32) error {
		flushed = make(map[heatmapCell]int32, len(cells))
		for key, clicks := range cells {
			flushed[key] = clicks
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[heatmapCell]int32{{siteID: 1, day: day, pathID: 2, device: DeviceTablet, gx: 10, gy: 2}: 2}
	if !reflect.DeepEqual(flushed, want) {
		t.Fatalf("shutdown flush = %#v, want %#v", flushed, want)
	}
}

func TestEnqueueHeatmapCopiesClicks(t *testing.T) {
	w := New(nil, nil, 1, time.Hour, time.Hour, nil)
	clicks := [][2]int{{10, 20}}
	if !w.EnqueueHeatmap(HeatmapEvent{Clicks: clicks}) {
		t.Fatal("heatmap rejected")
	}
	clicks[0] = [2]int{900, 900}
	queued := <-w.heatmapCh
	if queued.Clicks[0] != [2]int{10, 20} {
		t.Fatalf("queued clicks aliased caller: %v", queued.Clicks)
	}
}

func TestUpsertHeatmapsMergesFlushes(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	if err := db.EnsurePartitions(ctx, pool, day, day.AddDate(0, 1, 0)); err != nil {
		t.Fatal(err)
	}
	w := New(pool, nil, 1, time.Hour, time.Hour, nil)
	key := heatmapCell{siteID: 1, day: day, pathID: 2, device: DeviceDesktop, gx: 3, gy: 4}
	if err := w.upsertHeatmaps(ctx, map[heatmapCell]int32{key: 2}); err != nil {
		t.Fatal(err)
	}
	if err := w.upsertHeatmaps(ctx, map[heatmapCell]int32{key: 3}); err != nil {
		t.Fatal(err)
	}
	var clicks int
	if err := pool.QueryRow(ctx, `select clicks from heatmap_cells where site_id=1 and day=$1 and path_id=2`, day).Scan(&clicks); err != nil {
		t.Fatal(err)
	}
	if clicks != 5 {
		t.Fatalf("clicks = %d, want 5", clicks)
	}
}

func TestRunHeatmapsFlushesWhenCellMapIsLarge(t *testing.T) {
	old := heatmapMaxCells
	heatmapMaxCells = 3
	t.Cleanup(func() { heatmapMaxCells = old })
	day := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	w := New(nil, nil, 4, time.Hour, time.Hour, nil)
	if !w.EnqueueHeatmap(HeatmapEvent{SiteID: 1, LocalDay: day, PathID: 2, ViewportWidth: 1200, Clicks: [][2]int{{0, 0}, {100, 0}, {200, 0}}}) {
		t.Fatal("heatmap rejected")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	flushed := make(chan int, 4)
	done := make(chan error, 1)
	go func() {
		done <- w.runHeatmaps(ctx, func(_ context.Context, cells map[heatmapCell]int32) error {
			flushed <- len(cells)
			return nil
		})
	}()
	select {
	case n := <-flushed:
		if n != 3 {
			t.Fatalf("flushed %d cells, want 3", n)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("large cell map was not flushed before the interval")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
