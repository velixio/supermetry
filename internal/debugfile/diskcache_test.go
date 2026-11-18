package debugfile

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"testing"
)

func fetcher(data []byte, calls *atomic.Int32) func(context.Context) (io.ReadCloser, error) {
	return func(context.Context) (io.ReadCloser, error) {
		calls.Add(1)
		return io.NopCloser(bytes.NewReader(data)), nil
	}
}

func TestDiskCacheEvictsLeastRecentlyOpened(t *testing.T) {
	c := NewDiskCache(t.TempDir(), 3<<10)
	var calls atomic.Int32
	blob := bytes.Repeat([]byte{1}, 1536)
	open := func(key string) {
		t.Helper()
		f, size, err := c.Open(context.Background(), key, fetcher(blob, &calls))
		if err != nil || size != 1536 {
			t.Fatalf("open %s: %v size %d", key, err, size)
		}
		_ = f.Close()
	}
	open("a")
	open("b")
	open("a") // a is now more recent than b
	open("c") // evicts b
	if c.Size() != 3<<10 {
		t.Fatalf("size %d", c.Size())
	}
	before := calls.Load()
	open("a")
	open("c")
	if calls.Load() != before {
		t.Fatal("a and c should still be cached")
	}
	open("b")
	if calls.Load() != before+1 {
		t.Fatal("b should have been evicted and refetched")
	}
}

func TestDiskCacheSingleFlight(t *testing.T) {
	c := NewDiskCache(t.TempDir(), 1<<20)
	var calls atomic.Int32
	gate := make(chan struct{})
	fetch := func(context.Context) (io.ReadCloser, error) {
		calls.Add(1)
		<-gate
		return io.NopCloser(bytes.NewReader([]byte("data"))), nil
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f, _, err := c.Open(context.Background(), "k", fetch)
			if err != nil {
				t.Error(err)
				return
			}
			_ = f.Close()
		}()
	}
	close(gate)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("fetched %d times", calls.Load())
	}
}

func TestDiskCacheErrorsAreNotCached(t *testing.T) {
	c := NewDiskCache(t.TempDir(), 1<<20)
	boom := errors.New("boom")
	if _, _, err := c.Open(context.Background(), "k", func(context.Context) (io.ReadCloser, error) { return nil, boom }); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	var calls atomic.Int32
	f, _, err := c.Open(context.Background(), "k", fetcher([]byte("ok"), &calls))
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
}

func TestDiskCacheOversizeBlobRejected(t *testing.T) {
	dir := t.TempDir()
	c := NewDiskCache(dir, 100)
	var calls atomic.Int32
	if _, _, err := c.Open(context.Background(), "big", fetcher(make([]byte, 101), &calls)); err == nil {
		t.Fatal("expected an error")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("files left: %v", entries)
	}
}

func TestDiskCacheReloadsAndTrimsExistingFiles(t *testing.T) {
	dir := t.TempDir()
	c := NewDiskCache(dir, 4<<10)
	var calls atomic.Int32
	for _, k := range []string{"a", "b", "c"} {
		f, _, err := c.Open(context.Background(), k, fetcher(make([]byte, 1536), &calls))
		if err != nil {
			t.Fatal(err)
		}
		_ = f.Close()
	}
	c2 := NewDiskCache(dir, 2<<10)
	if c2.Size() > 2<<10 {
		t.Fatalf("reloaded size %d over budget", c2.Size())
	}
}
