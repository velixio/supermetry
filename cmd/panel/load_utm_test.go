package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"panel/internal/api"
	"panel/internal/closer"
	"panel/internal/db"
	"panel/internal/dict"
	"panel/internal/privacy"
	"panel/internal/query"
	"panel/internal/sites"
	"panel/internal/testpg"
)

func TestLoadPreservesUTMBreakdownsAcrossStorageTiers(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	registry := loadUTMRegistry(t, pool)
	fixtureDay := time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)

	var input bytes.Buffer
	for index, tags := range []map[string]string{
		{"utm_source": "newsletter", "utm_medium": "email", "utm_campaign": "autumn"},
		{},
	} {
		record := map[string]any{
			"ts": fixtureDay.Add(time.Duration(index) * time.Minute), "site": 1,
			"host": "site1.test", "path": "/", "ref": "", "name": "pageview",
			"props": map[string]string{}, "ip": fmt.Sprintf("192.0.2.%d", index+1),
			"ua": "fixture-browser", "screen_w": 1280, "country": "US",
			"browser": "Fixture", "os": "FixtureOS", "device": "desktop",
			"utm_source": tags["utm_source"], "utm_medium": tags["utm_medium"],
			"utm_campaign": tags["utm_campaign"],
		}
		if err := json.NewEncoder(&input).Encode(record); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := loadEvents(ctx, pool, registry, &input, privacy.Identified); err != nil {
		t.Fatal(err)
	}
	engine := loadUTMQueryEngine(t, pool, registry)
	assertLoadUTMBreakdowns(t, engine.WithForcedRoute(query.RouteHot), fixtureDay)

	if _, err := closer.CloseDay(ctx, pool, []int32{1}, fixtureDay); err != nil {
		t.Fatal(err)
	}
	// Mirror closeDueDays: forced-route engines share cached close metadata.
	engine.InvalidateSite(1)
	for _, route := range []query.Route{query.RouteClosed, query.RouteRollup} {
		assertLoadUTMBreakdowns(t, engine.WithForcedRoute(route), fixtureDay)
	}
	if _, err := pool.Exec(ctx, `delete from rollup_day_dim where site_id=1 and day=$1`, fixtureDay); err != nil {
		t.Fatal(err)
	}
	if days, err := closer.RecomputeRollups(ctx, pool); err != nil {
		t.Fatal(err)
	} else if days != 1 {
		t.Fatalf("recomputed days = %d, want 1", days)
	}
	assertLoadUTMBreakdowns(t, engine.WithForcedRoute(query.RouteRollup), fixtureDay)
}

func loadUTMRegistry(t *testing.T, pool *pgxpool.Pool) sites.Registry {
	t.Helper()
	if err := db.Migrate(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "sites.json")
	if err := os.WriteFile(path, []byte(`[{"id":1,"public_id":"site1.test","timezone":"UTC","retention_days":365,"allowed_hosts":["site1.test"],"profiles_enabled":true}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	registry, err := sites.LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func loadUTMQueryEngine(t *testing.T, pool *pgxpool.Pool, registry sites.Registry) *query.Engine {
	t.Helper()
	dictionary := dict.New(pool)
	if err := dictionary.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	engine := query.NewEngine(pool, dictionary, registry)
	engine.SetTrackingMode(privacy.Identified)
	return engine
}

func assertLoadUTMBreakdowns(t *testing.T, engine *query.Engine, day time.Time) {
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
