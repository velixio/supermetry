package pixels

import (
	"sync"
	"time"
)

type TotalCache struct {
	mu     sync.RWMutex
	totals map[int64]int64
	days   map[int64]map[string]int64
}

func NewTotalCache() *TotalCache {
	return &TotalCache{totals: map[int64]int64{}, days: map[int64]map[string]int64{}}
}

// AddDay counts a view on a site-local day so the stats badge never reads the database.
func (c *TotalCache) AddDay(id int64, day string, n int64) {
	c.mu.Lock()
	if c.days[id] == nil {
		c.days[id] = map[string]int64{}
	}
	c.days[id][day] += n
	c.mu.Unlock()
}

// SeedDays merges persisted per-day counts, keeping the larger value so views not yet
// flushed are not lost, and forgets days before cutoff (YYYY-MM-DD).
func (c *TotalCache) SeedDays(persisted map[int64]map[string]int64, cutoff string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, days := range c.days {
		for day := range days {
			if day < cutoff {
				delete(days, day)
			}
		}
		if len(days) == 0 {
			delete(c.days, id)
		}
	}
	for id, days := range persisted {
		for day, n := range days {
			if day < cutoff {
				continue
			}
			if c.days[id] == nil {
				c.days[id] = map[string]int64{}
			}
			if n > c.days[id][day] {
				c.days[id][day] = n
			}
		}
	}
}

// Series returns the last n days ending at today, oldest first.
func (c *TotalCache) Series(id int64, today string, n int) []int64 {
	end, err := time.Parse(time.DateOnly, today)
	out := make([]int64, n)
	if err != nil {
		return out
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	for i := range out {
		out[i] = c.days[id][end.AddDate(0, 0, i-n+1).Format(time.DateOnly)]
	}
	return out
}

func (c *TotalCache) Get(id int64) int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.totals[id]
}

func (c *TotalCache) Add(id, n int64) {
	c.mu.Lock()
	c.totals[id] += n
	c.mu.Unlock()
}

func (c *TotalCache) Seed(persisted map[int64]int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, n := range persisted {
		if n > c.totals[id] {
			c.totals[id] = n
		}
	}
}

func (c *TotalCache) Reconcile(persisted map[int64]int64, active map[int64]struct{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id := range c.totals {
		if _, ok := active[id]; !ok {
			delete(c.totals, id)
		}
	}
	for id, n := range persisted {
		if _, ok := active[id]; ok && n > c.totals[id] {
			c.totals[id] = n
		}
	}
}
