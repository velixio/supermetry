package sourcemap

import (
	"encoding/json"
	"os"
	"testing"
)

func readFixture(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/vite/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type expected struct {
	DebugID  string `json:"debugId"`
	Minified struct {
		Line   int `json:"line"`
		Col    int `json:"col"`
		NewCol int `json:"newCol"`
	} `json:"minified"`
}

func loadExpected(t testing.TB) expected {
	var e expected
	if err := json.Unmarshal(readFixture(t, "expected.json"), &e); err != nil {
		t.Fatal(err)
	}
	return e
}

// The README in testdata/vite records the throw site in the original source:
// src/cart.js line 12, `throw` at column 4 and `new Error(` at column 10
// (0-based).
func TestViteFixtureLookup(t *testing.T) {
	m, err := ParseMap(readFixture(t, "app.js.map"))
	if err != nil {
		t.Fatal(err)
	}
	e := loadExpected(t)
	if m.DebugID != e.DebugID || m.DebugID == "" {
		t.Fatalf("map debug id %q, want %q", m.DebugID, e.DebugID)
	}
	if got := DebugIDFromSource(readFixture(t, "app.js")); got != e.DebugID {
		t.Fatalf("source debug id %q, want %q", got, e.DebugID)
	}
	got, ok := m.Lookup(e.Minified.Line, e.Minified.NewCol)
	if !ok {
		t.Fatal("no mapping for throw site")
	}
	if got.Source != "../../src/cart.js" || got.Line != 12 || got.Col != 10 {
		t.Fatalf("got %+v, want ../../src/cart.js:12:10", got)
	}
	// The `throw` keyword itself maps to column 4.
	got, ok = m.Lookup(e.Minified.Line, e.Minified.Col)
	if !ok || got.Line != 12 || got.Col != 4 {
		t.Fatalf("throw keyword: %+v %v", got, ok)
	}
	pre, cur, post := m.SourceContext(got.Source, got.Line, 2)
	if cur != "    throw new Error(\"cart limit exceeded\");" || len(pre) != 2 || pre[1] != "  if (amount > 100) {" || len(post) != 2 || post[0] != "  }" {
		t.Fatalf("context: %q %q %q", pre, cur, post)
	}
}

func TestVLQDecodedMappings(t *testing.T) {
	// Hand-computed: line 0 has two segments, line 1 one; line 2 is empty.
	m, err := ParseMap([]byte(`{"version":3,"sources":["a.js"],"names":["foo"],"mappings":"AAAA,IACIA;AACA;;"}`))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		l, c      int
		line, col int
		name      string
		ok        bool
	}{
		{0, 0, 1, 0, "", true},
		{0, 4, 2, 4, "foo", true},
		{0, 99, 2, 4, "foo", true},
		{1, 0, 3, 4, "", true},
		{2, 0, 0, 0, "", false},
		{5, 0, 0, 0, "", false},
	}
	for _, c := range cases {
		got, ok := m.Lookup(c.l, c.c)
		if ok != c.ok || (ok && (got.Line != c.line || got.Col != c.col || got.Name != c.name)) {
			t.Errorf("Lookup(%d,%d) = %+v,%v want line %d col %d name %q ok %v", c.l, c.c, got, ok, c.line, c.col, c.name, c.ok)
		}
	}
}

// Review Focus 2: a position before the first mapping of a line, or column 0,
// resolves on that line only.
func TestLookupColumnBeforeFirstMapping(t *testing.T) {
	// line 0: mapping at col 5 -> a.js:10:0 ; line 1: mapping at col 3 -> a.js:20:0
	// line 2: mapping at col 0 only -> a.js:30:0 ; line 3 empty.
	mp := encodeVLQ(5) + "A" + encodeVLQ(9) + "A" + ";" +
		encodeVLQ(3) + "A" + encodeVLQ(10) + "A" + ";" +
		"A" + "A" + encodeVLQ(10) + "A" + ";;"
	m, err := ParseMap([]byte(`{"version":3,"sources":["a.js"],"names":[],"mappings":"` + mp + `"}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ l, col, line int }{
		{0, 0, 10}, {0, 4, 10}, {0, 5, 10}, {0, 50, 10},
		{1, 0, 20}, {1, 2, 20}, {1, 3, 20},
		{2, 0, 30}, {2, 9, 30},
	} {
		got, ok := m.Lookup(c.l, c.col)
		if !ok || got.Line != c.line {
			t.Errorf("Lookup(%d,%d) = %+v,%v want line %d", c.l, c.col, got, ok, c.line)
		}
	}
	if _, ok := m.Lookup(3, 0); ok {
		t.Error("empty line 3 must not borrow a neighbour")
	}
	if _, ok := m.Lookup(-1, 0); ok {
		t.Error("negative line")
	}
}

func TestUnmappedSegment(t *testing.T) {
	// A 1-field segment at col 2 ends the preceding mapping.
	m, err := ParseMap([]byte(`{"version":3,"sources":["a.js"],"names":[],"mappings":"AAAA,EAAA,E"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Lookup(0, 1); !ok {
		t.Error("col 1 should map")
	}
	if _, ok := m.Lookup(0, 5); ok {
		t.Error("col 5 follows an unmapped segment")
	}
}

func TestSourceRoot(t *testing.T) {
	m, err := ParseMap([]byte(`{"version":3,"sourceRoot":"webpack:///src/","sources":["a.js","/abs.js","http://h/x.js",null],"sourcesContent":["l1\nl2\r\nl3\nl4",null],"names":[],"mappings":"AAAA"}`))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"webpack:///src/a.js", "/abs.js", "http://h/x.js", ""}
	for i, w := range want {
		if m.Sources[i] != w {
			t.Errorf("source %d = %q want %q", i, m.Sources[i], w)
		}
	}
	got, _ := m.Lookup(0, 0)
	if got.Source != "webpack:///src/a.js" {
		t.Errorf("mapping source %q", got.Source)
	}
	pre, cur, post := m.SourceContext("webpack:///src/a.js", 2, 5)
	if cur != "l2" || len(pre) != 1 || pre[0] != "l1" || len(post) != 2 || post[1] != "l4" {
		t.Errorf("context %q %q %q", pre, cur, post)
	}
	if _, cur, _ := m.SourceContext("/abs.js", 1, 1); cur != "" {
		t.Error("null content must give no context")
	}
	if _, cur, _ := m.SourceContext("webpack:///src/a.js", 9, 1); cur != "" {
		t.Error("out of range line")
	}
}

// Review Focus 1: index map with sections.
func TestIndexMap(t *testing.T) {
	idx := `{"version":3,"file":"out.js","sections":[
	 {"offset":{"line":0,"column":0},"map":{"version":3,"sources":["a.js"],"sourcesContent":["A0\nA1"],"names":["fa"],"mappings":"AAAAA;AACA"}},
	 {"offset":{"line":2,"column":10},"map":{"version":3,"sourceRoot":"r","sources":["b.js"],"names":["fb"],"mappings":"AAAAA,KACA;AAEA"}}
	]}`
	m, err := ParseMap([]byte(idx))
	if err != nil {
		t.Fatal(err)
	}
	if m.File != "out.js" || len(m.Sources) != 2 || len(m.Names) != 2 {
		t.Fatalf("%+v", m)
	}
	check := func(l, c int, src string, line, col int, name string) {
		t.Helper()
		got, ok := m.Lookup(l, c)
		if !ok || got.Source != src || got.Line != line || got.Col != col || got.Name != name {
			t.Errorf("Lookup(%d,%d) = %+v,%v want %s:%d:%d %q", l, c, got, ok, src, line, col, name)
		}
	}
	check(0, 0, "a.js", 1, 0, "fa")
	check(1, 0, "a.js", 2, 0, "")
	check(2, 10, "r/b.js", 1, 0, "fb") // first line: column offset applied
	check(2, 14, "r/b.js", 1, 0, "fb") // before the second segment at 10+5
	check(2, 20, "r/b.js", 2, 0, "")   // second segment starts at 15
	check(3, 0, "r/b.js", 4, 0, "")    // later lines: no column shift
	pre, cur, _ := m.SourceContext("a.js", 2, 1)
	if cur != "A1" || len(pre) != 1 || pre[0] != "A0" {
		t.Errorf("section context %q %q", pre, cur)
	}
}

func TestIndexMapErrors(t *testing.T) {
	for name, in := range map[string]string{
		"url":      `{"version":3,"sections":[{"offset":{"line":0,"column":0},"url":"x.map"}]}`,
		"order":    `{"version":3,"sections":[{"offset":{"line":2,"column":0},"map":{"version":3,"sources":[],"names":[],"mappings":""}},{"offset":{"line":1,"column":0},"map":{"version":3,"sources":[],"names":[],"mappings":""}}]}`,
		"nested":   `{"version":3,"sections":[{"offset":{"line":0,"column":0},"map":{"version":3,"sections":[]}}]}`,
		"both":     `{"version":3,"mappings":"","sections":[]}`,
		"version":  `{"version":2,"sources":[],"mappings":""}`,
		"no maps":  `{"version":3,"sources":[]}`,
		"bad src":  `{"version":3,"sources":["a"],"mappings":"AEAA"}`,
		"bad name": `{"version":3,"sources":["a"],"names":[],"mappings":"AAAAC"}`,
		"neg col":  `{"version":3,"sources":["a"],"mappings":"DAAA"}`,
		"2 fields": `{"version":3,"sources":["a"],"mappings":"AA"}`,
		"not json": `nope`,
	} {
		if _, err := ParseMap([]byte(in)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestHugeSectionOffsetIsCheap(t *testing.T) {
	m, err := ParseMap([]byte(`{"version":3,"sections":[{"offset":{"line":2000000000,"column":0},"map":{"version":3,"sources":["a"],"names":[],"mappings":"AAAA"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Lookup(2000000000, 0); !ok {
		t.Fatal("expected mapping")
	}
}

func FuzzParseSourceMap(f *testing.F) {
	for _, s := range []string{
		`{"version":3,"sources":["a.js"],"names":["x"],"mappings":"AAAAA,IACA;;"}`,
		`{"version":3,"sections":[{"offset":{"line":1,"column":2},"map":{"version":3,"sources":["a"],"names":[],"mappings":"AAAA"}}]}`,
		`{"version":3,"sourceRoot":"r","sources":[null],"sourcesContent":[null],"mappings":""}`,
	} {
		f.Add([]byte(s))
	}
	f.Add(readFixture(f, "app.js.map"))
	f.Fuzz(func(t *testing.T, b []byte) {
		m, err := ParseMap(b)
		if err != nil {
			return
		}
		for _, p := range [][2]int{{0, 0}, {0, 100}, {1, 3}, {1 << 20, 0}} {
			m.Lookup(p[0], p[1])
		}
		if len(m.Sources) > 0 {
			m.SourceContext(m.Sources[0], 1, 3)
		}
	})
}
