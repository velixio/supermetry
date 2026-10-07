package query_test

import (
	"reflect"
	"testing"
	"time"

	"panel/internal/api"
	"panel/internal/query"
)

func TestPlanRoutes(t *testing.T) {
	closedThrough := day("2026-10-06")
	tests := []struct {
		name    string
		filters []api.Filter
		want    query.Route
	}{
		{name: "unfiltered", want: query.RouteRollup},
		{name: "one visitor dimension", filters: []api.Filter{filter("country", api.Is, "US")}, want: query.RouteVisitorDay},
		{name: "two visitor dimensions", filters: []api.Filter{filter("country", api.Is, "US"), filter("browser", api.Is, "Chrome")}, want: query.RouteVisitorDay},
		{name: "session dimension", filters: []api.Filter{filter("source", api.Is, "Google")}, want: query.RouteCube},
		{name: "region filter", filters: []api.Filter{filter("region", api.Is, "California")}, want: query.RouteClosed},
		{name: "city filter", filters: []api.Filter{filter("city", api.Is, "Bengaluru")}, want: query.RouteClosed},
		{name: "page operator", filters: []api.Filter{filter("page", api.Contains, "/blog")}, want: query.RouteClosed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parts := query.Plan(normalized("2026-09-01", "2026-10-07", nil, tt.filters), closedThrough)
			want := []query.Part{
				{Route: tt.want, From: day("2026-09-01"), To: day("2026-10-06")},
				{Route: query.RouteHot, From: day("2026-10-07"), To: day("2026-10-07")},
			}
			if !reflect.DeepEqual(parts, want) {
				t.Fatalf("Plan() = %+v, want %+v", parts, want)
			}
		})
	}
}

func TestPlanUsesRollupsForRegionAndCityBreakdowns(t *testing.T) {
	for _, dimension := range []string{"region", "city"} {
		t.Run(dimension, func(t *testing.T) {
			parts := query.Plan(normalized("2026-10-01", "2026-10-02", []string{dimension}, nil), day("2026-10-02"))
			if len(parts) != 1 || parts[0].Route != query.RouteRollup {
				t.Fatalf("Plan() = %+v, want one rollup part", parts)
			}
		})
	}
}

func TestPlanUsesClosedForUnsupportedRollupShape(t *testing.T) {
	tests := []struct {
		name       string
		metrics    []api.Metric
		dimensions []string
	}{
		{name: "mixed-grain dimensions", metrics: []api.Metric{api.Visitors}, dimensions: []string{"country", "page"}},
		{name: "events metric", metrics: []api.Metric{api.Events}, dimensions: []string{"country"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parts := query.Plan(normalized("2026-10-01", "2026-10-02", tt.dimensions, nil, tt.metrics...), day("2026-10-02"))
			if len(parts) != 1 || parts[0].Route != query.RouteClosed {
				t.Fatalf("Plan() = %+v, want one closed part", parts)
			}
		})
	}
}

func normalized(from, to string, dimensions []string, filters []api.Filter, metrics ...api.Metric) query.Normalized {
	if len(metrics) == 0 {
		metrics = []api.Metric{api.Visitors, api.Visits, api.Pageviews}
	}
	return query.Normalized{
		SiteID:     1,
		From:       day(from),
		To:         day(to),
		Location:   time.UTC,
		Metrics:    metrics,
		Dimensions: dimensions,
		Filters:    filters,
		Limit:      100,
	}
}

func filter(dimension string, operator api.FilterOperator, values ...string) api.Filter {
	return api.Filter{Dimension: dimension, Operator: operator, Values: values}
}

func day(value string) time.Time {
	parsed, err := time.Parse(time.DateOnly, value)
	if err != nil {
		panic(err)
	}
	return parsed
}
