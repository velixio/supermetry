package storageusage

// Source describes a PostgreSQL relation whose logical row bytes are either
// attributed to sites or deliberately omitted from the estimate.
type Source struct {
	Table           string
	Category        Category
	Partitioned     bool
	OwnerTable      string
	OwnerForeignKey string
	ExcludedReason  string
}

var sourceRegistry = []Source{
	{Table: "alert_delivery_log", Category: CategoryAuxiliary},
	{Table: "alerts", Category: CategoryAuxiliary},
	{Table: "annotations", Category: CategoryAuxiliary},
	{Table: "cohorts", Category: CategoryAuxiliary},
	{Table: "cube_day", Category: CategoryRetained, Partitioned: true},
	{Table: "custom_closed", Category: CategoryRetained, Partitioned: true},
	{Table: "dashboards", Category: CategoryAuxiliary},
	{Table: "data_purge_requests", Category: CategoryAuxiliary},
	{Table: "data_purge_sessions", Category: CategoryAuxiliary, OwnerTable: "data_purge_requests", OwnerForeignKey: "request_id"},
	{Table: "day_close", Category: CategoryAuxiliary},
	{Table: "dirty_day", Category: CategoryAuxiliary},
	{Table: "events", Category: CategoryHot, Partitioned: true},
	{Table: "funnels", Category: CategoryAuxiliary},
	{Table: "goals", Category: CategoryAuxiliary},
	{Table: "heatmap_cells", Category: CategoryAuxiliary, Partitioned: true},
	{Table: "imported_day", Category: CategoryAuxiliary},
	{Table: "imported_day_dim", Category: CategoryAuxiliary},
	{Table: "imports", Category: CategoryAuxiliary},
	{Table: "integration_connections", Category: CategoryAuxiliary},
	{Table: "integration_oauth_states", Category: CategoryAuxiliary},
	{Table: "links", Category: CategoryAuxiliary},
	{Table: "link_clicks_daily", Category: CategoryAuxiliary, OwnerTable: "links", OwnerForeignKey: "link_id"},
	{Table: "pixel_impressions_daily", Category: CategoryAuxiliary},
	{Table: "pixel_impressions_hourly", Category: CategoryAuxiliary},
	{Table: "pixels", Category: CategoryAuxiliary},
	{Table: "pixel_views_daily", Category: CategoryAuxiliary, OwnerTable: "pixels", OwnerForeignKey: "pixel_id"},
	{Table: "profile_erasure_blobs", Category: CategoryAuxiliary, OwnerTable: "profile_erasure_requests", OwnerForeignKey: "request_id"},
	{Table: "profile_erasure_requests", Category: CategoryAuxiliary},
	{Table: "profile_erasure_sessions", Category: CategoryAuxiliary, OwnerTable: "profile_erasure_requests", OwnerForeignKey: "request_id"},
	{Table: "profile_tombstones", Category: CategoryAuxiliary},
	{Table: "profiles", Category: CategoryAuxiliary},
	{Table: "replay_saved", Category: CategoryAuxiliary},
	{Table: "replay_sessions", Category: CategoryAuxiliary, Partitioned: true},
	{Table: "report_subscriptions", Category: CategoryAuxiliary},
	{Table: "rollup_block_dim", Category: CategoryRetained},
	{Table: "rollup_block_done", Category: CategoryRetained},
	{Table: "rollup_day", Category: CategoryRetained, Partitioned: true},
	{Table: "rollup_day_dim", Category: CategoryRetained, Partitioned: true},
	{Table: "rollup_month_dim", Category: CategoryRetained},
	{Table: "rollup_month_done", Category: CategoryRetained},
	{Table: "search_console_rows", Category: CategoryAuxiliary},
	{Table: "segments", Category: CategoryAuxiliary},
	{Table: "sessions", Category: CategoryHot, Partitioned: true},
	{Table: "sessions_closed", Category: CategoryRetained, Partitioned: true},
	{Table: "shared_links", Category: CategoryAuxiliary},
	{Table: "site_bot_rule_daily", Category: CategoryAuxiliary},
	{Table: "site_drop_daily", Category: CategoryAuxiliary},
	{Table: "site_last_event", Category: CategoryAuxiliary},
	{Table: "tracking_pixels", Category: CategoryAuxiliary},
	{Table: "verified_page_day", Category: CategoryRetained, Partitioned: true},
	{Table: "verified_page_sessions", Category: CategoryRetained, Partitioned: true},
	{Table: "visitor_day", Category: CategoryRetained, Partitioned: true},
	{Table: "visitors_per_minute", Category: CategoryAuxiliary},

	// Reconciliation metadata is instance overhead. Counting it would make the
	// estimate recursively depend on the estimate itself.
	{Table: "site_storage_usage", ExcludedReason: "published reconciliation metadata"},
	{Table: "storage_usage_run_sites", ExcludedReason: "reconciliation metadata"},
	{Table: "storage_usage_stage", ExcludedReason: "temporary reconciliation metadata"},
}

func SourceRegistry() []Source {
	return append([]Source(nil), sourceRegistry...)
}
