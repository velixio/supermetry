// Package search parses the log search grammar and compiles it to a SQL
// fragment made only of placeholders.
//
// Grammar: space-separated terms (at most 12, 300 characters in all). A bare
// word or "quoted phrase" is a case-insensitive substring of the body (at least
// 3 characters); severity:, service:, env:, release:, trace:, span: and
// attr.<key>: filter fields; a leading ! negates any term.
package search

import (
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Limits of the grammar.
const (
	MaxTerms   = 12
	MaxInput   = 300
	MinWord    = 3
	maxValue   = 256
	maxAttrKey = 128
)

const errShortMsg = "search words need at least 3 characters"

// Term kinds.
const (
	KindWord     = "word"
	KindSeverity = "severity"
	KindService  = "service"
	KindEnv      = "env"
	KindRelease  = "release"
	KindTrace    = "trace"
	KindSpan     = "span"
	KindAttr     = "attr"
)

// Term is one parsed search term.
type Term struct {
	Neg   bool
	Kind  string
	Op    string // severity only: "=", ">=", ">", "<=", "<"
	Key   string // attr only
	Value string // severity: the level name; trace and span: lower-case hex
	Pos   int    // byte offset in the input
}

// Query is a parsed search.
type Query struct{ Terms []Term }

// ParseError is a grammar error with the byte position it was found at.
type ParseError struct {
	Pos int
	Msg string
}

func (e *ParseError) Error() string { return fmt.Sprintf("%s (at position %d)", e.Msg, e.Pos) }

// SeverityBase maps a level name to the first OTLP severity number of its band.
var SeverityBase = map[string]int{"trace": 1, "debug": 5, "info": 9, "warn": 13, "error": 17, "fatal": 21}

// Parse parses input. An empty input is an empty query.
func Parse(input string) (Query, error) {
	if len(input) > MaxInput {
		return Query{}, &ParseError{Pos: MaxInput, Msg: "search is longer than 300 characters"}
	}
	var q Query
	i := 0
	for {
		for i < len(input) && (input[i] == ' ' || input[i] == '\t') {
			i++
		}
		if i >= len(input) {
			return q, nil
		}
		start := i
		if len(q.Terms) == MaxTerms {
			return Query{}, &ParseError{Pos: start, Msg: "too many search terms (at most 12)"}
		}
		t, next, err := term(input, i)
		if err != nil {
			return Query{}, err
		}
		t.Pos = start
		q.Terms = append(q.Terms, t)
		i = next
	}
}

func term(in string, i int) (Term, int, error) {
	start := i
	var t Term
	if in[i] == '!' {
		t.Neg = true
		i++
		if i >= len(in) || in[i] == ' ' || in[i] == '\t' {
			return t, 0, &ParseError{Pos: start, Msg: "! must be followed by a term"}
		}
	}
	if in[i] == '"' {
		v, next, err := quoted(in, i)
		if err != nil {
			return t, 0, err
		}
		return word(t, v, i, next)
	}
	// A bare token runs to the next space; a key is the part before the first
	// colon when it looks like one.
	end := i
	for end < len(in) && in[end] != ' ' && in[end] != '\t' {
		end++
	}
	tok := in[i:end]
	colon := strings.IndexByte(tok, ':')
	if colon < 0 || !keyShaped(tok[:colon]) {
		if strings.ContainsRune(tok, '"') {
			return t, 0, &ParseError{Pos: i, Msg: "unexpected quote in search word"}
		}
		return word(t, tok, i, end)
	}
	key := tok[:colon]
	valStart := i + colon + 1
	var val string
	next := end
	if valStart < len(in) && in[valStart] == '"' {
		v, n, err := quoted(in, valStart)
		if err != nil {
			return t, 0, err
		}
		val, next = v, n
	} else {
		val = in[valStart:end]
	}
	if val == "" {
		return t, 0, &ParseError{Pos: i, Msg: "missing value for " + key}
	}
	if len(val) > maxValue {
		return t, 0, &ParseError{Pos: valStart, Msg: "value is longer than 256 bytes"}
	}
	if !utf8.ValidString(val) {
		return t, 0, &ParseError{Pos: valStart, Msg: "value is not valid UTF-8"}
	}
	switch {
	case key == "severity":
		op, level := "=", val
		for _, o := range []string{">=", "<=", ">", "<"} {
			if strings.HasPrefix(val, o) {
				op, level = o, strings.TrimPrefix(val, o)
				break
			}
		}
		level = strings.ToLower(level)
		if _, ok := SeverityBase[level]; !ok {
			return t, 0, &ParseError{Pos: valStart, Msg: "severity must be one of trace, debug, info, warn, error, fatal"}
		}
		t.Kind, t.Op, t.Value = KindSeverity, op, level
	case key == "service":
		t.Kind, t.Value = KindService, val
	case key == "env":
		t.Kind, t.Value = KindEnv, val
	case key == "release":
		t.Kind, t.Value = KindRelease, val
	case key == "trace" || key == "span":
		want := 32
		if key == "span" {
			want = 16
		}
		v := strings.ToLower(val)
		if _, err := hex.DecodeString(v); err != nil || len(v) != want {
			return t, 0, &ParseError{Pos: valStart, Msg: fmt.Sprintf("%s must be %d hex characters", key, want)}
		}
		t.Kind, t.Value = key, v
	case strings.HasPrefix(key, "attr."):
		k := strings.TrimPrefix(key, "attr.")
		if k == "" || len(k) > maxAttrKey {
			return t, 0, &ParseError{Pos: i, Msg: "attr key must be 1 to 128 bytes"}
		}
		t.Kind, t.Key, t.Value = KindAttr, k, val
	default:
		return t, 0, &ParseError{Pos: i, Msg: "unknown search key " + key}
	}
	return t, next, nil
}

func word(t Term, v string, from, next int) (Term, int, error) {
	if utf8.RuneCountInString(v) < MinWord {
		return t, 0, &ParseError{Pos: from, Msg: errShortMsg}
	}
	if len(v) > maxValue {
		return t, 0, &ParseError{Pos: from, Msg: "search word is longer than 256 bytes"}
	}
	if !utf8.ValidString(v) {
		return t, 0, &ParseError{Pos: from, Msg: "search word is not valid UTF-8"}
	}
	t.Kind, t.Value = KindWord, v
	return t, next, nil
}

// quoted reads a "..." string starting at in[i] (a quote); \" and \\ escape.
func quoted(in string, i int) (string, int, error) {
	var b strings.Builder
	for j := i + 1; j < len(in); j++ {
		switch c := in[j]; {
		case c == '\\' && j+1 < len(in) && (in[j+1] == '"' || in[j+1] == '\\'):
			b.WriteByte(in[j+1])
			j++
		case c == '"':
			return b.String(), j + 1, nil
		default:
			b.WriteByte(c)
		}
	}
	return "", 0, &ParseError{Pos: i, Msg: "unterminated quote"}
}

// keyShaped reports whether s can be a search key: a letter or underscore
// first, then letters, digits, dots, dashes, underscores and slashes.
func keyShaped(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		letter := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_'
		if i == 0 && !letter {
			return false
		}
		if !letter && !(c >= '0' && c <= '9' || c == '.' || c == '-' || c == '/') {
			return false
		}
	}
	return true
}
