package proguard

import (
	"bytes"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

const fixtureMapping = "../sentryingest/testdata/android/mapping.txt"

func buildFrom(t testing.TB, mapping string) *Index {
	t.Helper()
	var out bytes.Buffer
	if err := BuildIndexIn(strings.NewReader(mapping), &out, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	ix, err := OpenIndex(bytes.NewReader(out.Bytes()), int64(out.Len()))
	if err != nil {
		t.Fatal(err)
	}
	return ix
}

func fixtureIndex(t testing.TB) *Index {
	t.Helper()
	b, err := os.ReadFile(fixtureMapping)
	if err != nil {
		t.Fatal(err)
	}
	return buildFrom(t, string(b))
}

func TestRemapInlineGroup(t *testing.T) {
	ix := fixtureIndex(t)
	got := ix.Remap("a.b.c.e", "a", 17)
	want := []Frame{
		{"com.example.fixture.CheckoutActivity", "checkAmount", "CheckoutActivity.kt", 90}, // 88 + (17-15)
		{"com.example.fixture.CheckoutActivity", "validate", "CheckoutActivity.kt", 42},    // outer call site
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
}

func TestRemapSingleAndBoundaries(t *testing.T) {
	ix := fixtureIndex(t)
	if got := ix.Remap("a.b.c.e", "b", 20); !reflect.DeepEqual(got, []Frame{{"com.example.fixture.CheckoutActivity", "onPay", "CheckoutActivity.kt", 30}}) {
		t.Fatalf("b:20 %+v", got)
	}
	if got := ix.Remap("a.b.c.e", "a", 15); got[0].Line != 88 {
		t.Fatalf("range start %+v", got)
	}
	if got := ix.Remap("a.b.c.e", "a", 18); got[0].Line != 91 {
		t.Fatalf("range end %+v", got)
	}
	// Default file name without sourceFile metadata.
	if got := ix.Remap("a.b.c.d", "run", 2); !reflect.DeepEqual(got, []Frame{{"com.example.fixture.Router", "run", "Router.java", 11}}) {
		t.Fatalf("router %+v", got)
	}
}

func TestRemapFallbacks(t *testing.T) {
	ix := fixtureIndex(t)
	if got := ix.Remap("x.y.Z", "a", 1); got != nil {
		t.Fatalf("unknown class %+v", got)
	}
	// Outside every range: the two entries disagree, so the frame stays obfuscated.
	if got := ix.Remap("a.b.c.e", "a", 99); got != nil {
		t.Fatalf("ambiguous %+v", got)
	}
	// Outside the range but only one candidate method: name only, no line.
	if got := ix.Remap("a.b.c.e", "b", 99); !reflect.DeepEqual(got, []Frame{{"com.example.fixture.CheckoutActivity", "onPay", "CheckoutActivity.kt", 0}}) {
		t.Fatalf("name only %+v", got)
	}
	// Method without line info.
	if got := ix.Remap("a.b.c.e", "c", 0); got[0].Method != "reset" || got[0].Line != 0 {
		t.Fatalf("reset %+v", got)
	}
	// Two methods share an obfuscated name and no line: ambiguous.
	if got := ix.Remap("a.b.c.e", "d", 5); got != nil {
		t.Fatalf("overloads %+v", got)
	}
	// Known class, method not in the mapping: class is still readable.
	if got := ix.Remap("a.b.c.f", "zz", 7); !reflect.DeepEqual(got, []Frame{{"com.example.fixture.PaymentException", "zz", "PaymentException.java", 7}}) {
		t.Fatalf("class only %+v", got)
	}
}

func TestClass(t *testing.T) {
	ix := fixtureIndex(t)
	if o, ok := ix.Class("a.b.c.f"); !ok || o != "com.example.fixture.PaymentException" {
		t.Fatalf("%q %v", o, ok)
	}
	if _, ok := ix.Class("nope"); ok {
		t.Fatal("found a missing class")
	}
}

func TestInlinedFromOtherClass(t *testing.T) {
	ix := buildFrom(t, "com.app.A -> a.a:\n    5:5:int com.app.util.Helper.twice(int):12:12 -> f\n    5:5:void run():40:40 -> f\n")
	got := ix.Remap("a.a", "f", 5)
	want := []Frame{{"com.app.util.Helper", "twice", "Helper.java", 12}, {"com.app.A", "run", "A.java", 40}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%+v", got)
	}
}

func TestParseLines(t *testing.T) {
	m, ok := methodLine("1:2:void <init>(int,java.lang.String):7:9 -> <init>")
	if !ok || m.obfStart != 1 || m.obfEnd != 2 || m.origStart != 7 || m.origEnd != 9 || m.origName != "<init>" || m.obfName != "<init>" {
		t.Fatalf("%+v %v", m, ok)
	}
	if _, ok := methodLine("int field -> a"); ok {
		t.Fatal("a field is not a method")
	}
	if _, ok := methodLine("garbage"); ok {
		t.Fatal("garbage parsed")
	}
	if f, ok := sourceFile(`# {"id":"sourceFile","fileName":"X.kt"}`); !ok || f != "X.kt" {
		t.Fatalf("%q", f)
	}
	if _, ok := sourceFile(`# {"id":"com.android.tools.r8.synthesized"}`); ok {
		t.Fatal("other metadata taken for a source file")
	}
	if _, _, ok := classLine("a.b -> c.d:"); !ok {
		t.Fatal("class line")
	}
	if _, _, ok := classLine("a.b -> c.d"); ok {
		t.Fatal("class line needs a colon")
	}
}

func TestBuildIndexSpillsAndMerges(t *testing.T) {
	// A tiny run size forces several spilled runs through the merge.
	var sb strings.Builder
	const classes = 300
	for c := 0; c < classes; c++ {
		name := "c" + strconv.Itoa(c)
		sb.WriteString("com.app." + name + " -> p." + name + ":\n")
		for m := 0; m < 20; m++ {
			sb.WriteString("    " + strconv.Itoa(m+1) + ":" + strconv.Itoa(m+1) + ":void m" + strconv.Itoa(m) + "():" + strconv.Itoa(100+m) + " -> " + string(rune('a'+m)) + "\n")
		}
	}
	old := memberRunBytes
	memberRunBytes = 40 * 100
	defer func() { memberRunBytes = old }()
	ix := buildFrom(t, sb.String())
	for _, c := range []int{0, 17, 299} {
		got := ix.Remap("p.c"+strconv.Itoa(c), "e", 5)
		if len(got) != 1 || got[0].Class != "com.app.c"+strconv.Itoa(c) || got[0].Method != "m4" || got[0].Line != 104 {
			t.Fatalf("class %d: %+v", c, got)
		}
	}
}

func TestOpenIndexRejectsHostile(t *testing.T) {
	good := bytes.Buffer{}
	if err := BuildIndexIn(strings.NewReader("a.A -> b:\n"), &good, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	b := good.Bytes()
	if _, err := OpenIndex(bytes.NewReader(b[:20]), 20); err == nil {
		t.Fatal("truncated header accepted")
	}
	bad := append([]byte(nil), b...)
	bad[8], bad[9], bad[10], bad[11] = 0xff, 0xff, 0xff, 0x7f // class count far beyond the file
	if _, err := OpenIndex(bytes.NewReader(bad), int64(len(bad))); err == nil {
		t.Fatal("oversized class count accepted")
	}
	if _, err := OpenIndex(bytes.NewReader([]byte("nope")), 4); err == nil {
		t.Fatal("garbage accepted")
	}
}
