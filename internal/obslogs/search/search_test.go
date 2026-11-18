package search

import (
	"errors"
	"strings"
	"testing"
)

func TestParseGrammar(t *testing.T) {
	cases := []struct {
		in   string
		want []Term
	}{
		{"payment", []Term{{Kind: KindWord, Value: "payment"}}},
		{`"payment failed"`, []Term{{Kind: KindWord, Value: "payment failed"}}},
		{"severity:error", []Term{{Kind: KindSeverity, Op: "=", Value: "error"}}},
		{"severity:>=warn", []Term{{Kind: KindSeverity, Op: ">=", Value: "warn"}}},
		{"severity:<info", []Term{{Kind: KindSeverity, Op: "<", Value: "info"}}},
		{"!service:api", []Term{{Neg: true, Kind: KindService, Value: "api"}}},
		{"env:prod release:1.4.2", []Term{{Kind: KindEnv, Value: "prod"}, {Kind: KindRelease, Value: "1.4.2"}}},
		{"trace:5B8EFFF798038103D269B633813FC60C", []Term{{Kind: KindTrace, Value: "5b8efff798038103d269b633813fc60c"}}},
		{"span:00f067aa0ba902b7", []Term{{Kind: KindSpan, Value: "00f067aa0ba902b7"}}},
		{"attr.http.route:/pay", []Term{{Kind: KindAttr, Key: "http.route", Value: "/pay"}}},
		{`attr.msg:"a b"`, []Term{{Kind: KindAttr, Key: "msg", Value: "a b"}}},
		{"", nil},
	}
	for _, c := range cases {
		q, err := Parse(c.in)
		if err != nil {
			t.Fatalf("%q: %v", c.in, err)
		}
		if len(q.Terms) != len(c.want) {
			t.Fatalf("%q: %d terms, want %d", c.in, len(q.Terms), len(c.want))
		}
		for i, w := range c.want {
			got := q.Terms[i]
			got.Pos = 0
			if got != w {
				t.Errorf("%q term %d = %+v, want %+v", c.in, i, got, w)
			}
		}
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		in, msg string
		pos     int
	}{
		{"ab", "search words need at least 3 characters", 0},
		{`payment "ab"`, "search words need at least 3 characters", 8},
		{"foo:bar", "unknown search key foo", 0},
		{"payment foo:bar", "unknown search key foo", 8},
		{"severity:loud", "severity must be one of", 9},
		{"trace:abc", "trace must be 32 hex characters", 6},
		{"span:zz", "span must be 16 hex characters", 5},
		{"service:", "missing value for service", 0},
		{`"open`, "unterminated quote", 0},
		{"! ", "! must be followed by a term", 0},
		{strings.Repeat("abc ", 13), "too many search terms", 48},
		{strings.Repeat("a", 301), "longer than 300", 300},
	}
	for _, c := range cases {
		_, err := Parse(c.in)
		var pe *ParseError
		if !errors.As(err, &pe) {
			t.Fatalf("%q: err = %v", c.in, err)
		}
		if !strings.Contains(pe.Msg, c.msg) || pe.Pos != c.pos {
			t.Errorf("%q: got %q at %d, want %q at %d", c.in, pe.Msg, pe.Pos, c.msg, c.pos)
		}
	}
	if _, err := Parse(strings.Repeat("abc ", 12)); err != nil {
		t.Fatalf("12 terms: %v", err)
	}
}

func TestCompile(t *testing.T) {
	cases := []struct {
		in    string
		where string
		args  []any
	}{
		{"", "true", nil},
		{`"payment failed" severity:>=error`, "(l.body ILIKE $3) AND (l.severity >= 17)", []any{"%payment failed%"}},
		{"severity:>=warn", "(l.severity >= 13)", nil},
		{"severity:>warn", "(l.severity >= 17)", nil},
		{"severity:<=warn", "(l.severity < 17)", nil},
		{"severity:error", "(l.severity >= 17 AND l.severity < 21)", nil},
		{"!service:api", "(NOT COALESCE(l.service = $3, false))", []any{"api"}},
		{"env:prod release:1.0", "(l.environment = $3) AND (l.release = $4)", []any{"prod", "1.0"}},
		{"100%_done", `(l.body ILIKE $3)`, []any{`%100\%\_done%`}},
		{`back\slash`, `(l.body ILIKE $3)`, []any{`%back\\slash%`}},
		{"attr.http.route:/pay", "(l.attributes @> $3::jsonb)", []any{`{"http.route":"/pay"}`}},
	}
	for _, c := range cases {
		q, err := Parse(c.in)
		if err != nil {
			t.Fatalf("%q: %v", c.in, err)
		}
		where, args, err := Compile(q, 2)
		if err != nil {
			t.Fatal(err)
		}
		if where != c.where || len(args) != len(c.args) {
			t.Errorf("%q: %q %v, want %q %v", c.in, where, args, c.where, c.args)
			continue
		}
		for i := range args {
			if args[i] != c.args[i] {
				t.Errorf("%q arg %d = %#v, want %#v", c.in, i, args[i], c.args[i])
			}
		}
	}
	q, _ := Parse("trace:5b8efff798038103d269b633813fc60c")
	_, args, _ := Compile(q, 0)
	if b, ok := args[0].([]byte); !ok || len(b) != 16 || b[0] != 0x5b {
		t.Fatalf("trace arg = %#v", args[0])
	}
}

// Compiled SQL never contains any part of the user's input.
func TestCompileHoldsNoInput(t *testing.T) {
	in := `drop'table logs;-- ` + `"x'); select 1" service:o'reilly attr.k:v'`
	q, err := Parse(in)
	if err != nil {
		t.Fatal(err)
	}
	where, args, _ := Compile(q, 0)
	if strings.ContainsAny(where, `'";`) || strings.Contains(where, "drop") || strings.Contains(where, "reilly") || len(args) != 5 {
		t.Fatalf("where = %q args = %v", where, args)
	}
}

func FuzzParse(f *testing.F) {
	for _, s := range []string{"", "payment", `"a b c"`, "severity:>=warn !service:api", "attr.a.b:c trace:00", `"`, "!", "a:b:c", "\xff\xfe"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		q, err := Parse(in)
		if err != nil {
			return
		}
		where, args, err := Compile(q, 0)
		if err != nil {
			t.Fatalf("compile of a parsed query failed: %v", err)
		}
		if strings.Count(where, "$") != len(args) {
			t.Fatalf("placeholder count %d != args %d in %q", strings.Count(where, "$"), len(args), where)
		}
	})
}
