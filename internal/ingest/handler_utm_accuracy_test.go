package ingest_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"panel/internal/api"
	"panel/internal/closer"
	"panel/internal/dict"
	"panel/internal/query"
)

func TestLiveIngestUTMBreakdownsExcludeUntaggedTrafficAcrossStorageTiers(t *testing.T) {
	fixture := newHandlerFixture(t, 32, nil)
	requests := []struct {
		body   string
		remote string
	}{
		{
			body:   `{"site":"abc123","name":"pageview","url":"https://example.com/?utm_source=newsletter&utm_medium=email&utm_campaign=autumn"}`,
			remote: "198.51.100.7:5555",
		},
		{
			body:   `{"site":"abc123","name":"pageview","url":"https://example.com/"}`,
			remote: "198.51.100.8:5555",
		},
	}
	for _, request := range requests {
		if response := post(t, fixture.h, request.body, testUA, request.remote); response.Code != http.StatusAccepted {
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
	fixtureDay := fakeNow().UTC().Truncate(24 * time.Hour)
	assertLiveUTMBreakdowns(t, engine.WithForcedRoute(query.RouteHot), fixtureDay)

	if _, err := closer.CloseDay(context.Background(), fixture.pool, []int32{1}, fixtureDay); err != nil {
		t.Fatal(err)
	}
	// The scheduler invalidates close metadata after closing days; do the same.
	engine.InvalidateSite(1)
	for _, route := range []query.Route{query.RouteClosed, query.RouteRollup} {
		t.Run(fmt.Sprint(route), func(t *testing.T) {
			assertLiveUTMBreakdowns(t, engine.WithForcedRoute(route), fixtureDay)
		})
	}
}

func assertLiveUTMBreakdowns(t *testing.T, engine *query.Engine, day time.Time) {
	t.Helper()
	wants := map[string]string{
		"utm_source": "newsletter", "utm_medium": "email", "utm_campaign": "autumn",
	}
	for dimension, want := range wants {
		dimensions := []string{dimension}
		request := api.QueryRequest{
			SiteId: 1,
			DateRange: &api.DateRange{
				From: openapi_types.Date{Time: day}, To: openapi_types.Date{Time: day},
			},
			Metrics: []api.Metric{api.Visitors}, Dimensions: &dimensions,
		}
		response, err := engine.Query(context.Background(), request)
		if err != nil {
			t.Fatalf("%s query: %v", dimension, err)
		}
		if len(response.Rows) != 1 || response.Rows[0][dimension] != want || response.Rows[0]["visitors"] != float64(1) {
			t.Fatalf("%s rows = %+v, want only %q with one visitor", dimension, response.Rows, want)
		}
	}
}
