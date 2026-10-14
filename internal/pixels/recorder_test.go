package pixels

import (
	"context"
	"errors"
	"maps"
	"testing"
)

func TestRecorderAggregatesAndUpdatesTotals(t *testing.T) {
	var written map[ViewKey]int64
	totals := NewTotalCache()
	r := NewRecorder(func(_ context.Context, rows map[ViewKey]int64) error { written = maps.Clone(rows); return nil }, totals)
	k := ViewKey{PixelID: 1, Day: "2026-10-14", Via: "proxy"}
	r.View(k)
	r.View(k)
	r.View(ViewKey{PixelID: 1, Day: "2026-10-14", Country: "NP", Via: "direct"})
	if totals.Get(1) != 3 {
		t.Fatalf("total = %d", totals.Get(1))
	}
	if err := r.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if written[k] != 2 || len(written) != 2 {
		t.Fatalf("written = %v", written)
	}
}

func TestFlushFailureKeepsCountsAndTotals(t *testing.T) {
	fail := true
	var sum int64
	totals := NewTotalCache()
	r := NewRecorder(func(_ context.Context, rows map[ViewKey]int64) error {
		if fail {
			return errors.New("db down")
		}
		for _, n := range rows {
			sum += n
		}
		return nil
	}, totals)
	for range 3 {
		r.View(ViewKey{PixelID: 9, Day: "2026-10-14", Via: "proxy"})
	}
	if err := r.Flush(context.Background()); err == nil {
		t.Fatal("want error")
	}
	r.View(ViewKey{PixelID: 9, Day: "2026-10-14", Via: "proxy"})
	fail = false
	if err := r.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sum != 4 || totals.Get(9) != 4 {
		t.Fatalf("sum=%d total=%d, want 4/4", sum, totals.Get(9))
	}
}
