package main

import (
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
)

func TestCollectRoutes(t *testing.T) {
	t.Parallel()

	routes, unknown, err := collectRoutes(filepath.Join("testdata", "router"))
	if err != nil {
		t.Fatalf("collect routes: %v", err)
	}
	if len(unknown) != 0 {
		t.Fatalf("unknown registrations = %v, want none", unknown)
	}
	want := []Route{
		{Method: "POST", Pattern: "/api/example/v1/widgets", Group: "/api/example/v1", Handler: "handler", Auth: "session"},
		{Method: "DELETE", Pattern: "/api/example/v1/widgets/{widget}", Group: "/api/example/v1", Handler: "handler", Auth: "none"},
		{Method: "GET", Pattern: "/api/example/v1/widgets/{widget}", Group: "/api/example/v1", Handler: "handler.ServeHTTP", Auth: "none"},
		{Method: "POST", Pattern: "/api/public/v1/events", Group: "/api/public/v1", Handler: "deps.Ingest", Auth: "none"},
		{Method: "GET", Pattern: "/js/script.full.js", Group: "/js/script.full.js", Handler: "handler", Auth: "none"},
	}
	if diff := diffRoutes(routes, want); diff != "" {
		t.Fatal(diff)
	}
}

func diffRoutes(got, want []Route) string {
	if reflect.DeepEqual(got, want) {
		return ""
	}
	return fmt.Sprintf("routes = %#v, want %#v", got, want)
}
