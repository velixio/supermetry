package dashboards

import (
	"time"

	"supermetry/internal/api"
)

// BuiltinName is the display name of the unsaved default dashboard.
const BuiltinName = "Overview"

func widgetConfig(metrics ...api.Metric) *api.DashboardWidgetConfig {
	return &api.DashboardWidgetConfig{Metrics: &metrics}
}

func breakdown(id, title, dimension string, x, y int) api.DashboardWidget {
	limit := 10
	config := widgetConfig(api.Visitors)
	config.Dimension, config.Limit = &dimension, &limit
	return api.DashboardWidget{Id: id, Kind: KindBreakdown, Title: &title, X: x, Y: y, W: 6, H: 5, Config: config}
}

// BuiltinLayout expresses the standard site dashboard in the layout model:
// the KPI strip, the main chart, four breakdown cards, the map and weekly
// rhythm band, and the goals card. It is rebuilt on every call so callers may
// modify the result.
func BuiltinLayout() api.DashboardLayout {
	period, compare := "30d", "off"
	visitors := api.Visitors
	series := api.DashboardWidgetConfig{Metric: &visitors}
	mapConfig := api.DashboardWidgetConfig{Metric: &visitors}
	return api.DashboardLayout{
		SchemaVersion: api.DashboardLayoutSchemaVersion(SchemaVersion),
		Period:        &period,
		Compare:       &compare,
		Widgets: []api.DashboardWidget{
			{Id: "kpis", Kind: KindKPIStrip, X: 0, Y: 0, W: 12, H: 2, Config: widgetConfig(
				api.Visitors, api.Visits, api.Pageviews, api.ViewsPerVisit, api.BounceRate, api.VisitDuration)},
			{Id: "chart", Kind: KindTimeSeries, X: 0, Y: 2, W: 12, H: 5, Config: &series},
			breakdown("pages", "Pages", "page", 0, 7),
			breakdown("sources", "Sources", "source", 6, 7),
			breakdown("location", "Location", "country", 0, 12),
			breakdown("environment", "Environment", "device", 6, 12),
			{Id: "where", Kind: KindMap, X: 0, Y: 17, W: 7, H: 6, Config: &mapConfig},
			{Id: "rhythm", Kind: KindWeeklyRhythm, X: 7, Y: 17, W: 5, H: 6, Config: &api.DashboardWidgetConfig{Metric: &visitors}},
			{Id: "goals", Kind: KindGoals, X: 0, Y: 23, W: 12, H: 4},
		},
	}
}

// Builtin returns the unsaved default dashboard for a site.
func Builtin(siteID int32) api.Dashboard {
	return api.Dashboard{
		Id: 0, SiteId: siteID, Name: BuiltinName, Layout: BuiltinLayout(),
		IsDefault: true, Builtin: true, Version: 1, CreatedAt: time.Time{}, UpdatedAt: time.Time{},
	}
}
