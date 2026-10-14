package pixels

import "sync"

type TotalCache struct {
	mu     sync.RWMutex
	totals map[int64]int64
}

func NewTotalCache() *TotalCache { return &TotalCache{totals: map[int64]int64{}} }

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
