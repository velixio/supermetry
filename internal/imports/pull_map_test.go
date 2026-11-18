package imports

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

func wantDim(t *testing.T, agg Aggregate, day string, dim int16, value string, want Metrics) {
	t.Helper()
	got, ok := agg.Dimensions[DimensionKey{Day: date(t, day), Dim: dim, Value: value}]
	if !ok {
		t.Fatalf("missing %s dim=%d %q", day, dim, value)
	}
	if got != want {
		t.Fatalf("%s dim=%d %q = %+v, want %+v", day, dim, value, got, want)
	}
}

func wantDay(t *testing.T, agg Aggregate, day string, want Metrics) {
	t.Helper()
	if got := agg.Days[date(t, day)]; got != want {
		t.Fatalf("day %s = %+v, want %+v", day, got, want)
	}
}

func newPlausibleTestPuller(fake *plausibleFake, key string, pageSize int) (*plausiblePuller, *[]time.Duration) {
	client, sleeps := testPullClient()
	return &plausiblePuller{client: client, base: fake.URL, siteID: "example.com", key: key, pageSize: pageSize}, sleeps
}

func TestPlausibleMappingMatchesHandComputedTotals(t *testing.T) {
	fake := newPlausibleFake(t, "key-1")
	puller, _ := newPlausibleTestPuller(fake, "key-1", plausiblePageSize)
	agg, stats, err := puller.Fetch(context.Background(), DayRange{date(t, "2026-01-01"), date(t, "2026-01-02")})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Rows != 30 || stats.BadRows != 0 {
		t.Fatalf("stats = %+v, want 30 rows 0 bad", stats)
	}
	for _, day := range []string{"2026-01-01", "2026-01-02"} {
		// bounces = round(12 visits * 50%) = 6; duration = 12 * 20.5 s = 246.
		wantDay(t, agg, day, Metrics{Visitors: 10, Visits: 12, Pageviews: 30, Bounces: 6, VisitDurationS: 246})
		wantDim(t, agg, day, DimPage, "/", Metrics{Visitors: 8, Pageviews: 20})
		wantDim(t, agg, day, DimPage, "/pricing", Metrics{Visitors: 3, Pageviews: 5})
		// round(9 * 33.3333%) = 3; 9 * 10 s = 90.
		wantDim(t, agg, day, DimEntry, "/", Metrics{Visitors: 7, Visits: 9, Bounces: 3, VisitDurationS: 90})
		wantDim(t, agg, day, DimExit, "/pricing", Metrics{Visitors: 3, Visits: 4})
		wantDim(t, agg, day, DimSource, "Google", Metrics{Visitors: 6, Visits: 7, Pageviews: 15, Bounces: 3, VisitDurationS: 84})
		wantDim(t, agg, day, DimSource, "Direct / None", Metrics{Visitors: 4, Visits: 5, Pageviews: 15, Bounces: 3, VisitDurationS: 150})
		wantDim(t, agg, day, DimCountry, "US", Metrics{Visitors: 5, Visits: 6, Pageviews: 20, Bounces: 3, VisitDurationS: 120})
		wantDim(t, agg, day, DimRegion, "California", Metrics{Visitors: 3, Visits: 4, Pageviews: 9, Bounces: 1, VisitDurationS: 40})
		wantDim(t, agg, day, DimCity, "Berlin", Metrics{Visitors: 2, Visits: 3, Pageviews: 5, VisitDurationS: 24})
		wantDim(t, agg, day, DimDevice, "Mobile", Metrics{Visitors: 6, Visits: 7, Pageviews: 12, Bounces: 5, VisitDurationS: 105})
		wantDim(t, agg, day, DimBrowser, "Chrome", Metrics{Visitors: 9, Visits: 10, Pageviews: 25, Bounces: 5, VisitDurationS: 200})
		wantDim(t, agg, day, DimOS, "Windows", Metrics{Visitors: 7, Visits: 8, Pageviews: 20, Bounces: 4, VisitDurationS: 176})
		wantDim(t, agg, day, DimEvent, "Signup", Metrics{Visitors: 3, Events: 4})
	}
	if len(agg.Days) != 2 || len(agg.Dimensions) != 28 {
		t.Fatalf("days=%d dims=%d, want 2 and 28", len(agg.Days), len(agg.Dimensions))
	}
	// The same numbers feed the preview summary the file import shows.
	summary := Summarize(agg, stats)
	if summary.Totals.Visitors != 20 || summary.Totals.Pageviews != 60 || summary.Totals.Events != 8 || summary.Days != 2 {
		t.Fatalf("summary = %+v", summary.Totals)
	}
}

func TestPlausiblePaginatesWithinAChunk(t *testing.T) {
	fake := newPlausibleFake(t, "key-1")
	puller, _ := newPlausibleTestPuller(fake, "key-1", 1)
	agg, _, err := puller.Fetch(context.Background(), DayRange{date(t, "2026-01-01"), date(t, "2026-01-02")})
	if err != nil {
		t.Fatal(err)
	}
	wantDim(t, agg, "2026-01-02", DimPage, "/pricing", Metrics{Visitors: 3, Pageviews: 5})
	offsets := map[int]bool{}
	for _, r := range fake.recorded() {
		if r.Key == "event:page" {
			offsets[r.Offset] = true
		}
	}
	// 2 pages x 2 days = 4 rows at one row per page: offsets 0..4 (the last is empty).
	if len(offsets) != 5 {
		t.Fatalf("page offsets = %v, want 5 distinct", offsets)
	}
}

func TestPlausibleBacksOffOn429AndHonoursRetryAfter(t *testing.T) {
	fake := newPlausibleFake(t, "key-1")
	fake.before = func(w http.ResponseWriter, r plausibleRecorded, attempt int) bool {
		if r.Key == "" && attempt <= 2 {
			if attempt == 1 {
				w.Header().Set("Retry-After", "3")
			}
			w.WriteHeader(http.StatusTooManyRequests)
			return true
		}
		return false
	}
	puller, sleeps := newPlausibleTestPuller(fake, "key-1", plausiblePageSize)
	agg, _, err := puller.Fetch(context.Background(), DayRange{date(t, "2026-01-01"), date(t, "2026-01-01")})
	if err != nil {
		t.Fatal(err)
	}
	wantDay(t, agg, "2026-01-01", Metrics{Visitors: 10, Visits: 12, Pageviews: 30, Bounces: 6, VisitDurationS: 246})
	// Retry-After 3 first, then exponential backoff (1s<<1) for the second try.
	if len(*sleeps) != 2 || (*sleeps)[0] != 3*time.Second || (*sleeps)[1] != 2*time.Second {
		t.Fatalf("sleeps = %v", *sleeps)
	}
}

func TestPlausibleGivesUpAfterRepeatedRateLimiting(t *testing.T) {
	fake := newPlausibleFake(t, "key-1")
	fake.before = func(w http.ResponseWriter, _ plausibleRecorded, _ int) bool {
		w.WriteHeader(http.StatusTooManyRequests)
		return true
	}
	puller, sleeps := newPlausibleTestPuller(fake, "key-1", plausiblePageSize)
	_, _, err := puller.Fetch(context.Background(), DayRange{date(t, "2026-01-01"), date(t, "2026-01-01")})
	if err == nil || !strings.Contains(err.Error(), "rate limiting") {
		t.Fatalf("err = %v", err)
	}
	if len(*sleeps) != maxPullAttempts-1 {
		t.Fatalf("slept %d times, want %d", len(*sleeps), maxPullAttempts-1)
	}
}

func TestPlausibleRejectedKeyAndSecretRedaction(t *testing.T) {
	fake := newPlausibleFake(t, "right-key")
	puller, _ := newPlausibleTestPuller(fake, "wrong-key", plausiblePageSize)
	_, _, err := puller.Fetch(context.Background(), DayRange{date(t, "2026-01-01"), date(t, "2026-01-01")})
	if err == nil || !strings.Contains(err.Error(), "rejected the credentials") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "wrong-key") {
		t.Fatal("error leaks the API key")
	}

	fake = newPlausibleFake(t, "echo-key")
	fake.before = func(w http.ResponseWriter, r plausibleRecorded, _ int) bool {
		if r.Key == "event:page" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"bad query for echo-key"}`))
			return true
		}
		return false
	}
	puller, _ = newPlausibleTestPuller(fake, "echo-key", plausiblePageSize)
	_, _, err = puller.Fetch(context.Background(), DayRange{date(t, "2026-01-01"), date(t, "2026-01-01")})
	if err == nil || strings.Contains(err.Error(), "echo-key") || !strings.Contains(err.Error(), "***") {
		t.Fatalf("err = %v", err)
	}
}

func TestPlausibleSkipsUnsupportedOptionalBreakdowns(t *testing.T) {
	fake := newPlausibleFake(t, "key-1")
	fake.before = func(w http.ResponseWriter, r plausibleRecorded, _ int) bool {
		if r.Key == "visit:city_name" || r.Key == "event:goal" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"Invalid dimension"}`))
			return true
		}
		return false
	}
	puller, _ := newPlausibleTestPuller(fake, "key-1", plausiblePageSize)
	agg, stats, err := puller.Fetch(context.Background(), DayRange{date(t, "2026-01-01"), date(t, "2026-01-01")})
	if err != nil {
		t.Fatal(err)
	}
	if stats.BadRows != 2 || len(agg.Dimensions) != 12 {
		t.Fatalf("bad=%d dims=%d", stats.BadRows, len(agg.Dimensions))
	}
}

func newMatomoTestPuller(fake *matomoFake, token string) *matomoPuller {
	client, _ := testPullClient()
	return &matomoPuller{client: client, base: fake.URL, siteID: "1", token: token}
}

func TestMatomoMappingMatchesHandComputedTotals(t *testing.T) {
	fake := newMatomoFake(t, "tok-1")
	agg, stats, err := newMatomoTestPuller(fake, "tok-1").Fetch(context.Background(), DayRange{date(t, "2026-02-01"), date(t, "2026-02-02")})
	if err != nil {
		t.Fatal(err)
	}
	for _, day := range []string{"2026-02-01", "2026-02-02"} {
		wantDay(t, agg, day, Metrics{Visitors: 10, Visits: 12, Pageviews: 30, Bounces: 5, VisitDurationS: 3000})
		wantDim(t, agg, day, DimPage, "/", Metrics{Visitors: 8, Pageviews: 20})
		wantDim(t, agg, day, DimPage, "/pricing", Metrics{Visitors: 3, Pageviews: 5})
		wantDim(t, agg, day, DimEntry, "/", Metrics{Visitors: 6, Visits: 6, Bounces: 2, VisitDurationS: 600})
		wantDim(t, agg, day, DimExit, "/", Metrics{Visitors: 3, Visits: 3})
		wantDim(t, agg, day, DimExit, "/pricing", Metrics{Visitors: 4, Visits: 4})
		wantDim(t, agg, day, DimSource, "Google", Metrics{Visitors: 5, Visits: 6, Pageviews: 15, Bounces: 2, VisitDurationS: 500})
		wantDim(t, agg, day, DimSource, "Direct / None", Metrics{Visitors: 5, Visits: 6, Pageviews: 25, Bounces: 3, VisitDurationS: 700})
		wantDim(t, agg, day, DimCountry, "US", Metrics{Visitors: 6, Visits: 7, Pageviews: 20, Bounces: 3, VisitDurationS: 900})
		wantDim(t, agg, day, DimCountry, "DE", Metrics{Visitors: 4, Visits: 5, Pageviews: 20, Bounces: 2, VisitDurationS: 700})
		wantDim(t, agg, day, DimDevice, "Smartphone", Metrics{Visitors: 10, Visits: 12, Pageviews: 40, Bounces: 5, VisitDurationS: 3000})
		wantDim(t, agg, day, DimBrowser, "Chrome", Metrics{Visitors: 10, Visits: 12, Pageviews: 40, Bounces: 5, VisitDurationS: 3000})
		wantDim(t, agg, day, DimOS, "Windows", Metrics{Visitors: 10, Visits: 12, Pageviews: 40, Bounces: 5, VisitDurationS: 3000})
		wantDim(t, agg, day, DimEvent, "play", Metrics{Visitors: 4, Events: 9})
		wantDim(t, agg, day, DimEvent, "Signup", Metrics{Visitors: 2, Events: 3})
	}
	if _, others := agg.Dimensions[DimensionKey{Day: date(t, "2026-02-01"), Dim: DimPage, Value: "/Others"}]; others {
		t.Fatal("the Others aggregate row must be dropped")
	}
	if stats.BadRows != 0 {
		t.Fatalf("stats = %+v", stats)
	}
	// The token travels in the POST body only, never in a URL.
	for _, query := range fake.queries {
		if query != "" {
			t.Fatalf("request URL carries a query string: %q", query)
		}
	}
	if fake.methodCount("Goals.getGoals") != 1 || fake.methodCount("VisitsSummary.get") != 1 {
		t.Fatalf("one request per report and chunk expected: %v", fake.methods)
	}
}

func TestMatomoEmptyDaysAndSkippedReports(t *testing.T) {
	fake := newMatomoFake(t, "tok-1")
	fake.emptyDays["2026-02-02"] = true
	fake.fail["Events.getAction"] = "The plugin Events is not enabled"
	agg, stats, err := newMatomoTestPuller(fake, "tok-1").Fetch(context.Background(), DayRange{date(t, "2026-02-01"), date(t, "2026-02-02")})
	if err != nil {
		t.Fatal(err)
	}
	if len(agg.Days) != 1 {
		t.Fatalf("days = %d, want only the populated day", len(agg.Days))
	}
	if stats.BadRows != 1 || !strings.Contains(stats.Errors[0].Message, "Events.getAction") {
		t.Fatalf("stats = %+v", stats)
	}
	if _, ok := agg.Dimensions[DimensionKey{Day: date(t, "2026-02-01"), Dim: DimEvent, Value: "play"}]; ok {
		t.Fatal("skipped report must not contribute rows")
	}
}

func TestMatomoWrongTokenFailsWithoutLeakingIt(t *testing.T) {
	fake := newMatomoFake(t, "right-token")
	_, _, err := newMatomoTestPuller(fake, "wrong-token").Fetch(context.Background(), DayRange{date(t, "2026-02-01"), date(t, "2026-02-01")})
	if err == nil || !strings.Contains(err.Error(), "Matomo error") || strings.Contains(err.Error(), "wrong-token") {
		t.Fatalf("err = %v", err)
	}
}

func newGA4TestPuller(t *testing.T, fake *googleFake, pageSize int) *ga4Puller {
	t.Helper()
	client, _ := testPullClient()
	cfg := fake.config().withDefaults()
	cfg.PageSize = pageSize
	token := &ga4AccessToken{now: time.Now, refresh: func(ctx context.Context) (string, time.Duration, error) {
		return "ya29.access-static", time.Hour, nil
	}}
	return &ga4Puller{client: client, cfg: cfg, property: "properties/123", token: token}
}

func TestGA4MappingMatchesHandComputedTotals(t *testing.T) {
	fake := newGoogleFake(t)
	agg, stats, err := newGA4TestPuller(t, fake, 3).Fetch(context.Background(), DayRange{date(t, "2026-03-01"), date(t, "2026-03-02")})
	if err != nil {
		t.Fatal(err)
	}
	for _, day := range []string{"2026-03-01", "2026-03-02"} {
		// bounces = round(12 * 0.5) = 6; duration = round(12 * 20.5) = 246.
		wantDay(t, agg, day, Metrics{Visitors: 10, Visits: 12, Pageviews: 30, Bounces: 6, VisitDurationS: 246})
		wantDim(t, agg, day, DimPage, "/", Metrics{Visitors: 8, Visits: 9, Pageviews: 20, Bounces: 4, VisitDurationS: 90})
		wantDim(t, agg, day, DimPage, "/pricing", Metrics{Visitors: 3, Visits: 3, Pageviews: 5, Bounces: 1, VisitDurationS: 90})
		wantDim(t, agg, day, DimEntry, "/", Metrics{Visitors: 7, Visits: 9, Pageviews: 9, Bounces: 3, VisitDurationS: 90})
		wantDim(t, agg, day, DimSource, "Google", Metrics{Visitors: 6, Visits: 7, Pageviews: 15, Bounces: 3, VisitDurationS: 84})
		wantDim(t, agg, day, DimSource, "Direct / None", Metrics{Visitors: 4, Visits: 5, Pageviews: 15, Bounces: 3, VisitDurationS: 150})
		wantDim(t, agg, day, DimCountry, "US", Metrics{Visitors: 5, Visits: 6, Pageviews: 20, Bounces: 3, VisitDurationS: 120})
		wantDim(t, agg, day, DimCountry, "DE", Metrics{Visitors: 5, Visits: 6, Pageviews: 10, Bounces: 3, VisitDurationS: 126})
		wantDim(t, agg, day, DimDevice, "Mobile", Metrics{Visitors: 6, Visits: 7, Pageviews: 12, Bounces: 5, VisitDurationS: 105})
		wantDim(t, agg, day, DimEvent, "sign_up", Metrics{Visitors: 3, Events: 5})
	}
	// page_view is automatic and "(not set)" carries no information.
	if _, ok := agg.Dimensions[DimensionKey{Day: date(t, "2026-03-01"), Dim: DimEvent, Value: "page_view"}]; ok {
		t.Fatal("automatic events must not be imported")
	}
	if stats.BadRows != 0 {
		t.Fatalf("stats = %+v", stats)
	}
	// Event report: 2 rows x 2 days = 4 rows at page size 3 means two pages.
	if got := fake.reportCount("eventName"); got != 2 {
		t.Fatalf("eventName report requests = %d, want 2", got)
	}
	for _, report := range fake.reports {
		if report.Auth != "Bearer ya29.access-static" {
			t.Fatalf("authorization header = %q", report.Auth)
		}
	}
}

func TestGA4BacksOffOnQuotaErrors(t *testing.T) {
	fake := newGoogleFake(t)
	fake.quotaHits = 2
	puller := newGA4TestPuller(t, fake, 100000)
	client, sleeps := testPullClient()
	puller.client = client
	agg, _, err := puller.Fetch(context.Background(), DayRange{date(t, "2026-03-01"), date(t, "2026-03-01")})
	if err != nil {
		t.Fatal(err)
	}
	wantDay(t, agg, "2026-03-01", Metrics{Visitors: 10, Visits: 12, Pageviews: 30, Bounces: 6, VisitDurationS: 246})
	if len(*sleeps) != 2 {
		t.Fatalf("sleeps = %v", *sleeps)
	}
}
