package dict_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"supermetry/internal/db"
	"supermetry/internal/dict"
	"supermetry/internal/testpg"
)

func TestIDsStableConcurrentAndCached(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	d := dict.New(pool)
	var wg sync.WaitGroup
	res := make([][]int32, 8)
	for i := range res {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res[i], _ = d.IDs(ctx, dict.Path, []string{"/", "/pricing", "/"})
		}(i)
	}
	wg.Wait()

	for _, r := range res {
		if r[0] != res[0][0] || r[1] != res[0][1] || r[0] != r[2] {
			t.Fatalf("unstable ids %v", res)
		}
	}

	d2 := dict.New(pool)
	if err := d2.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if v, ok := d2.Value(dict.Path, res[0][1]); !ok || v != "/pricing" {
		t.Fatalf("Load: %q %v", v, ok)
	}
}

func TestRegionAndCityUseSeparateNamespaces(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	d := dict.New(pool)
	regionIDs, err := d.IDs(ctx, dict.Region, []string{"Singapore"})
	if err != nil {
		t.Fatal(err)
	}
	cityIDs, err := d.IDs(ctx, dict.City, []string{"Singapore"})
	if err != nil {
		t.Fatal(err)
	}
	if regionIDs[0] == cityIDs[0] {
		t.Fatalf("region and city IDs share a namespace: %d", regionIDs[0])
	}
	if got, ok := d.Value(dict.Region, regionIDs[0]); !ok || got != "Singapore" {
		t.Fatalf("region lookup = %q, %v", got, ok)
	}
	if got, ok := d.Value(dict.City, cityIDs[0]); !ok || got != "Singapore" {
		t.Fatalf("city lookup = %q, %v", got, ok)
	}
}

func TestIDsFoldAtPerKindEntryLimit(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	const limit = 8
	d := dict.New(pool, dict.WithMaxEntriesPerKind(limit))
	values := make([]string, 32)
	for i := range values {
		values[i] = fmt.Sprintf("/%03d-%s", i, strings.Repeat("x", 300))
	}
	ids, err := d.IDs(ctx, dict.Path, values)
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Len(dict.Path); got > limit {
		t.Fatalf("cached path count = %d, want at most %d", got, limit)
	}
	var rows int
	if err := pool.QueryRow(ctx, `select count(*) from dict where kind=$1`, dict.Path).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows > limit {
		t.Fatalf("persisted path count = %d, want at most %d", rows, limit)
	}
	overflowID := ids[limit-1]
	if overflowID == 0 {
		t.Fatal("overflow ID is zero")
	}
	for i := limit - 1; i < len(ids); i++ {
		if ids[i] != overflowID {
			t.Fatalf("ID %d = %d, want overflow ID %d", i, ids[i], overflowID)
		}
	}
	if got, ok := d.Value(dict.Path, overflowID); !ok || got != dict.OtherValue {
		t.Fatalf("overflow lookup = %q, %v, want %q", got, ok, dict.OtherValue)
	}
}

func TestIDsBelowPerKindEntryLimitAreUnchanged(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	d := dict.New(pool, dict.WithMaxEntriesPerKind(4))
	values := []string{"/", "/pricing", "/docs"}
	ids, err := d.IDs(ctx, dict.Path, values)
	if err != nil {
		t.Fatal(err)
	}
	for i, id := range ids {
		if id == 0 {
			t.Fatalf("ID %d is zero", i)
		}
		if got, ok := d.Value(dict.Path, id); !ok || got != values[i] {
			t.Fatalf("Value(%d) = %q, %v, want %q", id, got, ok, values[i])
		}
	}
}

func TestLoadBoundsCacheAndResolvesOverflowedLegacyIDs(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	rows, err := pool.Query(ctx, `insert into dict(kind,value) select $1, '/legacy/' || n::text from generate_series(1, 8) as n returning id`, dict.Path)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int32
	for rows.Next() {
		var id int32
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()

	d := dict.New(pool, dict.WithMaxEntriesPerKind(4))
	if err := d.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if got := d.Len(dict.Path); got > 4 {
		t.Fatalf("loaded path count = %d, want at most 4", got)
	}
	for _, id := range ids {
		if value, ok := d.Value(dict.Path, id); !ok || value == "" {
			t.Fatalf("legacy ID %d did not resolve: %q, %v", id, value, ok)
		}
	}
}

func TestConcurrentIDsRespectLimitAndDoNotBurnIdentityValues(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	const limit = 16
	dicts := []*dict.Dict{
		dict.New(pool, dict.WithMaxEntriesPerKind(limit)),
		dict.New(pool, dict.WithMaxEntriesPerKind(limit)),
	}
	var wg sync.WaitGroup
	errs := make(chan error, 32)
	results := make(chan int32, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ids, err := dicts[i%len(dicts)].IDs(ctx, dict.EventName, []string{"shared"})
			if err != nil {
				errs <- err
				return
			}
			results <- ids[0]
		}(i)
	}
	wg.Wait()
	close(errs)
	close(results)
	for err := range errs {
		t.Fatal(err)
	}
	var sharedID int32
	for id := range results {
		if sharedID == 0 {
			sharedID = id
		}
		if id != sharedID {
			t.Fatalf("concurrent shared IDs differ: got %d and %d", sharedID, id)
		}
	}

	next, err := dictID(dicts[0], ctx, dict.EventName, "next")
	if err != nil {
		t.Fatal(err)
	}
	if next != sharedID+1 {
		t.Fatalf("next ID = %d, want %d; conflicts burned identity values", next, sharedID+1)
	}
}

func dictID(d *dict.Dict, ctx context.Context, kind dict.Kind, value string) (int32, error) {
	ids, err := d.IDs(ctx, kind, []string{value})
	if err != nil {
		return 0, err
	}
	return ids[0], nil
}
