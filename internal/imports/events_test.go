package imports

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func parseEventFixture(t *testing.T, source Source, name string, location *time.Location) (Aggregate, ParseStats) {
	t.Helper()
	aggregate, stats, err := ProcessFileInLocation(context.Background(), source, "testdata/"+name, location)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return aggregate, stats
}

func assertDay(t *testing.T, aggregate Aggregate, day string, want Metrics) {
	t.Helper()
	if got := aggregate.Days[date(t, day)]; got != want {
		t.Errorf("day %s = %+v, want %+v", day, got, want)
	}
}

// Hand-computed from testdata/mixpanel_events.jsonl (UTC):
// u1 views "/" and "/pricing" and signs up within 60 s; u2 views "/" once.
func TestParseMixpanelJSONLines(t *testing.T) {
	aggregate, stats := parseEventFixture(t, SourceMixpanel, "mixpanel_events.jsonl", time.UTC)
	if stats.Rows != 6 || stats.BadRows != 2 || stats.DuplicateRows != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	if len(stats.Errors) != 2 || stats.Errors[0].Row != 6 || stats.Errors[1].Row != 7 ||
		!strings.Contains(stats.Errors[0].Message, "JSON") || !strings.Contains(stats.Errors[1].Message, "distinct_id") {
		t.Fatalf("errors = %+v", stats.Errors)
	}
	u1 := Metrics{Visitors: 1, Visits: 1, Pageviews: 2, VisitDurationS: 60}
	u2 := Metrics{Visitors: 1, Visits: 1, Pageviews: 1, Bounces: 1}
	assertDay(t, aggregate, "2024-03-01", Metrics{Visitors: 2, Visits: 2, Pageviews: 3, Bounces: 1, VisitDurationS: 60})
	assertDay(t, aggregate, "2024-03-02", Metrics{Visitors: 1, Visits: 1, Pageviews: 1, Bounces: 1})
	d1, d2 := date(t, "2024-03-01"), date(t, "2024-03-02")
	assertDimension(t, aggregate, DimensionKey{d1, DimPage, "/"}, Metrics{Visitors: 2, Visits: 2, Pageviews: 2})
	assertDimension(t, aggregate, DimensionKey{d1, DimPage, "/pricing"}, Metrics{Visitors: 1, Visits: 1, Pageviews: 1})
	assertDimension(t, aggregate, DimensionKey{d1, DimEvent, "Signup"}, Metrics{Visitors: 1, Visits: 1, Events: 1})
	assertDimension(t, aggregate, DimensionKey{d1, DimCountry, "US"}, u1)
	assertDimension(t, aggregate, DimensionKey{d1, DimCountry, "DE"}, u2)
	assertDimension(t, aggregate, DimensionKey{d1, DimBrowser, "Chrome"}, u1)
	assertDimension(t, aggregate, DimensionKey{d1, DimBrowser, "Firefox"}, u2)
	assertDimension(t, aggregate, DimensionKey{d1, DimOS, "Windows"}, u1)
	assertDimension(t, aggregate, DimensionKey{d1, DimSource, "google.com"}, u1)
	assertDimension(t, aggregate, DimensionKey{d1, DimSource, "Direct / None"}, u2)
	assertDimension(t, aggregate, DimensionKey{d1, DimEntry, "/"}, Metrics{Visitors: 2, Visits: 2, Pageviews: 3, Bounces: 1, VisitDurationS: 60})
	assertDimension(t, aggregate, DimensionKey{d1, DimExit, "/pricing"}, u1)
	assertDimension(t, aggregate, DimensionKey{d1, DimExit, "/"}, u2)
	assertDimension(t, aggregate, DimensionKey{d2, DimPage, "/docs"}, Metrics{Visitors: 1, Visits: 1, Pageviews: 1})
}

func TestParseMixpanelCSV(t *testing.T) {
	aggregate, stats := parseEventFixture(t, SourceMixpanel, "mixpanel_events.csv", time.UTC)
	if stats.Rows != 3 || stats.BadRows != 1 || len(stats.Errors) != 1 || stats.Errors[0].Row != 4 ||
		!strings.Contains(stats.Errors[0].Message, "time") {
		t.Fatalf("stats = %+v", stats)
	}
	// c1 views two pages 120 s apart; c2 only downloads (no pageview, no bounce).
	assertDay(t, aggregate, "2024-03-01", Metrics{Visitors: 2, Visits: 2, Pageviews: 2, VisitDurationS: 120})
	d1 := date(t, "2024-03-01")
	assertDimension(t, aggregate, DimensionKey{d1, DimPage, "/a"}, Metrics{Visitors: 1, Visits: 1, Pageviews: 1})
	assertDimension(t, aggregate, DimensionKey{d1, DimPage, "/b"}, Metrics{Visitors: 1, Visits: 1, Pageviews: 1})
	assertDimension(t, aggregate, DimensionKey{d1, DimEvent, "Download"}, Metrics{Visitors: 1, Visits: 1, Events: 1})
	assertDimension(t, aggregate, DimensionKey{d1, DimCountry, "GB"}, Metrics{Visitors: 1, Visits: 1, Pageviews: 2, VisitDurationS: 120})
	assertDimension(t, aggregate, DimensionKey{d1, DimSource, "bing.com"}, Metrics{Visitors: 1, Visits: 1, Pageviews: 2, VisitDurationS: 120})
}

func TestParseAmplitudeJSONLines(t *testing.T) {
	aggregate, stats := parseEventFixture(t, SourceAmplitude, "amplitude_events.json", time.UTC)
	if stats.Rows != 6 || stats.BadRows != 1 || stats.DuplicateRows != 1 ||
		len(stats.Errors) != 1 || stats.Errors[0].Row != 7 || !strings.Contains(stats.Errors[0].Message, "event_time") {
		t.Fatalf("stats = %+v", stats)
	}
	a := Metrics{Visitors: 1, Visits: 1, Pageviews: 2, VisitDurationS: 60}
	b := Metrics{Visitors: 1, Visits: 1, Pageviews: 1, Bounces: 1}
	assertDay(t, aggregate, "2024-03-01", Metrics{Visitors: 2, Visits: 2, Pageviews: 3, Bounces: 1, VisitDurationS: 60})
	assertDay(t, aggregate, "2024-03-02", Metrics{Visitors: 1, Visits: 1, Pageviews: 1, Bounces: 1})
	d1 := date(t, "2024-03-01")
	assertDimension(t, aggregate, DimensionKey{d1, DimPage, "/"}, Metrics{Visitors: 2, Visits: 2, Pageviews: 2})
	assertDimension(t, aggregate, DimensionKey{d1, DimPage, "/pricing"}, Metrics{Visitors: 1, Visits: 1, Pageviews: 1})
	assertDimension(t, aggregate, DimensionKey{d1, DimEvent, "Button Clicked"}, Metrics{Visitors: 1, Visits: 1, Events: 1})
	assertDimension(t, aggregate, DimensionKey{d1, DimCountry, "US"}, a)
	assertDimension(t, aggregate, DimensionKey{d1, DimCountry, "IN"}, b)
	assertDimension(t, aggregate, DimensionKey{d1, DimBrowser, "Chrome"}, a)
	assertDimension(t, aggregate, DimensionKey{d1, DimOS, "Windows"}, a)
	assertDimension(t, aggregate, DimensionKey{d1, DimOS, "iOS"}, b)
	assertDimension(t, aggregate, DimensionKey{d1, DimDevice, "Mobile"}, b)
	assertDimension(t, aggregate, DimensionKey{d1, DimSource, "news.example"}, a)
	// A user without user_id is counted by device_id; neither is stored.
	if got := len(aggregate.Dimensions); got != 16 {
		t.Errorf("dimension rows = %d, want 16", got)
	}
}

func TestParseAmplitudeBucketsByTheSiteTimeZone(t *testing.T) {
	kolkata, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		t.Fatal(err)
	}
	aggregate, _ := parseEventFixture(t, SourceAmplitude, "amplitude_events.json", kolkata)
	// Device d2 viewed at 23:30 UTC on 1 March = 05:00 on 2 March in Kolkata,
	// where user a's 08:00 UTC view on 2 March (13:30) also falls.
	assertDay(t, aggregate, "2024-03-01", Metrics{Visitors: 1, Visits: 1, Pageviews: 2, VisitDurationS: 60})
	assertDay(t, aggregate, "2024-03-02", Metrics{Visitors: 2, Visits: 2, Pageviews: 2, Bounces: 2})
}

func TestEventImportersNeverKeepIdentifiersOrNetworkData(t *testing.T) {
	for _, tc := range []struct {
		source Source
		file   string
	}{{SourceMixpanel, "mixpanel_events.jsonl"}, {SourceAmplitude, "amplitude_events.json"}} {
		aggregate, _ := parseEventFixture(t, tc.source, tc.file, time.UTC)
		for key := range aggregate.Dimensions {
			for _, banned := range []string{"203.0.113.9", "198.51.100.7", "Mozilla", "secret", "u1", "u2", "d1", "d2", "plan=pro", "q=1", "#top"} {
				if key.Dim != DimEvent && strings.Contains(key.Value, banned) {
					t.Errorf("%s: dimension %+v contains %q", tc.file, key, banned)
				}
			}
		}
	}
}

func gzipBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	if _, err := writer.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func writeEventZIP(t *testing.T, entries map[string][]byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "export.zip")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	for name, data := range entries {
		writer, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAmplitudeZIPOfGzippedHourlyFilesMatchesPlainFile(t *testing.T) {
	raw, err := os.ReadFile("testdata/amplitude_events.json")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.SplitAfter(string(raw), "\n")
	// Amplitude exports one gzip per hour in a folder named by project id;
	// the duplicate event (line 5) lands in the second file.
	path := writeEventZIP(t, map[string][]byte{
		"123456/123456_2024-03-01_10#0.json.gz": gzipBytes(t, []byte(strings.Join(lines[:4], ""))),
		"123456/123456_2024-03-01_11#0.json.gz": gzipBytes(t, []byte(strings.Join(lines[4:], ""))),
		"__MACOSX/._junk":                       []byte("not an event file"),
		"readme.txt":                            []byte("ignored"),
	})
	want, _ := parseEventFixture(t, SourceAmplitude, "amplitude_events.json", time.UTC)
	got, stats, err := ProcessFileInLocation(context.Background(), SourceAmplitude, path, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Rows != 6 || stats.DuplicateRows != 1 || stats.BadRows != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	if len(got.Dimensions) != len(want.Dimensions) {
		t.Fatalf("dimensions = %d, want %d", len(got.Dimensions), len(want.Dimensions))
	}
	for day, metrics := range want.Days {
		if got.Days[day] != metrics {
			t.Errorf("day %s = %+v, want %+v", day, got.Days[day], metrics)
		}
	}
	// Row errors inside an archive name their file.
	if len(stats.Errors) != 1 || !strings.Contains(stats.Errors[0].Message, "_11#0.json.gz") {
		t.Fatalf("errors = %+v", stats.Errors)
	}
}

func TestGzippedUploadAndByteOrderMark(t *testing.T) {
	raw, err := os.ReadFile("testdata/mixpanel_events.csv")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "upload")
	if err := os.WriteFile(path, gzipBytes(t, append([]byte("\xef\xbb\xbf"), raw...)), 0o600); err != nil {
		t.Fatal(err)
	}
	aggregate, stats, err := ProcessFileInLocation(context.Background(), SourceMixpanel, path, time.UTC)
	if err != nil || stats.Rows != 3 || aggregate.Days[date(t, "2024-03-01")].Pageviews != 2 {
		t.Fatalf("stats=%+v err=%v", stats, err)
	}
}

func TestEventImportRejectsUnusableFiles(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"array.json": `[{"event":"x"}]`,
		"empty.json": "  \n",
		"nocols.csv": "name,value\nx,1\n",
	} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := ProcessFileInLocation(context.Background(), SourceMixpanel, path, time.UTC); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestEventImportSafetyLimits(t *testing.T) {
	line := `{"event":"Page View","properties":{"time":1709287200,"distinct_id":"u%d","$insert_id":"i%d","$current_url":"/"}}` + "\n"
	build := func(n int) string {
		var b strings.Builder
		for i := range n {
			b.WriteString(strings.NewReplacer("%d", string(rune('a'+i))).Replace(line))
		}
		return b.String()
	}
	run := func(t *testing.T, path string) error {
		t.Helper()
		_, _, err := ProcessFileInLocation(context.Background(), SourceMixpanel, path, time.UTC)
		return err
	}
	write := func(t *testing.T, name string, data []byte) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}

	t.Run("rows", func(t *testing.T) {
		old := maxEventRows
		maxEventRows = 3
		defer func() { maxEventRows = old }()
		if err := run(t, write(t, "r.json", []byte(build(5)))); err == nil || !strings.Contains(err.Error(), "more than 3 rows") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("sessions", func(t *testing.T) {
		old := maxEventSessions
		maxEventSessions = 2
		defer func() { maxEventSessions = old }()
		if err := run(t, write(t, "s.json", []byte(build(5)))); err == nil || !strings.Contains(err.Error(), "more than 2 sessions") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("long line is one bad row", func(t *testing.T) {
		old := maxEventLineBytes
		maxEventLineBytes = 200
		defer func() { maxEventLineBytes = old }()
		data := build(1) + `{"event":"` + strings.Repeat("x", 5000) + "\"}\n" + strings.SplitAfter(build(2), "\n")[1]
		_, stats, err := ProcessFileInLocation(context.Background(), SourceMixpanel, write(t, "l.json", []byte(data)), time.UTC)
		if err != nil || stats.BadRows != 1 || len(stats.Errors) != 1 || stats.Errors[0].Row != 2 || stats.Rows != 2 || stats.DuplicateRows != 0 {
			t.Fatalf("stats=%+v err=%v", stats, err)
		}
	})
	t.Run("zip entry count", func(t *testing.T) {
		old := maxZIPEntries
		maxZIPEntries = 1
		defer func() { maxZIPEntries = old }()
		path := writeEventZIP(t, map[string][]byte{"a.json": []byte(build(1)), "b.json": []byte(build(1))})
		if err := run(t, path); err == nil || !strings.Contains(err.Error(), "entries") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("gzip bomb inside zip", func(t *testing.T) {
		old := maxZIPEntryBytes
		maxZIPEntryBytes = 4096
		defer func() { maxZIPEntryBytes = old }()
		bomb := gzipBytes(t, bytes.Repeat([]byte(" "), 1<<20))
		path := writeEventZIP(t, map[string][]byte{"bomb.json.gz": bomb})
		if err := run(t, path); err == nil || !strings.Contains(err.Error(), "size limit") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("total uncompressed size", func(t *testing.T) {
		old := maxZIPTotalBytes
		maxZIPTotalBytes = 600
		defer func() { maxZIPTotalBytes = old }()
		gz := gzipBytes(t, []byte(build(3)))
		path := writeEventZIP(t, map[string][]byte{"a.json.gz": gz, "b.json.gz": gz, "c.json.gz": gz})
		if err := run(t, path); err == nil || !strings.Contains(err.Error(), "size limit") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, _, err := ProcessFileInLocation(ctx, SourceAmplitude, "testdata/amplitude_events.json", time.UTC)
		if err == nil {
			t.Fatal("want an error")
		}
	})
}

func TestEventImportStoresAndUndoes(t *testing.T) {
	store, siteID, userID := newImportStore(t)
	ctx := context.Background()
	for _, tc := range []struct {
		source Source
		file   string
		rows   int64
		pv     int64
	}{
		{SourceMixpanel, "mixpanel_events.jsonl", 6, 4},
		{SourceAmplitude, "amplitude_events.json", 6, 4},
	} {
		aggregate, stats := parseEventFixture(t, tc.source, tc.file, time.UTC)
		created, err := store.Create(ctx, CreateInput{SiteID: siteID, Source: tc.source, FileName: tc.file, CreatedBy: userID, FileHash: sha256.Sum256([]byte(tc.file))})
		if err != nil {
			t.Fatalf("%s: %v", tc.source, err)
		}
		if err := store.SetRunning(ctx, created.ID); err != nil {
			t.Fatal(err)
		}
		if err := store.Complete(ctx, created.ID, aggregate, stats); err != nil {
			t.Fatal(err)
		}
		done, err := store.Get(ctx, created.ID)
		if err != nil {
			t.Fatal(err)
		}
		if done.Source != tc.source || done.Status != StatusCompleted || done.Rows != tc.rows ||
			done.DateFrom.Format(time.DateOnly) != "2024-03-01" || done.DateTo.Format(time.DateOnly) != "2024-03-02" {
			t.Fatalf("import = %+v", done)
		}
		var pageviews int64
		if err := store.pool.QueryRow(ctx, `select coalesce(sum(pageviews),0) from imported_day where import_id=$1`, done.ID).Scan(&pageviews); err != nil {
			t.Fatal(err)
		}
		if pageviews != tc.pv {
			t.Fatalf("%s pageviews = %d, want %d", tc.source, pageviews, tc.pv)
		}
		if deleted, err := store.Delete(ctx, done.ID); err != nil || !deleted {
			t.Fatalf("Delete = (%t,%v)", deleted, err)
		}
	}
	var left int64
	if err := store.pool.QueryRow(ctx, `select (select count(*) from imported_day)+(select count(*) from imported_day_dim)`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("left=%d err=%v", left, err)
	}
}
