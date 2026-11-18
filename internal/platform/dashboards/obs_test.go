package dashboards

import (
	"errors"
	"testing"

	"supermetry/internal/api"
	"supermetry/internal/sites"
)

func obsLayout(query *api.ObsMetricWidgetQuery, chart string) api.DashboardLayout {
	config := &api.DashboardWidgetConfig{Query: query}
	if chart != "" {
		config.Chart = &chart
	}
	return api.DashboardLayout{SchemaVersion: SchemaVersion, Widgets: []api.DashboardWidget{
		{Id: "m1", Kind: KindObsMetric, W: 6, H: 3, Config: config},
	}}
}

func TestObsMetricWidgetAppSitesOnly(t *testing.T) {
	groupBy := []string{"service"}
	query := &api.ObsMetricWidgetQuery{Name: "http.requests", Agg: "sum", Interval: "1h", GroupBy: &groupBy}
	layout := obsLayout(query, "bar")
	if _, _, err := NormalizeLayoutFor(layout, sites.KindApp); err != nil {
		t.Fatalf("app site: %v", err)
	}
	if _, _, err := NormalizeLayoutFor(layout, sites.KindWeb); !errors.Is(err, ErrInvalid) {
		t.Fatalf("web site: err = %v, want ErrInvalid", err)
	}
	if _, _, err := NormalizeLayout(layout); !errors.Is(err, ErrInvalid) {
		t.Fatalf("NormalizeLayout (web) = %v, want ErrInvalid", err)
	}
}

func TestObsMetricWidgetConfigValidation(t *testing.T) {
	bad := map[string]api.DashboardLayout{
		"no query":     obsLayout(nil, "line"),
		"bad agg":      obsLayout(&api.ObsMetricWidgetQuery{Name: "m", Agg: "median", Interval: "1h"}, ""),
		"bad chart":    obsLayout(&api.ObsMetricWidgetQuery{Name: "m", Agg: "sum", Interval: "1h"}, "pie"),
		"empty name":   obsLayout(&api.ObsMetricWidgetQuery{Agg: "sum", Interval: "1h"}, ""),
		"bad interval": obsLayout(&api.ObsMetricWidgetQuery{Name: "m", Agg: "sum", Interval: "7d"}, ""),
	}
	for name, layout := range bad {
		if _, _, err := NormalizeLayoutFor(layout, sites.KindApp); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	// A query on another kind is rejected.
	other := api.DashboardLayout{SchemaVersion: SchemaVersion, Widgets: []api.DashboardWidget{
		{Id: "r", Kind: KindRetention, W: 4, H: 2, Config: &api.DashboardWidgetConfig{Query: &api.ObsMetricWidgetQuery{Name: "m", Agg: "sum", Interval: "1h"}}},
	}}
	if _, _, err := NormalizeLayoutFor(other, sites.KindApp); !errors.Is(err, ErrInvalid) {
		t.Errorf("query on retention: err = %v", err)
	}
}
