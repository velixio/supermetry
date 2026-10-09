package dict_test

import (
	"context"
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
