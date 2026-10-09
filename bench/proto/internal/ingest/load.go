package ingest

import (
	"context"
	"io"
	"time"

	"supermetry/bench/proto/internal/core"
	"supermetry/bench/proto/internal/enrich"
)

// LoadOptions configure a bulk load.
type LoadOptions struct {
	ParseWorkers int
	Pipeline     Config
	Enrich       *enrich.Real // nil: use the record's parsed dims (CONTRACT ground truth)
}

// LoadStats summarise a bulk load.
type LoadStats struct {
	Events   int64         `json:"events"`
	Sessions int64         `json:"sessions_upserted"`
	Elapsed  time.Duration `json:"elapsed_ns"`
	PerSec   float64       `json:"events_per_s"`
}

// Load streams NDJSON through the same pipeline as HTTP ingest (build →
// session cache → bounded queue → COPY batchers, session upserts), using
// each record's ts instead of now. Deviation: salts for historical dates are
// random and kept in memory only (never persisted), so a day split across
// two load runs would get two salts.
func Load(ctx context.Context, w Writer, sites *Sites, r io.Reader, o LoadOptions) (LoadStats, error) {
	start := time.Now()
	cache := core.NewCache(core.CacheConfig{})
	cfg := o.Pipeline
	cfg.Clock = cache.Watermark // event time drives idle eviction
	p := New(cfg, w, cache)
	b := &Builder{Sites: sites, Salts: core.NewSalts(nil), Enrich: o.Enrich}
	n, err := ReadEvents(ctx, r, b, o.ParseWorkers, func(ev *core.Event) error { return p.Submit(ctx, ev) })
	cerr := p.Close(context.Background())
	if err == nil {
		err = cerr
	}
	st := p.Stats()
	el := time.Since(start)
	return LoadStats{Events: n, Sessions: st.SessionsUpserted, Elapsed: el, PerSec: float64(n) / el.Seconds()}, err
}
