package closer

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strconv"
	"strings"
)

// rollupCounterColumns lists every additive counter column the closer writes
// into rollup_day, rollup_day_dim, rollup_month_dim and rollup_block_dim.
//
// REGISTER NEW ROLLUP COUNTERS HERE. When a migration adds a counter column
// to a rollup table and the closer starts filling it, append the column name
// to this list. That changes RollupLayout.Version, so `panel serve` rebuilds
// every closed day in the background with the full per-day recompute after
// the upgrade (old days would otherwise keep the column default). New rollup
// dimensions are registered in rollupDimensionCodes in
// internal/query/sql_rollup.go; TestRollupCounterColumnsMatchClosedSQL fails
// when an insert statement here writes an unregistered counter.
var rollupCounterColumns = []string{
	"visitors", "visits", "pageviews", "bounces", "duration_ms",
	"engagement_ms", "engaged_n", "scroll_sum", "scroll_n",
}

// RollupCounterColumns returns the sorted counter columns written by the closer.
func RollupCounterColumns() []string {
	counters := slices.Clone(rollupCounterColumns)
	slices.Sort(counters)
	return slices.Compact(counters)
}

// RollupLayout describes what one closed day's rollups contain: the dimension
// codes and the counter columns. Its Version is stored in rollup_state so an
// upgrade can tell which historical days need rebuilding.
type RollupLayout struct {
	Dimensions []int16
	Counters   []string
}

// NewRollupLayout returns a canonical (sorted, de-duplicated) copy.
func NewRollupLayout(dimensions []int16, counters []string) RollupLayout {
	dimensions = slices.Clone(dimensions)
	slices.Sort(dimensions)
	counters = slices.Clone(counters)
	slices.Sort(counters)
	return RollupLayout{Dimensions: slices.Compact(dimensions), Counters: slices.Compact(counters)}
}

// Version is a stable hash of the canonical layout.
func (l RollupLayout) Version() string {
	canonical := NewRollupLayout(l.Dimensions, l.Counters)
	var text strings.Builder
	text.WriteString("rollup-layout/v1;dims=")
	for i, dimension := range canonical.Dimensions {
		if i > 0 {
			text.WriteByte(',')
		}
		text.WriteString(strconv.Itoa(int(dimension)))
	}
	text.WriteString(";counters=")
	text.WriteString(strings.Join(canonical.Counters, ","))
	sum := sha256.Sum256([]byte(text.String()))
	return hex.EncodeToString(sum[:16])
}

// rollupBackfillMode is the cheapest rebuild that turns days built with the
// stored layout into days built with the current one.
type rollupBackfillMode string

const (
	rollupBackfillNone       rollupBackfillMode = ""
	rollupBackfillDimensions rollupBackfillMode = "dimensions"
	rollupBackfillFull       rollupBackfillMode = "full"
)

// planRollupBackfill compares layouts. A pure dimension addition rebuilds only
// the added dimensions; any counter change, removed dimension or unknown
// stored layout runs the full idempotent per-day recompute.
func planRollupBackfill(stored *RollupLayout, current RollupLayout) (rollupBackfillMode, []int16) {
	current = NewRollupLayout(current.Dimensions, current.Counters)
	if stored == nil {
		return rollupBackfillFull, nil
	}
	previous := NewRollupLayout(stored.Dimensions, stored.Counters)
	if previous.Version() == current.Version() {
		return rollupBackfillNone, nil
	}
	if !slices.Equal(previous.Counters, current.Counters) {
		return rollupBackfillFull, nil
	}
	for _, dimension := range previous.Dimensions {
		if !slices.Contains(current.Dimensions, dimension) {
			return rollupBackfillFull, nil
		}
	}
	return rollupBackfillDimensions, dimensionDifference(current.Dimensions, previous.Dimensions)
}
