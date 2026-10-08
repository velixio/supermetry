package query

import (
	"strconv"
	"strings"

	"panel/internal/dict"
	"panel/internal/enrich"
)

// channelSQL maps dict ids to a channel label. It embeds only constants from
// the channel rule table, never request input.
func channelSQL(sourceColumn, mediumColumn string) string {
	var builder strings.Builder
	builder.WriteString("(case")
	for _, rule := range enrich.ChannelRules() {
		conditions := make([]string, 0, 2)
		for _, condition := range []string{
			channelSetSQL(sourceColumn, dict.Source, rule.Sources),
			channelSetSQL(mediumColumn, dict.Medium, rule.Mediums),
		} {
			if condition != "" {
				conditions = append(conditions, condition)
			}
		}
		builder.WriteString(" when " + strings.Join(conditions, " and ") + " then " + sqlLiteral(rule.Channel))
	}
	builder.WriteString(" else " + sqlLiteral(enrich.ChannelOther) + " end)")
	return builder.String()
}

func channelSetSQL(column string, kind dict.Kind, values []string) string {
	if values == nil {
		return ""
	}
	var parts, named []string
	for _, value := range values {
		if value == "" {
			parts = append(parts, column+"=0")
		} else {
			named = append(named, sqlLiteral(value))
		}
	}
	if len(named) > 0 {
		parts = append(parts, column+" in (select cd.id from dict cd where cd.kind="+
			strconv.Itoa(int(kind))+" and cd.value in ("+strings.Join(named, ",")+"))")
	}
	return "(" + strings.Join(parts, " or ") + ")"
}

func sqlLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
