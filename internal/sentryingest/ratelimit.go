package sentryingest

import (
	"sync"
	"sync/atomic"
	"time"
)

// DefaultSpikePerMinute is the per-site event cap per minute.
const DefaultSpikePerMinute = 600

const (
	// rejectUnsupported is sent on every 2xx so SDKs stop sending categories
	// that Phase 1a does not store.
	rejectUnsupported = "3600:transaction;span;profile;profile_chunk;replay;log;trace_metric;check_in;feedback:project:not_supported"
	// spikeLimits and budgetLimits accompany a 429.
	spikeLimits  = "60:error;default:project:spike_protection"
	budgetLimits = "60:error;default:project:rate_limit"
)

// spikeGuard caps events per site per minute with a fixed window. It is
// in-memory and per process, on top of the intake's per-key budget.
type spikeGuard struct {
	mu    sync.Mutex
	limit int
	sites map[int32]*spikeWindow
}

type spikeWindow struct {
	start time.Time
	count int
}

func newSpikeGuard(limit int) *spikeGuard {
	if limit <= 0 {
		limit = DefaultSpikePerMinute
	}
	return &spikeGuard{limit: limit, sites: map[int32]*spikeWindow{}}
}

// take admits n events for a site, all or none.
func (g *spikeGuard) take(siteID int32, n int, now time.Time) bool {
	if n <= 0 {
		return true
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	w := g.sites[siteID]
	if w == nil {
		if len(g.sites) > 10_000 {
			for id, other := range g.sites {
				if now.Sub(other.start) >= time.Minute {
					delete(g.sites, id)
				}
			}
		}
		w = &spikeWindow{start: now}
		g.sites[siteID] = w
	}
	if now.Sub(w.start) >= time.Minute {
		w.start, w.count = now, 0
	}
	if w.count+n > g.limit {
		return false
	}
	w.count += n
	return true
}

// Stats counts what the front door dropped or ignored, by category, in
// memory. Categories are item types ("item:transaction"), "too_large",
// "attachment_too_large", "minidump_deferred", "invalid" and so on.
type Stats struct {
	mu     sync.Mutex
	counts map[string]*atomic.Uint64
}

// Add counts n occurrences of a category.
func (s *Stats) Add(category string, n uint64) {
	if s == nil || n == 0 {
		return
	}
	s.mu.Lock()
	if s.counts == nil {
		s.counts = map[string]*atomic.Uint64{}
	}
	c := s.counts[category]
	if c == nil {
		if len(s.counts) >= 256 { // a hostile sender cannot grow the map
			category = "other"
			c = s.counts[category]
		}
		if c == nil {
			c = new(atomic.Uint64)
			s.counts[category] = c
		}
	}
	s.mu.Unlock()
	c.Add(n)
}

// Snapshot returns the current counts.
func (s *Stats) Snapshot() map[string]uint64 {
	out := map[string]uint64{}
	if s == nil {
		return out
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, c := range s.counts {
		out[k] = c.Load()
	}
	return out
}
