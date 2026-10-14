package pixels

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTotalCacheAddGetSeed(t *testing.T) {
	c := NewTotalCache()
	c.Add(1, 2)
	c.Add(1, 3)
	if c.Get(1) != 5 || c.Get(2) != 0 {
		t.Fatalf("get = %d/%d", c.Get(1), c.Get(2))
	}
	c.Seed(map[int64]int64{1: 4, 2: 9})
	if c.Get(1) != 5 || c.Get(2) != 9 {
		t.Fatalf("after seed = %d/%d", c.Get(1), c.Get(2))
	}
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() { defer wg.Done(); c.Add(3, 1) }()
	}
	wg.Wait()
	if c.Get(3) != 50 {
		t.Fatalf("concurrent = %d", c.Get(3))
	}
}

func TestRegistryRunRefreshesPeriodically(t *testing.T) {
	var calls atomic.Int32
	r := NewRegistry(
		func(context.Context) ([]Pixel, error) {
			id := calls.Add(1)
			return []Pixel{{ID: int64(id), PublicID: "pAAAAAAAAAAA"}}, nil
		},
		func(context.Context) (map[int64]int64, error) { return map[int64]int64{}, nil },
		NewTotalCache(),
	)
	if err := r.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx, time.Millisecond) }()
	deadline := time.Now().Add(time.Second)
	for calls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if calls.Load() < 2 {
		t.Fatalf("reload calls = %d, want periodic refresh", calls.Load())
	}
}

func TestRegistryReloadAndLookup(t *testing.T) {
	loaded := []Pixel{{ID: 1, SiteID: 7, PublicID: "pAAAAAAAAAAA", Enabled: true}}
	r := NewRegistry(
		func(context.Context) ([]Pixel, error) { return loaded, nil },
		func(context.Context) (map[int64]int64, error) { return map[int64]int64{1: 42}, nil },
		NewTotalCache(),
	)
	if _, ok := r.ByPublicID("pAAAAAAAAAAA"); ok {
		t.Fatal("found before load")
	}
	if err := r.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	p, ok := r.ByPublicID("pAAAAAAAAAAA")
	if !ok || p.SiteID != 7 || r.Totals().Get(1) != 42 {
		t.Fatalf("p=%+v ok=%v total=%d", p, ok, r.Totals().Get(1))
	}
	loaded = nil
	_ = r.Reload(context.Background())
	if _, ok := r.ByPublicID("pAAAAAAAAAAA"); ok {
		t.Fatal("deleted pixel still resolves")
	}
	if total := r.Totals().Get(1); total != 0 {
		t.Fatalf("deleted pixel total remains cached: %d", total)
	}
}
