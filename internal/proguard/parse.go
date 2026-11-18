// Package proguard reads ProGuard and R8 mapping files and answers stack-frame
// deobfuscation queries from a compact on-disk index.
//
// The mapping format is implemented from its public description (the R8
// retrace documentation): a class line "original.Name -> obfuscated.Name:"
// followed by indented member lines, of which only methods matter for stack
// traces: "[first:last:]returnType name(args)[:origFirst[:origLast]] -> obf".
// Comment lines start with "#"; R8 puts JSON metadata in them, of which only
// the sourceFile entry is used.
package proguard

import (
	"encoding/json"
	"strconv"
	"strings"
)

const (
	maxLine   = 64 << 10 // longer lines are skipped
	maxString = 4096     // longer names are clipped
)

// method is one parsed method line.
type method struct {
	obfName   string
	obfStart  uint32 // 0 with obfEnd 0 means no line range
	obfEnd    uint32
	origStart uint32 // 0 means none
	origEnd   uint32
	origClass string // set when the method is qualified (an inlined method of another class)
	origName  string
}

// classLine parses "orig -> obf:".
func classLine(s string) (orig, obf string, ok bool) {
	if !strings.HasSuffix(s, ":") {
		return "", "", false
	}
	left, right, found := strings.Cut(strings.TrimSuffix(s, ":"), " -> ")
	if !found {
		return "", "", false
	}
	orig, obf = strings.TrimSpace(left), strings.TrimSpace(right)
	if orig == "" || obf == "" || strings.ContainsAny(orig, " \t") || strings.ContainsAny(obf, " \t") {
		return "", "", false
	}
	return clip(orig), clip(obf), true
}

// sourceFile extracts the file name of an R8 sourceFile metadata comment.
func sourceFile(comment string) (string, bool) {
	c := strings.TrimSpace(strings.TrimPrefix(comment, "#"))
	if !strings.HasPrefix(c, "{") || !strings.Contains(c, `"sourceFile"`) {
		return "", false
	}
	var m struct {
		ID       string `json:"id"`
		FileName string `json:"fileName"`
	}
	if json.Unmarshal([]byte(c), &m) != nil || m.ID != "sourceFile" || m.FileName == "" {
		return "", false
	}
	return clip(m.FileName), true
}

// methodLine parses one indented member line; fields and anything else
// return ok=false.
func methodLine(s string) (m method, ok bool) {
	left, right, found := strings.Cut(s, " -> ")
	if !found {
		return m, false
	}
	obf := strings.TrimSpace(right)
	if obf == "" || strings.ContainsAny(obf, " \t:") {
		return m, false
	}
	rest := strings.TrimSpace(left)
	// Leading "first:last:" obfuscated line range.
	if a, r1, ok1 := cutNum(rest); ok1 {
		if b, r2, ok2 := cutNum(r1); ok2 {
			m.obfStart, m.obfEnd = a, b
			rest = r2
		}
	}
	open := strings.IndexByte(rest, '(')
	closeP := strings.LastIndexByte(rest, ')')
	if open < 0 || closeP < open {
		return m, false
	}
	head := rest[:open]
	sp := strings.LastIndexByte(head, ' ')
	if sp < 0 {
		return m, false
	}
	name := strings.TrimSpace(head[sp+1:])
	if name == "" {
		return m, false
	}
	// Trailing ":origFirst[:origLast]" after the argument list.
	if tail := rest[closeP+1:]; tail != "" {
		if !strings.HasPrefix(tail, ":") {
			return m, false
		}
		parts := strings.Split(tail[1:], ":")
		if len(parts) > 2 {
			return m, false
		}
		a, err := strconv.ParseUint(parts[0], 10, 31)
		if err != nil {
			return m, false
		}
		m.origStart, m.origEnd = uint32(a), uint32(a)
		if len(parts) == 2 {
			b, err := strconv.ParseUint(parts[1], 10, 31)
			if err != nil {
				return m, false
			}
			m.origEnd = uint32(b)
		}
	}
	if i := strings.LastIndexByte(name, '.'); i > 0 && i < len(name)-1 {
		m.origClass, name = clip(name[:i]), name[i+1:]
	}
	m.obfName, m.origName = clip(obf), clip(name)
	return m, true
}

// cutNum reads "digits:" off the front of s.
func cutNum(s string) (n uint32, rest string, ok bool) {
	i := strings.IndexByte(s, ':')
	if i <= 0 || i > 10 {
		return 0, s, false
	}
	v, err := strconv.ParseUint(s[:i], 10, 31)
	if err != nil {
		return 0, s, false
	}
	return uint32(v), s[i+1:], true
}

func clip(s string) string {
	if len(s) > maxString {
		return s[:maxString]
	}
	return s
}
