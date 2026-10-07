package core

import (
	"sync"
	"sync/atomic"
)

// dict interns the low-cardinality, visitor-constant dimensions (country,
// browser, os, device) so a cached session holds 2-byte IDs instead of
// 16-byte string headers. ID 0 is "". Past 65535 values new strings map to 0.
// Reads are lock-free (copy-on-write snapshot); inserts are rare.
type dict struct {
	mu   sync.RWMutex
	ids  map[string]uint16
	vals atomic.Pointer[[]string]
}

var dims = newDict()

func newDict() *dict {
	d := &dict{ids: map[string]uint16{"": 0}}
	v := []string{""}
	d.vals.Store(&v)
	return d
}

func (d *dict) id(s string) uint16 {
	d.mu.RLock()
	id, ok := d.ids[s]
	d.mu.RUnlock()
	if ok {
		return id
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if id, ok := d.ids[s]; ok {
		return id
	}
	old := *d.vals.Load()
	if len(old) > 0xFFFF {
		return 0
	}
	id = uint16(len(old))
	nv := append(old[:len(old):len(old)], s) // never mutate a published snapshot
	d.ids[s] = id
	d.vals.Store(&nv)
	return id
}

func (d *dict) str(id uint16) string { return (*d.vals.Load())[id] }
