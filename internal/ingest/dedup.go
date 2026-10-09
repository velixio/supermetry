package ingest

import (
	"sync"
	"time"

	"supermetry/internal/health"
)

const (
	dedupTTL         = 15 * time.Minute
	dedupBucketWidth = time.Minute
)

type dedupKey struct {
	siteID int32
	id     string
}

type dedupEntry struct {
	expires  time.Time
	epoch    int64
	reserved bool
}

type dedupBucket struct {
	epoch int64
	keys  map[dedupKey]struct{}
}

// Deduplicator keeps client event IDs in a bounded, process-local timing wheel.
type Deduplicator struct {
	mu          sync.Mutex
	maxEntries  int
	ttl         time.Duration
	bucketWidth time.Duration
	entries     map[dedupKey]dedupEntry
	buckets     []dedupBucket
}

// NewDeduplicator creates a 15-minute event-ID window bounded by maxEntries.
func NewDeduplicator(maxEntries int) *Deduplicator {
	return newDeduplicator(maxEntries, dedupTTL, dedupBucketWidth)
}

func newDeduplicator(maxEntries int, ttl, bucketWidth time.Duration) *Deduplicator {
	if maxEntries < 1 {
		maxEntries = 1
	}
	if ttl <= 0 {
		ttl = dedupTTL
	}
	if bucketWidth <= 0 {
		bucketWidth = dedupBucketWidth
	}
	bucketCount := int(ttl/bucketWidth) + 2
	return &Deduplicator{
		maxEntries: maxEntries, ttl: ttl, bucketWidth: bucketWidth,
		entries: make(map[dedupKey]dedupEntry, min(maxEntries, 1024)),
		buckets: make([]dedupBucket, bucketCount),
	}
}

// Seen reports whether an ID remains inside the deduplication window. A new
// ID is atomically reserved until Record confirms it or Release abandons it.
func (d *Deduplicator) Seen(siteID int32, id string, now time.Time) bool {
	if d == nil || id == "" {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.expire(now)
	key := dedupKey{siteID: siteID, id: id}
	entry, ok := d.entries[key]
	if ok && !now.Before(entry.expires) {
		delete(d.entries, key)
		ok = false
	}
	if ok {
		return true
	}
	d.add(key, now, true)
	return false
}

// Record confirms a reservation after the event is accepted.
func (d *Deduplicator) Record(siteID int32, id string, now time.Time) {
	if d == nil || id == "" {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	key := dedupKey{siteID: siteID, id: id}
	entry, ok := d.entries[key]
	if !ok || !entry.reserved || !now.Before(entry.expires) {
		return
	}
	entry.reserved = false
	d.entries[key] = entry
}

// Release abandons an unconfirmed reservation so a retry can be accepted.
func (d *Deduplicator) Release(siteID int32, id string) {
	if d == nil || id == "" {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	key := dedupKey{siteID: siteID, id: id}
	if entry, ok := d.entries[key]; ok && entry.reserved {
		delete(d.entries, key)
	}
}

func (d *Deduplicator) add(key dedupKey, now time.Time, reserved bool) {
	if len(d.entries) >= d.maxEntries {
		d.evictOldest()
	}
	expires := now.Add(d.ttl)
	epoch := expires.UnixNano() / d.bucketWidth.Nanoseconds()
	index := int(epoch % int64(len(d.buckets)))
	bucket := &d.buckets[index]
	if bucket.epoch != epoch {
		d.clearBucket(bucket, false)
		bucket.epoch = epoch
		bucket.keys = make(map[dedupKey]struct{})
	}
	bucket.keys[key] = struct{}{}
	d.entries[key] = dedupEntry{expires: expires, epoch: epoch, reserved: reserved}
}

func (d *Deduplicator) expire(now time.Time) {
	currentEpoch := now.UnixNano() / d.bucketWidth.Nanoseconds()
	for i := range d.buckets {
		bucket := &d.buckets[i]
		if len(bucket.keys) != 0 && bucket.epoch < currentEpoch {
			d.clearBucket(bucket, false)
		}
	}
}

func (d *Deduplicator) evictOldest() {
	oldest := -1
	for i := range d.buckets {
		if len(d.buckets[i].keys) == 0 {
			continue
		}
		if oldest < 0 || d.buckets[i].epoch < d.buckets[oldest].epoch {
			oldest = i
		}
	}
	if oldest >= 0 {
		d.clearBucket(&d.buckets[oldest], true)
	}
}

func (d *Deduplicator) clearBucket(bucket *dedupBucket, eviction bool) {
	var removed uint64
	for key := range bucket.keys {
		if entry, ok := d.entries[key]; ok && entry.epoch == bucket.epoch {
			delete(d.entries, key)
			removed++
		}
	}
	clear(bucket.keys)
	if eviction && removed != 0 {
		health.IngestDedupEvicted.Add(removed)
	}
}

func (d *Deduplicator) Len() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.entries)
}
