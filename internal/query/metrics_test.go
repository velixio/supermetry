package query_test

import (
	"math"
	"testing"

	"panel/internal/query"
)

func TestMetricFormulas(t *testing.T) {
	s := query.Sums{
		Visitors:      80,
		Visits:        100,
		Pageviews:     250,
		Bounces:       55,
		DurationMS:    6_000_000,
		EngagementMS:  3_000_000,
		ScrollSum:     15_000,
		ScrollN:       250,
		Converted:     8,
		RevenueMicros: 12_500_000,
		RevenueEvents: 5,
	}
	want := map[query.Metric]float64{
		query.Visitors:       80,
		query.Visits:         100,
		query.Pageviews:      250,
		query.ViewsPerVisit:  2.5,
		query.BounceRate:     55,
		query.VisitDuration:  60,
		query.EngagementTime: 30,
		query.ScrollDepth:    60,
		query.ConversionRate: 10,
		query.TotalRevenue:   12.5,
		query.AverageRevenue: 2.5,
	}
	for metric, expected := range want {
		if got := metric.Final(s); math.Abs(got-expected) > 1e-9 {
			t.Errorf("%s: %v want %v", metric, got, expected)
		}
	}
	if got := query.BounceRate.Final(query.Sums{}); got != 0 {
		t.Errorf("bounce on empty = %v", got)
	}
	if got := query.AverageRevenue.Final(query.Sums{}); got != 0 {
		t.Errorf("average revenue on empty = %v", got)
	}
}
