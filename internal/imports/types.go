package imports

import (
	"fmt"
	"time"
)

// Parse limits keep a hostile or oversized upload from exhausting memory or
// disk bandwidth. They are variables so tests can lower them.
var (
	maxZIPEntries    = 1000
	maxZIPEntryBytes = uint64(2 << 30)
	maxZIPTotalBytes = uint64(8 << 30)
	maxAggregateKeys = 2_000_000
	maxUmamiSessions = 2_000_000
)

// EventValuePrefix distinguishes logical event-name rows from page rows in
// the original imported_day_dim layout, which has no dedicated event code.
const EventValuePrefix = "\x1e:supermetry:event:v1:"

const (
	// DimEvent is a logical import-only dimension. Store maps it to a tagged
	// representation because the deployed imported_day_dim schema predates
	// event-name breakdowns and only accepts dimension codes 1 through 10.
	DimEvent   int16 = 0
	DimPage    int16 = 1
	DimSource  int16 = 2
	DimCountry int16 = 3
	DimBrowser int16 = 4
	DimOS      int16 = 5
	DimDevice  int16 = 6
	DimEntry   int16 = 7
	DimExit    int16 = 8
	DimRegion  int16 = 9
	DimCity    int16 = 10
)

type Metrics struct {
	Visitors       int64
	Visits         int64
	Pageviews      int64
	Bounces        int64
	VisitDurationS int64
	Events         int64
}

type DimensionKey struct {
	Day   time.Time
	Dim   int16
	Value string
}

type Aggregate struct {
	Days       map[time.Time]Metrics
	Dimensions map[DimensionKey]Metrics
}

type ParseStats struct {
	Rows    int64
	BadRows int64
}

func newAggregate() Aggregate {
	return Aggregate{Days: make(map[time.Time]Metrics), Dimensions: make(map[DimensionKey]Metrics)}
}

func addMetrics(left, right Metrics) Metrics {
	left.Visitors += right.Visitors
	left.Visits += right.Visits
	left.Pageviews += right.Pageviews
	left.Bounces += right.Bounces
	left.VisitDurationS += right.VisitDurationS
	left.Events += right.Events
	return left
}

func (a Aggregate) addDay(day time.Time, metrics Metrics) {
	a.Days[day] = addMetrics(a.Days[day], metrics)
}

func (a Aggregate) addDimension(key DimensionKey, metrics Metrics) {
	if key.Value == "" {
		return
	}
	a.Dimensions[key] = addMetrics(a.Dimensions[key], metrics)
}

// checkLimit reports a fatal error once the aggregate holds more distinct
// day and dimension keys than an import may keep in memory.
func (a Aggregate) checkLimit() error {
	if len(a.Days)+len(a.Dimensions) > maxAggregateKeys {
		return fatalError{fmt.Errorf("import has more than %d distinct day and dimension values", maxAggregateKeys)}
	}
	return nil
}
