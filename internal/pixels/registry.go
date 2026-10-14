package pixels

import (
	"context"
	"sync/atomic"
)

type Registry struct {
	load   func(context.Context) ([]Pixel, error)
	seed   func(context.Context) (map[int64]int64, error)
	totals *TotalCache
	byID   atomic.Pointer[map[string]Pixel]
	seeded atomic.Bool
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
	if !r.seeded.Load() {
		totals, err := r.seed(ctx)
		if err != nil {
			return err
		}
		r.totals.Seed(totals)
		r.seeded.Store(true)
	}
	next := make(map[string]Pixel, len(list))
	for _, p := range list {
		next[p.PublicID] = p
	}
	r.byID.Store(&next)
	return nil
}
