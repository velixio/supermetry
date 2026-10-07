package ingest

import (
	"context"
	"io"
	"time"

	"panel/bench/proto/internal/core"
)

// StreamOptions configure rollup-stream.
type StreamOptions struct {
	ParseWorkers int
	FlushRows    int // emit when this many rows are pending (default 200k)
}

// StreamStats summarise a rollup-stream run.
type StreamStats struct {
	Events  int64         `json:"events"`
	Buckets int64         `json:"buckets"`
	Rows    int64         `json:"rows"`
	Elapsed time.Duration `json:"elapsed_ns"`
	PerSec  float64       `json:"events_per_s"`
}

// Stream builds daily rollups straight from NDJSON without storing raw rows
// (tier M-history). It runs the same build/hash/sessionize steps as Load;
// a (site, day) bucket is finalized once the stream (sorted by ts) has
// passed that site's next local midnight, which also ends all its sessions
// (visitor IDs rotate at midnight).
func Stream(ctx context.Context, sites *Sites, r io.Reader, o StreamOptions, emit func([]core.SaltKey, []core.RollupRow) error) (StreamStats, error) {
	if o.FlushRows <= 0 {
		o.FlushRows = 200_000
	}
	start := time.Now()
	agg := core.NewAggregator()
	cache := core.NewCache(core.CacheConfig{TrackPaths: true, NoDirty: true, OnRetire: agg.AddSession})
	salts := core.NewSalts(nil)
	b := &Builder{Sites: sites, Salts: salts}
	timeout := core.SessionTimeout.Microseconds()

	ends := map[core.SaltKey]int64{}
	dayEnd := func(site int32, d core.Day) int64 {
		k := core.SaltKey{Site: site, Day: d}
		e, ok := ends[k]
		if !ok {
			_, end := d.Bounds(sites.ByID(site).Loc)
			e = end.UnixMicro()
			ends[k] = e
		}
		return e
	}
	var st StreamStats
	var pendKeys []core.SaltKey
	var pendRows []core.RollupRow
	flush := func() error {
		if len(pendKeys) == 0 {
			return nil
		}
		if err := emit(pendKeys, pendRows); err != nil {
			return err
		}
		st.Buckets += int64(len(pendKeys))
		st.Rows += int64(len(pendRows))
		pendKeys, pendRows = nil, nil
		return nil
	}
	finalize := func(w int64, all bool) error {
		if all {
			cache.RetireAll()
		} else {
			cache.EvictIf(func(s *core.Session) bool { return w-s.Last > timeout || w >= dayEnd(s.SiteID, s.Day()) })
		}
		for _, k := range agg.Open() {
			if all || w >= dayEnd(k.Site, k.Day) {
				pendKeys = append(pendKeys, k)
				pendRows = append(pendRows, agg.Finalize(k)...)
			}
		}
		if all || len(pendRows) >= o.FlushRows {
			return flush()
		}
		return nil
	}
	const checkEvery = int64(15 * time.Minute / time.Microsecond) // event time
	var lastCheck int64
	n, err := ReadEvents(ctx, r, b, o.ParseWorkers, func(ev *core.Event) error {
		cache.Assign(ev, nil)
		agg.AddEvent(ev)
		if ev.TS-lastCheck >= checkEvery {
			lastCheck = ev.TS
			salts.Purge(ev.Day - 2)
			return finalize(ev.TS, false)
		}
		return nil
	})
	if err == nil {
		err = finalize(0, true)
	}
	st.Events = n
	st.Elapsed = time.Since(start)
	st.PerSec = float64(n) / st.Elapsed.Seconds()
	return st, err
}
