package query

import (
	"reflect"
	"slices"
	"testing"
)

func TestRollupDimensionSetIsSortedAndUnique(t *testing.T) {
	want := []int16{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18}
	if got := RollupDimensionSet(); !reflect.DeepEqual(got, want) {
		t.Fatalf("RollupDimensionSet() = %v, want %v", got, want)
	}
	got := RollupDimensionSet()
	got[0] = 99
	if again := RollupDimensionSet(); again[0] != 1 {
		t.Fatalf("RollupDimensionSet returned mutable shared state: %v", again)
	}
}

func TestCurrentRollupLayoutCombinesDimensionsAndClosedCounters(t *testing.T) {
	layout := CurrentRollupLayout()
	if !reflect.DeepEqual(layout.Dimensions, RollupDimensionSet()) {
		t.Fatalf("layout dimensions = %v, want %v", layout.Dimensions, RollupDimensionSet())
	}
	if !slices.Contains(layout.Counters, "engaged_n") {
		t.Fatalf("layout counters = %v, want engaged_n registered", layout.Counters)
	}
	if layout.Version() == "" || layout.Version() != CurrentRollupLayout().Version() {
		t.Fatal("layout version is empty or unstable")
	}
}
