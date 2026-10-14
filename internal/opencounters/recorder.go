package opencounters

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"supermetry/internal/ingest"
)

const maxKeys = 200_000

type ViewKey struct {
	Key string
	Day string
}

type Store interface {
	Upsert(context.Context, map[ViewKey]int64) error
	Totals(context.Context, []string) (map[string]int64, error)
}

type Recorder struct {
	store   Store
	newCap  ingest.RateLimiter
	mu      sync.Mutex
	rows    map[ViewKey]int64
	totals  map[string]int64
	known   map[string]bool
	cold    map[string]bool
	flushMu sync.Mutex
}

func NewRecorder(store Store, newPerMinute float64, burst int) *Recorder {
	return &Recorder{
		store: store, newCap: ingest.NewTokenBuckets(newPerMinute/60, burst),
		rows: map[ViewKey]int64{}, totals: map[string]int64{}, known: map[string]bool{}, cold: map[string]bool{},
	}
}

func (r *Recorder) Total(key string) int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.totals[key]; !ok && !r.known[key] && len(r.cold) < maxKeys {
		r.cold[key] = true
	}
	return r.totals[key]
}

func (r *Recorder) View(key, day string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.known[key] {
		if len(r.known) >= maxKeys || !r.newCap.Allow("new") {
			return
		}
		r.known[key] = true
		delete(r.cold, key)
	}
	r.rows[ViewKey{key, day}]++
	r.totals[key]++
}

func (r *Recorder) Flush(ctx context.Context) error {
	r.flushMu.Lock()
	defer r.flushMu.Unlock()
	r.mu.Lock()
	rows, cold := r.rows, make([]string, 0, len(r.cold))
	r.rows = map[ViewKey]int64{}
	for key := range r.cold {
		cold = append(cold, key)
	}
	r.cold = map[string]bool{}
	r.mu.Unlock()
	if len(rows) > 0 {
		if err := r.store.Upsert(ctx, rows); err != nil {
			r.mu.Lock()
			for key, views := range rows {
				r.rows[key] += views
			}
			r.mu.Unlock()
			return err
		}
	}
	if len(cold) == 0 {
		return nil
	}
	totals, err := r.store.Totals(ctx, cold)
	if err != nil {
		return err
	}
	r.mu.Lock()
	for key, total := range totals {
		r.totals[key] += total
		r.known[key] = true
	}
	r.mu.Unlock()
	return nil
}

func (r *Recorder) Run(ctx context.Context) error {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			flushCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			err := r.Flush(flushCtx)
			cancel()
			return err
		case <-ticker.C:
			if err := r.Flush(ctx); err != nil {
				slog.Warn("open counter flush failed", "err", err)
			}
		}
	}
}
