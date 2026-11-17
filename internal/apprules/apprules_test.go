package apprules

import (
	"errors"
	"strings"
	"testing"
)

func TestGlob(t *testing.T) {
	for _, c := range []struct {
		pat, s string
		want   bool
	}{
		{"src/*", "src/a/b.go", true},
		{"src/*.go", "src/a/b.go", true},
		{"src/*.go", "src/a/b.rs", false},
		{"*", "", true},
		{"a?c", "abc", true},
		{"a?c", "ac", false},
		{"a?c", "aéc", true},
		{`a\*c`, "a*c", true},
		{`a\*c`, "abc", false},
		{"**/x", "a/b/x", true},
		{"*a*b*", "xxaxxbxx", true},
		{"*a*b*", "xxbxxaxx", false},
		{"exact", "exact", true},
		{"exact", "exact2", false},
	} {
		g, err := compileGlob(c.pat)
		if err != nil {
			t.Fatal(c.pat, err)
		}
		if got := g.match(c.s); got != c.want {
			t.Errorf("%q ~ %q = %v, want %v", c.pat, c.s, got, c.want)
		}
	}
	for _, bad := range []string{"", `a\`, strings.Repeat("a", 201), "a\x00b", "a\nb"} {
		if _, err := compileGlob(bad); err == nil {
			t.Errorf("%q compiled", bad)
		}
	}
}

func TestOwnership(t *testing.T) {
	m, err := CompileOwnership([]OwnershipRule{
		{"path", "vendor/*", 9},
		{"path", "src/billing/*", 2},
		{"module", "app.auth.*", 3},
		{"tag:team", "pay*", 4},
		{"path", "src/*", 5},
	})
	if err != nil {
		t.Fatal(err)
	}
	frame := func(p, mod string, in bool) Frame { return Frame{Filename: p, Module: mod, InApp: in} }
	for _, c := range []struct {
		name   string
		frames []Frame
		tags   map[string]string
		owner  int64
		ok     bool
	}{
		{"path", []Frame{frame("src/billing/x.go", "", true)}, nil, 2, true},
		{"position wins over later broader rule", []Frame{frame("src/billing/x.go", "", true)}, nil, 2, true},
		{"later rule when earlier misses", []Frame{frame("src/other.go", "", true)}, nil, 5, true},
		{"module", []Frame{frame("z.go", "app.auth.login", true)}, nil, 3, true},
		{"tag", nil, map[string]string{"team": "payments"}, 4, true},
		{"not in-app ignored", []Frame{frame("vendor/x.go", "", false)}, nil, 0, false},
		{"no match", []Frame{frame("lib/x.go", "m", true)}, map[string]string{"team": "core"}, 0, false},
	} {
		owner, ok := m.Owner(c.frames, c.tags)
		if owner != c.owner || ok != c.ok {
			t.Errorf("%s: got %d %v, want %d %v", c.name, owner, ok, c.owner, c.ok)
		}
	}
	if owner, ok := (OwnershipMatcher{}).Owner([]Frame{frame("a", "b", true)}, nil); ok || owner != 0 {
		t.Error("zero matcher matched")
	}
}

func TestOwnershipValidation(t *testing.T) {
	for name, rules := range map[string][]OwnershipRule{
		"bad matcher":  {{"path", "a", 1}, {"message", "a", 1}},
		"empty tag":    {{"path", "a", 1}, {"tag:", "a", 1}},
		"bad glob":     {{"path", "a", 1}, {"path", `a\`, 1}},
		"no owner":     {{"path", "a", 0}},
		"unknown kind": {{"frame", "a", 1}},
	} {
		_, err := CompileOwnership(rules)
		var re *RuleError
		if !errors.As(err, &re) {
			t.Errorf("%s: err = %v", name, err)
			continue
		}
		if name != "no owner" && name != "unknown kind" && re.Index != 1 {
			t.Errorf("%s: index = %d, want 1", name, re.Index)
		}
	}
	many := make([]OwnershipRule, MaxRules+1)
	for i := range many {
		many[i] = OwnershipRule{"path", "a", 1}
	}
	if _, err := CompileOwnership(many[:MaxRules]); err != nil {
		t.Fatal(err)
	}
	if _, err := CompileOwnership(many); err == nil {
		t.Error("201 rules accepted")
	}
}

func TestGrouping(t *testing.T) {
	m, err := CompileGrouping([]GroupingRule{
		{"error_type", "Timeout*", "timeouts"},
		{"message", "connection * refused", "conn"},
		{"path", "src/net/*", "net"},
		{"module", "app.db.*", "db"},
		{"tag:tenant", "acme", "tenant-acme"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		in   GroupInput
		want string
		ok   bool
	}{
		{"type", GroupInput{ErrorTypes: []string{"TimeoutError"}}, "timeouts", true},
		{"message", GroupInput{Messages: []string{"connection to x refused"}}, "conn", true},
		{"path in-app", GroupInput{Frames: []Frame{{Filename: "src/net/a.go", InApp: true}}}, "net", true},
		{"path skips non-app when an app frame exists", GroupInput{Frames: []Frame{{Filename: "src/net/a.go"}, {Filename: "x", InApp: true}}}, "", false},
		{"path falls back to all frames", GroupInput{Frames: []Frame{{Filename: "src/net/a.go"}}}, "net", true},
		{"module", GroupInput{Frames: []Frame{{Module: "app.db.q", InApp: true}}}, "db", true},
		{"tag", GroupInput{Tags: map[string]string{"tenant": "acme"}}, "tenant-acme", true},
		{"first rule wins", GroupInput{ErrorTypes: []string{"TimeoutError"}, Tags: map[string]string{"tenant": "acme"}}, "timeouts", true},
		{"none", GroupInput{ErrorTypes: []string{"Other"}}, "", false},
	} {
		got, ok := m.Match(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: got %q %v, want %q %v", c.name, got, ok, c.want, c.ok)
		}
	}
}

func TestGroupingValidation(t *testing.T) {
	for name, r := range map[string]GroupingRule{
		"empty fingerprint": {"message", "a", ""},
		"long fingerprint":  {"message", "a", strings.Repeat("f", 65)},
		"bad matcher":       {"function", "a", "f"},
		"bad glob":          {"message", "", "f"},
	} {
		_, err := CompileGrouping([]GroupingRule{{"message", "ok", "f"}, r})
		var re *RuleError
		if !errors.As(err, &re) || re.Index != 1 {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func BenchmarkOwnership200(b *testing.B) {
	rules := make([]OwnershipRule, 200)
	for i := range rules {
		rules[i] = OwnershipRule{"path", "src/pkg" + string(rune('a'+i%26)) + "/*/file*.go", 1}
	}
	m, _ := CompileOwnership(rules)
	frames := make([]Frame, 10)
	for i := range frames {
		frames[i] = Frame{Filename: "lib/vendor/thing/other.go", Module: "x.y", InApp: true}
	}
	b.ReportAllocs()
	for b.Loop() {
		m.Owner(frames, nil)
	}
}
