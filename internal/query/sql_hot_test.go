package query

import (
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"supermetry/internal/api"
)

func TestBuildHotSQLUsesSetBasedEventAccess(t *testing.T) {
	tests := []struct {
		name       string
		dimensions []string
		metrics    []api.Metric
		filters    []api.Filter
	}{
		{name: "time on page totals", metrics: []api.Metric{api.Metric(TimeOnPage)}},
		{name: "scroll totals", metrics: []api.Metric{api.ScrollDepth}},
		{name: "page breakdown", dimensions: []string{"page"}, metrics: []api.Metric{api.Pageviews, api.Metric(ExitRate), api.Metric(TimeOnPage), api.ScrollDepth}},
		{name: "page filter", metrics: []api.Metric{api.Visits}, filters: []api.Filter{{Dimension: "page", Operator: api.Contains, Values: []string{"/docs"}}}},
		{name: "event filter", metrics: []api.Metric{api.Visits}, filters: []api.Filter{{Dimension: "event", Operator: api.Is, Values: []string{"signup"}}}},
		{name: "property filter", metrics: []api.Metric{api.Visits}, filters: []api.Filter{{Dimension: "prop:plan", Operator: api.Is, Values: []string{"pro"}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sql, _, err := buildHotSQL(Normalized{
				SiteID: 1, From: testDate("2026-10-09"), To: testDate("2026-10-09"),
				Dimensions: tt.dimensions, Metrics: tt.metrics, Filters: tt.filters,
			}, Part{Route: RouteHot, From: testDate("2026-10-09"), To: testDate("2026-10-09")})
			if err != nil {
				t.Fatal(err)
			}
			compact := strings.ReplaceAll(sql, " ", "")
			compact = strings.ReplaceAll(compact, "\n", "")
			for _, correlated := range []string{"e.session_id=s.session_id", "ce.session_id=s.session_id", "ge.session_id=s.session_id"} {
				if strings.Contains(compact, correlated) {
					t.Fatalf("hot SQL contains correlated event access %q:\n%s", correlated, sql)
				}
			}
			if strings.Contains(strings.ToLower(sql), "join lateral") {
				t.Fatalf("hot SQL contains per-session lateral join:\n%s", sql)
			}
		})
	}
}

func TestBuildHotSQLOnlyTouchesEventsWhenRequested(t *testing.T) {
	part := Part{Route: RouteHot, From: testDate("2026-10-09"), To: testDate("2026-10-09")}
	tests := []struct {
		name       string
		dimensions []string
		metrics    []api.Metric
		wantEvents bool
	}{
		{name: "source visits", dimensions: []string{"source"}, metrics: []api.Metric{api.Visitors, api.Visits}, wantEvents: false},
		{name: "time on page", dimensions: []string{"source"}, metrics: []api.Metric{api.Metric(TimeOnPage)}, wantEvents: true},
		{name: "scroll depth", dimensions: []string{"source"}, metrics: []api.Metric{api.ScrollDepth}, wantEvents: true},
		{name: "page breakdown", dimensions: []string{"page"}, metrics: []api.Metric{api.Pageviews}, wantEvents: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sql, _, err := buildHotSQL(Normalized{
				SiteID: 1, From: part.From, To: part.To, Dimensions: tt.dimensions, Metrics: tt.metrics,
			}, part)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Contains(strings.ToLower(sql), "events "); got != tt.wantEvents {
				t.Fatalf("events access = %v, want %v:\n%s", got, tt.wantEvents, sql)
			}
		})
	}
}

func TestBuildHotSQLUsesDateParametersForCivilDays(t *testing.T) {
	_, args, err := buildHotSQL(Normalized{
		SiteID: 1, From: testDate("2026-10-09"), To: testDate("2026-10-09"), Metrics: []api.Metric{api.Visits},
	}, Part{Route: RouteHot, From: testDate("2026-10-09"), To: testDate("2026-10-09")})
	if err != nil {
		t.Fatal(err)
	}
	var dates int
	for _, arg := range args {
		if _, ok := arg.(pgtype.Date); ok {
			dates++
		}
	}
	if dates != 2 {
		t.Fatalf("date parameters = %d, want 2; args=%#v", dates, args)
	}
}

func testDate(value string) time.Time {
	date, err := time.Parse(time.DateOnly, value)
	if err != nil {
		panic(err)
	}
	return date
}
