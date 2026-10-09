package query

import (
	"fmt"
	"strings"
)

// dictionaryMatchPredicate builds the value predicate used by analytics
// definitions. Wildcards are anchored: only '*' expands to any characters.
func dictionaryMatchPredicate(builder *sqlBuilder, match, value string) (string, error) {
	switch match {
	case "exact":
		return "value=" + builder.arg(value), nil
	case "prefix":
		return "value like " + builder.arg(EscapeLike(value)+"%") + ` escape '\'`, nil
	case "wildcard":
		parts := strings.Split(value, "*")
		for i := range parts {
			parts[i] = EscapeLike(parts[i])
		}
		return "value like " + builder.arg(strings.Join(parts, "%")) + ` escape '\'`, nil
	default:
		return "", fmt.Errorf("unknown match %q", match)
	}
}
