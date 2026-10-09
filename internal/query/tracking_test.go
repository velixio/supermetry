package query_test

import (
	"context"
	"errors"
	"testing"

	"panel/internal/api"
	"panel/internal/privacy"
	"panel/internal/query"
)

func TestAnonymousEngineRejectsBeforeStorageOrCache(t *testing.T) {
	engine := query.NewEngine(nil, nil, nil)
	ctx := context.Background()
	for _, dimension := range []string{"page", "source", "utm_source", "hostname", "event", "prop:email", "goal_id"} {
		t.Run(dimension, func(t *testing.T) {
			_, err := engine.Query(ctx, api.QueryRequest{Dimensions: &[]string{dimension}})
			if !errors.Is(err, api.ErrAnonymousHistory) {
				t.Fatalf("got %v", err)
			}
		})
	}
	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{"events", func() error { _, e := engine.Events(ctx, api.EventExploreRequest{}); return e }},
		{"properties", func() error { _, e := engine.Properties(ctx, api.PropertyExploreRequest{}); return e }},
		{"values", func() error { _, e := engine.PropertyValues(ctx, api.PropertyValueExploreRequest{}); return e }},
		{"suggestions", func() error { _, e := engine.Suggestions(ctx, api.SuggestionExploreRequest{}); return e }},
		{"sessions", func() error { _, e := engine.Sessions(ctx, 1, api.ListSessionsParams{}, nil); return e }},
		{"session", func() error { _, e := engine.Session(ctx, 1, 1); return e }},
		{"journey", func() error { _, e := engine.Journey(ctx, api.JourneyRequest{}); return e }},
		{"funnel", func() error { _, e := engine.Funnel(ctx, api.FunnelRequest{}); return e }},
		{"retention", func() error { _, e := engine.Retention(ctx, api.RetentionRequest{}); return e }},
		{"vitals", func() error { _, e := engine.Vitals(ctx, api.VitalsRequest{}); return e }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); !errors.Is(err, api.ErrAnonymousHistory) {
				t.Fatalf("got %v", err)
			}
		})
	}
}

// This serial fixture models reconstruction at a restart boundary, not a
// supported live policy mutation while queries are running.
func TestAnonymousHistoryRestrictsWarmCacheWithoutDeletingRows(t *testing.T) {
	pool, engine := setupTiny(t)
	ctx := context.Background()
	engine.SetCache(query.NewCache(16))
	request := queryRequestRange("2026-10-06", "2026-10-07", []string{"page"}, nil)
	before := mustQuery(t, engine, request)
	cached := mustQuery(t, engine, request)
	if len(before.Rows) == 0 || !cached.Meta.Cached {
		t.Fatal("fixture must expose cached historical URL labels")
	}
	var rowsBefore int
	if err := pool.QueryRow(ctx, "select count(*) from events").Scan(&rowsBefore); err != nil {
		t.Fatal(err)
	}
	engine.SetTrackingMode(privacy.Anonymous)
	if _, err := engine.Query(ctx, request); !errors.Is(err, api.ErrAnonymousHistory) {
		t.Fatalf("cached URL query got %v", err)
	}
	if _, err := engine.Session(ctx, 1, 301); !errors.Is(err, api.ErrAnonymousHistory) {
		t.Fatalf("historical session got %v", err)
	}
	counts := mustQuery(t, engine, queryRequestRange("2026-10-06", "2026-10-07", nil, nil))
	if counts.Totals["visitors"] == 0 {
		t.Fatal("aggregate visitors unavailable")
	}
	var rowsAfter int
	if err := pool.QueryRow(ctx, "select count(*) from events").Scan(&rowsAfter); err != nil {
		t.Fatal(err)
	}
	if rowsAfter != rowsBefore {
		t.Fatalf("stored rows changed %d -> %d", rowsBefore, rowsAfter)
	}
	engine.SetTrackingMode(privacy.Identified)
	restored := mustQuery(t, engine, request)
	if len(restored.Rows) != len(before.Rows) {
		t.Fatal("historic labels lost after explicit restoration")
	}
}

func TestAnonymousCanonicalBreakdownsHideHostileImportsAndPreserveCounts(t *testing.T) {
	pool, engine := setupTiny(t)
	importID := insertImport(t, pool, "completed")
	insertImportedDay(t, pool, importID, "2026-10-05", 30, 30, 30, 0, 0)
	insertImportedDimension(t, pool, importID, "2026-10-05", 3, "private-one", 10, 10, 10, 0, 0, 0)
	insertImportedDimension(t, pool, importID, "2026-10-05", 3, "private-two", 20, 20, 20, 0, 0, 0)
	engine.SetCache(query.NewCache(16))
	req := queryRequestRange("2026-10-05", "2026-10-07", []string{"country"}, nil)
	nativeReq := queryRequestRange("2026-10-06", "2026-10-07", []string{"country"}, nil)
	mustQuery(t, engine, nativeReq)
	if !mustQuery(t, engine, nativeReq).Meta.Cached {
		t.Fatal("identified native cache fixture not warm")
	}
	identified := mustQuery(t, engine, req)
	if len(identified.Rows) < 4 {
		t.Fatal("hostile import fixture not present")
	}
	engine.SetTrackingMode(privacy.Anonymous)
	result := mustQuery(t, engine, req)
	if result.Meta.Cached {
		t.Fatal("anonymous reused identified cache namespace")
	}
	foundUnknown, foundUS := false, false
	for _, row := range result.Rows {
		value := row["country"].(string)
		if value == "" {
			foundUnknown = true
			if row["visitors"] != float64(30) {
				t.Fatalf("unknown counts=%v", row)
			}
		}
		if value == "US" {
			foundUS = true
		}
		if _, valid := privacy.CanonicalDimensionValue("country", value); !valid {
			t.Fatalf("leaked label %q", value)
		}
	}
	if !foundUnknown || !foundUS || result.Totals["visitors"] != 34 {
		t.Fatalf("result=%+v", result)
	}
	native := mustQuery(t, engine, nativeReq)
	if native.Meta.Cached {
		t.Fatal("anonymous reused identified native cache")
	}
	cached := mustQuery(t, engine, nativeReq)
	if !cached.Meta.Cached {
		t.Fatal("safe response not cached")
	}
	devices := mustQuery(t, engine, queryRequestRange("2026-10-06", "2026-10-08", []string{"device"}, nil))
	if len(devices.Rows) == 0 {
		t.Fatal("safe device breakdown unavailable")
	}
	for _, row := range devices.Rows {
		if _, valid := privacy.CanonicalDimensionValue("device", row["device"].(string)); !valid {
			t.Fatalf("device=%v", row)
		}
	}
	filtered := mustQuery(t, engine, queryRequestRange("2026-10-06", "2026-10-08", []string{"country"}, []api.Filter{{Dimension: "country", Operator: api.Is, Values: []string{"US"}}}))
	if len(filtered.Rows) != 1 || filtered.Rows[0]["country"] != "US" {
		t.Fatalf("safe filtered result=%+v", filtered)
	}
}
