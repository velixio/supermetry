package search

import (
	"errors"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

func TestParseGrammar(t *testing.T) {
	cases := []struct {
		in   string
		want []Term
	}{
		{"", nil},
		{"   \t ", nil},
		{"timeout", []Term{{Value: "timeout"}}},
		{`"null pointer"`, []Term{{Value: "null pointer"}}},
		{"!flaky", []Term{{Value: "flaky", Negate: true}}},
		{"is:unresolved", []Term{{Key: "is", Value: "unresolved"}}},
		{"IS:Resolved !is:ignored", []Term{{Key: "is", Value: "resolved"}, {Key: "is", Value: "ignored", Negate: true}}},
		{"is:archived is:snoozed is:assigned is:unassigned", []Term{{Key: "is", Value: "archived"}, {Key: "is", Value: "snoozed"}, {Key: "is", Value: "assigned"}, {Key: "is", Value: "unassigned"}}},
		{"assigned:me assigned:none assigned:Ann@Example.com", []Term{{Key: "assigned", Value: "me"}, {Key: "assigned", Value: "none"}, {Key: "assigned", Value: "ann@example.com"}}},
		{`release:"1.2 beta" environment:prod platform:linux level:error`, []Term{{Key: "release", Value: "1.2 beta"}, {Key: "environment", Value: "prod"}, {Key: "platform", Value: "linux"}, {Key: "level", Value: "error"}}},
		{"kind:crash priority:high", []Term{{Key: "kind", Value: "crash"}, {Key: "priority", Value: "high"}}},
		{"first-seen:-24h last-seen:+30d first-seen:2026-11-01", []Term{{Key: "first-seen", Op: ">=", Value: "-24h"}, {Key: "last-seen", Op: "<=", Value: "+30d"}, {Key: "first-seen", Op: "=", Value: "2026-11-01"}}},
		{"events:>10 events:<=5 users:>=2 users:<3 events:7", []Term{{Key: "events", Op: ">", Value: "10"}, {Key: "events", Op: "<=", Value: "5"}, {Key: "users", Op: ">=", Value: "2"}, {Key: "users", Op: "<", Value: "3"}, {Key: "events", Op: "=", Value: "7"}}},
		{`release:"a\"b\\c"`, []Term{{Key: "release", Value: `a"b\c`}}},
		{"'; drop table app_issues; -- é日本", []Term{{Value: "';"}, {Value: "drop"}, {Value: "table"}, {Value: "app_issues;"}, {Value: "--"}, {Value: "é日本"}}},
	}
	for _, c := range cases {
		q, err := Parse(c.in)
		if err != nil {
			t.Errorf("Parse(%q): %v", c.in, err)
			continue
		}
		if !reflect.DeepEqual(q.Terms, c.want) {
			t.Errorf("Parse(%q) = %+v, want %+v", c.in, q.Terms, c.want)
		}
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		in   string
		pos  int
		frag string
	}{
		{"timeout bogus:1", 8, `unknown search key "bogus"`},
		{"!nope:x", 0, `unknown search key "nope"`},
		{"is:", 3, "needs a value"},
		{"is:weird", 3, "is: must be"},
		{"priority:urgent", 9, "priority:"},
		{"kind:meteor", 5, "kind:"},
		{"assigned:bob", 9, "assigned:"},
		{"events:lots", 7, "events:"},
		{"events:>-1", 7, "events:"},
		{"users:>=", 6, "users:"},
		{"first-seen:yesterday", 11, "first-seen:"},
		{"last-seen:-7", 10, "last-seen:"},
		{"first-seen:2026-13-45", 11, "first-seen:"},
		{`release:"open`, 8, "unterminated quote"},
		{`release:"a"b`, 11, "after the closing quote"},
		{"a\x00b", 1, "control"},
		{"ok \xff", 3, "UTF-8"},
		{"!", 0, "empty"},
		{`""`, 0, "empty"},
		{strings.Repeat("a ", 100) + "x", 200, "longer than 200"},
		{strings.Repeat("é", 201), 200, "longer than 200"},
		{strings.TrimSpace(strings.Repeat("a ", 21)), 40, "more than 20 terms"},
	}
	for _, c := range cases {
		_, err := Parse(c.in)
		var pe *ParseError
		if !errors.As(err, &pe) {
			t.Errorf("Parse(%q): err = %v, want *ParseError", c.in, err)
			continue
		}
		if pe.Pos != c.pos || !strings.Contains(pe.Msg, c.frag) {
			t.Errorf("Parse(%q) = pos %d %q, want pos %d containing %q", c.in, pe.Pos, pe.Msg, c.pos, c.frag)
		}
	}
	if _, err := Parse(strings.TrimSpace(strings.Repeat("a ", 20))); err != nil {
		t.Errorf("20 terms must parse: %v", err)
	}
	if _, err := Parse(strings.Repeat("a", 200)); err != nil {
		t.Errorf("200 characters must parse: %v", err)
	}
}

func lookup(email string) (int64, bool) {
	if email == "ann@example.com" {
		return 7, true
	}
	return 0, false
}

func TestCompile(t *testing.T) {
	cases := []struct {
		in       string
		wantArgs []any
		frag     string
	}{
		{"", nil, "true"},
		{"Boom", []any{"Boom"}, "strpos(lower(i.title), lower($3))"},
		{"!is:resolved", nil, "not (i.status = 'resolved')"},
		{"is:unresolved", nil, "i.status in ('open', 'regressed')"},
		{"is:snoozed", nil, "i.snooze_until is not null"},
		{"assigned:me", []any{int64(5)}, "i.assignee_user_id = $3"},
		{"assigned:none", nil, "i.assignee_user_id is null"},
		{"assigned:ann@example.com", []any{int64(7)}, "i.assignee_user_id = $3"},
		{"assigned:zed@example.com", nil, "false"},
		{"release:1.0", []any{"release", "1.0"}, "t.key = $3 and t.value = $4"},
		{"!level:error", []any{"level", "error"}, "not (exists"},
		{"kind:crash", []any{"crash"}, "i.kind = $3"},
		{"priority:low", []any{"low"}, "i.priority = $3"},
		{"events:>=4", []any{int64(4)}, "a.events >= $3"},
		{"users:<2", []any{int64(2)}, "a.users < $3"},
		{"first-seen:-24h", []any{now.Add(-24 * time.Hour)}, "i.first_seen >= $3"},
		{"last-seen:+30d", []any{now.Add(-30 * 24 * time.Hour)}, "i.last_seen <= $3"},
		{"first-seen:2026-11-01", []any{time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)}, "i.first_seen >= $3 and i.first_seen < $4"},
	}
	for _, c := range cases {
		q, err := Parse(c.in)
		if err != nil {
			t.Fatalf("Parse(%q): %v", c.in, err)
		}
		sql, args, err := Compile(q, now, 5, lookup, 2)
		if err != nil {
			t.Fatalf("Compile(%q): %v", c.in, err)
		}
		if !strings.Contains(sql, c.frag) {
			t.Errorf("Compile(%q) sql %q lacks %q", c.in, sql, c.frag)
		}
		if !reflect.DeepEqual(args, c.wantArgs) {
			t.Errorf("Compile(%q) args = %#v, want %#v", c.in, args, c.wantArgs)
		}
	}
	q, _ := Parse("assigned:me")
	if _, _, err := Compile(q, now, 0, nil, 0); !errors.Is(err, ErrNoUser) {
		t.Errorf("assigned:me without a user: err = %v", err)
	}
}

// onlyFixedSQL holds for every compiled fragment: placeholders and fixed
// vocabulary only, no quotes beyond the status literals, no comments.
var allowedSQL = regexp.MustCompile(`^[a-z0-9_ .,()$<>=']*$`)

func checkSQL(t testing.TB, base int, in string, q Query, sql string, args []any) {
	t.Helper()
	if !allowedSQL.MatchString(sql) {
		t.Fatalf("input %q compiled to SQL with unexpected characters: %q", in, sql)
	}
	for _, s := range []string{";", "--", "/*"} {
		if strings.Contains(sql, s) {
			t.Fatalf("input %q compiled to SQL containing %q: %q", in, s, sql)
		}
	}
	max := 0
	for _, m := range regexp.MustCompile(`\$([0-9]+)`).FindAllStringSubmatch(sql, -1) {
		n := 0
		for _, c := range m[1] {
			n = n*10 + int(c-'0')
		}
		if n > max {
			max = n
		}
	}
	if len(args) > 0 && max != base+len(args) || len(args) == 0 && max != 0 {
		t.Fatalf("input %q: SQL uses %d placeholders but has %d args: %q", in, max, len(args), sql)
	}
	// The SQL must not depend on any user text: replacing every free-text
	// value by a fixed string compiles to the identical SQL.
	canon := Query{}
	for _, term := range q.Terms {
		switch {
		case term.Key == "" || textKeys[term.Key]:
			term.Value = "x"
		case term.Key == "assigned" && strings.Contains(term.Value, "@"):
			term.Value = "x@example.com"
		}
		canon.Terms = append(canon.Terms, term)
	}
	again, _, err := Compile(canon, now, 1, nil, base)
	if err != nil {
		t.Fatalf("input %q: canonical compile: %v", in, err)
	}
	if got, _, _ := Compile(q, now, 1, nil, base); got != again {
		t.Fatalf("input %q: SQL depends on input text:\n%q\n%q", in, got, again)
	}
}

func TestCompileNeverEmbedsInput(t *testing.T) {
	for _, in := range []string{
		`zzzzzzzz' or 1=1 --`, `"zzzzzzzz'); drop table app_issues; --"`, `release:"qqqqqqqq' union select 1"`,
		`!environment:"\"; select pg_sleep(9)"`, `日本語日本語 assigned:mallory@example.com`, `%_%_%_%_ \\\\`,
	} {
		q, err := Parse(in)
		if err != nil {
			t.Fatalf("Parse(%q): %v", in, err)
		}
		sql, args, err := Compile(q, now, 1, lookup, 0)
		if err != nil {
			t.Fatal(err)
		}
		checkSQL(t, 0, in, q, sql, args)
		for _, marker := range []string{"zzzzzzzz", "qqqqqqqq", "pg_sleep", "mallory", "drop table", "日本語"} {
			if strings.Contains(sql, marker) {
				t.Errorf("input %q: SQL text contains %q: %s", in, marker, sql)
			}
		}
	}
}

func FuzzParse(f *testing.F) {
	for _, s := range []string{"", "is:unresolved", `release:"a b" !level:error`, "events:>10", "first-seen:-24h", "'; drop table x; --", "assigned:me", "\x00", `"`, "!", "a:b:c", strings.Repeat("a ", 30)} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		q, err := Parse(in)
		if err != nil {
			var pe *ParseError
			if !errors.As(err, &pe) {
				t.Fatalf("error %v is not a *ParseError", err)
			}
			if pe.Pos < 0 || pe.Pos > len([]rune(in)) {
				t.Fatalf("Parse(%q): position %d outside the input", in, pe.Pos)
			}
			return
		}
		if len(q.Terms) > MaxTerms {
			t.Fatalf("%d terms accepted", len(q.Terms))
		}
		for _, term := range q.Terms {
			if strings.ContainsRune(term.Value, 0) {
				t.Fatalf("NUL accepted in %q", in)
			}
		}
		sql, args, err := Compile(q, now, 1, lookup, 3)
		if err != nil {
			t.Fatalf("Compile(%q): %v", in, err)
		}
		checkSQL(t, 3, in, q, sql, args)
	})
}
