package closer

import (
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestPlanRollupBackfill(t *testing.T) {
	counters := RollupCounterColumns()
	current := NewRollupLayout([]int16{1, 2, 3}, counters)
	cases := []struct {
		name   string
		stored *RollupLayout
		mode   rollupBackfillMode
		dims   []int16
	}{
		{name: "unknown stored layout", stored: nil, mode: rollupBackfillFull},
		{name: "unchanged", stored: &RollupLayout{Dimensions: []int16{3, 2, 1}, Counters: counters}, mode: rollupBackfillNone},
		{name: "dimension added", stored: &RollupLayout{Dimensions: []int16{1}, Counters: counters}, mode: rollupBackfillDimensions, dims: []int16{2, 3}},
		{name: "counter added", stored: &RollupLayout{Dimensions: []int16{1, 2, 3}, Counters: counters[1:]}, mode: rollupBackfillFull},
		{name: "dimension and counter added", stored: &RollupLayout{Dimensions: []int16{1}, Counters: counters[1:]}, mode: rollupBackfillFull},
		{name: "dimension removed", stored: &RollupLayout{Dimensions: []int16{1, 2, 3, 4}, Counters: counters}, mode: rollupBackfillFull},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mode, dims := planRollupBackfill(tc.stored, current)
			if mode != tc.mode || !reflect.DeepEqual(dims, tc.dims) {
				t.Fatalf("plan = %q %v, want %q %v", mode, dims, tc.mode, tc.dims)
			}
		})
	}
}

func TestRollupLayoutVersionIsCanonical(t *testing.T) {
	a := RollupLayout{Dimensions: []int16{2, 1, 1}, Counters: []string{"visits", "visitors"}}
	b := RollupLayout{Dimensions: []int16{1, 2}, Counters: []string{"visitors", "visits"}}
	if a.Version() != b.Version() {
		t.Fatal("equivalent layouts hash differently")
	}
	c := RollupLayout{Dimensions: []int16{1, 2}, Counters: []string{"visitors"}}
	if a.Version() == c.Version() {
		t.Fatal("counter change did not change the layout version")
	}
}

// Guards the registration rule: every counter column the closer inserts into
// a rollup table must be listed in rollupCounterColumns.
func TestRollupCounterColumnsMatchClosedSQL(t *testing.T) {
	keys := map[string]bool{"site_id": true, "day": true, "month": true, "dim": true, "value_id": true, "k": true, "start_day": true}
	insert := regexp.MustCompile(`(?s)insert into (rollup_\w+) \(([^)]*)\)`)
	written := map[string]bool{}
	for _, sql := range []string{rollupDaySQL, rollupSessionDimensionsSQL, rollupPathSQL, refreshMonthInsertSQL, insertFirstBlockSQL, insertParentBlockSQL} {
		for _, match := range insert.FindAllStringSubmatch(sql, -1) {
			if strings.HasSuffix(match[1], "_done") {
				continue
			}
			for _, column := range strings.Split(match[2], ",") {
				if column = strings.TrimSpace(column); !keys[column] {
					written[column] = true
				}
			}
		}
	}
	if len(written) == 0 {
		t.Fatal("no rollup insert statements found")
	}
	for column := range written {
		if !slices.Contains(RollupCounterColumns(), column) {
			t.Errorf("closer writes counter %q that is not registered in rollupCounterColumns", column)
		}
	}
	for _, column := range RollupCounterColumns() {
		if !written[column] {
			t.Errorf("registered counter %q is not written by any closer insert", column)
		}
	}
}
