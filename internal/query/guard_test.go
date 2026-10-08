package query_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"panel/internal/api"
	"panel/internal/query"
	"panel/internal/sites"
	"panel/internal/testpg"
)

func TestNormalizeRejectsWithProblems(t *testing.T) {
	t.Parallel()

	utc := sites.Site{ID: 1, TZ: time.UTC}
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	fourDimensions := []string{"page", "source", "country", "browser"}
	elevenFilters := make([]api.Filter, 11)
	for i := range elevenFilters {
		elevenFilters[i] = api.Filter{Dimension: "country", Operator: api.Is, Values: []string{"US"}}
	}

	tests := []struct {
		name string
		req  api.QueryRequest
	}{
		{name: "from after to", req: requestRange("2026-10-05", "2026-10-01")},
		{name: "empty range", req: request()},
		{name: "400 days", req: requestRange("2025-08-01", "2026-09-05")},
		{name: "unknown dimension", req: withDimensions(requestPeriod(api.N30d), "planet")},
		{name: "too many dimensions", req: withDimensions(requestPeriod(api.N30d), fourDimensions...)},
		{name: "too many filters", req: withFilters(requestPeriod(api.N30d), elevenFilters...)},
		{name: "limit above maximum", req: withLimit(requestPeriod(api.N30d), 5000)},
		{name: "zero limit", req: withLimit(requestPeriod(api.N30d), 0)},
		{name: "unknown period", req: requestPeriod(api.Period("quarter"))},
		{name: "both period and range", req: withRange(requestPeriod(api.N7d), "2026-10-01", "2026-10-07")},
		{name: "wrong site", req: func() api.QueryRequest { r := requestPeriod(api.N7d); r.SiteId = 2; return r }()},
		{name: "unknown metric", req: func() api.QueryRequest { r := requestPeriod(api.N7d); r.Metrics = []api.Metric{"money"}; return r }()},
		{name: "unknown operator", req: withFilters(requestPeriod(api.N7d), api.Filter{Dimension: "country", Operator: "matches", Values: []string{"US"}})},
		{name: "filter without values", req: withFilters(requestPeriod(api.N7d), api.Filter{Dimension: "country", Operator: api.Is})},
		{name: "invalid timezone", req: func() api.QueryRequest { r := requestPeriod(api.N7d); tz := "Mars/Olympus"; r.Timezone = &tz; return r }()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, problem := query.Normalize(tt.req, utc, now); problem == nil || problem.Status != 400 || problem.Detail == nil || *problem.Detail == "" {
				t.Fatalf("Normalize() problem = %+v, want detailed status 400", problem)
			}
		})
	}
}

func TestValidateFiltersUsesDashboardQueryRules(t *testing.T) {
	t.Parallel()

	eleven := make([]api.Filter, 11)
	for i := range eleven {
		eleven[i] = api.Filter{Dimension: "country", Operator: api.Is, Values: []string{"US"}}
	}
	tests := []struct {
		name    string
		filters []api.Filter
		wantErr bool
	}{
		{name: "standard dimension", filters: []api.Filter{{Dimension: "country", Operator: api.Is, Values: []string{"US"}}}},
		{name: "custom property", filters: []api.Filter{{Dimension: "prop:plan", Operator: api.Contains, Values: []string{"pro"}}}},
		{name: "too many", filters: eleven, wantErr: true},
		{name: "unknown dimension", filters: []api.Filter{{Dimension: "planet", Operator: api.Is, Values: []string{"earth"}}}, wantErr: true},
		{name: "unknown operator", filters: []api.Filter{{Dimension: "country", Operator: "matches", Values: []string{"US"}}}, wantErr: true},
		{name: "empty values", filters: []api.Filter{{Dimension: "country", Operator: api.Is}}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := query.ValidateFilters(tt.filters)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateFilters() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestNormalizeResolvesPeriodsAndDefaults(t *testing.T) {
	t.Parallel()

	kolkata, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		t.Fatal(err)
	}
	site := sites.Site{ID: 1, TZ: kolkata}
	now := time.Date(2026, 10, 7, 20, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		period   api.Period
		wantFrom string
		wantTo   string
	}{
		{name: "today", period: api.Today, wantFrom: "2026-10-08", wantTo: "2026-10-08"},
		{name: "yesterday", period: api.Yesterday, wantFrom: "2026-10-07", wantTo: "2026-10-07"},
		{name: "seven days", period: api.N7d, wantFrom: "2026-10-02", wantTo: "2026-10-08"},
		{name: "thirty days", period: api.N30d, wantFrom: "2026-09-09", wantTo: "2026-10-08"},
		{name: "month", period: api.Month, wantFrom: "2026-10-01", wantTo: "2026-10-08"},
		{name: "last month", period: api.LastMonth, wantFrom: "2026-09-01", wantTo: "2026-09-30"},
		{name: "year", period: api.Year, wantFrom: "2026-01-01", wantTo: "2026-10-08"},
		{name: "twelve months", period: api.N12mo, wantFrom: "2025-10-09", wantTo: "2026-10-08"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			normalized, problem := query.Normalize(requestPeriod(tt.period), site, now)
			if problem != nil {
				t.Fatalf("Normalize() problem = %+v", problem)
			}
			if got := normalized.From.Format(time.DateOnly); got != tt.wantFrom {
				t.Errorf("From = %s, want %s", got, tt.wantFrom)
			}
			if got := normalized.To.Format(time.DateOnly); got != tt.wantTo {
				t.Errorf("To = %s, want %s", got, tt.wantTo)
			}
			if normalized.Limit != 100 {
				t.Errorf("Limit = %d, want 100", normalized.Limit)
			}
			if normalized.Location != kolkata {
				t.Errorf("Location = %v, want %v", normalized.Location, kolkata)
			}
		})
	}
}

func TestNormalizeAcceptsMaximumRangeAndLimit(t *testing.T) {
	t.Parallel()

	req := withLimit(requestRange("2024-01-01", "2024-12-31"), 1000)
	normalized, problem := query.Normalize(req, sites.Site{ID: 1, TZ: time.UTC}, time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC))
	if problem != nil {
		t.Fatalf("Normalize() problem = %+v", problem)
	}
	if normalized.Limit != 1000 {
		t.Fatalf("Limit = %d, want 1000", normalized.Limit)
	}
}

func TestNormalizeAcceptsRegionAndCityDimensionsAndFilters(t *testing.T) {
	t.Parallel()

	req := withDimensions(requestPeriod(api.N7d), "region", "city")
	req = withFilters(req, api.Filter{Dimension: "region", Operator: api.Is, Values: []string{"California"}})
	normalized, problem := query.Normalize(req, sites.Site{ID: 1, TZ: time.UTC}, time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC))
	if problem != nil {
		t.Fatalf("Normalize() problem = %+v", problem)
	}
	if !reflect.DeepEqual(normalized.Dimensions, []string{"region", "city"}) || len(normalized.Filters) != 1 {
		t.Fatalf("Normalize() = dimensions %v filters %v", normalized.Dimensions, normalized.Filters)
	}
}

func TestNormalizeUsesRequestedTimezone(t *testing.T) {
	t.Parallel()

	tz := "America/New_York"
	req := requestPeriod(api.Today)
	req.Timezone = &tz
	normalized, problem := query.Normalize(req, sites.Site{ID: 1, TZ: time.UTC}, time.Date(2026, 10, 8, 2, 0, 0, 0, time.UTC))
	if problem != nil {
		t.Fatalf("Normalize() problem = %+v", problem)
	}
	if got := normalized.From.Format(time.DateOnly); got != "2026-10-07" {
		t.Fatalf("From = %s, want 2026-10-07", got)
	}
}

func TestNormalizeCountsCivilDaysAcrossSkippedDate(t *testing.T) {
	t.Parallel()

	apia, err := time.LoadLocation("Pacific/Apia")
	if err != nil {
		t.Fatal(err)
	}
	req := requestRange("2010-12-31", "2012-01-01")
	if _, problem := query.Normalize(req, sites.Site{ID: 1, TZ: apia}, time.Date(2012, 1, 1, 12, 0, 0, 0, time.UTC)); problem == nil {
		t.Fatal("Normalize() problem = nil, want range-over-366-days problem")
	}
}

func TestNormalizePreservesDateAcrossMidnightDSTGap(t *testing.T) {
	t.Parallel()

	santiago, err := time.LoadLocation("America/Santiago")
	if err != nil {
		t.Fatal(err)
	}
	req := requestRange("2026-09-06", "2026-09-06")
	normalized, problem := query.Normalize(req, sites.Site{ID: 1, TZ: santiago}, time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC))
	if problem != nil {
		t.Fatalf("Normalize() problem = %+v", problem)
	}
	if got := normalized.From.Format(time.DateOnly); got != "2026-09-06" {
		t.Fatalf("From = %s, want 2026-09-06", got)
	}
}

func TestEscapeLike(t *testing.T) {
	t.Parallel()

	if got := query.EscapeLike("50%_off\\x"); got != "50\\%\\_off\\\\x" {
		t.Fatalf("EscapeLike() = %q", got)
	}
}

func TestWithStatementTimeoutRollsBackAndMapsTimeout(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()

	err := query.WithStatementTimeout(ctx, pool, 10*time.Millisecond, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "create table timeout_probe (id integer)"); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, "select pg_sleep(0.1)")
		return err
	})
	if err == nil {
		t.Fatal("WithStatementTimeout() error = nil, want timeout")
	}
	problem := query.QueryProblem(err)
	if problem == nil || problem.Status != 503 || problem.Type != "https://panel.dev/problems/query-timeout" {
		t.Fatalf("QueryProblem() = %+v, want query-timeout status 503", problem)
	}

	var relation *string
	if scanErr := pool.QueryRow(ctx, "select to_regclass('timeout_probe')::text").Scan(&relation); scanErr != nil {
		t.Fatal(scanErr)
	}
	if relation != nil {
		t.Fatalf("timed-out transaction was partially committed: %q", *relation)
	}
}

func TestQueryProblemIgnoresNonTimeoutErrors(t *testing.T) {
	t.Parallel()

	if problem := query.QueryProblem(errors.New("database unavailable")); problem != nil {
		t.Fatalf("QueryProblem() = %+v, want nil", problem)
	}
}

func request() api.QueryRequest {
	return api.QueryRequest{SiteId: 1, Metrics: []api.Metric{api.Visitors}}
}

func requestPeriod(period api.Period) api.QueryRequest {
	req := request()
	req.Period = &period
	return req
}

func requestRange(from, to string) api.QueryRequest {
	return withRange(request(), from, to)
}

func withRange(req api.QueryRequest, from, to string) api.QueryRequest {
	req.DateRange = &api.DateRange{From: apiDate(from), To: apiDate(to)}
	return req
}

func withDimensions(req api.QueryRequest, dimensions ...string) api.QueryRequest {
	req.Dimensions = &dimensions
	return req
}

func withFilters(req api.QueryRequest, filters ...api.Filter) api.QueryRequest {
	req.Filters = &filters
	return req
}

func withLimit(req api.QueryRequest, limit int) api.QueryRequest {
	req.Limit = &limit
	return req
}

func apiDate(value string) openapi_types.Date {
	return openapi_types.Date{Time: mustDay(value)}
}

func mustDay(value string) time.Time {
	day, err := time.Parse(time.DateOnly, value)
	if err != nil {
		panic(err)
	}
	return day
}
