// Package apprules compiles and evaluates the per-project ownership and
// grouping rules of an app project. It has no dependencies, so the site
// registry can carry compiled rules to the ingest hot path: evaluating a set
// is pure memory work, never a database read.
//
// Rule values are globs: "*" matches any run of characters (including "/"),
// "?" matches one character, and a backslash makes the next character
// literal. Matching is case-sensitive.
package apprules

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	// MaxRules is the most rules of one kind a project may have.
	MaxRules = 200
	// MaxGlobLen is the longest rule value, in bytes.
	MaxGlobLen = 200
	// MaxFingerprintLen is the longest grouping fingerprint, in bytes.
	MaxFingerprintLen = 64
	maxTagKeyLen      = 64
)

// Frame is the part of a stack frame the rules look at.
type Frame struct {
	Filename string
	Module   string
	InApp    bool
}

// RuleError names the rule (zero-based position in the submitted list) that
// failed validation.
type RuleError struct {
	Index int
	Msg   string
}

func (e *RuleError) Error() string { return fmt.Sprintf("rule %d: %s", e.Index, e.Msg) }

type tokKind uint8

const (
	tokLit tokKind = iota
	tokAny
	tokStar
)

type tok struct {
	kind tokKind
	lit  string // one rune, for tokLit
}

// glob is a compiled pattern.
type glob []tok

func compileGlob(p string) (glob, error) {
	if p == "" {
		return nil, fmt.Errorf("value must not be empty")
	}
	if len(p) > MaxGlobLen {
		return nil, fmt.Errorf("value is longer than %d characters", MaxGlobLen)
	}
	if !utf8.ValidString(p) {
		return nil, fmt.Errorf("value is not valid text")
	}
	var g glob
	for i := 0; i < len(p); {
		r, n := utf8.DecodeRuneInString(p[i:])
		if r < 0x20 || r == 0x7f {
			return nil, fmt.Errorf("value contains a control character")
		}
		i += n
		switch r {
		case '*':
			if len(g) > 0 && g[len(g)-1].kind == tokStar {
				continue
			}
			g = append(g, tok{kind: tokStar})
		case '?':
			g = append(g, tok{kind: tokAny})
		case '\\':
			if i >= len(p) {
				return nil, fmt.Errorf("value ends with a lone backslash")
			}
			_, n2 := utf8.DecodeRuneInString(p[i:])
			g = append(g, tok{kind: tokLit, lit: p[i : i+n2]})
			i += n2
		default:
			g = append(g, tok{kind: tokLit, lit: p[i-n : i]})
		}
	}
	return g, nil
}

// match reports whether s matches the whole pattern, without allocating.
func (g glob) match(s string) bool {
	pi, si := 0, 0
	starP, starS := -1, 0
	for si < len(s) {
		if pi < len(g) {
			t := g[pi]
			switch t.kind {
			case tokStar:
				starP, starS = pi, si
				pi++
				continue
			case tokAny:
				_, n := utf8.DecodeRuneInString(s[si:])
				pi++
				si += n
				continue
			default:
				if strings.HasPrefix(s[si:], t.lit) {
					pi++
					si += len(t.lit)
					continue
				}
			}
		}
		if starP < 0 {
			return false
		}
		_, n := utf8.DecodeRuneInString(s[starS:])
		starS += n
		pi, si = starP+1, starS
	}
	for pi < len(g) && g[pi].kind == tokStar {
		pi++
	}
	return pi == len(g)
}

// target is what a rule's matcher looks at.
type target struct {
	kind   string // path, module, tag, error_type, message
	tagKey string
}

func parseTarget(matcher string, allowed ...string) (target, error) {
	if key, ok := strings.CutPrefix(matcher, "tag:"); ok {
		if !contains(allowed, "tag") {
			return target{}, fmt.Errorf("matcher %q is not allowed here", matcher)
		}
		if key == "" || len(key) > maxTagKeyLen || strings.ContainsAny(key, " \t\r\n\x00") {
			return target{}, fmt.Errorf("matcher %q needs a tag key", matcher)
		}
		return target{kind: "tag", tagKey: key}, nil
	}
	if !contains(allowed, matcher) {
		return target{}, fmt.Errorf("matcher %q is not one of %s", matcher, strings.Join(allowed, ", "))
	}
	return target{kind: matcher}, nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// OwnershipRule routes new issues whose in-app frames or tags match to a user.
type OwnershipRule struct {
	Matcher     string `json:"matcher"` // path, module or tag:<key>
	Value       string `json:"value"`   // glob
	OwnerUserID int64  `json:"owner_user_id"`
}

type ownership struct {
	t     target
	g     glob
	owner int64
}

// OwnershipMatcher is a compiled, ordered rule list. The zero value matches nothing.
type OwnershipMatcher struct{ rules []ownership }

// CompileOwnership validates the rules (in position order) and compiles them.
func CompileOwnership(rules []OwnershipRule) (OwnershipMatcher, error) {
	if len(rules) > MaxRules {
		return OwnershipMatcher{}, &RuleError{Index: MaxRules, Msg: fmt.Sprintf("at most %d ownership rules are allowed", MaxRules)}
	}
	out := make([]ownership, 0, len(rules))
	for i, r := range rules {
		t, err := parseTarget(r.Matcher, "path", "module", "tag")
		if err != nil {
			return OwnershipMatcher{}, &RuleError{Index: i, Msg: err.Error()}
		}
		g, err := compileGlob(r.Value)
		if err != nil {
			return OwnershipMatcher{}, &RuleError{Index: i, Msg: err.Error()}
		}
		if r.OwnerUserID <= 0 {
			return OwnershipMatcher{}, &RuleError{Index: i, Msg: "owner is required"}
		}
		out = append(out, ownership{t: t, g: g, owner: r.OwnerUserID})
	}
	return OwnershipMatcher{rules: out}, nil
}

// Len is the number of compiled rules.
func (m OwnershipMatcher) Len() int { return len(m.rules) }

// Owner returns the owner of the first rule (by position) that matches an
// in-app frame or a tag. Frames that are not in-app are ignored.
func (m OwnershipMatcher) Owner(frames []Frame, tags map[string]string) (int64, bool) {
	for i := range m.rules {
		r := &m.rules[i]
		switch r.t.kind {
		case "tag":
			if v, ok := tags[r.t.tagKey]; ok && r.g.match(v) {
				return r.owner, true
			}
		default:
			for _, f := range frames {
				if !f.InApp {
					continue
				}
				if r.t.kind == "path" && r.g.match(f.Filename) || r.t.kind == "module" && r.g.match(f.Module) {
					return r.owner, true
				}
			}
		}
	}
	return 0, false
}

// GroupingRule replaces the computed grouping key of matching events.
type GroupingRule struct {
	Matcher     string `json:"matcher"`     // error_type, message, path, module or tag:<key>
	Value       string `json:"value"`       // glob
	Fingerprint string `json:"fingerprint"` // 1..64 characters; may contain {{ default }}
}

type grouping struct {
	t           target
	g           glob
	fingerprint string
}

// GroupingMatcher is a compiled, ordered rule list. The zero value matches nothing.
type GroupingMatcher struct{ rules []grouping }

// CompileGrouping validates the rules (in position order) and compiles them.
func CompileGrouping(rules []GroupingRule) (GroupingMatcher, error) {
	if len(rules) > MaxRules {
		return GroupingMatcher{}, &RuleError{Index: MaxRules, Msg: fmt.Sprintf("at most %d grouping rules are allowed", MaxRules)}
	}
	out := make([]grouping, 0, len(rules))
	for i, r := range rules {
		t, err := parseTarget(r.Matcher, "error_type", "message", "path", "module", "tag")
		if err != nil {
			return GroupingMatcher{}, &RuleError{Index: i, Msg: err.Error()}
		}
		g, err := compileGlob(r.Value)
		if err != nil {
			return GroupingMatcher{}, &RuleError{Index: i, Msg: err.Error()}
		}
		if n := len(strings.TrimSpace(r.Fingerprint)); n < 1 || len(r.Fingerprint) > MaxFingerprintLen {
			return GroupingMatcher{}, &RuleError{Index: i, Msg: fmt.Sprintf("fingerprint must be 1 to %d characters", MaxFingerprintLen)}
		}
		if strings.ContainsAny(r.Fingerprint, "\x00\r\n") || !utf8.ValidString(r.Fingerprint) {
			return GroupingMatcher{}, &RuleError{Index: i, Msg: "fingerprint contains an invalid character"}
		}
		out = append(out, grouping{t: t, g: g, fingerprint: r.Fingerprint})
	}
	return GroupingMatcher{rules: out}, nil
}

// Len is the number of compiled rules.
func (m GroupingMatcher) Len() int { return len(m.rules) }

// GroupInput is what grouping rules see of an event. Slices are read, never kept.
type GroupInput struct {
	ErrorTypes []string // exception types, plus the event's error type
	Messages   []string // exception values or the event message (already scrubbed)
	Frames     []Frame
	Tags       map[string]string
}

// Match returns the fingerprint of the first rule that matches. path and
// module rules look at in-app frames, or at every frame when none is in-app.
func (m GroupingMatcher) Match(in GroupInput) (string, bool) {
	if len(m.rules) == 0 {
		return "", false
	}
	anyInApp := false
	for _, f := range in.Frames {
		if f.InApp {
			anyInApp = true
			break
		}
	}
	for i := range m.rules {
		r := &m.rules[i]
		hit := false
		switch r.t.kind {
		case "error_type":
			hit = anyMatch(r.g, in.ErrorTypes)
		case "message":
			hit = anyMatch(r.g, in.Messages)
		case "tag":
			v, ok := in.Tags[r.t.tagKey]
			hit = ok && r.g.match(v)
		default:
			for _, f := range in.Frames {
				if anyInApp && !f.InApp {
					continue
				}
				if r.t.kind == "path" && r.g.match(f.Filename) || r.t.kind == "module" && r.g.match(f.Module) {
					hit = true
					break
				}
			}
		}
		if hit {
			return r.fingerprint, true
		}
	}
	return "", false
}

func anyMatch(g glob, values []string) bool {
	for _, v := range values {
		if g.match(v) {
			return true
		}
	}
	return false
}

// Set is the compiled ownership and grouping rules of one project. A nil *Set
// has no rules.
type Set struct {
	Ownership OwnershipMatcher
	Grouping  GroupingMatcher
}
