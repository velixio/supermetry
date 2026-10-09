package query

import (
	"strings"
	"testing"
	"time"

	"supermetry/internal/api"
	platformgoals "supermetry/internal/platform/goals"
)

func TestGoalBreakdownSQLScansSessionTierOnce(t *testing.T) {
	req := Normalized{
		SiteID: 1, Dimensions: []string{"goal"}, Metrics: []api.Metric{api.Visits},
		GoalDefinitions: map[int64]platformgoals.Goal{
			1: {ID: 1, SiteID: 1, Kind: "event", Match: "exact", Value: "Signup"},
			2: {ID: 2, SiteID: 1, Kind: "page", Match: "prefix", Value: "/pricing"},
		},
	}
	part := Part{From: time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)}
	for _, tt := range []struct {
		name, source string
		tier         tierKind
	}{
		{name: "hot", source: "from sessions s", tier: tierHot},
		{name: "closed", source: "from sessions_closed s", tier: tierClosed},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sql, _, err := buildGoalBreakdownSQL(req, part, tt.tier)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Count(sql, tt.source); got != 1 {
				t.Fatalf("session source occurrences=%d, want 1\n%s", got, sql)
			}
		})
	}
}

func TestClosedPropertyGoalPreaggregatesMatchesWithoutCorrelatedCounts(t *testing.T) {
	req := Normalized{
		SiteID: 1, Dimensions: []string{"goal"}, Metrics: []api.Metric{api.Conversions},
		GoalDefinitions: map[int64]platformgoals.Goal{
			1: {ID: 1, SiteID: 1, Kind: "event", Match: "exact", Value: "Signup", PropKey: "plan", PropValue: "pro"},
		},
	}
	part := Part{From: time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)}
	sql, _, err := buildGoalBreakdownSQL(req, part, tierClosed)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sql, "select count(*) from custom_closed") || strings.Contains(sql, "session_id<=s.session_id") {
		t.Fatalf("property goal uses per-session correlated counts:\n%s", sql)
	}
	for _, fragment := range []string{"exact_matches as materialized", "legacy_days as materialized", "custom_goal_matches"} {
		if !strings.Contains(sql, fragment) {
			t.Fatalf("property goal SQL missing set-based %q CTE:\n%s", fragment, sql)
		}
	}
	if got := strings.Count(sql, "ce.day between"); got != 2 {
		t.Fatalf("property goal date bounds=%d, want exact and legacy CTE bounds:\n%s", got, sql)
	}
}

func TestClosedPropertyGoalFilterBoundsExactAndLegacyMatches(t *testing.T) {
	part := Part{From: time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)}
	req := Normalized{
		SiteID: 1, Metrics: []api.Metric{api.Conversions},
		Filters: []api.Filter{{Dimension: "goal_id", Operator: api.Is, Values: []string{"1"}}},
		GoalDefinitions: map[int64]platformgoals.Goal{
			1: {ID: 1, SiteID: 1, Kind: "event", Match: "exact", Value: "Signup", PropKey: "plan", PropValue: "pro"},
		},
	}
	sql, _, err := buildClosedSQL(req, part)
	if err != nil {
		t.Fatal(err)
	}
	exactStart := strings.Index(sql, "exact_matches as materialized")
	legacyStart := strings.Index(sql, "legacy_days as materialized")
	legacyMatchesStart := strings.Index(sql, "legacy_matches as materialized")
	if exactStart < 0 || legacyStart <= exactStart || legacyMatchesStart <= legacyStart {
		t.Fatalf("goal_id filter SQL is missing exact or legacy match CTEs:\n%s", sql)
	}
	if got := strings.Count(sql[exactStart:legacyStart], "ce.day between"); got != 1 {
		t.Fatalf("goal_id filter exact-match date bounds=%d, want 1:\n%s", got, sql)
	}
	if got := strings.Count(sql[legacyStart:legacyMatchesStart], "ce.day between"); got != 1 {
		t.Fatalf("goal_id filter legacy-fallback date bounds=%d, want 1:\n%s", got, sql)
	}
}
