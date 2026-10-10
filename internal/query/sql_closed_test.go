package query

import (
	"strings"
	"testing"

	"supermetry/internal/api"
)

func TestBuildClosedSQLBindsPropertyDimensionKey(t *testing.T) {
	key := "quote'\\unicode-雪--"
	dimension := "prop:" + key
	querySQL, args, err := buildClosedSQL(Normalized{
		SiteID: 1, From: testDate("2026-10-09"), To: testDate("2026-10-09"),
		Dimensions: []string{dimension}, Metrics: []api.Metric{api.Events},
	}, Part{Route: RouteClosed, From: testDate("2026-10-09"), To: testDate("2026-10-09")})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(querySQL, key) || strings.Contains(querySQL, "unicode-雪--") {
		t.Fatalf("closed SQL contains property key literal %q:\n%s", key, querySQL)
	}
	if !strings.Contains(querySQL, "props ->> $") {
		t.Fatalf("closed SQL does not bind property key:\n%s", querySQL)
	}
	if !containsArgument(args, key) {
		t.Fatalf("closed SQL args = %#v, want property key %q", args, key)
	}
}

func containsArgument(args []any, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}
