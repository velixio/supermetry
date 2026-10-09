package query

import (
	"reflect"
	"strings"
	"testing"
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
