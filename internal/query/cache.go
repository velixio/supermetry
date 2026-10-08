package query

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/sync/singleflight"

	"panel/internal/api"
)

const defaultCacheCapacity = 1024
const closeMetadataTTL = 3 * time.Second

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

	metadataMu sync.RWMutex
	metadata   map[int32]closeMetadataEntry
	metadataSF singleflight.Group
	resultSF   singleflight.Group
	generation map[int32]uint64
}

type closeMetadataEntry struct {
	value     closeMetadata
	expiresAt time.Time
}

type closeMetadata struct {
	closed map[time.Time]int64
	blocks map[dayBlock]bool
	// maxCloseVersion is a site-wide generation: per-day versions only
	// increase, so their sum changes whenever any closed day is rewritten.
	maxCloseVersion int64
}

// NewCache returns an empty cache. A non-positive capacity selects the
// production default of 1,024 entries.
func NewCache(capacity int) *Cache {
	if capacity <= 0 {
		capacity = defaultCacheCapacity
	}
	return &Cache{
		capacity:   capacity,
		entries:    make(map[Key]*list.Element, capacity),
		recent:     list.New(),
		metadata:   make(map[int32]closeMetadataEntry),
		generation: make(map[int32]uint64),
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

// InvalidateSite expires close metadata immediately. Result entries do not
// need a scan: the next metadata load observes a newer close version and thus
// addresses them with a different key.
func (c *Cache) InvalidateSite(siteID int32) {
	if c == nil {
		return
	}
	c.metadataMu.Lock()
	c.generation[siteID]++
	delete(c.metadata, siteID)
	c.metadataMu.Unlock()
	c.metadataSF.Forget(fmt.Sprint(siteID))
}

func (c *Cache) closeMetadata(ctx context.Context, engine *Engine, siteID int32) (closeMetadata, error) {
	if c == nil {
		return engine.loadCloseMetadata(ctx, siteID)
	}
	now := time.Now()
	c.metadataMu.RLock()
	entry, ok := c.metadata[siteID]
	c.metadataMu.RUnlock()
	if ok && now.Before(entry.expiresAt) {
		return entry.value, nil
	}

	value, err, _ := c.metadataSF.Do(fmt.Sprint(siteID), func() (any, error) {
		now := time.Now()
		c.metadataMu.RLock()
		entry, ok := c.metadata[siteID]
		generation := c.generation[siteID]
		c.metadataMu.RUnlock()
		if ok && now.Before(entry.expiresAt) {
			return entry.value, nil
		}
		loaded, loadErr := engine.loadCloseMetadata(ctx, siteID)
		if loadErr != nil {
			return closeMetadata{}, loadErr
		}
		c.metadataMu.Lock()
		if c.generation[siteID] == generation {
			c.metadata[siteID] = closeMetadataEntry{
				value: loaded, expiresAt: time.Now().Add(closeMetadataTTL),
			}
		}
		c.metadataMu.Unlock()
		return loaded, nil
	})
	if err != nil {
		return closeMetadata{}, err
	}
	return value.(closeMetadata), nil
}

func (e *Engine) loadCloseMetadata(ctx context.Context, siteID int32) (closeMetadata, error) {
	metadata := closeMetadata{
		closed: make(map[time.Time]int64),
		blocks: make(map[dayBlock]bool),
	}
	err := WithStatementTimeout(ctx, e.pool, queryTimeout, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `select day, version from day_close where site_id=$1 and status='done'`, siteID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var day time.Time
			var version int64
			if err := rows.Scan(&day, &version); err != nil {
				rows.Close()
				return err
			}
			metadata.closed[civilDate(day)] = version
			metadata.maxCloseVersion += version
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()

		rows, err = tx.Query(ctx, `select k, start_day from rollup_block_done where site_id=$1`, siteID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var block dayBlock
			if err := rows.Scan(&block.level, &block.start); err != nil {
				return err
			}
			block.start = civilDate(block.start)
			metadata.blocks[block] = true
		}
		return rows.Err()
	})
	if err != nil {
		return closeMetadata{}, fmt.Errorf("query close metadata: %w", err)
	}
	return metadata, nil
}

func (m closeMetadata) closedThrough(from, to time.Time) time.Time {
	for day := civilDate(from); !day.After(to); day = day.AddDate(0, 0, 1) {
		if _, ok := m.closed[day]; !ok {
			return day.AddDate(0, 0, -1)
		}
	}
	return civilDate(to)
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
	if response.Comparison != nil {
		comparison := *response.Comparison
		comparison.Rows = cloneRows(response.Comparison.Rows)
		comparison.Totals = cloneTotals(response.Comparison.Totals)
		cloned.Comparison = &comparison
	}
	return cloned
}

func cloneRows(rows []map[string]interface{}) []map[string]interface{} {
	cloned := make([]map[string]interface{}, len(rows))
	for i, row := range rows {
		cloned[i] = make(map[string]interface{}, len(row))
		for key, value := range row {
			cloned[i][key] = cloneRowValue(value)
		}
	}
	return cloned
}

func cloneTotals(totals map[string]float64) map[string]float64 {
	cloned := make(map[string]float64, len(totals))
	for key, value := range totals {
		cloned[key] = value
	}
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
