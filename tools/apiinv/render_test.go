package main

import (
	"strings"
	"testing"
)

func TestRenderMarkdown(t *testing.T) {
	t.Parallel()

	inventory := Inventory{
		Routes: []Route{
			{Method: "GET", Pattern: "/api/example/v1/hit", Group: "/api/example/v1", Handler: "hit", Auth: "session", InOpenAPI: true, HitByTests: []string{"TestHit"}},
			{Method: "POST", Pattern: "/api/example/v1/missing", Group: "/api/example/v1", Handler: "missing", Auth: "unknown", HitByTests: []string{}},
		},
		OpenAPIWithoutRoute: []Operation{{Method: "DELETE", Pattern: "/api/example/v1/orphan", OperationID: "deleteOrphan"}},
	}

	report := renderMarkdown(inventory)
	for _, want := range []string{
		"Registered routes | 2", "Documented routes | 1", "Routes hit by tests | 1",
		"Routes not hit by tests", "Routes missing from OpenAPI", "OpenAPI operations with no route",
		"Unknown auth derivation", "POST", "/api/example/v1/missing", "deleteOrphan",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("report does not contain %q:\n%s", want, report)
		}
	}
}
