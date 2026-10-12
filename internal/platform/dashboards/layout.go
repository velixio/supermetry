// Package dashboards stores per-site saved dashboards. A dashboard is a
// versioned JSON layout of widgets; every widget kind maps to an existing query
// or report capability and carries only whitelisted options, so a layout can
// never express SQL or an unbounded query.
package dashboards

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"supermetry/internal/api"
	"supermetry/internal/query"
)

// Limits applied to every layout and dashboard.
const (
	SchemaVersion  = 1
	MaxPerSite     = 50
	MaxWidgets     = 24
	MaxLayoutBytes = 32 << 10
	GridColumns    = 12
	maxRows        = 200
	maxHeight      = 12
	maxNameRunes   = 80
	maxTitleRunes  = 80
	maxFilters     = 10
	maxDimension   = 60
)

// ErrInvalid wraps problems the caller can fix.
var ErrInvalid = errors.New("invalid dashboard")

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// Widget kinds.
const (
	KindKPIStrip     = "kpi_strip"
	KindTimeSeries   = "time_series"
	KindBreakdown    = "breakdown"
	KindMap          = "map"
	KindWeeklyRhythm = "weekly_rhythm"
	KindGoals        = "goals"
	KindFunnel       = "funnel"
	KindRetention    = "retention"
	KindWebVitals    = "web_vitals"
)

// options says which config fields a kind accepts and how many metrics it may list.
type options struct {
	metric, dimension, limit, funnel bool
	minMetrics, maxMetrics           int // 0 maxMetrics: the metrics field is not accepted
	requireMetric, requireDimension  bool
	requireFunnel                    bool
	metricSet                        map[api.Metric]bool // nil: any known metric
}

var visitorsOrPageviews = map[api.Metric]bool{api.Visitors: true, api.Pageviews: true}

var kinds = map[string]options{
	KindKPIStrip:     {minMetrics: 1, maxMetrics: 6},
	KindTimeSeries:   {metric: true, requireMetric: true},
	KindBreakdown:    {dimension: true, limit: true, requireDimension: true, minMetrics: 1, maxMetrics: 4},
	KindMap:          {metric: true, metricSet: visitorsOrPageviews},
	KindWeeklyRhythm: {metric: true, metricSet: visitorsOrPageviews},
	KindGoals:        {limit: true},
	KindFunnel:       {funnel: true, requireFunnel: true},
	KindRetention:    {},
	KindWebVitals:    {},
}

var periods = map[string]bool{
	"today": true, "yesterday": true, "7d": true, "28d": true, "30d": true, "91d": true,
	"6mo": true, "12mo": true, "month": true, "last_month": true, "year": true,
}

var compares = map[string]bool{"off": true, "previous_period": true, "previous_year": true}

// ValidateName trims and checks a dashboard name.
func ValidateName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > maxNameRunes || strings.ContainsRune(name, 0) {
		return "", invalid("name must be 1 to %d characters", maxNameRunes)
	}
	return name, nil
}

// NormalizeLayout validates a layout against the schema and limits and returns
// the canonical form to store (titles trimmed, nil slices made empty). The
// result re-encodes within MaxLayoutBytes.
func NormalizeLayout(layout api.DashboardLayout) (api.DashboardLayout, []byte, error) {
	if int(layout.SchemaVersion) != SchemaVersion {
		return layout, nil, invalid("schema_version must be %d", SchemaVersion)
	}
	if layout.Period != nil && !periods[*layout.Period] {
		return layout, nil, invalid("unknown period %q", *layout.Period)
	}
	if layout.Compare != nil && !compares[*layout.Compare] {
		return layout, nil, invalid("unknown compare mode %q", *layout.Compare)
	}
	if err := validateFilters("layout", layout.Filters); err != nil {
		return layout, nil, err
	}
	if len(layout.Widgets) > MaxWidgets {
		return layout, nil, invalid("a dashboard holds at most %d widgets", MaxWidgets)
	}
	if layout.Widgets == nil {
		layout.Widgets = []api.DashboardWidget{}
	}
	seen := make(map[string]bool, len(layout.Widgets))
	for i := range layout.Widgets {
		widget := &layout.Widgets[i]
		if err := normalizeWidget(widget); err != nil {
			return layout, nil, err
		}
		if seen[widget.Id] {
			return layout, nil, invalid("duplicate widget id %q", widget.Id)
		}
		seen[widget.Id] = true
	}
	encoded, err := json.Marshal(layout)
	if err != nil {
		return layout, nil, err
	}
	if len(encoded) > MaxLayoutBytes {
		return layout, nil, invalid("layout exceeds %d bytes", MaxLayoutBytes)
	}
	return layout, encoded, nil
}

func validateFilters(where string, filters *[]api.Filter) error {
	if filters == nil {
		return nil
	}
	if len(*filters) > maxFilters {
		return invalid("%s: at most %d filters are allowed", where, maxFilters)
	}
	if err := query.ValidateFilters(*filters); err != nil {
		return invalid("%s: %v", where, err)
	}
	return nil
}

func validWidgetID(id string) bool {
	if id == "" || len(id) > 40 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case (c == '-' || c == '_') && i > 0:
		default:
			return false
		}
	}
	return true
}

func normalizeWidget(widget *api.DashboardWidget) error {
	if !validWidgetID(widget.Id) {
		return invalid("widget id %q must be 1 to 40 characters of a-z, 0-9, - or _", widget.Id)
	}
	where := "widget " + widget.Id
	spec, ok := kinds[widget.Kind]
	if !ok {
		return invalid("%s: unknown kind %q", where, widget.Kind)
	}
	if widget.Title != nil {
		title := strings.TrimSpace(*widget.Title)
		if utf8.RuneCountInString(title) > maxTitleRunes || strings.ContainsRune(title, 0) {
			return invalid("%s: title must be at most %d characters", where, maxTitleRunes)
		}
		widget.Title = &title
	}
	if widget.X < 0 || widget.X >= GridColumns || widget.W < 1 || widget.X+widget.W > GridColumns {
		return invalid("%s: x and w must fit the %d-column grid", where, GridColumns)
	}
	if widget.Y < 0 || widget.Y >= maxRows || widget.H < 1 || widget.H > maxHeight {
		return invalid("%s: y must be 0 to %d and h 1 to %d", where, maxRows-1, maxHeight)
	}
	if widget.Period != nil && !periods[*widget.Period] {
		return invalid("%s: unknown period %q", where, *widget.Period)
	}
	if err := validateFilters(where, widget.Filters); err != nil {
		return err
	}
	return validateConfig(where, spec, widget.Config)
}

func validateConfig(where string, spec options, config *api.DashboardWidgetConfig) error {
	if config == nil {
		config = &api.DashboardWidgetConfig{}
	}
	if config.Metric != nil {
		if !spec.metric {
			return invalid("%s: metric is not accepted by this kind", where)
		}
		if !config.Metric.Valid() || (spec.metricSet != nil && !spec.metricSet[*config.Metric]) {
			return invalid("%s: metric %q is not allowed", where, *config.Metric)
		}
	} else if spec.requireMetric {
		return invalid("%s: metric is required", where)
	}
	count := 0
	if config.Metrics != nil {
		count = len(*config.Metrics)
		if spec.maxMetrics == 0 {
			return invalid("%s: metrics is not accepted by this kind", where)
		}
		if count > spec.maxMetrics {
			return invalid("%s: at most %d metrics are allowed", where, spec.maxMetrics)
		}
		for _, metric := range *config.Metrics {
			if !metric.Valid() {
				return invalid("%s: unknown metric %q", where, metric)
			}
		}
	}
	if count < spec.minMetrics {
		return invalid("%s: at least %d metrics are required", where, spec.minMetrics)
	}
	if config.Dimension != nil {
		if !spec.dimension {
			return invalid("%s: dimension is not accepted by this kind", where)
		}
		dimension := *config.Dimension
		if len(dimension) > maxDimension || !query.ValidDimension(dimension) || dimension == "prop:" {
			return invalid("%s: unknown dimension %q", where, dimension)
		}
	} else if spec.requireDimension {
		return invalid("%s: dimension is required", where)
	}
	if config.Limit != nil {
		if !spec.limit {
			return invalid("%s: limit is not accepted by this kind", where)
		}
		if *config.Limit < 1 || *config.Limit > 50 {
			return invalid("%s: limit must be 1 to 50", where)
		}
	}
	if config.FunnelId != nil {
		if !spec.funnel {
			return invalid("%s: funnel_id is not accepted by this kind", where)
		}
		if *config.FunnelId < 1 {
			return invalid("%s: funnel_id must be positive", where)
		}
	} else if spec.requireFunnel {
		return invalid("%s: funnel_id is required", where)
	}
	return nil
}
