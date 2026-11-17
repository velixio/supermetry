package spans

import (
	"sync"
	"time"
)

// Budget is a per-site token bucket in memory: perMinute spans of burst,
// refilled continuously. A perMinute of zero or less means unlimited.
type Budget struct {
	perMinute float64
	mu        sync.Mutex
	sites     map[int32]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

// NewBudget builds a budget of perMinute spans per site per minute.
func NewBudget(perMinute int) *Budget {
	return &Budget{perMinute: float64(perMinute), sites: map[int32]*bucket{}}
}

// Take returns how many of n spans fit in the site's budget at now, and
// consumes them.
func (b *Budget) Take(siteID int32, n int, now time.Time) int {
	if b == nil || b.perMinute <= 0 || n <= 0 {
		return max(n, 0)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	s, ok := b.sites[siteID]
	if !ok {
		s = &bucket{tokens: b.perMinute, last: now}
		b.sites[siteID] = s
	}
	if el := now.Sub(s.last); el > 0 {
		s.tokens = min(b.perMinute, s.tokens+el.Seconds()*b.perMinute/60)
		s.last = now
	}
	take := min(n, int(s.tokens))
	s.tokens -= float64(take)
	return take
}
