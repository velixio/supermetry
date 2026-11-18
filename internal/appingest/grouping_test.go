package appingest

import (
	"testing"

	"supermetry/internal/apprules"
)

func frame(fn string, inApp bool) Frame {
	return Frame{Module: "m", Function: fn, Filename: "f.go", Lineno: 10, InApp: inApp}
}

func bodyWith(typ, value string, frames ...Frame) Body {
	return Body{Exceptions: []Exception{{Type: typ, Value: value, Frames: frames}}}
}

func key(t *testing.T, b Body, fp []string) string {
	t.Helper()
	got, v := GroupingKey(Event{Kind: "error"}, b, fp)
	if v != GroupingVersion || len(got) != 64 {
		t.Fatalf("version %d, fingerprint %q", v, got)
	}
	return got
}

func TestGroupingKeyInAppFramesOnly(t *testing.T) {
	a := bodyWith("Error", "x", frame("libA", false), frame("handler", true), frame("libB", false))
	b := bodyWith("Error", "other text", frame("libC", false), frame("handler", true))
	if key(t, a, nil) != key(t, b, nil) {
		t.Fatal("events differing only in library frames and message must group together")
	}
	c := bodyWith("Error", "x", frame("libA", false), frame("otherHandler", true))
	if key(t, a, nil) == key(t, c, nil) {
		t.Fatal("a different in-app frame must split the group")
	}
}

func TestGroupingKeyIgnoresLineNumbers(t *testing.T) {
	f1, f2 := frame("handler", true), frame("handler", true)
	f2.Lineno, f2.Colno = 99, 7
	if key(t, bodyWith("E", "", f1), nil) != key(t, bodyWith("E", "", f2), nil) {
		t.Fatal("line numbers must not affect the key")
	}
}

func TestGroupingKeyTypeSplits(t *testing.T) {
	if key(t, bodyWith("A", "", frame("h", true)), nil) == key(t, bodyWith("B", "", frame("h", true)), nil) {
		t.Fatal("exception type must be part of the key")
	}
}

func TestGroupingKeyFallsBackToAllFramesWhenNoneInApp(t *testing.T) {
	a := bodyWith("E", "", frame("x", false))
	b := bodyWith("E", "", frame("y", false))
	if key(t, a, nil) == key(t, b, nil) {
		t.Fatal("with no in-app frames all frames count")
	}
}

func TestGroupingKeySDKFingerprint(t *testing.T) {
	b := bodyWith("E", "", frame("h", true))
	x := key(t, b, []string{"{{ default }}", "tenant-x"})
	y := key(t, b, []string{"{{ default }}", "tenant-y"})
	if x == y || x == key(t, b, nil) {
		t.Fatal("custom parts must split the default grouping")
	}
	if x != key(t, b, []string{"{{default}}", "tenant-x"}) {
		t.Fatal("{{default}} spelling must be equivalent")
	}
	s1 := bodyWith("E", "", frame("a", true))
	s2 := bodyWith("F", "", frame("b", true), frame("c", true))
	if key(t, s1, []string{"db-timeout"}) != key(t, s2, []string{"db-timeout"}) {
		t.Fatal("a fully custom fingerprint ignores the stack")
	}
	if key(t, s1, []string{"db-timeout"}) == key(t, s1, []string{"cache-timeout"}) {
		t.Fatal("different custom fingerprints must differ")
	}
}

func TestGroupingKeyStacklessTemplate(t *testing.T) {
	a := Body{Message: "Order 123 failed for [email]"}
	b := Body{Message: "Order 9876 failed for [email]"}
	if key(t, a, nil) != key(t, b, nil) {
		t.Fatal("digits must not split stackless messages")
	}
	c := Body{Message: "Payment 123 failed for [email]"}
	if key(t, a, nil) == key(t, c, nil) {
		t.Fatal("different wording must split")
	}
	if key(t, Body{Message: "order  [num] failed"}, nil) != key(t, Body{Message: "order [num] failed"}, nil) {
		t.Fatal("whitespace is collapsed")
	}
}

func TestGroupingKeyStacklessExceptionUsesValue(t *testing.T) {
	a := bodyWith("Timeout", "after 30 s")
	b := bodyWith("Timeout", "after 45 s")
	if key(t, a, nil) != key(t, b, nil) {
		t.Fatal("stackless exception values are templated")
	}
}

func TestGroupingKeyKindSplits(t *testing.T) {
	b := Body{Message: "boom"}
	x, _ := GroupingKey(Event{Kind: "crash"}, b, nil)
	y, _ := GroupingKey(Event{Kind: "error"}, b, nil)
	if x == y {
		t.Fatal("kind is part of the key")
	}
}

func TestGroupingKeyEmptyBodyStable(t *testing.T) {
	if key(t, Body{}, nil) != key(t, Body{}, nil) {
		t.Fatal("unstable")
	}
}

func TestGroupingKeyCapsInAppFrames(t *testing.T) {
	var frames []Frame
	for i := 0; i < 50; i++ {
		frames = append(frames, frame("f"+itoa(i), true))
	}
	a := bodyWith("E", "", frames...)
	// the oldest frames (first in the slice) fall outside the cap
	b := bodyWith("E", "", append([]Frame{frame("extra", true)}, frames...)...)
	if key(t, a, nil) != key(t, b, nil) {
		t.Fatal("only the innermost frames count")
	}
}

func TestFingerprintV1Unchanged(t *testing.T) {
	// v1 events keep their fingerprint: the golden value pins Fingerprint.
	got := Fingerprint("error", "TypeError", "a.go:1\nb.go:2", "")
	if got != Fingerprint("error", "TypeError", "a.go:9\nb.go:8", "") {
		t.Fatal("v1 ignores line numbers")
	}
	if GroupingVersionV1 != 1 || GroupingVersion != 2 {
		t.Fatal("versions")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}

func ruleSet(t *testing.T, rules ...apprules.GroupingRule) *apprules.Set {
	t.Helper()
	m, err := apprules.CompileGrouping(rules)
	if err != nil {
		t.Fatal(err)
	}
	return &apprules.Set{Grouping: m}
}

func keyRules(t *testing.T, b Body, fp []string, set *apprules.Set) string {
	t.Helper()
	got, v := GroupingKeyRules(Event{Kind: "error", ErrorType: "Error"}, b, fp, set)
	if v != GroupingVersion || len(got) != 64 {
		t.Fatalf("version %d, fingerprint %q", v, got)
	}
	return got
}

func TestGroupingRuleMergesDifferentStacks(t *testing.T) {
	set := ruleSet(t, apprules.GroupingRule{Matcher: "error_type", Value: "TimeoutError", Fingerprint: "timeouts"})
	a := bodyWith("TimeoutError", "a", frame("fetchUsers", true))
	b := bodyWith("TimeoutError", "b", frame("fetchOrders", true), frame("deeper", true))
	if key(t, a, nil) == key(t, b, nil) {
		t.Fatal("precondition: the default keys differ")
	}
	if keyRules(t, a, nil, set) != keyRules(t, b, nil, set) {
		t.Fatal("a grouping rule must put both stacks into one group")
	}
	other := bodyWith("ValueError", "a", frame("fetchUsers", true))
	if keyRules(t, other, nil, set) != keyRules(t, other, nil, nil) {
		t.Fatal("an event no rule matches keeps its default key")
	}
	if keyRules(t, a, nil, set) == keyRules(t, a, nil, nil) {
		t.Fatal("a matching rule must replace the default key")
	}
	if keyRules(t, a, []string{"sdk-says"}, set) != keyRules(t, b, []string{"other"}, set) {
		t.Fatal("a rule must win over the SDK fingerprint")
	}
}

func TestGroupingRuleDefaultTokenAppends(t *testing.T) {
	set := ruleSet(t, apprules.GroupingRule{Matcher: "tag:tenant", Value: "acme", Fingerprint: "{{ default }}-acme"})
	a := bodyWith("E", "", frame("h1", true))
	b := bodyWith("E", "", frame("h2", true))
	a.Tags, b.Tags = map[string]string{"tenant": "acme"}, map[string]string{"tenant": "acme"}
	if keyRules(t, a, nil, set) == keyRules(t, b, nil, set) {
		t.Fatal("{{ default }} must keep the default split by stack")
	}
	plain := bodyWith("E", "", frame("h1", true))
	if keyRules(t, a, nil, set) == keyRules(t, plain, nil, nil) {
		t.Fatal("the appended text must change the key")
	}
}

func TestGroupingRulePositionOrder(t *testing.T) {
	set := ruleSet(t,
		apprules.GroupingRule{Matcher: "path", Value: "f.go", Fingerprint: "first"},
		apprules.GroupingRule{Matcher: "error_type", Value: "*", Fingerprint: "second"})
	b := bodyWith("E", "", frame("h", true))
	want := keyRules(t, b, nil, ruleSet(t, apprules.GroupingRule{Matcher: "path", Value: "f.go", Fingerprint: "first"}))
	if keyRules(t, b, nil, set) != want {
		t.Fatal("the earlier rule must win")
	}
}
