package symcache

import (
	"io"
	"slices"
	"sort"
)

// Location is one resolved frame: a function and the source position inside it.
type Location struct {
	Function string
	File     string
	Line     uint32
}

// Cache reads a symbol cache through an io.ReaderAt. It is safe for
// concurrent use when the ReaderAt is.
type Cache struct {
	r    io.ReaderAt
	h    header
	arch string
}

// maxInlineScan bounds how many inline records one lookup walks backwards.
const maxInlineScan = 4096

// Open validates the header of a cache of the given size. Counts and offsets
// that do not fit the file are rejected before anything is read.
func Open(r io.ReaderAt, size int64) (*Cache, error) {
	if size < headerSize {
		return nil, ErrFormat
	}
	buf := make([]byte, headerSize)
	if _, err := r.ReadAt(buf, 0); err != nil {
		return nil, ErrFormat
	}
	h, ok := decodeHeader(buf)
	if !ok || h.version != Version {
		return nil, ErrFormat
	}
	sz := uint64(size)
	fits := func(off, n, rec uint64) bool {
		if rec != 0 && n > sz/rec {
			return false
		}
		return off <= sz && n*rec <= sz-off
	}
	if h.strOff < headerSize || !fits(h.strOff, h.strLen, 1) ||
		!fits(h.funcOff, h.funcN, funcRecSize) || !fits(h.lineOff, h.lineN, lineRecSize) ||
		!fits(h.inlOff, h.inlN, inlRecSize) {
		return nil, ErrFormat
	}
	c := &Cache{r: r, h: h}
	c.arch = c.str(h.arch)
	return c, nil
}

// Arch is the architecture the cache was built for.
func (c *Cache) Arch() string { return c.arch }

// Counts reports the table sizes.
func (c *Cache) Counts() (funcs, lines, inlines int) {
	return int(c.h.funcN), int(c.h.lineN), int(c.h.inlN)
}

func (c *Cache) str(off uint32) string {
	if off == 0 {
		return ""
	}
	o := uint64(off)
	if o+2 > c.h.strLen {
		return ""
	}
	var lb [2]byte
	if _, err := c.r.ReadAt(lb[:], int64(c.h.strOff+o)); err != nil {
		return ""
	}
	n := uint64(le.Uint16(lb[:]))
	if o+2+n > c.h.strLen {
		return ""
	}
	b := make([]byte, n)
	if _, err := c.r.ReadAt(b, int64(c.h.strOff+o+2)); err != nil {
		return ""
	}
	return string(b)
}

func (c *Cache) u32(off uint64) (uint32, bool) {
	var b [4]byte
	if _, err := c.r.ReadAt(b[:], int64(off)); err != nil {
		return 0, false
	}
	return le.Uint32(b[:]), true
}

// upper returns the index of the first record of a table whose first uint32
// (the start address) is greater than rel.
func (c *Cache) upper(off, n uint64, rec uint64, rel uint32) uint64 {
	return uint64(sort.Search(int(n), func(i int) bool {
		v, ok := c.u32(off + uint64(i)*rec)
		return !ok || v > rel
	}))
}

func (c *Cache) rec(off, i, size uint64) ([]byte, bool) {
	b := make([]byte, size)
	if _, err := c.r.ReadAt(b, int64(off+i*size)); err != nil {
		return nil, false
	}
	return b, true
}

// Lookup returns the frames at an address relative to the image base,
// innermost inlined call first and the enclosing function last. ok is false
// when no function covers the address.
func (c *Cache) Lookup(rel uint64) ([]Location, bool) {
	if rel >= 1<<32 {
		return nil, false
	}
	r32 := uint32(rel)
	fi := c.upper(c.h.funcOff, c.h.funcN, funcRecSize, r32)
	if fi == 0 {
		return nil, false
	}
	fb, ok := c.rec(c.h.funcOff, fi-1, funcRecSize)
	if !ok {
		return nil, false
	}
	fn := getFunc(fb)
	if r32 >= fn.end {
		return nil, false
	}

	var file string
	var line uint32
	if li := c.upper(c.h.lineOff, c.h.lineN, lineRecSize, r32); li > 0 {
		if lb, ok := c.rec(c.h.lineOff, li-1, lineRecSize); ok {
			if lr := getLine(lb); lr.line != 0 {
				file, line = c.str(lr.file), lr.line
			}
		}
	}

	var ins []inlRec
	hi := c.upper(c.h.inlOff, c.h.inlN, inlRecSize, r32)
	for i, scanned := hi, 0; i > 0 && scanned < maxInlineScan; scanned++ {
		i--
		ib, ok := c.rec(c.h.inlOff, i, inlRecSize)
		if !ok {
			break
		}
		r := getInl(ib)
		if r32-r.start >= c.h.maxInlineLen {
			break // every earlier record starts even further back
		}
		if r32 < r.end {
			ins = append(ins, r)
		}
	}
	slices.SortStableFunc(ins, func(a, b inlRec) int { return int(a.depth) - int(b.depth) })

	out := make([]Location, 0, len(ins)+1)
	for i := len(ins) - 1; i >= 0; i-- {
		out = append(out, Location{Function: c.str(ins[i].name), File: file, Line: line})
		file, line = c.str(ins[i].callFile), ins[i].callLine
	}
	out = append(out, Location{Function: c.str(fn.name), File: file, Line: line})
	return out, true
}
