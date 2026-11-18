package search

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// Compile turns q into a WHERE fragment over the alias l, ANDed, with
// placeholders numbered from argOffset+1. User input only ever travels in args;
// the fragment holds fixed SQL and integer severity bounds. An empty query
// compiles to "true".
func Compile(q Query, argOffset int) (where string, args []any, err error) {
	if len(q.Terms) == 0 {
		return "true", nil, nil
	}
	parts := make([]string, 0, len(q.Terms))
	n := argOffset
	ph := func(v any) string {
		n++
		args = append(args, v)
		return fmt.Sprintf("$%d", n)
	}
	for _, t := range q.Terms {
		var expr string
		switch t.Kind {
		case KindWord:
			expr = "l.body ILIKE " + ph("%"+likeEscaper.Replace(t.Value)+"%")
		case KindSeverity:
			expr = severityExpr(t)
		case KindService:
			expr = "l.service = " + ph(t.Value)
		case KindEnv:
			expr = "l.environment = " + ph(t.Value)
		case KindRelease:
			expr = "l.release = " + ph(t.Value)
		case KindTrace, KindSpan:
			raw, derr := hex.DecodeString(t.Value)
			if derr != nil {
				return "", nil, derr
			}
			col := "l.trace_id"
			if t.Kind == KindSpan {
				col = "l.span_id"
			}
			expr = col + " = " + ph(raw)
		case KindAttr:
			doc, merr := json.Marshal(map[string]string{t.Key: t.Value})
			if merr != nil {
				return "", nil, merr
			}
			expr = "l.attributes @> " + ph(string(doc)) + "::jsonb"
		default:
			return "", nil, fmt.Errorf("unknown term kind %q", t.Kind)
		}
		if t.Neg {
			expr = "NOT COALESCE(" + expr + ", false)"
		}
		parts = append(parts, "("+expr+")")
	}
	return strings.Join(parts, " AND "), args, nil
}

// severityExpr uses fixed integer bounds: each level owns a band of four OTLP
// severity numbers (error = 17 to 20).
func severityExpr(t Term) string {
	base := SeverityBase[t.Value]
	switch t.Op {
	case ">=":
		return fmt.Sprintf("l.severity >= %d", base)
	case ">":
		return fmt.Sprintf("l.severity >= %d", base+4)
	case "<=":
		return fmt.Sprintf("l.severity < %d", base+4)
	case "<":
		return fmt.Sprintf("l.severity < %d", base)
	}
	return fmt.Sprintf("l.severity >= %d AND l.severity < %d", base, base+4)
}
