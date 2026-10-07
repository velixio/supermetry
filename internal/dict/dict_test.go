package dict_test

import (
	"context"
	"sync"
	"testing"

	"panel/internal/db"
	"panel/internal/dict"
	"panel/internal/testpg"
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
