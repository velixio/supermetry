package debugfile

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/sync/singleflight"
)

// DiskCache keeps blobs fetched from the shared blob store as local files, so
// readers can parse them through io.ReaderAt without holding them in memory.
// Files are named by the SHA-256 of their key, evicted least recently opened
// first when the byte budget is exceeded, and each key is fetched at most once
// at a time.
type DiskCache struct {
	dir    string
	budget int64

	flight singleflight.Group

	mu    sync.Mutex
	used  int64
	order *list.List // front = most recently opened
	items map[string]*list.Element
}

type diskEntry struct {
	name string // file name, sha256(key) in hex
	size int64
}

const tmpPrefix = ".tmp-"

// NewDiskCache opens (creating if needed) a cache directory with a byte
// budget. Files already there from an earlier run count against the budget;
// leftover temporary files are removed.
func NewDiskCache(dir string, budget int64) *DiskCache {
	c := &DiskCache{dir: dir, budget: budget, order: list.New(), items: map[string]*list.Element{}}
	_ = os.MkdirAll(dir, 0o700)
	entries, _ := os.ReadDir(dir)
	type old struct {
		name string
		size int64
		mod  int64
	}
	var olds []old
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasPrefix(e.Name(), tmpPrefix) {
			_ = os.Remove(filepath.Join(dir, e.Name()))
			continue
		}
		if info, err := e.Info(); err == nil && len(e.Name()) == 64 {
			olds = append(olds, old{e.Name(), info.Size(), info.ModTime().UnixNano()})
		}
	}
	// Sort oldest first, then push each to the front: the newest ends up in front.
	for i := 1; i < len(olds); i++ {
		for j := i; j > 0 && olds[j].mod < olds[j-1].mod; j-- {
			olds[j], olds[j-1] = olds[j-1], olds[j]
		}
	}
	for _, o := range olds {
		c.items[o.name] = c.order.PushFront(&diskEntry{o.name, o.size})
		c.used += o.size
	}
	c.evict("")
	return c
}

func fileName(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// Open returns the cached file for key, calling fetch to materialise it when
// absent. The caller closes the file. An error from fetch is not cached.
func (c *DiskCache) Open(ctx context.Context, key string, fetch func(context.Context) (io.ReadCloser, error)) (*os.File, int64, error) {
	name := fileName(key)
	for attempt := 0; attempt < 3; attempt++ {
		if f, size, ok := c.openExisting(name); ok {
			return f, size, nil
		}
		_, err, _ := c.flight.Do(name, func() (any, error) {
			if _, _, ok := c.peek(name); ok {
				return nil, nil
			}
			return nil, c.materialise(ctx, name, fetch)
		})
		if err != nil {
			return nil, 0, err
		}
	}
	return nil, 0, fmt.Errorf("debugfile: cache entry %q kept disappearing", key)
}

func (c *DiskCache) peek(name string) (*list.Element, int64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[name]
	if !ok {
		return nil, 0, false
	}
	return el, el.Value.(*diskEntry).size, true
}

func (c *DiskCache) openExisting(name string) (*os.File, int64, bool) {
	c.mu.Lock()
	el, ok := c.items[name]
	if ok {
		c.order.MoveToFront(el)
	}
	c.mu.Unlock()
	if !ok {
		return nil, 0, false
	}
	f, err := os.Open(filepath.Join(c.dir, name))
	if err != nil {
		c.forget(name)
		return nil, 0, false
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		c.forget(name)
		return nil, 0, false
	}
	return f, info.Size(), true
}

func (c *DiskCache) forget(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[name]; ok {
		c.used -= el.Value.(*diskEntry).size
		c.order.Remove(el)
		delete(c.items, name)
	}
}

func (c *DiskCache) materialise(ctx context.Context, name string, fetch func(context.Context) (io.ReadCloser, error)) error {
	rc, err := fetch(ctx)
	if err != nil {
		return err
	}
	defer rc.Close()
	tmp, err := os.CreateTemp(c.dir, tmpPrefix+"*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	size, err := io.Copy(tmp, rc)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if size > c.budget {
		return fmt.Errorf("debugfile: blob of %d bytes exceeds the %d byte cache budget", size, c.budget)
	}
	if err := os.Rename(tmp.Name(), filepath.Join(c.dir, name)); err != nil {
		return err
	}
	c.mu.Lock()
	c.items[name] = c.order.PushFront(&diskEntry{name, size})
	c.used += size
	c.mu.Unlock()
	c.evict(name)
	return nil
}

// evict removes least recently opened files until the budget holds, never the
// entry named keep. A file a reader still has open stays readable (unlinked).
func (c *DiskCache) evict(keep string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for c.used > c.budget {
		el := c.order.Back()
		for el != nil && el.Value.(*diskEntry).name == keep {
			el = el.Prev()
		}
		if el == nil {
			return
		}
		e := el.Value.(*diskEntry)
		c.order.Remove(el)
		delete(c.items, e.name)
		c.used -= e.size
		_ = os.Remove(filepath.Join(c.dir, e.name))
	}
}

// Size reports the bytes currently held.
func (c *DiskCache) Size() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.used
}
