package query_test

import (
	"testing"

	"supermetry/internal/api"
	"supermetry/internal/query"
)

func TestUTMSourceDiffersFromBlendedSourceAcrossStorageTiers(t *testing.T) {
	_, engine := setupTiny(t)
	req := queryRequest([]string{"utm_source"}, nil)

	for _, route := range []query.Route{query.RouteHot, query.RouteClosed, query.RouteRollup} {
		t.Run(routeName(route), func(t *testing.T) {
			got := mustQuery(t, engine.WithForcedRoute(route), req)
			if len(got.Rows) != 1 {
				t.Fatalf("rows = %+v, want only tagged traffic", got.Rows)
			}
			row := got.Rows[0]
			if row["utm_source"] != "newsletter" || row["visitors"] != float64(2) || row["visits"] != float64(2) || row["pageviews"] != float64(4) {
				t.Fatalf("UTM source row = %+v, want newsletter visitors=2 visits=2 pageviews=4", row)
			}
		})
	}

	source := mustQuery(t, engine.WithForcedRoute(query.RouteHot), queryRequest([]string{"source"}, nil))
	if len(source.Rows) != 2 {
		t.Fatalf("source rows = %+v, want blended Google and Direct traffic", source.Rows)
	}
}

func TestUTMSourceFilterUsesRawTag(t *testing.T) {
	_, engine := setupTiny(t)
	req := queryRequest(nil, []api.Filter{filter("utm_source", api.Is, "newsletter")})
	for _, route := range []query.Route{query.RouteHot, query.RouteClosed} {
		got := mustQuery(t, engine.WithForcedRoute(route), req)
		if got.Totals["visitors"] != float64(2) || got.Totals["visits"] != float64(2) || got.Totals["pageviews"] != float64(4) {
			t.Fatalf("route %s totals = %+v, want tagged traffic only", routeName(route), got.Totals)
		}
	}
}

func TestUTMSourceCustomEventBreakdownUsesRawTag(t *testing.T) {
	_, engine := setupTiny(t)
	req := queryRequest([]string{"event", "utm_source"}, nil)
	req.Metrics = []api.Metric{api.Visitors, api.Events, api.Conversions}

	for _, route := range []query.Route{query.RouteHot, query.RouteClosed} {
		got := mustQuery(t, engine.WithForcedRoute(route), req)
		if len(got.Rows) != 1 {
			t.Fatalf("route %s rows = %+v, want only tagged custom events", routeName(route), got.Rows)
		}
		row := got.Rows[0]
		if row["event"] != "signup" || row["utm_source"] != "newsletter" || row["visitors"] != float64(1) || row["events"] != float64(1) {
			t.Fatalf("route %s row = %+v, want one tagged signup", routeName(route), row)
		}
	}
}

func routeName(route query.Route) string {
	switch route {
	case query.RouteHot:
		return "hot"
	case query.RouteClosed:
		return "closed"
	case query.RouteRollup:
		return "rollup"
	default:
		return "other"
	}
}
