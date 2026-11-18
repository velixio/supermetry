package obsmetrics

import (
	"sync"
	"time"
)

const (
	deltaTTL        = time.Hour
	deltaMaxEntries = 1_000_000
)

type deltaEntry struct {
	start   time.Time
	seen    time.Time
	value   float64
	count   int64
	sum     float64
	buckets []int64
}

// DeltaCache remembers the last cumulative reading of each series so counters
// and histograms can be stored as per-interval deltas. It is in memory only: a
// first sighting (including the first after a server restart) is a baseline
// and yields no delta, never the whole running total as a spike. Entries expire
// after an hour without a point.
type DeltaCache struct {
	mu      sync.Mutex
	now     func() time.Time
	max     int
	entries map[string]*deltaEntry
	swept   time.Time
}

// NewDeltaCache builds a cache; now may be nil.
func NewDeltaCache(now func() time.Time) *DeltaCache {
	return newDeltaCache(now, deltaMaxEntries)
}

func newDeltaCache(now func() time.Time, max int) *DeltaCache {
	if now == nil {
		now = time.Now
	}
	return &DeltaCache{now: now, max: max, entries: map[string]*deltaEntry{}}
}

// lookup returns the live entry for key, or nil. Caller holds mu.
func (c *DeltaCache) lookup(key []byte, now time.Time) *deltaEntry {
	e := c.entries[string(key)]
	if e != nil && now.Sub(e.seen) >= deltaTTL {
		delete(c.entries, string(key))
		return nil
	}
	return e
}

// store makes room (sweeping expired entries at most once a second) and adds a
// new entry; when the cache is still full the baseline is simply not kept.
func (c *DeltaCache) store(key []byte, e *deltaEntry, now time.Time) {
	if len(c.entries) >= c.max && now.Sub(c.swept) >= time.Second {
		c.swept = now
		for k, v := range c.entries {
			if now.Sub(v.seen) >= deltaTTL {
				delete(c.entries, k)
			}
		}
	}
	if _, ok := c.entries[string(key)]; ok || len(c.entries) < c.max {
		c.entries[string(key)] = e
	}
}

// Counter converts a cumulative reading to a delta. A new start time or a
// decrease is a reset: the delta is the new cumulative value.
func (c *DeltaCache) Counter(key []byte, start time.Time, cumulative float64) (float64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	prev := c.lookup(key, now)
	if prev == nil {
		c.store(key, &deltaEntry{start: start, seen: now, value: cumulative}, now)
		return 0, false
	}
	delta := cumulative - prev.value
	if !prev.start.Equal(start) || delta < 0 {
		delta = cumulative
	}
	prev.start, prev.value, prev.seen = start, cumulative, now
	return delta, true
}

// Histogram converts a cumulative histogram reading to a delta, with the same
// first-sighting and reset rules as Counter. A change in bucket count is a
// reset too.
func (c *DeltaCache) Histogram(key []byte, start time.Time, count int64, sum float64, buckets []int64) (int64, float64, []int64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	prev := c.lookup(key, now)
	keep := append([]int64(nil), buckets...)
	if prev == nil {
		c.store(key, &deltaEntry{start: start, seen: now, count: count, sum: sum, buckets: keep}, now)
		return 0, 0, nil, false
	}
	reset := !prev.start.Equal(start) || count < prev.count || len(prev.buckets) != len(buckets)
	if !reset {
		for i := range buckets {
			if buckets[i] < prev.buckets[i] {
				reset = true
				break
			}
		}
	}
	var dCount int64
	var dSum float64
	dBuckets := make([]int64, len(buckets))
	if reset {
		dCount, dSum = count, sum
		copy(dBuckets, buckets)
	} else {
		dCount, dSum = count-prev.count, sum-prev.sum
		for i := range buckets {
			dBuckets[i] = buckets[i] - prev.buckets[i]
		}
	}
	prev.start, prev.count, prev.sum, prev.buckets, prev.seen = start, count, sum, keep, now
	return dCount, dSum, dBuckets, true
}

// Len returns the number of remembered series.
func (c *DeltaCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}
