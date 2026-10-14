package pixels

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"
)

type Registry struct {
	load   func(context.Context) ([]Pixel, error)
	seed   func(context.Context) (map[int64]int64, error)
	totals *TotalCache
	byID   atomic.Pointer[map[string]Pixel]
}

func (r *Registry) Run(ctx context.Context, interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := r.Reload(ctx); err != nil {
				slog.Warn("reload pixel registry", "err", err)
			}
		}
	}
}

func NewRegistry(load func(context.Context) ([]Pixel, error), seed func(context.Context) (map[int64]int64, error), totals *TotalCache) *Registry {
	r := &Registry{load: load, seed: seed, totals: totals}
	empty := map[string]Pixel{}
	r.byID.Store(&empty)
	return r
}

func (r *Registry) Totals() *TotalCache { return r.totals }

func (r *Registry) ByPublicID(id string) (Pixel, bool) {
	p, ok := (*r.byID.Load())[id]
	return p, ok
}

func (r *Registry) Reload(ctx context.Context) error {
	list, err := r.load(ctx)
	if err != nil {
		return err
	}
	totals, err := r.seed(ctx)
	if err != nil {
		return err
	}
	next := make(map[string]Pixel, len(list))
	active := make(map[int64]struct{}, len(list))
	for _, p := range list {
		next[p.PublicID] = p
		active[p.ID] = struct{}{}
	}
	r.totals.Reconcile(totals, active)
	r.byID.Store(&next)
	return nil
}
