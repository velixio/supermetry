package health_test

import (
	"bytes"
	"strconv"
	"strings"
	"testing"

	"panel/internal/health"
)

func TestGeoIPSourceIsBoundedAndOneHot(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{name: "maxmind", source: "maxmind", want: "maxmind"},
		{name: "dbip", source: "dbip", want: "dbip"},
		{name: "invalid preserves current source", source: "other", want: "dbip"},
		{name: "empty preserves current source", source: "", want: "dbip"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			health.SetGeoIPSource("dbip")
			health.SetGeoIPSource(tt.source)

			if got := health.GeoIPSource(); got != tt.want {
				t.Fatalf("GeoIPSource() = %q, want %q", got, tt.want)
			}
			snapshot := health.Snapshot()
			wantMaxMind := float64(0)
			wantDBIP := float64(1)
			if tt.want == "maxmind" {
				wantMaxMind, wantDBIP = 1, 0
			}
			if got := snapshot[`geoip_source{source="maxmind"}`]; got != wantMaxMind {
				t.Errorf("maxmind source gauge = %v, want %v", got, wantMaxMind)
			}
			if got := snapshot[`geoip_source{source="dbip"}`]; got != wantDBIP {
				t.Errorf("dbip source gauge = %v, want %v", got, wantDBIP)
			}
		})
	}
}

func TestGeoIPMetricsPrometheusTextFormat(t *testing.T) {
	const lastSuccess = 1770000000
	beforeFailures := health.GeoIPUpdateFailures.Load()
	health.GeoIPUpdateLastSuccessUnix.Store(lastSuccess)
	health.GeoIPUpdateFailures.Add(2)
	health.SetGeoIPSource("maxmind")
	t.Cleanup(func() { health.SetGeoIPSource("dbip") })
	if got := health.Snapshot()["geoip_update_last_success_unix"]; got != lastSuccess {
		t.Fatalf("last success gauge = %v, want %v", got, lastSuccess)
	}

	var b bytes.Buffer
	health.WritePrometheus(&b)
	wantFailures := strconv.FormatUint(beforeFailures+2, 10)
	for _, want := range []string{
		"# TYPE panel_geoip_update_last_success_unix gauge",
		"panel_geoip_update_last_success_unix 1.77e+09",
		"# TYPE panel_geoip_update_failures_total counter",
		"panel_geoip_update_failures_total " + wantFailures,
		"# TYPE panel_geoip_source gauge",
		`panel_geoip_source{source="dbip"} 0`,
		`panel_geoip_source{source="maxmind"} 1`,
	} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("missing %q in\n%s", want, b.String())
		}
	}
}

func TestPrometheusTextFormat(t *testing.T) {
	health.IngestAccepted.Add(3)
	health.IngestRevenueInvalid.Add(2)
	health.HeatmapInvalid.Add(1)
	health.HeatmapDropped.Add(2)
	health.BotsFiltered[1].Add(2)
	health.ExclusionsFiltered[health.ExclusionPath].Add(4)
	health.ExclusionsFiltered[health.ExclusionCountry].Add(5)
	var b bytes.Buffer
	health.WritePrometheus(&b)
	for _, want := range []string{
		"# TYPE panel_ingest_accepted_total counter",
		"panel_ingest_accepted_total 3",
		"# TYPE panel_ingest_revenue_invalid_total counter",
		"panel_ingest_revenue_invalid_total 2",
		"# TYPE panel_heatmap_invalid_total counter",
		"# TYPE panel_heatmap_dropped_total counter",
		`panel_bots_filtered_total{reason="ua"} 2`,
		`panel_ingest_excluded_total{reason="path"} 4`,
		`panel_ingest_excluded_total{reason="country"} 5`,
	} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("missing %q in\n%s", want, b.String())
		}
	}
}

func TestDeliveryFailuresAreExported(t *testing.T) {
	before := health.AlertDeliveryFailures.Load()
	health.AlertDeliveryFailures.Add(2)

	snapshot := health.Snapshot()
	if got := snapshot["alert_delivery_failures_total"]; got != float64(before+2) {
		t.Fatalf("alert_delivery_failures_total = %v, want %d", got, before+2)
	}

	var b strings.Builder
	health.WritePrometheus(&b)
	if !strings.Contains(b.String(), "panel_alert_delivery_failures_total") {
		t.Fatalf("Prometheus output missing alert delivery failures:\n%s", b.String())
	}
}

func TestProfilesSuppressedIsExported(t *testing.T) {
	snapshot := health.Snapshot()
	if _, ok := snapshot["ingest_profiles_suppressed_total"]; !ok {
		t.Fatal("snapshot missing ingest_profiles_suppressed_total")
	}

	var b strings.Builder
	health.WritePrometheus(&b)
	if !strings.Contains(b.String(), "# TYPE panel_ingest_profiles_suppressed_total counter") {
		t.Fatalf("Prometheus output missing suppressed profile counter:\n%s", b.String())
	}
}

func TestPrivacySignalDropsAreExportedSeparately(t *testing.T) {
	before := health.PrivacySignalsDropped.Load()
	health.PrivacySignalsDropped.Add(1)

	snapshot := health.Snapshot()
	if got := snapshot["ingest_privacy_dropped_total"]; got != float64(before+1) {
		t.Fatalf("privacy signal drop metric = %v, want %d", got, before+1)
	}
	var b strings.Builder
	health.WritePrometheus(&b)
	if !strings.Contains(b.String(), "panel_ingest_privacy_dropped_total ") {
		t.Fatalf("Prometheus output missing privacy signal drop counter:\n%s", b.String())
	}
}
