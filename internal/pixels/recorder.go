package pixels

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	flushEvery = 10 * time.Second
	maxKeys    = 100_000
)

type ViewKey struct {
	PixelID int64
	Day     string
	Country string
	Via     string
}

type WriteFunc func(ctx context.Context, rows map[ViewKey]int64) error

type Recorder struct {
	write   WriteFunc
	totals  *TotalCache
	mu      sync.Mutex
	rows    map[ViewKey]int64
	flushMu sync.Mutex
	dropped atomic.Int64
}

func NewRecorder(write WriteFunc, totals *TotalCache) *Recorder {
	return &Recorder{write: write, totals: totals, rows: map[ViewKey]int64{}}
}

func (r *Recorder) Dropped() int64 { return r.dropped.Load() }

func (r *Recorder) View(k ViewKey) {
	r.mu.Lock()
	if _, ok := r.rows[k]; !ok && len(r.rows) >= maxKeys {
		r.mu.Unlock()
		r.dropped.Add(1)
		return
	}
	r.rows[k]++
	r.mu.Unlock()
	r.totals.Add(k.PixelID, 1)
}

func (r *Recorder) Flush(ctx context.Context) error {
	r.flushMu.Lock()
	defer r.flushMu.Unlock()
	r.mu.Lock()
	rows := r.rows
	r.rows = map[ViewKey]int64{}
	r.mu.Unlock()
	if len(rows) == 0 {
		return nil
	}
	if err := r.write(ctx, rows); err != nil {
		r.mu.Lock()
		for k, n := range rows {
			if _, ok := r.rows[k]; ok || len(r.rows) < maxKeys {
				r.rows[k] += n
			} else {
				r.dropped.Add(n)
			}
		}
		r.mu.Unlock()
		return err
	}
	return nil
}

func (r *Recorder) Run(ctx context.Context) error {
	ticker := time.NewTicker(flushEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			flushCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			return r.Flush(flushCtx)
		case <-ticker.C:
			if err := r.Flush(ctx); err != nil {
				slog.Warn("pixel view flush failed", "err", err)
			}
		}
	}
}

func PoolWriter(pool *pgxpool.Pool) WriteFunc {
	return func(ctx context.Context, rows map[ViewKey]int64) error {
		batch := &pgx.Batch{}
		for k, n := range rows {
			batch.Queue(`
				insert into pixel_views_daily(pixel_id, day, country, via, views)
				select $1, $2::date, $3, $4, $5 where exists (select 1 from pixels where id=$1)
				on conflict (pixel_id, day, country, via)
				do update set views = pixel_views_daily.views + excluded.views`,
				k.PixelID, k.Day, k.Country, k.Via, n)
		}
		return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
			return tx.SendBatch(ctx, batch).Close()
		})
	}
}
