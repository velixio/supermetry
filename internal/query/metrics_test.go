package query_test

import (
	"math"
	"testing"

	"supermetry/internal/query"
)

func TestMetricFormulas(t *testing.T) {
	s := query.Sums{
		Visitors:      80,
		Visits:        100,
		Pageviews:     250,
		Bounces:       55,
		DurationMS:    6_000_000,
		EngagementMS:  3_000_000,
		Exits:         25,
		EngagedN:      100,
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
		query.ExitRate:       10,
		query.TimeOnPage:     30,
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

func TestPageMetricFormulas(t *testing.T) {
	tests := []struct {
		name   string
		metric query.Metric
		sums   query.Sums
		want   float64
	}{
		{name: "exit rate", metric: query.ExitRate, sums: query.Sums{Pageviews: 8, Exits: 2}, want: 25},
		{name: "exit rate is capped", metric: query.ExitRate, sums: query.Sums{Pageviews: 2, Exits: 3}, want: 100},
		{name: "exit rate without pageviews", metric: query.ExitRate, sums: query.Sums{Exits: 1}, want: 0},
		{name: "time on page", metric: query.TimeOnPage, sums: query.Sums{EngagementMS: 90_000, EngagedN: 3}, want: 30},
		{name: "time on page without engaged sessions", metric: query.TimeOnPage, sums: query.Sums{EngagementMS: 90_000}, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.metric.Final(tt.sums); got != tt.want {
				t.Fatalf("Final() = %v, want %v", got, tt.want)
			}
		})
	}
}
