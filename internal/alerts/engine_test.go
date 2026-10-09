package alerts

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"supermetry/internal/api"
	"supermetry/internal/platform/goals"
	queryengine "supermetry/internal/query"
	"supermetry/internal/sites"
)

func TestEngineSourceMapsExactInstantsToInclusiveSiteCivilDates(t *testing.T) {
	kathmandu := mustLocation(t, "Asia/Kathmandu")
	query := &fakeQueryer{responses: []api.QueryResponse{{Totals: map[string]float64{"visitors": 42}}}}
	source := NewEngineSource(query, staticGoals{})
	if query.goals == nil {
		t.Fatal("NewEngineSource did not attach goal definitions to query engine")
	}
	from := time.Date(2026, 10, 7, 17, 0, 0, 0, time.UTC)
	to := time.Date(2026, 10, 8, 18, 14, 59, 0, time.UTC)

	got, err := source.Visitors(context.Background(), sites.Site{ID: 9, TZ: kathmandu}, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if got != 42 {
		t.Fatalf("Visitors() = %v, want 42", got)
	}
	request := query.requests[0]
	if request.DateRange.From.Time.Format(time.DateOnly) != "2026-10-07" || request.DateRange.To.Time.Format(time.DateOnly) != "2026-10-08" {
		t.Fatalf("date range = %s..%s", request.DateRange.From.Time.Format(time.DateOnly), request.DateRange.To.Time.Format(time.DateOnly))
	}
	if request.Timezone == nil || *request.Timezone != "Asia/Kathmandu" || len(request.Metrics) != 1 || request.Metrics[0] != api.Visitors {
		t.Fatalf("request = %+v", request)
	}
}

func TestEngineSourceFiltersConversionsByGoalIDForEveryGoalKind(t *testing.T) {
	for _, goal := range []goals.Goal{
		{ID: 7, SiteID: 9, Kind: "event", Value: "signup"},
		{ID: 8, SiteID: 9, Kind: "page", Value: "/pricing"},
		{ID: 9, SiteID: 9, Kind: "scroll", Value: "/pricing", ScrollThreshold: 75},
	} {
		t.Run(goal.Kind, func(t *testing.T) {
			query := &fakeQueryer{responses: []api.QueryResponse{{Totals: map[string]float64{"conversions": 5}}}}
			source := NewEngineSource(query, staticGoals{values: []goals.Goal{goal}})

			got, err := source.Conversions(context.Background(), sites.Site{ID: 9, TZ: time.UTC}, goal.ID, time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC))
			if err != nil {
				t.Fatal(err)
			}
			if got != 5 {
				t.Fatalf("Conversions() = %v, want 5", got)
			}
			request := query.requests[0]
			if len(request.Metrics) != 1 || request.Metrics[0] != api.Conversions || request.Filters == nil || len(*request.Filters) != 1 {
				t.Fatalf("request = %+v", request)
			}
			filter := (*request.Filters)[0]
			if filter.Dimension != "goal_id" || filter.Operator != api.Is || len(filter.Values) != 1 || filter.Values[0] != stringInt(goal.ID) {
				t.Fatalf("goal filter = %+v", filter)
			}
		})
	}
}

func TestEngineSourceReportsDeletedGoalUnavailableWithoutQuerying(t *testing.T) {
	query := &fakeQueryer{}
	source := NewEngineSource(query, staticGoals{})
	_, err := source.Conversions(context.Background(), sites.Site{ID: 9, TZ: time.UTC}, 77, time.Now(), time.Now())
	if err == nil || !strings.Contains(err.Error(), "goal 77 is unavailable") {
		t.Fatalf("Conversions() error = %v", err)
	}
	if len(query.requests) != 0 {
		t.Fatalf("deleted goal issued requests: %+v", query.requests)
	}
}

func stringInt(value int64) string { return strconv.FormatInt(value, 10) }

type fakeQueryer struct {
	requests  []api.QueryRequest
	responses []api.QueryResponse
	goals     queryengine.GoalLookup
}

func (f *fakeQueryer) SetGoalLookup(definitions queryengine.GoalLookup) { f.goals = definitions }

func (f *fakeQueryer) Query(_ context.Context, request api.QueryRequest) (api.QueryResponse, error) {
	f.requests = append(f.requests, request)
	response := f.responses[0]
	f.responses = f.responses[1:]
	return response, nil
}

type staticGoals struct{ values []goals.Goal }

func (g staticGoals) Goals(int32) []goals.Goal { return append([]goals.Goal(nil), g.values...) }
