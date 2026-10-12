package imports

import (
	"context"
	"strings"
	"testing"
)

func parseGA4Fixture(t *testing.T, name string) (Aggregate, ParseStats) {
	t.Helper()
	aggregate, stats, err := ProcessFileInLocation(context.Background(), SourceGA4, "testdata/"+name, nil)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return aggregate, stats
}

func TestParseGA4DailyOverviewWithCommentPreamble(t *testing.T) {
	aggregate, stats := parseGA4Fixture(t, "ga4_daily.csv")
	if stats.Rows != 3 || stats.BadRows != 0 || stats.DuplicateRows != 0 {
		t.Fatalf("stats = %+v", stats)
	}
	// Bounces are round(sessions*rate); duration is sessions*average seconds.
	want := map[string]Metrics{
		"2024-03-01": {Visitors: 100, Visits: 150, Pageviews: 400, Bounces: 60, VisitDurationS: 9000, Events: 900},
		"2024-03-02": {Visitors: 1200, Visits: 200, Pageviews: 500, Bounces: 90, VisitDurationS: 6100, Events: 1000},
		"2024-03-03": {Visitors: 80, Visits: 100, Pageviews: 250, Bounces: 50, VisitDurationS: 1000, Events: 500},
	}
	if len(aggregate.Days) != 3 || len(aggregate.Dimensions) != 0 {
		t.Fatalf("days=%d dims=%d", len(aggregate.Days), len(aggregate.Dimensions))
	}
	for day, metrics := range want {
		if got := aggregate.Days[date(t, day)]; got != metrics {
			t.Errorf("%s = %+v, want %+v", day, got, metrics)
		}
	}
}

func TestParseGA4DimensionsDedupeAndRowErrors(t *testing.T) {
	aggregate, stats := parseGA4Fixture(t, "ga4_pages_sources.csv")
	if stats.Rows != 3 || stats.BadRows != 2 || stats.DuplicateRows != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	if len(stats.Errors) != 2 || stats.Errors[0].Row != 6 || stats.Errors[1].Row != 7 ||
		!strings.Contains(stats.Errors[0].Message, "date") || !strings.Contains(stats.Errors[1].Message, "active users") {
		t.Fatalf("errors = %+v", stats.Errors)
	}
	d1, d2 := date(t, "2024-03-01"), date(t, "2024-03-02")
	home1 := Metrics{Visitors: 10, Visits: 12, Pageviews: 20, Bounces: 6, VisitDurationS: 1200}
	pricing := Metrics{Visitors: 5, Visits: 5, Pageviews: 8, Bounces: 1, VisitDurationS: 200}
	news := Metrics{Visitors: 3, Visits: 4, Pageviews: 6, Bounces: 1, VisitDurationS: 80}
	assertDimension(t, aggregate, DimensionKey{d1, DimPage, "/"}, home1)
	assertDimension(t, aggregate, DimensionKey{d1, DimPage, "/pricing"}, pricing)
	assertDimension(t, aggregate, DimensionKey{d2, DimPage, "/"}, news)
	assertDimension(t, aggregate, DimensionKey{d1, DimSource, "Google"}, home1)
	assertDimension(t, aggregate, DimensionKey{d1, DimSource, "Direct / None"}, pricing)
	assertDimension(t, aggregate, DimensionKey{d2, DimSource, "Newsletter"}, news)
	assertDimension(t, aggregate, DimensionKey{d1, DimCountry, "US"}, home1)
	assertDimension(t, aggregate, DimensionKey{d1, DimCountry, "DE"}, pricing)
	assertDimension(t, aggregate, DimensionKey{d1, DimDevice, "Desktop"}, home1)
	assertDimension(t, aggregate, DimensionKey{d2, DimDevice, "Mobile"}, news)
	// "(not set)" values are dropped rather than imported as a literal value.
	for key := range aggregate.Dimensions {
		if key.Dim == DimCountry && key.Day.Equal(d2) {
			t.Errorf("unexpected country row %+v", key)
		}
	}
	// A page report only contributes the additive pageview metric to the day.
	if got := aggregate.Days[d1]; got != (Metrics{Pageviews: 28}) {
		t.Errorf("day 1 = %+v", got)
	}
	if got := aggregate.Days[d2]; got != (Metrics{Pageviews: 6}) {
		t.Errorf("day 2 = %+v", got)
	}
}

func TestParseGA4EventsSkipAutomaticEvents(t *testing.T) {
	aggregate, stats := parseGA4Fixture(t, "ga4_events.csv")
	if stats.Rows != 4 || stats.BadRows != 0 {
		t.Fatalf("stats = %+v", stats)
	}
	d1, d2 := date(t, "2024-03-01"), date(t, "2024-03-02")
	assertDimension(t, aggregate, DimensionKey{d1, DimEvent, "signup"}, Metrics{Visitors: 6, Events: 7})
	assertDimension(t, aggregate, DimensionKey{d2, DimEvent, "signup"}, Metrics{Visitors: 3, Events: 3})
	assertDimension(t, aggregate, DimensionKey{d1, DimEvent, "file_download"}, Metrics{Visitors: 9, Events: 12})
	if len(aggregate.Dimensions) != 3 || len(aggregate.Days) != 0 {
		t.Fatalf("dims=%d days=%d", len(aggregate.Dimensions), len(aggregate.Days))
	}
}

func TestParseGA4ExportStyleColumnsAndLeapDay(t *testing.T) {
	aggregate, stats := parseGA4Fixture(t, "ga4_export_style.csv")
	if stats.Rows != 3 || stats.BadRows != 0 {
		t.Fatalf("stats = %+v", stats)
	}
	leap, next := date(t, "2024-02-29"), date(t, "2024-03-01")
	assertDimension(t, aggregate, DimensionKey{leap, DimCountry, "US"}, Metrics{Visitors: 9, Visits: 11})
	assertDimension(t, aggregate, DimensionKey{leap, DimDevice, "Desktop"}, Metrics{Visitors: 7, Visits: 9})
	assertDimension(t, aggregate, DimensionKey{leap, DimDevice, "Mobile"}, Metrics{Visitors: 2, Visits: 2})
	assertDimension(t, aggregate, DimensionKey{next, DimCountry, "DE"}, Metrics{Visitors: 1, Visits: 1})
	// Sessions are additive across country and device rows, users are not.
	if got := aggregate.Days[leap]; got != (Metrics{Visits: 11}) {
		t.Errorf("leap day = %+v", got)
	}
	if got := aggregate.Days[next]; got != (Metrics{Visits: 1}) {
		t.Errorf("next day = %+v", got)
	}
}

func TestParseGA4RejectsUnusableHeaders(t *testing.T) {
	for name, input := range map[string]string{
		"no date":    "Country,Sessions\nUS,1\n",
		"no metrics": "Date,Country\n20240301,US\n",
		"empty":      "",
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := ParseGA4(strings.NewReader(input)); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestParseGA4RejectsTooManyRows(t *testing.T) {
	defer setLimit(&maxGA4Rows, 2)()
	input := "Date,Sessions\n20240301,1\n20240302,2\n20240303,3\n"
	if _, _, err := ParseGA4(strings.NewReader(input)); err == nil || !strings.Contains(err.Error(), "more than 2 rows") {
		t.Fatalf("err = %v", err)
	}
}

func TestParseGA4CapsRecordedErrorsButCountsAll(t *testing.T) {
	var input strings.Builder
	input.WriteString("Date,Sessions\n")
	for i := 0; i < maxRecordedErrors+10; i++ {
		input.WriteString("bad,1\n")
	}
	_, stats, err := ParseGA4(strings.NewReader(input.String()))
	if err != nil {
		t.Fatal(err)
	}
	if stats.BadRows != int64(maxRecordedErrors+10) || len(stats.Errors) != maxRecordedErrors {
		t.Fatalf("bad=%d errors=%d", stats.BadRows, len(stats.Errors))
	}
}

func TestParseGA4ValueNormalization(t *testing.T) {
	cases := []struct {
		dim   int16
		in    string
		value string
	}{
		{DimCountry, "United Kingdom", "GB"}, {DimCountry, "de", "DE"}, {DimCountry, "Atlantis", "Atlantis"},
		{DimSource, "(direct) / (none)", "Direct / None"}, {DimSource, "bing / cpc", "Bing"},
		{DimDevice, "smart tv", "Smart TV"}, {DimPage, "pricing", "/pricing"},
	}
	for _, tc := range cases {
		if got := normalizeGA4Value(tc.dim, tc.in); got != tc.value {
			t.Errorf("normalize(%d,%q)=%q, want %q", tc.dim, tc.in, got, tc.value)
		}
	}
}

func TestSummarizePreview(t *testing.T) {
	aggregate, stats := parseGA4Fixture(t, "ga4_daily.csv")
	summary := Summarize(aggregate, stats)
	if summary.Days != 3 || summary.DateFrom != "2024-03-01" || summary.DateTo != "2024-03-03" ||
		summary.Totals.Visitors != 1380 || summary.Totals.Pageviews != 1150 || summary.Rows != 3 {
		t.Fatalf("summary = %+v", summary)
	}
	pages, _ := parseGA4Fixture(t, "ga4_pages_sources.csv")
	got := Summarize(pages, ParseStats{})
	if got.Dimensions["page"] != 2 || got.Dimensions["country"] != 2 || got.Dimensions["source"] != 3 {
		t.Fatalf("dimensions = %+v", got.Dimensions)
	}
	if got.Totals.Pageviews != 34 {
		t.Fatalf("pageviews = %d", got.Totals.Pageviews)
	}
}
