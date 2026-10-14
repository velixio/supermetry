package opencounters

import (
	"container/list"
	"context"
	"log/slog"
	"sync"
	"time"

	"supermetry/internal/ingest"
)

const (
	maxKeys          = 200_000
	coldBatchSize    = 1_000
	passiveColdLimit = 128
	negativeCacheTTL = 5 * time.Minute
)

type ViewKey struct {
	Key string
	Day string
}

type Store interface {
	Upsert(context.Context, map[ViewKey]int64) error
	Totals(context.Context, []string) (map[string]int64, error)
}

type Recorder struct {
	store        Store
	newCap       ingest.RateLimiter
	mu           sync.Mutex
	rows         map[ViewKey]int64
	totals       map[string]int64
	known        map[string]bool
	cold         map[string]bool // true when reached by an accepted proxied view
	passive      *list.List
	passiveIndex map[string]*list.Element
	misses       map[string]time.Time
	now          func() time.Time
	flushMu      sync.Mutex
}

func NewRecorder(store Store, newPerMinute float64, burst int) *Recorder {
	return &Recorder{
		store: store, newCap: ingest.NewTokenBuckets(newPerMinute/60, burst),
		rows: map[ViewKey]int64{}, totals: map[string]int64{}, known: map[string]bool{}, cold: map[string]bool{},
		passive: list.New(), passiveIndex: map[string]*list.Element{}, misses: map[string]time.Time{}, now: time.Now,
	}
}

func (r *Recorder) Total(key string) int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.totals[key]; !ok && !r.known[key] && !r.missCachedLocked(key) {
		r.queueColdLocked(key, false)
	}
	return r.totals[key]
}

func (r *Recorder) View(key, day string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.known[key] {
		if len(r.known) >= maxKeys || !r.newCap.Allow("new") {
			return
		}
		delete(r.misses, key)
		r.queueColdLocked(key, true)
		r.known[key] = true
	}
	r.rows[ViewKey{key, day}]++
	r.totals[key]++
}

func (r *Recorder) missCachedLocked(key string) bool {
	expires, ok := r.misses[key]
	if ok && r.now().Before(expires) {
		return true
	}
	delete(r.misses, key)
	return false
}

func (r *Recorder) queueColdLocked(key string, counted bool) {
	if existing, ok := r.cold[key]; ok {
		if counted && !existing {
			r.cold[key] = true
			r.removePassiveLocked(key)
		} else if elem := r.passiveIndex[key]; elem != nil {
			r.passive.MoveToBack(elem)
		}
		return
	}
	if counted {
		if len(r.cold) < maxKeys {
			r.cold[key] = true
		}
		return
	}
	if r.passive.Len() >= passiveColdLimit {
		oldest := r.passive.Front()
		oldKey := oldest.Value.(string)
		delete(r.passiveIndex, oldKey)
		delete(r.cold, oldKey)
		r.passive.Remove(oldest)
	}
	r.cold[key] = false
	r.passiveIndex[key] = r.passive.PushBack(key)
}

func (r *Recorder) removePassiveLocked(key string) {
	if elem := r.passiveIndex[key]; elem != nil {
		r.passive.Remove(elem)
		delete(r.passiveIndex, key)
	}
}

func (r *Recorder) Flush(ctx context.Context) error {
	r.flushMu.Lock()
	defer r.flushMu.Unlock()
	r.mu.Lock()
	cold := make([]string, 0, min(coldBatchSize, len(r.cold)))
	selected := make(map[string]bool, cap(cold))
	for key, counted := range r.cold {
		if counted && len(cold) < coldBatchSize {
			cold = append(cold, key)
			selected[key] = true
		}
	}
	for elem := r.passive.Front(); elem != nil && len(cold) < coldBatchSize; elem = elem.Next() {
		key := elem.Value.(string)
		cold = append(cold, key)
		selected[key] = false
	}
	for key := range selected {
		delete(r.cold, key)
		r.removePassiveLocked(key)
	}
	rows := make(map[ViewKey]int64)
	for key, views := range r.rows {
		if _, stillCold := r.cold[key.Key]; stillCold {
			continue
		}
		rows[key] = views
		delete(r.rows, key)
	}
	r.mu.Unlock()
	if len(cold) > 0 {
		totals, err := r.store.Totals(ctx, cold)
		if err != nil {
			r.mu.Lock()
			for key := range selected {
				r.queueColdLocked(key, selected[key])
			}
			for key, views := range rows {
				r.rows[key] += views
			}
			r.mu.Unlock()
			return err
		}
		r.mu.Lock()
		expires := r.now().Add(negativeCacheTTL)
		for _, key := range cold {
			if total, ok := totals[key]; ok {
				r.totals[key] += total
				r.known[key] = true
			} else {
				r.misses[key] = expires
			}
		}
		r.mu.Unlock()
	}
	if len(rows) > 0 {
		if err := r.store.Upsert(ctx, rows); err != nil {
			r.mu.Lock()
			for key, views := range rows {
				r.rows[key] += views
			}
			r.mu.Unlock()
			return err
		}
	}
	return nil
}

func (r *Recorder) Run(ctx context.Context) error {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			flushCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			err := r.Flush(flushCtx)
			cancel()
			return err
		case <-ticker.C:
			if err := r.Flush(ctx); err != nil {
				slog.Warn("open counter flush failed", "err", err)
			}
		}
	}
}
