package imports

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParsePlausibleUsesHeadersAndCountsBadRows(t *testing.T) {
	path := plausibleZIP(t, map[string]string{
		"imported_visitors_20260101_20260102.csv": "unknown,pageviews,date,visits,visitors,bounces,visit_duration\nignored,7,2026-01-01,4,3,2,61\nignored,nope,2026-01-02,1,1,0,2\n",
		"imported_pages_20260101_20260102.csv":    "page,date,pageviews,visitors,visits,extra\n/docs,2026-01-01,5,2,3,x\n",
		"imported_sources_20260101_20260102.csv":  "date,referrer,source,visitors,visits,pageviews,visit_duration,bounces\n2026-01-01,google.com,Google,2,3,5,50,1\n",
	})

	got, stats, err := ParsePlausible(path)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Rows != 3 || stats.BadRows != 1 {
		t.Fatalf("stats = %+v, want 3 good and 1 bad", stats)
	}
	day := date(t, "2026-01-01")
	if got.Days[day] != (Metrics{Visitors: 3, Visits: 4, Pageviews: 7, Bounces: 2, VisitDurationS: 61}) {
		t.Fatalf("daily = %+v", got.Days[day])
	}
	assertDimension(t, got, DimensionKey{Day: day, Dim: DimPage, Value: "/docs"}, Metrics{Visitors: 2, Visits: 3, Pageviews: 5})
	assertDimension(t, got, DimensionKey{Day: day, Dim: DimSource, Value: "Google"}, Metrics{Visitors: 2, Visits: 3, Pageviews: 5, Bounces: 1, VisitDurationS: 50})
}

func TestParsePlausibleSupportsEveryDocumentedFileFamily(t *testing.T) {
	customEvents, err := os.ReadFile(filepath.Join("testdata", "plausible_custom_events.csv"))
	if err != nil {
		t.Fatal(err)
	}
	path := plausibleZIP(t, map[string]string{
		"imported_entry_pages_a.csv":       "date,entry_page,visitors,entrances,visit_duration,bounces,pageviews\n2026-01-01,/in,2,3,4,1,5\n",
		"imported_exit_pages_a.csv":        "date,exit_page,visitors,exits,visit_duration,bounces,pageviews\n2026-01-01,/out,2,3,4,1,5\n",
		"imported_locations_a.csv":         "date,country,region,city,visitors,visits,visit_duration,bounces,pageviews\n2026-01-01,US,US-CA,Los Angeles,2,3,4,1,5\n",
		"imported_devices_a.csv":           "date,device,visitors,visits,visit_duration,bounces,pageviews\n2026-01-01,Desktop,2,3,4,1,5\n",
		"imported_browsers_a.csv":          "date,browser,browser_version,visitors,visits,visit_duration,bounces,pageviews\n2026-01-01,Chrome,100,2,3,4,1,5\n",
		"imported_operating_systems_a.csv": "date,operating_system,operating_system_version,visitors,visits,visit_duration,bounces,pageviews\n2026-01-01,Linux,6,2,3,4,1,5\n",
		"imported_custom_events_a.csv":     string(customEvents),
	})

	got, stats, err := ParsePlausible(path)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Rows != 7 || stats.BadRows != 0 {
		t.Fatalf("stats = %+v", stats)
	}
	day := date(t, "2026-01-01")
	for _, tc := range []struct {
		dim   int16
		value string
	}{
		{DimEntry, "/in"}, {DimExit, "/out"}, {DimCountry, "US"},
		{DimRegion, "US-CA"}, {DimCity, "Los Angeles"}, {DimDevice, "Desktop"},
		{DimBrowser, "Chrome"}, {DimOS, "Linux"},
	} {
		if _, ok := got.Dimensions[DimensionKey{Day: day, Dim: tc.dim, Value: tc.value}]; !ok {
			t.Errorf("missing dim=%d value=%q", tc.dim, tc.value)
		}
	}
	assertDimension(t, got, DimensionKey{Day: day, Dim: DimEvent, Value: "Signup"}, Metrics{Visitors: 2, Events: 7})
	if _, ok := got.Dimensions[DimensionKey{Day: day, Dim: DimPage, Value: "/signup"}]; ok {
		t.Fatal("custom event was added to the page dimension")
	}
}

func TestParsePlausibleCountsInvalidAlternateVisitColumnsAsBad(t *testing.T) {
	path := plausibleZIP(t, map[string]string{
		"imported_entry_pages_a.csv": "date,entry_page,visitors,entrances,pageviews\n2026-01-01,/good,1,2,3\n2026-01-01,/bad,1,nope,3\n",
	})
	got, stats, err := ParsePlausible(path)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Rows != 1 || stats.BadRows != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	assertDimension(t, got, DimensionKey{Day: date(t, "2026-01-01"), Dim: DimEntry, Value: "/good"}, Metrics{Visitors: 1, Visits: 2, Pageviews: 3})
}

func TestParseUmamiAggregatesSessionsAndDimensions(t *testing.T) {
	csv := `unknown,event_name,created_at,session_id,url_path,referrer_domain,event_type,country,browser,os,device
x,,2026-01-01T10:00:00Z,s1,/one,google.com,1,US,Chrome,Linux,desktop
x,Signup,2026-01-01T10:00:20Z,s1,/one,google.com,2,US,Chrome,Linux,desktop
x,,2026-01-01T10:01:00Z,s1,/two,google.com,1,US,Chrome,Linux,desktop
x,,2026-01-01T11:00:00Z,s2,/one,,pageview,IN,Firefox,Android,mobile
x,,bad,s3,/bad,,1,IN,Firefox,Android,mobile
`
	got, stats, err := ParseUmami(bytes.NewBufferString(csv))
	if err != nil {
		t.Fatal(err)
	}
	if stats.Rows != 4 || stats.BadRows != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	day := date(t, "2026-01-01")
	if got.Days[day] != (Metrics{Visitors: 2, Visits: 2, Pageviews: 3, Bounces: 1, VisitDurationS: 60}) {
		t.Fatalf("daily = %+v", got.Days[day])
	}
	assertDimension(t, got, DimensionKey{Day: day, Dim: DimEntry, Value: "/one"}, Metrics{Visitors: 2, Visits: 2, Pageviews: 3, Bounces: 1, VisitDurationS: 60})
	assertDimension(t, got, DimensionKey{Day: day, Dim: DimExit, Value: "/two"}, Metrics{Visitors: 1, Visits: 1, Pageviews: 2, VisitDurationS: 60})
	assertDimension(t, got, DimensionKey{Day: day, Dim: DimPage, Value: "/one"}, Metrics{Visitors: 2, Visits: 2, Pageviews: 2})
	assertDimension(t, got, DimensionKey{Day: day, Dim: DimEvent, Value: "Signup"}, Metrics{Visitors: 1, Visits: 1, Events: 1})
}

func TestParseUmamiBucketsSessionsInSiteTimezone(t *testing.T) {
	fixture, err := os.Open(filepath.Join("testdata", "umami_kolkata.csv"))
	if err != nil {
		t.Fatal(err)
	}
	defer fixture.Close()
	location, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		t.Fatal(err)
	}

	got, stats, err := ParseUmamiInLocation(fixture, location)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Rows != 2 || stats.BadRows != 0 {
		t.Fatalf("stats = %+v, want 2 good rows", stats)
	}
	localDay := date(t, "2026-01-02")
	if got.Days[localDay] != (Metrics{Visitors: 1, Visits: 1, Pageviews: 1, Bounces: 1, VisitDurationS: 90}) {
		t.Fatalf("local daily = %+v", got.Days[localDay])
	}
	if _, ok := got.Days[date(t, "2026-01-01")]; ok {
		t.Fatal("late-evening UTC session was bucketed on the UTC day")
	}
	assertDimension(t, got, DimensionKey{Day: localDay, Dim: DimEvent, Value: "Signup"}, Metrics{Visitors: 1, Visits: 1, Events: 1})
}

func TestParseUmamiRejectsMissingRequiredHeader(t *testing.T) {
	_, _, err := ParseUmami(bytes.NewBufferString("created_at,url_path\n2026-01-01T00:00:00Z,/\n"))
	if err == nil {
		t.Fatal("expected missing session_id error")
	}
}

func TestParseUmamiCountsUnknownEventTypesAndDoesNotBounceCustomOnlySessions(t *testing.T) {
	csv := `created_at,session_id,event_type,event_name,url_path
2026-01-01T00:00:00Z,custom,2,Signup,/signup
2026-01-01T00:00:01Z,bad,mystery,,/bad
`
	got, stats, err := ParseUmami(bytes.NewBufferString(csv))
	if err != nil {
		t.Fatal(err)
	}
	if stats.Rows != 1 || stats.BadRows != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	metrics := got.Days[date(t, "2026-01-01")]
	if metrics.Bounces != 0 || metrics.Visitors != 1 || metrics.Pageviews != 0 {
		t.Fatalf("daily = %+v", metrics)
	}
	assertDimension(t, got, DimensionKey{Day: date(t, "2026-01-01"), Dim: DimEvent, Value: "Signup"}, Metrics{Visitors: 1, Visits: 1, Events: 1})
}

func plausibleZIP(t *testing.T, files map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "export.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, contents := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(contents)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertDimension(t *testing.T, aggregate Aggregate, key DimensionKey, want Metrics) {
	t.Helper()
	if got := aggregate.Dimensions[key]; got != want {
		t.Fatalf("dimension %+v = %+v, want %+v", key, got, want)
	}
}

func date(t *testing.T, value string) time.Time {
	t.Helper()
	got, err := time.Parse("2006-01-02", value)
	if err != nil {
		t.Fatal(err)
	}
	return got
}
