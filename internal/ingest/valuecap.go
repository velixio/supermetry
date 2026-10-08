package ingest

import (
	"sync"
	"time"

	"panel/internal/dict"
)

// OtherValue replaces values past the per-site, per-day distinct limit.
const OtherValue = "Other"

type capKey struct {
	site int32
	day  int32
	kind dict.Kind
}

// valueCap bounds distinct dimension values per site, local day and kind in
// process memory. The closer makes the closed day exact (top 200 by sessions).
type valueCap struct {
	mu     sync.Mutex
	limit  int
	newest int32
	seen   map[capKey]map[string]struct{}
}

func newValueCap(limit int) *valueCap {
	return &valueCap{limit: limit, seen: make(map[capKey]map[string]struct{})}
}

// Admit returns value, or OtherValue once limit other values were admitted.
func (c *valueCap) Admit(site int32, localDay time.Time, kind dict.Kind, value string) string {
	if value == "" {
		return ""
	}
	year, month, dayOfMonth := localDay.Date()
	day := int32(time.Date(year, month, dayOfMonth, 0, 0, 0, 0, time.UTC).Unix() / 86400)

	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.seen) == 0 || day > c.newest {
		c.newest = day
		for key := range c.seen {
			if key.day < day-1 {
				delete(c.seen, key)
			}
		}
	}
	if day < c.newest-1 {
		return value
	}
	key := capKey{site: site, day: day, kind: kind}
	set := c.seen[key]
	if set == nil {
		set = make(map[string]struct{})
		c.seen[key] = set
	}
	if _, ok := set[value]; !ok {
		if len(set) >= c.limit {
			return OtherValue
		}
		set[value] = struct{}{}
	}
	return value
}
