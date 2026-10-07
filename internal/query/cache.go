package query

import (
	"container/list"
	"crypto/sha256"
	"encoding/binary"
	"sync"
	"time"

	"panel/internal/api"
)

const defaultCacheCapacity = 1024

// Key is the digest used to address a closed-range query result.
type Key [sha256.Size]byte

type cacheEntry struct {
	key      Key
	response api.QueryResponse
}

// Cache is a concurrency-safe, fixed-capacity LRU of closed-range results.
type Cache struct {
	mu       sync.Mutex
	capacity int
	entries  map[Key]*list.Element
	recent   *list.List
}

// NewCache returns an empty cache. A non-positive capacity selects the
// production default of 1,024 entries.
func NewCache(capacity int) *Cache {
	if capacity <= 0 {
		capacity = defaultCacheCapacity
	}
	return &Cache{
		capacity: capacity,
		entries:  make(map[Key]*list.Element, capacity),
		recent:   list.New(),
	}
}

// CacheKey hashes every value that can make a closed-range result stale.
func CacheKey(siteID int32, normalizedQuery string, closedThrough time.Time, maxCloseVersion int64) Key {
	hash := sha256.New()
	var number [8]byte
	binary.BigEndian.PutUint32(number[:4], uint32(siteID))
	_, _ = hash.Write(number[:4])
	binary.BigEndian.PutUint64(number[:], uint64(len(normalizedQuery)))
	_, _ = hash.Write(number[:])
	_, _ = hash.Write([]byte(normalizedQuery))
	_, _ = hash.Write([]byte(closedThrough.Format(time.DateOnly)))
	binary.BigEndian.PutUint64(number[:], uint64(maxCloseVersion))
	_, _ = hash.Write(number[:])

	var key Key
	copy(key[:], hash.Sum(nil))
	return key
}

// Get returns a copy of the cached response and marks it recently used.
func (c *Cache) Get(key Key) (api.QueryResponse, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	element, ok := c.entries[key]
	if !ok {
		return api.QueryResponse{}, false
	}
	c.recent.MoveToFront(element)
	return cloneResponse(element.Value.(*cacheEntry).response), true
}

// Put adds or replaces a response and evicts the least recently used entry
// when the cache is full.
func (c *Cache) Put(key Key, response api.QueryResponse) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if element, ok := c.entries[key]; ok {
		element.Value.(*cacheEntry).response = cloneResponse(response)
		c.recent.MoveToFront(element)
		return
	}
	element := c.recent.PushFront(&cacheEntry{key: key, response: cloneResponse(response)})
	c.entries[key] = element
	if c.recent.Len() <= c.capacity {
		return
	}
	oldest := c.recent.Back()
	delete(c.entries, oldest.Value.(*cacheEntry).key)
	c.recent.Remove(oldest)
}

func cloneResponse(response api.QueryResponse) api.QueryResponse {
	cloned := response
	cloned.Columns = append([]string(nil), response.Columns...)
	cloned.Rows = make([]map[string]interface{}, len(response.Rows))
	for i, row := range response.Rows {
		cloned.Rows[i] = make(map[string]interface{}, len(row))
		for key, value := range row {
			cloned.Rows[i][key] = cloneRowValue(value)
		}
	}
	cloned.Totals = make(map[string]float64, len(response.Totals))
	for key, value := range response.Totals {
		cloned.Totals[key] = value
	}
	cloned.Meta.SourcesUsed = append([]string(nil), response.Meta.SourcesUsed...)
	cloned.NextCursor = clonePointer(response.NextCursor)
	return cloned
}

func cloneRowValue(value interface{}) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		cloned := make(map[string]interface{}, len(typed))
		for key, nested := range typed {
			cloned[key] = cloneRowValue(nested)
		}
		return cloned
	case []interface{}:
		cloned := make([]interface{}, len(typed))
		for i, nested := range typed {
			cloned[i] = cloneRowValue(nested)
		}
		return cloned
	case []string:
		return append([]string(nil), typed...)
	default:
		return value
	}
}
