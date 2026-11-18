// Package search parses the app issue search syntax and compiles it to
// parameterised SQL. User text only ever reaches the database as a bound
// argument: the SQL text is assembled from fixed fragments.
//
// Grammar: space-separated terms. A term is key:value, key:"quoted value",
// !key:value (negation) or a bare word (matches the title or culprit,
// case-insensitive substring; a bare "quoted phrase" works too).
package search

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Limits of one search.
const (
	MaxTerms = 20
	MaxChars = 200
)

// ParseError is a syntax error. Pos is the character (rune) offset of the
// problem in the input.
type ParseError struct {
	Pos int
	Msg string
}

func (e *ParseError) Error() string { return fmt.Sprintf("%s (at position %d)", e.Msg, e.Pos) }

// Query is a parsed search: every term must match.
type Query struct{ Terms []Term }

// Term is one condition. Key is empty for a bare word. Op is "" (equals or
// contains), one of > < >= <= = for the number and date keys. A relative date
// has Op >= (-24h, within the last day) or <= (+30d, older than 30 days) and
// an absolute date has Op = and means that UTC day.
type Term struct {
	Key, Op, Value string
	Negate         bool
}

// Keys is the set of keys the grammar knows.
var Keys = []string{"is", "assigned", "release", "environment", "platform", "process", "level", "kind", "priority", "first-seen", "last-seen", "events", "users"}

var (
	isValues       = map[string]bool{"unresolved": true, "resolved": true, "ignored": true, "archived": true, "snoozed": true, "assigned": true, "unassigned": true}
	priorityValues = map[string]bool{"high": true, "medium": true, "low": true}
	kindValues     = map[string]bool{"crash": true, "oom": true, "watchdog": true, "error": true, "hang": true, "install_failed": true, "install_stalled": true, "incompatibility": true, "operation_failed": true}
	textKeys       = map[string]bool{"release": true, "environment": true, "platform": true, "process": true, "level": true}
)

// Parse parses input. Every failure is a *ParseError.
func Parse(input string) (Query, error) {
	if !utf8.ValidString(input) {
		i := 0
		for p, r := range input {
			if r == utf8.RuneError {
				if _, size := utf8.DecodeRuneInString(input[p:]); size == 1 {
					break
				}
			}
			i++
		}
		return Query{}, &ParseError{Pos: i, Msg: "search must be valid UTF-8 text"}
	}
	rs := []rune(input)
	if len(rs) > MaxChars {
		return Query{}, &ParseError{Pos: MaxChars, Msg: fmt.Sprintf("search is longer than %d characters", MaxChars)}
	}
	var q Query
	p := &parser{rs: rs}
	for {
		p.skipSpace()
		if p.i >= len(rs) {
			return q, nil
		}
		if len(q.Terms) == MaxTerms {
			return Query{}, &ParseError{Pos: p.i, Msg: fmt.Sprintf("search has more than %d terms", MaxTerms)}
		}
		t, err := p.term()
		if err != nil {
			return Query{}, err
		}
		q.Terms = append(q.Terms, t)
	}
}

type parser struct {
	rs []rune
	i  int
}

func (p *parser) skipSpace() {
	for p.i < len(p.rs) && unicode.IsSpace(p.rs[p.i]) {
		p.i++
	}
}

func (p *parser) errAt(pos int, format string, args ...any) error {
	return &ParseError{Pos: pos, Msg: fmt.Sprintf(format, args...)}
}

// term parses one term starting at p.i (not a space).
func (p *parser) term() (Term, error) {
	start := p.i
	var t Term
	if p.rs[p.i] == '!' {
		t.Negate = true
		p.i++
	}
	// A key is letters and dashes followed by a colon.
	j := p.i
	for j < len(p.rs) && (p.rs[j] == '-' || (p.rs[j] < 128 && unicode.IsLetter(p.rs[j]))) {
		j++
	}
	if j > p.i && j < len(p.rs) && p.rs[j] == ':' {
		t.Key = strings.ToLower(string(p.rs[p.i:j]))
		p.i = j + 1
		return p.keyed(t, start)
	}
	// A bare word or phrase.
	val, err := p.value()
	if err != nil {
		return t, err
	}
	if val == "" {
		return t, p.errAt(start, "empty search term")
	}
	t.Value = val
	return t, nil
}

// value reads a quoted or unquoted value at p.i.
func (p *parser) value() (string, error) {
	if p.i < len(p.rs) && p.rs[p.i] == '"' {
		open := p.i
		p.i++
		var b strings.Builder
		for p.i < len(p.rs) {
			c := p.rs[p.i]
			switch {
			case c == '\\' && p.i+1 < len(p.rs) && (p.rs[p.i+1] == '"' || p.rs[p.i+1] == '\\'):
				b.WriteRune(p.rs[p.i+1])
				p.i += 2
			case c == '"':
				p.i++
				if p.i < len(p.rs) && !unicode.IsSpace(p.rs[p.i]) {
					return "", p.errAt(p.i, "expected a space after the closing quote")
				}
				return b.String(), nil
			case unicode.IsControl(c):
				return "", p.errAt(p.i, "control characters are not allowed")
			default:
				b.WriteRune(c)
				p.i++
			}
		}
		return "", p.errAt(open, "unterminated quote")
	}
	from := p.i
	for p.i < len(p.rs) && !unicode.IsSpace(p.rs[p.i]) {
		if unicode.IsControl(p.rs[p.i]) {
			return "", p.errAt(p.i, "control characters are not allowed")
		}
		p.i++
	}
	return string(p.rs[from:p.i]), nil
}

func (p *parser) keyed(t Term, start int) (Term, error) {
	valPos := p.i
	if !knownKey(t.Key) {
		return t, p.errAt(start, "unknown search key %q; keys are %s", t.Key, strings.Join(Keys, ", "))
	}
	val, err := p.value()
	if err != nil {
		return t, err
	}
	if val == "" {
		return t, p.errAt(valPos, "%s: needs a value", t.Key)
	}
	switch t.Key {
	case "is":
		if !isValues[strings.ToLower(val)] {
			return t, p.errAt(valPos, "is: must be unresolved, resolved, ignored, archived, snoozed, assigned or unassigned")
		}
		t.Value = strings.ToLower(val)
	case "priority":
		if !priorityValues[strings.ToLower(val)] {
			return t, p.errAt(valPos, "priority: must be high, medium or low")
		}
		t.Value = strings.ToLower(val)
	case "kind":
		if !kindValues[strings.ToLower(val)] {
			return t, p.errAt(valPos, "kind: is not a known issue kind")
		}
		t.Value = strings.ToLower(val)
	case "assigned":
		switch lv := strings.ToLower(val); {
		case lv == "me" || lv == "none":
			t.Value = lv
		case strings.Count(val, "@") == 1 && len(val) <= 254 && !strings.ContainsAny(val, " \t"):
			t.Value = strings.ToLower(val)
		default:
			return t, p.errAt(valPos, "assigned: must be me, none or a member email")
		}
	case "events", "users":
		op, num := splitOp(val)
		n, err := strconv.ParseInt(num, 10, 64)
		if err != nil || n < 0 || len(num) > 15 {
			return t, p.errAt(valPos, "%s: needs a number such as >10, <=5 or 3", t.Key)
		}
		if op == "" {
			op = "="
		}
		t.Op, t.Value = op, strconv.FormatInt(n, 10)
	case "first-seen", "last-seen":
		op, v, ok := parseWhen(val)
		if !ok {
			return t, p.errAt(valPos, "%s: needs -24h, -7d, +30d or a date such as 2026-11-01", t.Key)
		}
		t.Op, t.Value = op, v
	default: // text keys
		if len([]rune(val)) > MaxChars {
			return t, p.errAt(valPos, "%s: value is too long", t.Key)
		}
		t.Value = val
	}
	return t, nil
}

func knownKey(k string) bool {
	for _, known := range Keys {
		if k == known {
			return true
		}
	}
	return false
}

// splitOp splits a leading comparison operator from a number.
func splitOp(s string) (op, rest string) {
	for _, o := range []string{">=", "<=", ">", "<"} {
		if strings.HasPrefix(s, o) {
			return o, s[len(o):]
		}
	}
	return "", s
}

// parseWhen understands -24h, -7d, +30d (units m, h, d, w) and YYYY-MM-DD.
func parseWhen(s string) (op, value string, ok bool) {
	if len(s) >= 3 && (s[0] == '-' || s[0] == '+') {
		digits := s[1 : len(s)-1]
		if len(digits) == 0 || len(digits) > 5 || strings.Trim(digits, "0123456789") != "" || strings.IndexByte("mhdw", s[len(s)-1]) < 0 {
			return "", "", false
		}
		if s[0] == '-' {
			return ">=", s, true
		}
		return "<=", s, true
	}
	if len(s) == 10 {
		if _, err := parseDay(s); err == nil {
			return "=", s, true
		}
	}
	return "", "", false
}
