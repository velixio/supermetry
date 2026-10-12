package dashboards

import (
	"errors"
	"strings"
	"testing"

	"supermetry/internal/api"
)

func TestBuiltinLayoutIsValid(t *testing.T) {
	layout, encoded, err := NormalizeLayout(BuiltinLayout())
	if err != nil {
		t.Fatalf("builtin layout invalid: %v", err)
	}
	if len(layout.Widgets) != 9 || len(encoded) > MaxLayoutBytes {
		t.Fatalf("widgets=%d bytes=%d", len(layout.Widgets), len(encoded))
	}
	kinds := map[string]bool{}
	for _, widget := range layout.Widgets {
		kinds[widget.Kind] = true
	}
	for _, want := range []string{KindKPIStrip, KindTimeSeries, KindBreakdown, KindMap, KindWeeklyRhythm, KindGoals} {
		if !kinds[want] {
			t.Errorf("builtin lacks %s", want)
		}
	}
}

func TestNormalizeLayoutBounds(t *testing.T) {
	retention := func(id string) api.DashboardWidget {
		return api.DashboardWidget{Id: id, Kind: KindRetention, W: 4, H: 2}
	}
	valid := api.DashboardLayout{SchemaVersion: SchemaVersion, Widgets: []api.DashboardWidget{retention("a")}}
	if _, _, err := NormalizeLayout(valid); err != nil {
		t.Fatal(err)
	}

	tooMany := api.DashboardLayout{SchemaVersion: SchemaVersion}
	for i := 0; i <= MaxWidgets; i++ {
		tooMany.Widgets = append(tooMany.Widgets, retention("w"+strings.Repeat("x", i)))
	}
	if _, _, err := NormalizeLayout(tooMany); !errors.Is(err, ErrInvalid) {
		t.Fatalf("25 widgets: %v", err)
	}

	// 24 widgets of 10 filters with 50 long values each exceed the byte cap.
	big := api.DashboardLayout{SchemaVersion: SchemaVersion}
	values := make([]string, 50)
	for i := range values {
		values[i] = strings.Repeat("v", 90)
	}
	for i := 0; i < MaxWidgets; i++ {
		filters := make([]api.Filter, 10)
		for j := range filters {
			filters[j] = api.Filter{Dimension: "source", Operator: api.Is, Values: values}
		}
		widget := retention("w" + strings.Repeat("x", i))
		widget.Filters = &filters
		big.Widgets = append(big.Widgets, widget)
	}
	if _, _, err := NormalizeLayout(big); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "bytes") {
		t.Fatalf("oversized layout: %v", err)
	}

	bad := valid
	bad.SchemaVersion = 2
	if _, _, err := NormalizeLayout(bad); !errors.Is(err, ErrInvalid) {
		t.Fatalf("schema version 2: %v", err)
	}
}

func TestPropDimensionAndMetricSets(t *testing.T) {
	prop, limit := "prop:plan", 10
	ok := api.DashboardLayout{SchemaVersion: SchemaVersion, Widgets: []api.DashboardWidget{{
		Id: "p", Kind: KindBreakdown, W: 6, H: 4,
		Config: &api.DashboardWidgetConfig{Dimension: &prop, Limit: &limit, Metrics: &[]api.Metric{api.Visitors}},
	}}}
	if _, _, err := NormalizeLayout(ok); err != nil {
		t.Fatal(err)
	}
	empty := "prop:"
	ok.Widgets[0].Config.Dimension = &empty
	if _, _, err := NormalizeLayout(ok); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty prop key: %v", err)
	}
}
