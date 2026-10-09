package ingest

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"supermetry/internal/health"
)

func TestDeduplicatorConcurrentSameIDOnlyOneCommits(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	d := newDeduplicator(10, 15*time.Minute, time.Minute)
	start := make(chan struct{})
	checked := make(chan struct{}, 2)
	commit := make(chan struct{})
	var commits atomic.Int32
	var wg sync.WaitGroup

	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			duplicate := d.Seen(1, "same-event", now)
			checked <- struct{}{}
			<-commit
			if duplicate {
				return
			}
			commits.Add(1)
			d.Record(1, "same-event", now)
		}()
	}

	close(start)
	<-checked
	<-checked
	close(commit)
	wg.Wait()

	if got := commits.Load(); got != 1 {
		t.Fatalf("commits=%d, want 1", got)
	}
}

func TestDeduplicatorWindowAndSiteIsolation(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	d := newDeduplicator(10, 15*time.Minute, time.Minute)

	if d.Seen(1, "event-1", now) {
		t.Fatal("new event reported as duplicate")
	}
	d.Record(1, "event-1", now)
	if !d.Seen(1, "event-1", now.Add(14*time.Minute+59*time.Second)) {
		t.Fatal("event expired before the TTL")
	}
	if d.Seen(2, "event-1", now.Add(time.Minute)) {
		t.Fatal("event id leaked across sites")
	}
	if d.Seen(1, "event-1", now.Add(15*time.Minute)) {
		t.Fatal("event remained after the TTL")
	}
}

func TestDeduplicatorCapEvictsOldestBucket(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	d := newDeduplicator(2, 15*time.Minute, time.Minute)
	evictedBefore := health.IngestDedupEvicted.Load()
	if d.Seen(1, "old", now) {
		t.Fatal("old was already reserved")
	}
	d.Record(1, "old", now)
	if d.Seen(1, "newer", now.Add(time.Minute)) {
		t.Fatal("newer was already reserved")
	}
	d.Record(1, "newer", now.Add(time.Minute))
	if d.Seen(1, "newest", now.Add(2*time.Minute)) {
		t.Fatal("newest was already reserved")
	}
	d.Record(1, "newest", now.Add(2*time.Minute))

	if d.Len() > 2 {
		t.Fatalf("entries=%d, want at most 2", d.Len())
	}
	if !d.Seen(1, "newer", now.Add(2*time.Minute)) || !d.Seen(1, "newest", now.Add(2*time.Minute)) {
		t.Fatal("cap evicted a newer bucket")
	}
	if got := health.IngestDedupEvicted.Load() - evictedBefore; got != 1 {
		t.Fatalf("evicted metric delta=%d, want 1", got)
	}
	if d.Seen(1, "old", now.Add(2*time.Minute)) {
		t.Fatal("oldest bucket was not evicted")
	}
}
