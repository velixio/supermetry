package ingest_test

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"supermetry/internal/api"
	"supermetry/internal/closer"
	"supermetry/internal/dict"
	"supermetry/internal/privacy"
	"supermetry/internal/query"
)

// Mixed traffic: a fully tagged visit, an organic search visit, a referral, a
// direct visit and a visit tagged with medium and campaign but no source.
func TestChannelAndCampaignBreakdownsAcrossStorageTiers(t *testing.T) {
	fixture := newHandlerFixture(t, 32, nil)
	fixture.h.TrackingMode = privacy.Identified
	setProfilesEnabled(t, fixture, true)
	bodies := []string{
		`{"site":"abc123","name":"pageview","url":"https://supermetry.com/?utm_source=newsletter&utm_medium=email&utm_campaign=autumn"}`,
		`{"site":"abc123","name":"pageview","url":"https://supermetry.com/","referrer":"https://www.google.com/"}`,
		`{"site":"abc123","name":"pageview","url":"https://supermetry.com/","referrer":"https://blog.example.org/post"}`,
		`{"site":"abc123","name":"pageview","url":"https://supermetry.com/"}`,
		`{"site":"abc123","name":"pageview","url":"https://supermetry.com/?utm_medium=cpc&utm_campaign=winter","referrer":"https://www.bing.com/"}`,
	}
	for index, body := range bodies {
		remote := fmt.Sprintf("198.51.100.%d:5555", 10+index)
		if response := post(t, fixture.h, body, testUA, remote); response.Code != http.StatusAccepted {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		fixture.clock.now = fixture.clock.now.Add(time.Minute)
	}
	fixture.stop(t)
	dictionary := dict.New(fixture.pool)
	if err := dictionary.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	engine := query.NewEngine(fixture.pool, dictionary, fixture.h.Sites)
	engine.SetTrackingMode(privacy.Identified)
	day := fakeNow().UTC().Truncate(24 * time.Hour)

	check := func(t *testing.T, engine *query.Engine) {
		t.Helper()
		for dimension, want := range map[string]string{
			"channel":      "Direct=1,Email=1,Organic Search=1,Paid Search=1,Referral=1",
			"utm_campaign": "autumn=1,winter=1",
		} {
			if got := breakdown(t, engine, day, dimension); got != want {
				t.Errorf("%s = %s, want %s", dimension, got, want)
			}
		}
	}
	t.Run("hot", func(t *testing.T) { check(t, engine.WithForcedRoute(query.RouteHot)) })
	if _, err := closer.CloseDay(context.Background(), fixture.pool, []int32{1}, day); err != nil {
		t.Fatal(err)
	}
	engine.InvalidateSite(1)
	t.Run("closed", func(t *testing.T) { check(t, engine.WithForcedRoute(query.RouteClosed)) })
	t.Run("auto", func(t *testing.T) { check(t, engine) })
}

func breakdown(t *testing.T, engine *query.Engine, day time.Time, dimension string) string {
	t.Helper()
	// Shaped like the dashboard's breakdown card request.
	dimensions := []string{dimension}
	desc, limit, imported, compare := api.Desc, 10, true, api.PreviousPeriod
	orderBy := []api.OrderBy{{Column: "visitors", Direction: &desc}}
	filters := []api.Filter{}
	response, err := engine.Query(context.Background(), api.QueryRequest{
		SiteId:    1,
		DateRange: &api.DateRange{From: openapi_types.Date{Time: day}, To: openapi_types.Date{Time: day}},
		Metrics:   []api.Metric{api.Visitors}, Dimensions: &dimensions,
		OrderBy: &orderBy, Limit: &limit, IncludeImported: &imported, Compare: &compare, Filters: &filters,
	})
	if err != nil {
		return "error: " + err.Error()
	}
	parts := make([]string, 0, len(response.Rows))
	for _, row := range response.Rows {
		parts = append(parts, fmt.Sprintf("%v=%v", row[dimension], row["visitors"]))
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}
