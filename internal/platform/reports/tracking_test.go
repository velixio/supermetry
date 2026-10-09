package reports

import (
	"context"
	"panel/internal/api"
	"panel/internal/sites"
	"testing"
	"time"
)

type countOnlyQuery struct {
	t     *testing.T
	calls int
}

func (q *countOnlyQuery) Query(_ context.Context, r api.QueryRequest) (api.QueryResponse, error) {
	q.calls++
	if r.Dimensions != nil || r.Filters != nil {
		q.t.Fatal("anonymous report queried historical labels")
	}
	return api.QueryResponse{Totals: map[string]float64{"visitors": 12, "pageviews": 20}}, nil
}
func TestAnonymousReportKeepsCountsWithoutHistoricalLabels(t *testing.T) {
	q := &countOnlyQuery{t: t}
	r := New(nil, q, nil, nil)
	summary, err := r.summary(context.Background(), sites.Site{ID: 1, TZ: time.UTC}, Weekly, reportRange{})
	if err != nil || summary.Visitors != 12 || summary.Pageviews != 20 || len(summary.Pages) != 0 || len(summary.Sources) != 0 || q.calls != 1 {
		t.Fatalf("summary=%+v err=%v calls=%d", summary, err, q.calls)
	}
}
