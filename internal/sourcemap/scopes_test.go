package sourcemap

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

// posOf returns the 0-based line and UTF-16 column of the first occurrence of
// sub in src.
func posOf(t testing.TB, src, sub string) (int, int) {
	t.Helper()
	i := strings.Index(src, sub)
	if i < 0 {
		t.Fatalf("%q not in source", sub)
	}
	line := strings.Count(src[:i], "\n")
	rest := src[:i]
	if j := strings.LastIndex(rest, "\n"); j >= 0 {
		rest = rest[j+1:]
	}
	return line, len(utf16.Encode([]rune(rest)))
}

type nameSeg struct {
	line, col int
	name      string // "" for an unnamed segment
}

// buildNamedMap encodes a one-source map with the given segments.
func buildNamedMap(t testing.TB, segs []nameSeg) *Map {
	t.Helper()
	sort.Slice(segs, func(i, j int) bool {
		if segs[i].line != segs[j].line {
			return segs[i].line < segs[j].line
		}
		return segs[i].col < segs[j].col
	})
	var names []string
	nameIdx := map[string]int{}
	var sb strings.Builder
	curLine, prevCol, prevSrcLine, prevSrcCol, prevName := 0, 0, 0, 0, 0
	for k, s := range segs {
		for curLine < s.line {
			sb.WriteByte(';')
			curLine++
			prevCol = 0
		}
		if k > 0 && segs[k-1].line == s.line {
			sb.WriteByte(',')
		}
		srcLine, srcCol := k+1, 0 // distinct original positions
		sb.WriteString(encodeVLQ(s.col - prevCol))
		sb.WriteString(encodeVLQ(0))
		sb.WriteString(encodeVLQ(srcLine - prevSrcLine))
		sb.WriteString(encodeVLQ(srcCol - prevSrcCol))
		prevCol, prevSrcLine, prevSrcCol = s.col, srcLine, srcCol
		if s.name != "" {
			ix, ok := nameIdx[s.name]
			if !ok {
				ix = len(names)
				nameIdx[s.name] = ix
				names = append(names, s.name)
			}
			sb.WriteString(encodeVLQ(ix - prevName))
			prevName = ix
		}
	}
	raw, err := json.Marshal(map[string]any{
		"version": 3, "file": "app.js", "sources": []string{"src/app.ts"},
		"names": append([]string{}, names...), "mappings": sb.String(),
	})
	if err != nil {
		t.Fatal(err)
	}
	m, err := ParseMap(raw)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

const scopeSrc = "function wu(a){return a+1}\n" +
	"var f=function(b){return b*2};\n" +
	"var o={g:function(c){return c-1},m(d){return d-2},h:e=>e+3,k:async(x)=>{return x-4}};\n" +
	"const ar=(p)=>{const inner=(q)=>{return q-5};return inner(p)};\n" +
	"let z=a=>a?1:2,rx=/re[/]gex/.test(\"s`}{\")&&0,tpl=`t${(()=>1)()}u}{`;\n" +
	"var host=function(){return [1].map(v=>v+6)};\n" +
	"class K{constructor(){this.v=`x`}run(){return 7}}\n" +
	"var s=\"é😀\";var uni=function(){return 8};\n" +
	"var after=function(){return 9};\n" +
	"var fx=function named(){return 10};\n"

func TestScopeNames(t *testing.T) {
	sc, err := BuildScopes(context.Background(), []byte(scopeSrc))
	if err != nil || sc == nil {
		t.Fatal(sc, err)
	}
	// declaration token -> original name; marker is a position inside the body.
	cases := []struct{ decl, orig, marker string }{
		{"wu(", "checkout", "a+1"},
		{"f=function", "assigned", "b*2"},
		{"g:function", "prop", "c-1"},
		{"m(d)", "method", "d-2"},
		{"h:e=>", "arrowProp", "e+3"},
		{"k:async", "asyncArrow", "x-4"},
		{"inner=", "innerArrow", "q-5"},
		{"ar=(p)", "outerArrow", "inner(p)"},
		{"constructor", "ctor", "this.v"},
		{"run()", "runMethod", "7}"},
		{"host=", "hostFn", "[1].map"},
		{"uni=", "unicode", "8}"},
		{"after=", "afterAll", "9}"},
		{"named(", "namedExpr", "10}"},
	}
	var segs []nameSeg
	for _, c := range cases {
		l, col := posOf(t, scopeSrc, c.decl)
		segs = append(segs, nameSeg{l, col, c.orig})
	}
	m := buildNamedMap(t, segs)
	m.SetScopes(sc)
	for _, c := range cases {
		l, col := posOf(t, scopeSrc, c.marker)
		got, ok := m.ScopeName(l, col)
		if !ok || got != c.orig {
			t.Errorf("%s: got %q,%v want %q", c.decl, got, ok, c.orig)
		}
	}
	// an anonymous arrow inside a named function: the innermost scope has no
	// name, so there is none (the caller falls back to the position's name)
	l, col := posOf(t, scopeSrc, "v+6")
	if got, ok := m.ScopeName(l, col); ok {
		t.Errorf("anonymous arrow: got %q", got)
	}
	// top level and the gap between functions
	if got, ok := m.ScopeName(0, 0); ok && got != "checkout" {
		t.Errorf("start of file: %q", got)
	}
	l, col = posOf(t, scopeSrc, "const ar")
	if got, ok := m.ScopeName(l, col); ok {
		t.Errorf("top level got %q", got)
	}
}

func TestScopeNoNamesUnchanged(t *testing.T) {
	sc, err := BuildScopes(context.Background(), []byte(scopeSrc))
	if err != nil {
		t.Fatal(err)
	}
	l, col := posOf(t, scopeSrc, "wu(")
	m := buildNamedMap(t, []nameSeg{{l, col, ""}})
	m.SetScopes(sc)
	l, col = posOf(t, scopeSrc, "a+1")
	if got, ok := m.ScopeName(l, col); ok {
		t.Errorf("map without names: got %q", got)
	}
	var none Map
	if _, ok := none.ScopeName(0, 0); ok {
		t.Error("map without scopes")
	}
}

func TestScopeSkipsLiterals(t *testing.T) {
	// braces and function-like text inside strings, comments, regexes and
	// templates must not open or close scopes.
	src := "var a=function(){return \"}function z(){\"};\n" +
		"// function c(){\n" +
		"/* function d(){ */var b=function(){return /}function e(){/.test(1)};\n" +
		"var t=`${\"}\"}function g(){`;var c=function(){return 5};\n"
	sc, err := BuildScopes(context.Background(), []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if sc.Len() != 3 {
		t.Fatalf("functions = %d, want 3", sc.Len())
	}
	var segs []nameSeg
	for _, n := range []string{"a=", "b=", "c=function"} {
		l, col := posOf(t, src, n)
		segs = append(segs, nameSeg{l, col, "fn_" + n[:1]})
	}
	m := buildNamedMap(t, segs)
	m.SetScopes(sc)
	for _, c := range [][2]string{{"\"}function z", "fn_a"}, {"/}function e", "fn_b"}, {"return 5", "fn_c"}} {
		l, col := posOf(t, src, c[0])
		if got, _ := m.ScopeName(l, col); got != c[1] {
			t.Errorf("%q: got %q want %q", c[0], got, c[1])
		}
	}
}

func TestScopeHugeSingleLine(t *testing.T) {
	var sb strings.Builder
	for i := 0; sb.Len() < 8<<20; i++ {
		fmt.Fprintf(&sb, "function f%d(a){return a+%d}var v%d=(x)=>{return \"}{\"+x};", i, i, i)
	}
	src := sb.String()
	start := time.Now()
	sc, err := BuildScopes(context.Background(), []byte(src))
	took := time.Since(start)
	if err != nil || sc == nil {
		t.Fatal(sc, err)
	}
	if took > 5*time.Second {
		t.Errorf("indexing %d bytes took %v", len(src), took)
	}
	if sc.Len() < 100_000 {
		t.Errorf("functions = %d", sc.Len())
	}
	l, col := posOf(t, src, "function f777(")
	m := buildNamedMap(t, []nameSeg{{l, col + len("function "), "seven"}})
	m.SetScopes(sc)
	l, col = posOf(t, src, "a+777}")
	if got, ok := m.ScopeName(l, col); !ok || got != "seven" {
		t.Errorf("got %q,%v", got, ok)
	}
	t.Logf("%d bytes, %d functions, %v, index %d bytes", len(src), sc.Len(), took, sc.Bytes())
}

func TestScopeOversizedAndCancelled(t *testing.T) {
	if sc, err := BuildScopes(context.Background(), make([]byte, MaxScopeSource+1)); sc != nil || err != nil {
		t.Errorf("oversized: %v %v", sc, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	src := strings.Repeat("a=1;", 100_000)
	if _, err := BuildScopes(ctx, []byte(src)); err == nil {
		t.Error("cancelled context not honoured")
	}
}

func FuzzBuildScopes(f *testing.F) {
	f.Add([]byte(scopeSrc))
	f.Add([]byte("a=>{`${"))
	f.Fuzz(func(t *testing.T, b []byte) {
		sc, _ := BuildScopes(context.Background(), b)
		if sc != nil {
			var m Map
			m.SetScopes(sc)
			m.ScopeName(0, 0)
		}
	})
}
