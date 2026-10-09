package query_test

import (
	"fmt"
	"sync"
	"testing"

	"supermetry/internal/api"
	"supermetry/internal/query"
)

func TestCacheInvalidatesOnReclose(t *testing.T) {
	t.Parallel()

	cache := query.NewCache(16)
	closedThrough := mustDay("2026-10-06")
	cache.Put(query.CacheKey(1, "q", closedThrough, 3), response(1))
	if _, ok := cache.Get(query.CacheKey(1, "q", closedThrough, 4)); ok {
		t.Fatal("Get() served a stale result after re-close")
	}
}

func TestCacheEvictsLeastRecentlyUsed(t *testing.T) {
	t.Parallel()

	cache := query.NewCache(2)
	day := mustDay("2026-10-06")
	k1 := query.CacheKey(1, "first", day, 1)
	k2 := query.CacheKey(1, "second", day, 1)
	k3 := query.CacheKey(1, "third", day, 1)
	cache.Put(k1, response(1))
	cache.Put(k2, response(2))
	if _, ok := cache.Get(k1); !ok {
		t.Fatal("Get(first) missed")
	}
	cache.Put(k3, response(3))
	if _, ok := cache.Get(k2); ok {
		t.Fatal("Get(second) hit after least-recently-used eviction")
	}
	if got, ok := cache.Get(k1); !ok || got.Totals["visitors"] != 1 {
		t.Fatalf("Get(first) = %+v, %v", got, ok)
	}
}

func TestCacheDefaultCapacityIs1024(t *testing.T) {
	t.Parallel()

	cache := query.NewCache(0)
	day := mustDay("2026-10-06")
	for i := 0; i < 1025; i++ {
		cache.Put(query.CacheKey(1, fmt.Sprintf("query-%d", i), day, 1), response(int64(i)))
	}
	if _, ok := cache.Get(query.CacheKey(1, "query-0", day, 1)); ok {
		t.Fatal("Get(oldest) hit after default-capacity eviction")
	}
	if _, ok := cache.Get(query.CacheKey(1, "query-1", day, 1)); !ok {
		t.Fatal("Get(next-oldest) missed; default capacity is not 1024")
	}
}

func TestCacheConcurrentAccess(t *testing.T) {
	t.Parallel()

	cache := query.NewCache(32)
	day := mustDay("2026-10-06")
	var workers sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		workers.Add(1)
		go func(worker int) {
			defer workers.Done()
			for i := 0; i < 100; i++ {
				key := query.CacheKey(1, fmt.Sprintf("query-%d-%d", worker, i%16), day, int64(i%4))
				cache.Put(key, response(int64(i)))
				cache.Get(key)
			}
		}(worker)
	}
	workers.Wait()
}

func TestCacheOwnsNestedRowValues(t *testing.T) {
	t.Parallel()

	cache := query.NewCache(1)
	key := query.CacheKey(1, "nested", mustDay("2026-10-06"), 1)
	original := response(1)
	original.Rows = []map[string]interface{}{
		{"properties": map[string]interface{}{"tags": []interface{}{"original"}}},
	}
	cache.Put(key, original)

	original.Rows[0]["properties"].(map[string]interface{})["tags"].([]interface{})[0] = "changed-before-get"
	got, ok := cache.Get(key)
	if !ok {
		t.Fatal("Get() missed")
	}
	tags := got.Rows[0]["properties"].(map[string]interface{})["tags"].([]interface{})
	if tags[0] != "original" {
		t.Fatalf("Get() observed caller mutation: %v", tags)
	}

	tags[0] = "changed-after-get"
	again, ok := cache.Get(key)
	if !ok {
		t.Fatal("second Get() missed")
	}
	againTags := again.Rows[0]["properties"].(map[string]interface{})["tags"].([]interface{})
	if againTags[0] != "original" {
		t.Fatalf("cached response was mutated through Get(): %v", againTags)
	}
}

func TestCacheOwnsComparisonValues(t *testing.T) {
	t.Parallel()

	cache := query.NewCache(1)
	key := query.CacheKey(1, "comparison", mustDay("2026-10-06"), 1)
	original := response(1)
	original.Comparison = &api.ComparisonResult{
		Rows:   []map[string]interface{}{{"page": "/original"}},
		Totals: map[string]float64{"visitors": 2},
	}
	cache.Put(key, original)

	original.Comparison.Rows[0]["page"] = "/changed-before-get"
	got, ok := cache.Get(key)
	if !ok {
		t.Fatal("Get() missed")
	}
	if got.Comparison.Rows[0]["page"] != "/original" {
		t.Fatalf("Get() observed caller comparison mutation: %+v", got.Comparison)
	}

	got.Comparison.Rows[0]["page"] = "/changed-after-get"
	again, ok := cache.Get(key)
	if !ok {
		t.Fatal("second Get() missed")
	}
	if again.Comparison.Rows[0]["page"] != "/original" {
		t.Fatalf("cached comparison was mutated through Get(): %+v", again.Comparison)
	}
}

func TestCacheSeparatesSites(t *testing.T) {
	t.Parallel()

	cache := query.NewCache(2)
	day := mustDay("2026-10-06")
	cache.Put(query.CacheKey(1, "same-query", day, 1), response(1))
	cache.Put(query.CacheKey(2, "same-query", day, 1), response(2))

	for siteID, want := range map[int32]float64{1: 1, 2: 2} {
		got, ok := cache.Get(query.CacheKey(siteID, "same-query", day, 1))
		if !ok || got.Totals["visitors"] != want {
			t.Fatalf("site %d Get() = %+v, %v; want visitors=%v", siteID, got, ok, want)
		}
	}
}

func TestCacheKeySeparatesEveryInput(t *testing.T) {
	t.Parallel()

	day := mustDay("2026-10-06")
	base := query.CacheKey(1, "q", day, 3)
	tests := []struct {
		name string
		key  query.Key
	}{
		{name: "site", key: query.CacheKey(2, "q", day, 3)},
		{name: "query", key: query.CacheKey(1, "other", day, 3)},
		{name: "closed through", key: query.CacheKey(1, "q", day.AddDate(0, 0, -1), 3)},
		{name: "close version", key: query.CacheKey(1, "q", day, 4)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if tt.key == base {
				t.Fatal("CacheKey() collision")
			}
		})
	}
}

func response(visitors int64) api.QueryResponse {
	return api.QueryResponse{
		Columns: []string{"visitors"},
		Rows:    []map[string]interface{}{},
		Totals:  map[string]float64{"visitors": float64(visitors)},
		Meta:    api.QueryMeta{SourcesUsed: []string{"closed"}},
	}
}
