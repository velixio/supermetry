package spans

import (
	"sync"
	"time"
)

// SpanKey identifies a span within a site: the same (trace, span) pair sent
// twice is one span.
type SpanKey struct {
	Site  int32
	Trace [16]byte
	Span  [8]byte
}

// KeyOf returns the dedup key of a span.
func KeyOf(s *Span) SpanKey { return SpanKey{s.SiteID, s.TraceID, s.SpanID} }

// Recent remembers recently claimed span keys so an SDK retry of a container
// without an event id is stored once. It is in memory only (no database read
// on the hot path) and bounded: two rotating generations, so a key is
// remembered for between ttl and 2*ttl, and when a generation fills up the
// older one is dropped early, which evicts the oldest keys first. Total keys
// never exceed max.
type Recent struct {
	mu      sync.Mutex
	half    int
	ttl     time.Duration
	rotated time.Time
	cur     map[SpanKey]struct{}
	prev    map[SpanKey]struct{}
}

// NewRecent builds a set holding at most max keys for about ttl.
func NewRecent(max int, ttl time.Duration) *Recent {
	half := max / 2
	if half < 1 {
		half = 1
	}
	return &Recent{half: half, ttl: ttl, cur: map[SpanKey]struct{}{}, prev: map[SpanKey]struct{}{}}
}

// Claim reports whether k is new and records it; false means a duplicate.
func (r *Recent) Claim(k SpanKey, now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.rotated.IsZero() {
		r.rotated = now
	}
	if age := now.Sub(r.rotated); age >= 2*r.ttl {
		r.prev, r.cur, r.rotated = map[SpanKey]struct{}{}, map[SpanKey]struct{}{}, now
	} else if age >= r.ttl {
		r.prev, r.cur, r.rotated = r.cur, make(map[SpanKey]struct{}, min(len(r.cur), r.half)), now
	}
	if _, ok := r.cur[k]; ok {
		return false
	}
	if _, ok := r.prev[k]; ok {
		return false
	}
	if len(r.cur) >= r.half {
		r.prev, r.cur, r.rotated = r.cur, make(map[SpanKey]struct{}, min(len(r.cur), r.half)), now
	}
	r.cur[k] = struct{}{}
	return true
}

// Release forgets k, so a retry of a span that was not stored (queue full,
// rate limited) is not mistaken for a duplicate.
func (r *Recent) Release(k SpanKey) {
	r.mu.Lock()
	delete(r.cur, k)
	delete(r.prev, k)
	r.mu.Unlock()
}

// Len is the number of remembered keys.
func (r *Recent) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.cur) + len(r.prev)
}

// DedupeBatch drops repeats of a (site, trace, span) inside one batch, keeping
// the first, in place. It returns the kept spans and how many were dropped.
func DedupeBatch(batch []Span) ([]Span, int) {
	if len(batch) < 2 {
		return batch, 0
	}
	seen := make(map[SpanKey]struct{}, len(batch))
	kept := batch[:0]
	for i := range batch {
		k := KeyOf(&batch[i])
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		kept = append(kept, batch[i])
	}
	return kept, len(batch) - len(kept)
}
