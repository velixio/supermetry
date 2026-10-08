package alerts

import (
	"context"
	"testing"
	"time"

	"panel/internal/api"
	"panel/internal/platform/goals"
	"panel/internal/sites"
)

func TestEngineSourceMapsExactInstantsToInclusiveSiteCivilDates(t *testing.T) {
	kathmandu := mustLocation(t, "Asia/Kathmandu")
	query := &fakeQueryer{responses: []api.QueryResponse{{Totals: map[string]float64{"visitors": 42}}}}
	source := NewEngineSource(query, staticGoals{})
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

func TestEngineSourceFiltersConversionsByGoalValue(t *testing.T) {
	query := &fakeQueryer{responses: []api.QueryResponse{{Totals: map[string]float64{"conversions": 5}}}}
	source := NewEngineSource(query, staticGoals{values: []goals.Goal{{ID: 7, SiteID: 9, Value: "signup"}}})

	got, err := source.Conversions(context.Background(), sites.Site{ID: 9, TZ: time.UTC}, 7, time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC))
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
	if filter.Dimension != "goal" || filter.Operator != api.Is || len(filter.Values) != 1 || filter.Values[0] != "signup" {
		t.Fatalf("goal filter = %+v", filter)
	}
}

type fakeQueryer struct {
	requests  []api.QueryRequest
	responses []api.QueryResponse
}

func (f *fakeQueryer) Query(_ context.Context, request api.QueryRequest) (api.QueryResponse, error) {
	f.requests = append(f.requests, request)
	response := f.responses[0]
	f.responses = f.responses[1:]
	return response, nil
}

type staticGoals struct{ values []goals.Goal }

func (g staticGoals) Goals(int32) []goals.Goal { return append([]goals.Goal(nil), g.values...) }
