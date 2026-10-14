package opencounters

import (
	"context"
	"sync"
	"testing"

	"supermetry/internal/db"
	"supermetry/internal/testpg"
)

type memoryStore struct {
	mu     sync.Mutex
	rows   map[ViewKey]int64
	totals map[string]int64
	loads  int
}

func newMemoryStore() *memoryStore {
	return &memoryStore{rows: map[ViewKey]int64{}, totals: map[string]int64{}}
}

func (s *memoryStore) Upsert(_ context.Context, rows map[ViewKey]int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, views := range rows {
		s.rows[key] += views
		s.totals[key.Key] += views
	}
	return nil
}

func (s *memoryStore) Totals(_ context.Context, keys []string) (map[string]int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loads++
	out := make(map[string]int64, len(keys))
	for _, key := range keys {
		out[key] = s.totals[key]
	}
	return out, nil
}

func TestRecorderLoadsColdTotal(t *testing.T) {
	store := newMemoryStore()
	store.totals["github.com/old"] = 500
	recorder := NewRecorder(store, 50, 500)
	if got := recorder.Total("github.com/old"); got != 0 {
		t.Fatalf("cold Total = %d, want 0", got)
	}
	if err := recorder.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := recorder.Total("github.com/old"); got != 500 {
		t.Fatalf("loaded Total = %d, want 500", got)
	}
	recorder.View("github.com/old", "2026-10-14")
	if got := recorder.Total("github.com/old"); got != 501 {
		t.Fatalf("incremented Total = %d, want 501", got)
	}
	if store.loads != 1 {
		t.Fatalf("total loads = %d, want 1", store.loads)
	}
}

func TestRecorderLimitsNewKeys(t *testing.T) {
	store := newMemoryStore()
	recorder := NewRecorder(store, 0, 2)
	recorder.View("github.com/a", "2026-10-14")
	recorder.View("github.com/b", "2026-10-14")
	recorder.View("github.com/c", "2026-10-14")
	recorder.View("github.com/a", "2026-10-14")
	if err := recorder.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := store.rows[ViewKey{Key: "github.com/a", Day: "2026-10-14"}]; got != 2 {
		t.Fatalf("a views = %d, want 2", got)
	}
	if got := store.rows[ViewKey{Key: "github.com/b", Day: "2026-10-14"}]; got != 1 {
		t.Fatalf("b views = %d, want 1", got)
	}
	if _, ok := store.rows[ViewKey{Key: "github.com/c", Day: "2026-10-14"}]; ok {
		t.Fatal("rate-limited key was stored")
	}
}

func TestPoolStoreRoundTrip(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := PoolStore(pool)
	rows := map[ViewKey]int64{
		{Key: "github.com/a", Day: "2026-10-13"}: 2,
		{Key: "github.com/a", Day: "2026-10-14"}: 1,
	}
	if err := store.Upsert(ctx, rows); err != nil {
		t.Fatal(err)
	}
	if err := store.Upsert(ctx, rows); err != nil {
		t.Fatal(err)
	}
	totals, err := store.Totals(ctx, []string{"github.com/a"})
	if err != nil || totals["github.com/a"] != 6 {
		t.Fatalf("totals = %v, %v", totals, err)
	}
	var day string
	if err := pool.QueryRow(ctx, `select last_seen_day::text from open_counters where key='github.com/a'`).Scan(&day); err != nil || day != "2026-10-14" {
		t.Fatalf("last_seen_day = %q, %v", day, err)
	}
}
