package sourcemap

import (
	"context"
	"sort"
)

// MaxScopeSource is the largest generated file BuildScopes will index.
const MaxScopeSource = 32 << 20

// maxScopeFns bounds the index: a file with more functions is indexed up to
// the point the limit was reached.
const maxScopeFns = 400_000

const noPos = ^uint64(0)

// Scopes is a compact index of the function scopes of a generated (minified)
// JavaScript file. A frame position is resolved to its enclosing function, and
// the function's declaration token is looked up in the source map to recover
// the original function name, the idea behind "source map scopes".
//
// It is built by a lightweight tokenizer, not a parser: it follows strings,
// template literals, regular expression literals and comments, and recognises
// function declarations and expressions, arrow functions and method
// shorthand. Positions are (line, UTF-16 column) as in source maps.
type Scopes struct {
	fns []scopeFn // sorted by start; parent indexes refer to this order
}

type scopeFn struct {
	start, end uint64 // line<<32 | col; end is exclusive
	name       uint64 // position of the name token, noPos if anonymous
	parent     int32  // enclosing function, -1 at top level
}

// Bytes estimates the memory held by the index.
func (sc *Scopes) Bytes() int64 {
	if sc == nil {
		return 0
	}
	return int64(len(sc.fns))*32 + 64
}

// Len is the number of indexed functions.
func (sc *Scopes) Len() int {
	if sc == nil {
		return 0
	}
	return len(sc.fns)
}

// SetScopes attaches a scope index. Call it before the map is shared.
func (m *Map) SetScopes(sc *Scopes) { m.scopes = sc }

// ScopeName returns the original name of the function that encloses the
// 0-based generated position: the name the map records at the function's
// declaration token. ok is false when there is no scope index, the innermost
// function is anonymous, or the map has no name there.
func (m *Map) ScopeName(line, col int) (string, bool) {
	sc := m.scopes
	if sc == nil || line < 0 || col < 0 {
		return "", false
	}
	pos := uint64(line)<<32 | uint64(col)
	i := sort.Search(len(sc.fns), func(i int) bool { return sc.fns[i].start > pos }) - 1
	for i >= 0 && sc.fns[i].end <= pos {
		i = int(sc.fns[i].parent)
	}
	if i < 0 {
		return "", false
	}
	fn := sc.fns[i]
	if fn.name == noPos {
		return "", false
	}
	for _, at := range [2]uint64{fn.name, fn.start} {
		if mp, ok := m.LookupExact(int(at>>32), int(at&0xffffffff)); ok && mp.HasName && mp.Name != "" {
			return mp.Name, true
		}
	}
	return "", false
}

// token kinds
const (
	tNone byte = iota
	tIdent
	tNum
	tStr
	tRegex
	tTemplate
	tPunct
	tArrow
)

type tok struct {
	kind byte
	c    byte // punctuator character; 'E' for an equality operator
	s, e int32
}

type rawFn struct{ start, end, name int32 }

type brk struct {
	kind byte  // '(' '[' '{' 'T' (template expression) 'a' (arrow expression body)
	fn   int32 // function whose end this entry closes, or -1
	name int32 // '(': offset of a method-name identifier before it, or -1
	ctrl bool  // '(': preceded by a control keyword
}

type scanner struct {
	src   []byte
	fns   []rawFn
	stack []brk
	h     [3]tok // h[0] is the previous token

	pendActive bool // saw `function`, waiting for its body
	pendStart  int32
	pendName   int32
	pendDepth  int
	pendStage  int // 0: right after `function` or `*`; 1: past the name

	arrowPending int32 // function index waiting to see `{` or an expression body

	candOK    bool // `name =` or `name :` waiting for an arrow function
	candName  int32
	candFirst int32
	candDepth int

	lastCloseName int32 // method name of the `)` that was just closed, or -1
	ctxCheck      int
}

// BuildScopes indexes src. It returns nil for an oversized file and the
// context error if ctx ends first.
func BuildScopes(ctx context.Context, src []byte) (*Scopes, error) {
	if len(src) == 0 || len(src) > MaxScopeSource {
		return nil, nil
	}
	s := &scanner{src: src, arrowPending: -1, lastCloseName: -1}
	end, err := s.run(ctx)
	if err != nil {
		return nil, err
	}
	for i := range s.fns {
		if s.fns[i].end < 0 {
			s.fns[i].end = int32(end)
		}
	}
	return finishScopes(src, s.fns), nil
}

func isIdentByte(c byte) bool {
	return c == '_' || c == '$' || c >= 0x80 || (c >= '0' && c <= '9') || (c|0x20 >= 'a' && c|0x20 <= 'z')
}

func isIdentStart(c byte) bool {
	return c == '_' || c == '$' || c == '#' || c >= 0x80 || (c|0x20 >= 'a' && c|0x20 <= 'z')
}

// run tokenizes the file; it returns the offset where scanning stopped.
func (s *scanner) run(ctx context.Context) (int, error) {
	src := s.src
	n := len(src)
	i := 0
	inTemplate := false // true: i is inside template text
	for i < n {
		if s.ctxCheck++; s.ctxCheck&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return i, err
			}
		}
		if len(s.fns) >= maxScopeFns {
			return i, nil
		}
		if inTemplate {
			j, expr := scanTemplate(src, i)
			s.emit(tok{kind: tTemplate, s: int32(i), e: int32(j)})
			i = j
			inTemplate = false
			if expr {
				s.stack = append(s.stack, brk{kind: 'T', fn: -1, name: -1})
			}
			continue
		}
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f':
			i++
		case c == '/' && i+1 < n && src[i+1] == '/':
			i += 2
			for i < n && src[i] != '\n' && src[i] != '\r' {
				i++
			}
		case c == '/' && i+1 < n && src[i+1] == '*':
			i += 2
			for i < n && !(src[i] == '*' && i+1 < n && src[i+1] == '/') {
				i++
			}
			i = min(i+2, n)
		case c == '/':
			if s.regexAllowed() {
				if j, ok := scanRegex(src, i); ok {
					s.emit(tok{kind: tRegex, s: int32(i), e: int32(j)})
					i = j
					break
				}
			}
			s.emit(tok{kind: tPunct, c: '/', s: int32(i), e: int32(i + 1)})
			i++
		case c == '"' || c == '\'':
			j := i + 1
			for j < n && src[j] != c && src[j] != '\n' {
				if src[j] == '\\' {
					j++
				}
				j++
			}
			j = min(j+1, n)
			s.emit(tok{kind: tStr, s: int32(i), e: int32(j)})
			i = j
		case c == '`':
			inTemplate = true
			i++
		case c >= '0' && c <= '9':
			j := i + 1
			hex := c == '0' && j < n && (src[j]|0x20 == 'x')
			for j < n {
				d := src[j]
				if isIdentByte(d) || d == '.' {
					j++
				} else if (d == '+' || d == '-') && !hex && src[j-1]|0x20 == 'e' {
					j++
				} else {
					break
				}
			}
			s.emit(tok{kind: tNum, s: int32(i), e: int32(j)})
			i = j
		case isIdentStart(c):
			j := i + 1
			for j < n && isIdentByte(src[j]) {
				j++
			}
			s.emit(tok{kind: tIdent, s: int32(i), e: int32(j)})
			i = j
		case c == '=' && i+1 < n && src[i+1] == '>':
			s.emit(tok{kind: tArrow, s: int32(i), e: int32(i + 2)})
			i += 2
		case c == '=' && i+1 < n && src[i+1] == '=':
			j := i + 2
			for j < n && src[j] == '=' {
				j++
			}
			s.emit(tok{kind: tPunct, c: 'E', s: int32(i), e: int32(j)})
			i = j
		case c == '}' && len(s.stack) > 0 && s.topTemplate():
			s.closeBrace(int32(i))
			inTemplate = true
			i++
		default:
			s.emit(tok{kind: tPunct, c: c, s: int32(i), e: int32(i + 1)})
			i++
		}
	}
	return n, nil
}

// topTemplate reports whether the innermost open bracket, ignoring arrow
// expression bodies, is a template expression.
func (s *scanner) topTemplate() bool {
	for k := len(s.stack) - 1; k >= 0; k-- {
		switch s.stack[k].kind {
		case 'a':
			continue
		case 'T':
			return true
		default:
			return false
		}
	}
	return false
}

// scanTemplate scans template text starting at i, just after the opening
// backtick or the `}` that ends a substitution. It returns the offset after the
// closing backtick, or after "${" when expr is true.
func scanTemplate(src []byte, i int) (j int, expr bool) {
	for i < len(src) {
		switch src[i] {
		case '\\':
			i += 2
			continue
		case '`':
			return i + 1, false
		case '$':
			if i+1 < len(src) && src[i+1] == '{' {
				return i + 2, true
			}
		}
		i++
	}
	return len(src), false
}

// scanRegex scans a regular expression literal starting at the slash and
// returns the offset after its flags. ok is false when none ends on the line.
func scanRegex(src []byte, i int) (int, bool) {
	j := i + 1
	inClass := false
	for j < len(src) {
		c := src[j]
		switch {
		case c == '\n' || c == '\r':
			return 0, false
		case c == '\\':
			j++
		case c == '[':
			inClass = true
		case c == ']':
			inClass = false
		case c == '/' && !inClass:
			j++
			for j < len(src) && isIdentByte(src[j]) {
				j++
			}
			return j, true
		}
		j++
	}
	return 0, false
}

func (s *scanner) text(t tok) string {
	if t.kind != tIdent || t.e-t.s > 12 {
		return ""
	}
	return string(s.src[t.s:t.e])
}

func (s *scanner) regexAllowed() bool {
	t := s.h[0]
	switch t.kind {
	case tNone:
		return true
	case tNum, tStr, tRegex, tTemplate:
		return false
	case tIdent:
		switch s.text(t) {
		case "return", "typeof", "instanceof", "in", "of", "new", "delete", "void", "throw", "case", "do", "else", "yield", "await":
			return true
		}
		return false
	case tPunct:
		return t.c != ')' && t.c != ']'
	}
	return true
}

func isCtrlKeyword(w string) bool {
	switch w {
	case "if", "for", "while", "switch", "catch", "with", "function":
		return true
	}
	return false
}

// methodPredOK reports whether tok p, the token before a would-be method
// name, can precede a method definition (rules out `extends f(x) {`).
func (s *scanner) methodPredOK(p tok) bool {
	switch p.kind {
	case tNone:
		return true
	case tPunct:
		return p.c == ',' || p.c == '{' || p.c == '}' || p.c == ';' || p.c == '*'
	case tIdent:
		switch s.text(p) {
		case "static", "get", "set", "async":
			return true
		}
	}
	return false
}

func (s *scanner) endArrows(at int32) {
	for k := len(s.stack) - 1; k >= 0 && s.stack[k].kind == 'a'; k = len(s.stack) - 1 {
		s.fns[s.stack[k].fn].end = at
		s.stack = s.stack[:k]
	}
}

// closeBrace handles `}` at offset at (also the end of a template substitution).
func (s *scanner) closeBrace(at int32) {
	s.endArrows(at)
	if k := len(s.stack) - 1; k >= 0 && (s.stack[k].kind == '{' || s.stack[k].kind == 'T') {
		if f := s.stack[k].fn; f >= 0 {
			s.fns[f].end = at + 1
		}
		s.stack = s.stack[:k]
	}
}

func (s *scanner) newFn(start, name int32) int32 {
	s.fns = append(s.fns, rawFn{start: start, end: -1, name: name})
	return int32(len(s.fns) - 1)
}

// emit feeds one token to the function-recognition state machine.
func (s *scanner) emit(t tok) {
	depth := len(s.stack)

	// An `name =` / `name :` target only names an arrow function when what
	// follows is a parameter list and `=>`.
	if s.candOK && depth == s.candDepth {
		switch {
		case t.kind == tIdent || (t.kind == tPunct && t.c == '('):
			if s.candFirst < 0 {
				s.candFirst = t.s
			}
		case t.kind == tArrow:
		default:
			s.candOK = false
		}
	}

	if s.arrowPending >= 0 {
		f := s.arrowPending
		s.arrowPending = -1
		if t.kind == tPunct && t.c == '{' {
			s.stack = append(s.stack, brk{kind: '{', fn: f, name: -1})
			s.shift(t)
			return
		}
		s.stack = append(s.stack, brk{kind: 'a', fn: f, name: -1})
		depth = len(s.stack)
	}

	switch t.kind {
	case tIdent:
		switch s.text(t) {
		case "function":
			a, b := s.h[0], s.h[1]
			start := t.s
			if s.text(a) == "async" {
				start = a.s
				a, b = s.h[1], s.h[2]
			}
			name := int32(-1)
			if a.kind == tPunct && (a.c == '=' || a.c == ':') && b.kind == tIdent {
				name = b.s
			}
			s.pendActive, s.pendStart, s.pendName, s.pendDepth, s.pendStage = true, start, name, depth, 0
		default:
			if s.pendActive && s.pendStage == 0 && depth == s.pendDepth {
				s.pendName = t.s // function name(  /  x = function name(
				s.pendStage = 1
			}
		}
	case tArrow:
		start, name := t.s, int32(-1)
		if s.candOK && depth == s.candDepth {
			name = s.candName
			if s.candFirst >= 0 {
				start = s.candFirst
			}
		}
		s.candOK = false
		s.arrowPending = s.newFn(start, name)
	case tPunct:
		switch t.c {
		case '(':
			b := brk{kind: '(', fn: -1, name: -1}
			if s.h[0].kind == tIdent {
				w := s.text(s.h[0])
				b.ctrl = isCtrlKeyword(w)
				if !b.ctrl && s.methodPredOK(s.h[1]) {
					b.name = s.h[0].s
				}
			}
			if s.pendActive && depth == s.pendDepth {
				s.pendStage = 1
			}
			s.stack = append(s.stack, b)
		case '[':
			s.stack = append(s.stack, brk{kind: '[', fn: -1, name: -1})
		case ')', ']':
			s.endArrows(t.s)
			s.lastCloseName = -1
			open := byte('(')
			if t.c == ']' {
				open = '['
			}
			if k := len(s.stack) - 1; k >= 0 && s.stack[k].kind == open {
				if t.c == ')' {
					s.lastCloseName = s.stack[k].name
				}
				s.stack = s.stack[:k]
			}
		case '{':
			switch {
			case s.pendActive && depth == s.pendDepth:
				f := s.newFn(s.pendStart, s.pendName)
				s.pendActive = false
				s.stack = append(s.stack, brk{kind: '{', fn: f, name: -1})
			case s.h[0].kind == tPunct && s.h[0].c == ')' && s.lastCloseName >= 0:
				f := s.newFn(s.lastCloseName, s.lastCloseName)
				s.stack = append(s.stack, brk{kind: '{', fn: f, name: -1})
			default:
				s.stack = append(s.stack, brk{kind: '{', fn: -1, name: -1})
			}
		case '}':
			s.closeBrace(t.s)
		case ';', ',':
			s.endArrows(t.s)
		case '=', ':':
			if s.h[0].kind == tIdent {
				s.candOK, s.candName, s.candFirst, s.candDepth = true, s.h[0].s, -1, depth
			} else {
				s.candOK = false
			}
		}
	}
	s.shift(t)
}

func (s *scanner) shift(t tok) {
	s.h[2], s.h[1], s.h[0] = s.h[1], s.h[0], t
}

type offRef struct {
	off   int32
	fn    int32
	field uint8 // 0 start, 1 end, 2 name
}

// finishScopes sorts the functions, links parents and converts byte offsets
// to (line, UTF-16 column) positions.
func finishScopes(src []byte, raw []rawFn) *Scopes {
	sort.SliceStable(raw, func(i, j int) bool {
		if raw[i].start != raw[j].start {
			return raw[i].start < raw[j].start
		}
		return raw[i].end > raw[j].end
	})
	fns := make([]scopeFn, len(raw))
	refs := make([]offRef, 0, len(raw)*3)
	var open []int32
	for i, r := range raw {
		for len(open) > 0 && raw[open[len(open)-1]].end <= r.start {
			open = open[:len(open)-1]
		}
		fns[i].parent = -1
		if len(open) > 0 {
			fns[i].parent = open[len(open)-1]
		}
		open = append(open, int32(i))
		refs = append(refs, offRef{r.start, int32(i), 0}, offRef{r.end, int32(i), 1})
		fns[i].name = noPos
		if r.name >= 0 {
			refs = append(refs, offRef{r.name, int32(i), 2})
		}
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].off < refs[j].off })

	var line, col uint64
	p, k := 0, 0
	for k < len(refs) {
		for k < len(refs) && int(refs[k].off) <= p {
			pos := line<<32 | col
			f := &fns[refs[k].fn]
			switch refs[k].field {
			case 0:
				f.start = pos
			case 1:
				f.end = pos
			default:
				f.name = pos
			}
			k++
		}
		if k >= len(refs) || p >= len(src) {
			break
		}
		c := src[p]
		switch {
		case c == '\n':
			line, col = line+1, 0
			p++
		case c == '\r':
			if p+1 < len(src) && src[p+1] == '\n' {
				col++
			} else {
				line, col = line+1, 0
			}
			p++
		case c == 0xE2 && p+2 < len(src) && src[p+1] == 0x80 && (src[p+2] == 0xA8 || src[p+2] == 0xA9):
			line, col = line+1, 0
			p += 3
		case c < 0x80:
			col++
			p++
		case c >= 0xF0:
			col += 2
			p++
		case c >= 0xC0:
			col++
			p++
		default: // continuation byte
			p++
		}
	}
	// offsets past the end of the source (none expected) keep their zero value
	return &Scopes{fns: fns}
}
