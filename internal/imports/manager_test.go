package imports

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestProcessFileDispatchesBySource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.csv")
	contents := "created_at,session_id,event_type,url_path\n2026-01-01T00:00:00Z,s1,1,/\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	aggregate, stats, err := ProcessFile(context.Background(), SourceUmami, path)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Rows != 1 || aggregate.Days[date(t, "2026-01-01")].Visitors != 1 {
		t.Fatalf("aggregate=%+v stats=%+v", aggregate, stats)
	}
	if _, _, err := ProcessFile(context.Background(), Source("unknown"), path); err == nil {
		t.Fatal("expected unsupported source error")
	}
}

func TestManagerProcessesOneJobAtATimeAndRemovesFiles(t *testing.T) {
	store := &recordingStore{completed: make(chan int64, 2)}
	manager := NewManager(store, 2)
	started := make(chan int64, 2)
	release := make(chan struct{})
	var mu sync.Mutex
	active, maximum := 0, 0
	manager.process = func(_ context.Context, _ Source, _ string) (Aggregate, ParseStats, error) {
		mu.Lock()
		active++
		if active > maximum {
			maximum = active
		}
		mu.Unlock()
		started <- 1
		<-release
		mu.Lock()
		active--
		mu.Unlock()
		return newAggregate(), ParseStats{Rows: 1}, nil
	}
	paths := []string{filepath.Join(t.TempDir(), "one"), filepath.Join(t.TempDir(), "two")}
	for _, path := range paths {
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- manager.Run(ctx) }()
	for index, path := range paths {
		if err := manager.Queue(Job{ImportID: int64(index + 1), Source: SourceUmami, Path: path}); err != nil {
			t.Fatal(err)
		}
	}
	<-started
	select {
	case <-started:
		t.Fatal("second job started before first was released")
	default:
	}
	release <- struct{}{}
	<-started
	release <- struct{}{}
	<-store.completed
	<-store.completed
	mu.Lock()
	if maximum != 1 {
		t.Fatalf("maximum active processors = %d", maximum)
	}
	mu.Unlock()
	for _, path := range paths {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("temporary file %s was not removed: %v", path, err)
		}
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v", err)
	}
}

func TestManagerQueueIsBounded(t *testing.T) {
	manager := NewManager(&recordingStore{completed: make(chan int64)}, 1)
	if err := manager.Queue(Job{ImportID: 1}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Queue(Job{ImportID: 2}); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("second Queue error = %v", err)
	}
}

type recordingStore struct {
	completed chan int64
}

func (s *recordingStore) SetRunning(context.Context, int64) error       { return nil }
func (s *recordingStore) SetFailed(context.Context, int64, error) error { return nil }
func (s *recordingStore) Complete(_ context.Context, id int64, _ Aggregate, _ ParseStats) error {
	s.completed <- id
	return nil
}
