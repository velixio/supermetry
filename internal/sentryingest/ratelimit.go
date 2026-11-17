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
	// perfUnsupported is sent instead of rejectUnsupported for projects with
	// performance on: transactions and spans are accepted.
	perfUnsupported = "3600:profile;profile_chunk;replay;log;trace_metric;check_in;feedback:project:not_supported"
	// spanLimits accompanies a span or transaction that was over the project's
	// span budget.
	spanLimits = "60:transaction;span:project:rate_limit"
	// spikeLimits and budgetLimits accompany a 429.
	spikeLimits  = "60:error;default:project:spike_protection"
	budgetLimits = "60:error;default:project:rate_limit"
	ipLimits     = "60:error;default:ip:rate_limit"
)

// unsupportedLimits is the not_supported quota list sent on every 2xx.
func unsupportedLimits(perf bool) string {
	if perf {
		return perfUnsupported
	}
	return rejectUnsupported
}

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

// Defaults for the per-client-IP request limit and the in-flight body cap.
const (
	DefaultIPPerMinute = 600
	ipBurst            = 120
	DefaultMaxInflight = 32
	maxTrackedIPs      = 100_000
	// ipSweepEvery bounds how often a full table is scanned for evictable
	// buckets, so a flood of new addresses costs one scan per interval, not
	// one per request.
	ipSweepEvery = time.Second
)

// ipLimiter is an in-memory token bucket per client address, checked before
// any body is read. Addresses are keys of a bounded map and are never stored.
type ipLimiter struct {
	mu        sync.Mutex
	perSec    float64
	buckets   map[string]*ipBucket
	lastSweep time.Time
	sweeps    int // for tests
}

type ipBucket struct {
	tokens float64
	last   time.Time
}

func newIPLimiter(perMinute int) *ipLimiter {
	if perMinute == 0 {
		perMinute = DefaultIPPerMinute
	}
	if perMinute < 0 {
		return nil // disabled
	}
	return &ipLimiter{perSec: float64(perMinute) / 60, buckets: map[string]*ipBucket{}}
}

func (l *ipLimiter) allow(key string, now time.Time) bool {
	if l == nil {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.buckets[key]
	if b == nil {
		if len(l.buckets) >= maxTrackedIPs {
			if l.sweeps == 0 || now.Sub(l.lastSweep) >= ipSweepEvery {
				l.sweep(now)
			}
			if len(l.buckets) >= maxTrackedIPs {
				return true // fail open rather than grow without bound
			}
		}
		b = &ipBucket{tokens: ipBurst, last: now}
		l.buckets[key] = b
	}
	if el := now.Sub(b.last).Seconds(); el > 0 {
		b.tokens = min(ipBurst, b.tokens+el*l.perSec)
		b.last = now
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// sweep drops buckets idle long enough to have refilled completely: such a
// bucket behaves exactly like a new one, so dropping it changes no decision.
func (l *ipLimiter) sweep(now time.Time) {
	l.lastSweep = now
	l.sweeps++
	refill := time.Duration(float64(ipBurst) / l.perSec * float64(time.Second))
	for k, v := range l.buckets {
		if now.Sub(v.last) >= refill {
			delete(l.buckets, k)
		}
	}
}
