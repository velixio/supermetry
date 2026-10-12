package main

import (
	"path/filepath"
	"testing"
)

func TestMergeInventory(t *testing.T) {
	t.Parallel()

	routes, _, err := collectRoutes(filepath.Join("testdata", "router"))
	if err != nil {
		t.Fatalf("collect routes: %v", err)
	}
	operations, err := readOpenAPI(filepath.Join("testdata", "openapi.yaml"))
	if err != nil {
		t.Fatalf("read OpenAPI: %v", err)
	}
	hits, err := readHits(filepath.Join("testdata", "hits.txt"))
	if err != nil {
		t.Fatalf("read hits: %v", err)
	}

	inventory := mergeInventory(routes, operations, hits)
	assertRouteState(t, inventory.Routes, "GET", "/api/example/v1/widgets/{widget}", true, []string{"TestWidget/found"})
	assertRouteState(t, inventory.Routes, "POST", "/api/example/v1/widgets", true, nil)
	assertRouteState(t, inventory.Routes, "DELETE", "/api/example/v1/widgets/{widget}", false, nil)
	if len(inventory.OpenAPIWithoutRoute) != 1 || inventory.OpenAPIWithoutRoute[0].Pattern != "/api/example/v1/orphans" {
		t.Fatalf("OpenAPI without route = %#v", inventory.OpenAPIWithoutRoute)
	}
}

func TestMergeInventoryMatchesMultiSegmentWildcard(t *testing.T) {
	t.Parallel()

	routes := []Route{{Method: "GET", Pattern: "/api/private/sites/{site}/replays/{replay_path...}", Group: "/api/private/sites", Handler: "replayAsset", Auth: "session"}}
	operations := []Operation{
		{Method: "GET", Pattern: "/api/private/sites/{site_id}/replays/{session_id}/data", canonical: canonicalPattern("/api/private/sites/{site_id}/replays/{session_id}/data")},
		{Method: "GET", Pattern: "/api/private/sites/{site_id}/replays/css/{hash}", canonical: canonicalPattern("/api/private/sites/{site_id}/replays/css/{hash}")},
	}

	inventory := mergeInventory(routes, operations, hitSet{})
	if !inventory.Routes[0].InOpenAPI {
		t.Fatal("wildcard route was not marked documented")
	}
	if len(inventory.OpenAPIWithoutRoute) != 0 {
		t.Fatalf("OpenAPI without route = %#v, want none", inventory.OpenAPIWithoutRoute)
	}
}

func assertRouteState(t *testing.T, routes []Route, method, pattern string, documented bool, hits []string) {
	t.Helper()
	for _, route := range routes {
		if route.Method != method || route.Pattern != pattern {
			continue
		}
		if route.InOpenAPI != documented {
			t.Errorf("%s %s in_openapi = %v, want %v", method, pattern, route.InOpenAPI, documented)
		}
		if len(route.HitByTests) != len(hits) {
			t.Fatalf("%s %s hit_by_tests = %v, want %v", method, pattern, route.HitByTests, hits)
		}
		for i := range hits {
			if route.HitByTests[i] != hits[i] {
				t.Errorf("%s %s hit_by_tests = %v, want %v", method, pattern, route.HitByTests, hits)
			}
		}
		return
	}
	t.Errorf("route %s %s not found", method, pattern)
}
