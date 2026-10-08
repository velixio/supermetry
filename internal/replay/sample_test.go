package replay

import "testing"

func TestSampledRateAccuracy(t *testing.T) {
	const total = 100_000
	var sampled int
	for id := int64(0); id < total; id++ {
		if Sampled(id, 15) {
			sampled++
		}
	}
	share := float64(sampled) / total * 100
	if share < 14 || share > 16 {
		t.Fatalf("sampled share = %.3f%%, want 15%% ±1%%", share)
	}
}

func TestSampledDeterministic(t *testing.T) {
	for id := int64(-1000); id <= 1000; id++ {
		want := Sampled(id, 37)
		for range 10 {
			if got := Sampled(id, 37); got != want {
				t.Fatalf("Sampled(%d, 37) changed from %t to %t", id, want, got)
			}
		}
	}
	if Sampled(42, 0) {
		t.Fatal("zero-percent sampling selected a visitor")
	}
	if !Sampled(42, 100) {
		t.Fatal("100-percent sampling rejected a visitor")
	}
}
