package query

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"supermetry/internal/api"
	"supermetry/internal/dict"
)

func TestDictionaryMatchPredicateUsesBoundEscapedPatterns(t *testing.T) {
	tests := []struct {
		name      string
		match     string
		value     string
		wantArg   string
		wantError bool
	}{
		{name: "exact keeps every metacharacter literal", match: "exact", value: `/a%_x*' OR true --`, wantArg: `/a%_x*' OR true --`},
		{name: "prefix escapes like syntax and keeps star literal", match: "prefix", value: `/a%_x*' OR true --`, wantArg: `/a\%\_x*' OR true --%`},
		{name: "wildcard only expands stars", match: "wildcard", value: `/a%_x*' OR true --`, wantArg: `/a\%\_x%' OR true --`},
		{name: "unknown", match: "contains", value: "/a", wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := &sqlBuilder{}
			predicate, err := dictionaryMatchPredicate(builder, tt.match, tt.value)
			if tt.wantError {
				if err == nil {
					t.Fatal("dictionaryMatchPredicate() error = nil")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(predicate, tt.value) {
				t.Fatalf("predicate contains unbound input: %q", predicate)
			}
			if len(builder.args) != 1 || !reflect.DeepEqual(builder.args[0], tt.wantArg) {
				t.Fatalf("args = %#v, want [%q]", builder.args, tt.wantArg)
			}
		})
	}
}

func TestTextMatchIsCaseSensitiveAndEscapesWildcards(t *testing.T) {
	t.Parallel()
	builder := &sqlBuilder{}
	predicate, err := textMatch(builder, "d.value", api.Filter{
		Dimension: "page", Operator: api.Contains, Values: []string{`MiXeD%_Value`},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(predicate, `d.value like $1 escape '\'`) {
		t.Fatalf("predicate = %q, want case-sensitive LIKE", predicate)
	}
	if !reflect.DeepEqual(builder.args, []any{`%MiXeD\%\_Value%`}) {
		t.Fatalf("args = %#v", builder.args)
	}
}

func TestSessionFunnelKeepsCompactSequences(t *testing.T) {
	sql, _ := buildFunnelSQL(1, time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC), []funnelMatcher{
		{page: true, ids: []int32{1}},
		{ids: []int32{2}},
	}, api.FunnelCountBySessions, api.FunnelWindowSession)
	if strings.Contains(sql, "funnel_events") || strings.Contains(sql, "ordered_events") {
		t.Fatalf("session funnel expands event rows:\n%s", sql)
	}
	for _, fragment := range []string{"sequences as (", "unnest(s.seq,s.props)", "e.path_id=any(", "e.name_id=any("} {
		if !strings.Contains(sql, fragment) {
			t.Fatalf("session funnel SQL missing compact matcher shape %q:\n%s", fragment, sql)
		}
	}
}

func TestTimedFunnelFiltersInputAndAvoidsCorrelatedRescans(t *testing.T) {
	sql, _ := buildFunnelSQL(1, time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC), []funnelMatcher{
		{page: true, ids: []int32{1}},
		{ids: []int32{2}},
	}, api.FunnelCountBySessions, api.FunnelWindow1h)
	if strings.Contains(sql, "join lateral (\n    select e.event_order") {
		t.Fatalf("timed funnel rescans ordered_events per prior row:\n%s", sql)
	}
	for _, fragment := range []string{"funnel_events as (", "ordered_events as materialized (", "rows between unbounded preceding and 1 preceding", "u.code>0", "u.code<0"} {
		if !strings.Contains(sql, fragment) {
			t.Fatalf("timed funnel SQL missing filtered linear-pass shape %q:\n%s", fragment, sql)
		}
	}
}

func TestRegionFilterIncludesCountryPredicate(t *testing.T) {
	t.Parallel()
	builder := &sqlBuilder{}
	conditions, err := filterConditions(builder, []api.Filter{{
		Dimension: "region", Operator: api.Is, Values: []string{"Georgia", "US"},
	}}, func(dimension string) (string, *dict.Kind, error) {
		switch dimension {
		case "region":
			return "s.region_id", dictionaryKind(dict.Region), nil
		case "country":
			return "s.country", nil, nil
		default:
			return "", nil, nil
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(conditions) != 1 || !strings.Contains(conditions[0], "s.region_id in") || !strings.Contains(conditions[0], "s.country =") {
		t.Fatalf("conditions = %#v", conditions)
	}
	if !reflect.DeepEqual(builder.args, []any{[]string{"Georgia"}, "US"}) {
		t.Fatalf("args = %#v", builder.args)
	}
}

func TestFunnelPropertyMatchesArePreaggregatedAndJoined(t *testing.T) {
	key, value := "plan", "pro"
	sql, _ := buildFunnelSQL(1, time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC), []funnelMatcher{
		{page: true, ids: []int32{1}},
		{ids: []int32{2}, propKey: key, propValue: value},
	}, api.FunnelCountBySessions, api.FunnelWindowSession)
	if strings.Contains(sql, "select count(*) from custom_closed") || strings.Contains(sql, "session_id<=") {
		t.Fatalf("funnel uses per-session correlated counts:\n%s", sql)
	}
	for _, fragment := range []string{"property_match_2 as materialized", "left join property_match_2", "using(day,visitor_id,session_id)"} {
		if !strings.Contains(sql, fragment) {
			t.Fatalf("funnel SQL missing set-based %q shape:\n%s", fragment, sql)
		}
	}
}
