package spans

import (
	"math"
	"testing"
)

func TestBucket(t *testing.T) {
	for _, c := range []struct {
		us   int64
		want int16
	}{{1, 0}, {0, 0}, {-5, 0}, {50_000, 113}, {3_600_000_000, 230}, {1_000_000_000_000, 230}} {
		if got := Bucket(c.us); got != c.want {
			t.Errorf("Bucket(%d) = %d, want %d", c.us, got, c.want)
		}
	}
}

func TestPercentilesNearestRank(t *testing.T) {
	counts := map[int16]int64{}
	for ms := int64(1); ms <= 100; ms++ {
		counts[Bucket(ms*1000)]++
	}
	ps := []float64{0.5, 0.75, 0.9, 0.95, 0.99}
	exact := []float64{50_000, 75_000, 90_000, 95_000, 99_000}
	want := []float64{49_896, 73_053, 88_395, 97_234, 97_234}
	got := Percentiles(counts, ps...)
	for i := range ps {
		if math.Abs(got[i]-want[i]) > 1 {
			t.Errorf("p%v = %.1f, want %.0f", ps[i]*100, got[i], want[i])
		}
		if rel := math.Abs(got[i]-exact[i]) / exact[i]; rel > 0.0488 {
			t.Errorf("p%v relative error %.4f exceeds bound", ps[i]*100, rel)
		}
	}
}

func TestPercentilesEmpty(t *testing.T) {
	for _, v := range Percentiles(nil, 0.5, 0.95) {
		if !math.IsNaN(v) {
			t.Fatalf("got %v, want NaN", v)
		}
	}
}

func TestGroupHash(t *testing.T) {
	a, b := GroupHash("db", "select ?"), GroupHash("db", "select ?")
	if a != b || a < 0 {
		t.Fatalf("hash %d %d", a, b)
	}
	if GroupHash("db", "x") == GroupHash("dbx", "") {
		t.Fatal("separator not applied")
	}
}
