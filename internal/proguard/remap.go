package proguard

import (
	"encoding/binary"
	"sort"
	"strings"
)

// Frame is one deobfuscated stack frame.
type Frame struct {
	Class, Method, File string
	Line                int
}

type classRec struct {
	hash               uint64
	obf, orig, src     uint32
	memStart, memCount uint32
}

type memRec struct {
	obfName, nameHash   uint32
	obfStart, obfEnd    uint32
	origStart, origEnd  uint32
	origClass, origName uint32
}

const maxEntries = 4096 // overloads of one obfuscated name considered per lookup

func (ix *Index) classAt(i uint32) (classRec, error) {
	var b [classRecSize]byte
	if _, err := ix.r.ReadAt(b[:], int64(ix.classOff)+int64(i)*classRecSize); err != nil {
		return classRec{}, err
	}
	return classRec{
		hash: binary.LittleEndian.Uint64(b[0:]), obf: binary.LittleEndian.Uint32(b[8:]),
		orig: binary.LittleEndian.Uint32(b[12:]), src: binary.LittleEndian.Uint32(b[16:]),
		memStart: binary.LittleEndian.Uint32(b[20:]), memCount: binary.LittleEndian.Uint32(b[24:]),
	}, nil
}

func (ix *Index) memberAt(i uint32) (memRec, error) {
	var b [memRecSize]byte
	if _, err := ix.r.ReadAt(b[:], int64(ix.memberOff)+int64(i)*memRecSize); err != nil {
		return memRec{}, err
	}
	u := func(o int) uint32 { return binary.LittleEndian.Uint32(b[o:]) }
	return memRec{u(0), u(4), u(8), u(12), u(16), u(20), u(24), u(28)}, nil
}

func (ix *Index) findClass(obf string) (classRec, bool) {
	h := hash64(obf)
	var rerr error
	i := sort.Search(int(ix.nClass), func(i int) bool {
		c, err := ix.classAt(uint32(i))
		if err != nil {
			rerr = err
			return true
		}
		return c.hash >= h
	})
	if rerr != nil {
		return classRec{}, false
	}
	for ; i < int(ix.nClass); i++ {
		c, err := ix.classAt(uint32(i))
		if err != nil || c.hash != h {
			return classRec{}, false
		}
		if name, err := ix.str(c.obf); err == nil && name == obf {
			return c, true
		}
	}
	return classRec{}, false
}

// Class returns the original name of an obfuscated class.
func (ix *Index) Class(obf string) (orig string, ok bool) {
	c, found := ix.findClass(obf)
	if !found {
		return "", false
	}
	orig, err := ix.str(c.orig)
	return orig, err == nil
}

// entries returns the method entries of the class whose obfuscated name is
// method, in (first obfuscated line, file) order.
func (ix *Index) entries(c classRec, method string) []memRec {
	h := hash32(method)
	var rerr error
	n := int(c.memCount)
	i := sort.Search(n, func(i int) bool {
		m, err := ix.memberAt(c.memStart + uint32(i))
		if err != nil {
			rerr = err
			return true
		}
		return m.nameHash >= h
	})
	if rerr != nil {
		return nil
	}
	var out []memRec
	for ; i < n && len(out) < maxEntries; i++ {
		m, err := ix.memberAt(c.memStart + uint32(i))
		if err != nil || m.nameHash != h {
			break
		}
		if name, err := ix.str(m.obfName); err == nil && name == method {
			out = append(out, m)
		}
	}
	return out
}

// Remap deobfuscates one frame. It returns nil when the class is not in the
// mapping. The frames are innermost first: when R8 inlined methods into the
// obfuscated one, each inlined callee comes before its caller, and the callers
// carry the call-site line.
func (ix *Index) Remap(class, method string, line int) []Frame {
	c, ok := ix.findClass(class)
	if !ok {
		return nil
	}
	orig, err := ix.str(c.orig)
	if err != nil {
		return nil
	}
	src, _ := ix.str(c.src)
	if src == "" {
		src = defaultFile(orig)
	}
	frame := func(m memRec, ln int) Frame {
		cls, name := orig, ""
		file := src
		if oc, _ := ix.str(m.origClass); oc != "" && oc != orig {
			cls, file = oc, defaultFile(oc)
		}
		name, _ = ix.str(m.origName)
		return Frame{Class: cls, Method: name, File: file, Line: ln}
	}
	es := ix.entries(c, method)
	if len(es) == 0 {
		return []Frame{{Class: orig, Method: method, File: src, Line: line}}
	}
	if line > 0 {
		for _, e := range es {
			if e.obfStart == 0 && e.obfEnd == 0 || line < int(e.obfStart) || line > int(e.obfEnd) {
				continue
			}
			var out []Frame
			for _, g := range es {
				if g.obfStart == e.obfStart && g.obfEnd == e.obfEnd {
					out = append(out, frame(g, origLine(g, line)))
				}
			}
			return out
		}
	}
	// No line range covers the frame: name only, when every candidate agrees.
	first := es[0]
	for _, e := range es[1:] {
		if e.origName != first.origName || e.origClass != first.origClass {
			// Offsets are string-table offsets; equal text can sit at two
			// offsets once the dedupe window rolled over, so compare text.
			a1, _ := ix.str(e.origName)
			a2, _ := ix.str(first.origName)
			b1, _ := ix.str(e.origClass)
			b2, _ := ix.str(first.origClass)
			if a1 != a2 || b1 != b2 {
				return nil
			}
		}
	}
	return []Frame{frame(first, 0)}
}

// origLine maps an obfuscated line through one entry. A single original line
// is a constant (an inline call site); a range shifts with the obfuscated line.
func origLine(m memRec, line int) int {
	switch {
	case m.origStart == 0:
		return line
	case m.origEnd > m.origStart:
		return int(m.origStart) + line - int(m.obfStart)
	default:
		return int(m.origStart)
	}
}

// defaultFile is the source file R8 assumes without metadata: the outermost
// class's simple name plus ".java".
func defaultFile(class string) string {
	if i := strings.LastIndexByte(class, '.'); i >= 0 {
		class = class[i+1:]
	}
	if i := strings.IndexByte(class, '$'); i > 0 {
		class = class[:i]
	}
	return class + ".java"
}
