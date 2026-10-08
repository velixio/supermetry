package writer

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

const heatmapBatchSize = 500

// heatmapMaxCells bounds the in-memory aggregate between flushes. Queue slots
// already cap pending events (and so cells, at 200 clicks per event); this
// flushes early when many distinct cells pile up. A var so tests can lower it.
var heatmapMaxCells = 50_000

const (
	DeviceDesktop int16 = iota
	DeviceMobile
	DeviceTablet
)

// HeatmapEvent is accepted only for the dedicated heatmap intake event. It is
// aggregated before persistence and never written to the raw events table.
type HeatmapEvent struct {
	SiteID        int32
	LocalDay      time.Time
	PathID        int32
	ViewportWidth int
	Clicks        [][2]int
}

type heatmapCell struct {
	siteID int32
	day    time.Time
	pathID int32
	device int16
	gx     int16
	gy     int32
}

// EnqueueHeatmap transfers a defensive copy to the bounded heatmap queue.
func (w *Writer) EnqueueHeatmap(event HeatmapEvent) bool {
	w.stateMu.RLock()
	defer w.stateMu.RUnlock()
	if !w.accepting {
		Dropped.Add(1)
		return false
	}
	select {
	case w.heatmapSlots <- struct{}{}:
	default:
		Dropped.Add(1)
		return false
	}
	event.Clicks = append([][2]int(nil), event.Clicks...)
	select {
	case w.heatmapCh <- event:
		return true
	default:
		<-w.heatmapSlots
		Dropped.Add(1)
		return false
	}
}

func (w *Writer) runHeatmaps(ctx context.Context, write func(context.Context, map[heatmapCell]int32) error) error {
	timer := time.NewTimer(w.flushEvery)
	defer timer.Stop()
	pending := make(map[heatmapCell]int32)
	events := 0
	flush := func(waitCtx, attemptCtx context.Context) error {
		if len(pending) == 0 {
			return nil
		}
		if err := retryOperation(waitCtx, attemptCtx, func(ctx context.Context) error { return write(ctx, pending) }); err != nil {
			return err
		}
		for range events {
			<-w.heatmapSlots
		}
		clear(pending)
		events = 0
		return nil
	}
	for {
		select {
		case event := <-w.heatmapCh:
			mergeHeatmap(pending, event)
			events++
			if events >= heatmapBatchSize || len(pending) >= heatmapMaxCells {
				if err := flush(ctx, context.WithoutCancel(ctx)); err != nil {
					return w.shutdownHeatmaps(pending, events, write)
				}
				timer.Reset(w.flushEvery)
			}
		case <-timer.C:
			if err := flush(ctx, context.WithoutCancel(ctx)); err != nil {
				return w.shutdownHeatmaps(pending, events, write)
			}
			timer.Reset(w.flushEvery)
		case <-ctx.Done():
			return w.shutdownHeatmaps(pending, events, write)
		}
	}
}

func (w *Writer) shutdownHeatmaps(pending map[heatmapCell]int32, events int, write func(context.Context, map[heatmapCell]int32) error) error {
	w.stopAccepting()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	for {
		for {
			select {
			case event := <-w.heatmapCh:
				mergeHeatmap(pending, event)
				events++
			default:
				goto drained
			}
		}
	drained:
		if len(pending) == 0 {
			return nil
		}
		if err := retryOperation(shutdownCtx, shutdownCtx, func(ctx context.Context) error { return write(ctx, pending) }); err != nil {
			return fmt.Errorf("flush heatmaps at shutdown: %w", err)
		}
		for range events {
			<-w.heatmapSlots
		}
		clear(pending)
		events = 0
	}
}

func aggregateHeatmaps(events []HeatmapEvent) map[heatmapCell]int32 {
	cells := make(map[heatmapCell]int32)
	for _, event := range events {
		mergeHeatmap(cells, event)
	}
	return cells
}

func mergeHeatmap(cells map[heatmapCell]int32, event HeatmapEvent) {
	device := DeviceDesktop
	if event.ViewportWidth < 768 {
		device = DeviceMobile
	} else if event.ViewportWidth < 1024 {
		device = DeviceTablet
	}
	for _, click := range event.Clicks {
		gx := min(click[0]/10, 99)
		key := heatmapCell{
			siteID: event.SiteID, day: event.LocalDay, pathID: event.PathID, device: device,
			gx: int16(gx), gy: int32(click[1] / 20),
		}
		cells[key]++
	}
}

func (w *Writer) upsertHeatmaps(ctx context.Context, cells map[heatmapCell]int32) error {
	pool := w.currentPool()
	if pool == nil {
		return errors.New("writer: database pool is nil")
	}
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `create temporary table heatmap_cells_stage (like heatmap_cells including defaults) on commit drop`); err != nil {
			return err
		}
		rows := make([][]any, 0, len(cells))
		for key, clicks := range cells {
			rows = append(rows, []any{key.siteID, key.day, key.pathID, key.device, key.gx, key.gy, clicks})
		}
		columns := []string{"site_id", "day", "path_id", "device", "gx", "gy", "clicks"}
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"heatmap_cells_stage"}, columns, pgx.CopyFromRows(rows)); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			insert into heatmap_cells (site_id, day, path_id, device, gx, gy, clicks)
			select site_id, day, path_id, device, gx, gy, clicks from heatmap_cells_stage
			on conflict (site_id, day, path_id, device, gx, gy)
			do update set clicks = heatmap_cells.clicks + excluded.clicks`)
		return err
	})
}
