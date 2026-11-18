package appmonitors

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeRecorder struct {
	mu      sync.Mutex
	got     []CheckIn
	err     error
	release chan struct{}
}

func (f *fakeRecorder) Record(_ context.Context, ci CheckIn) error {
	if f.release != nil {
		<-f.release
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got = append(f.got, ci)
	return f.err
}

type countStats struct {
	mu sync.Mutex
	m  map[string]uint64
}

func (c *countStats) Add(k string, n uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]uint64{}
	}
	c.m[k] += n
}

func (c *countStats) get(k string) uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.m[k]
}

func TestWriterDrainsOnClose(t *testing.T) {
	rec := &fakeRecorder{}
	w := NewWriter(rec, 16, nil)
	for i := 0; i < 5; i++ {
		if !w.Enqueue(CheckIn{Slug: "a"}) {
			t.Fatal("enqueue refused")
		}
	}
	w.Close()
	w.Close() // safe twice
	if len(rec.got) != 5 {
		t.Fatalf("recorded %d, want 5", len(rec.got))
	}
	if w.Enqueue(CheckIn{}) {
		t.Fatal("a closed writer accepts nothing")
	}
}

func TestWriterQueueFullDropsAndCounts(t *testing.T) {
	rec := &fakeRecorder{release: make(chan struct{})}
	stats := &countStats{}
	w := NewWriter(rec, 2, stats)
	w.Enqueue(CheckIn{}) // taken by the loop, which then blocks in Record
	deadline := time.Now().Add(2 * time.Second)
	for len(w.ch) != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	accepted := 0
	for i := 0; i < 6; i++ {
		if w.Enqueue(CheckIn{}) {
			accepted++
		}
	}
	if accepted != 2 || stats.get("checkin_queue_full") != 4 {
		t.Fatalf("accepted=%d full=%d, want 2 and 4", accepted, stats.get("checkin_queue_full"))
	}
	close(rec.release)
	w.Close()
}

func TestWriterCountsPermanentFailuresWithoutRetry(t *testing.T) {
	for err, category := range map[error]string{
		ErrUnknownMonitor: "checkin_unknown_monitor", ErrInvalidConfig: "checkin_invalid_config", ErrMonitorLimit: "checkin_monitor_limit",
	} {
		rec := &fakeRecorder{err: err}
		stats := &countStats{}
		w := NewWriter(rec, 4, stats)
		w.Enqueue(CheckIn{})
		w.Close()
		if stats.get(category) != 1 || len(rec.got) != 1 {
			t.Errorf("%v: %s=%d attempts=%d, want 1 and 1", err, category, stats.get(category), len(rec.got))
		}
	}
}

func TestWriterRetriesOnceThenCountsWriteFailed(t *testing.T) {
	rec := &fakeRecorder{err: errors.New("db down")}
	stats := &countStats{}
	w := NewWriter(rec, 4, stats)
	w.retry = time.Millisecond
	w.Enqueue(CheckIn{})
	w.Close()
	if len(rec.got) != 2 || stats.get("checkin_write_failed") != 1 {
		t.Fatalf("attempts=%d failed=%d, want 2 and 1", len(rec.got), stats.get("checkin_write_failed"))
	}
}
