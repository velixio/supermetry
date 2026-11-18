package breakpad

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"slices"
	"sort"
	"strings"

	"supermetry/internal/symcache"
)

// The CFI section follows the symcache tables in the same file:
//
//	header (32 bytes)  "SMCF", version, nInit, nDelta, initOff, deltaOff, strOff, strLen (uint32; offsets relative to the section)
//	init table         {start, size, rules, deltaFirst, deltaN uint32}, sorted by start
//	delta table        {addr, rules uint32}, grouped by init, sorted by addr within a group
//	strings            [len uint32][bytes]; "rules" fields are offsets into this blob
const (
	cfiMagic      = "SMCF"
	cfiVersion    = 1
	cfiHeaderSize = 32
	initRecSize   = 20
	deltaRecSize  = 8
	// maxCFIRecords bounds the in-memory index of one build.
	maxCFIRecords = 3 << 20
	// maxCFIBytes bounds the rule strings of one build.
	maxCFIBytes = 1 << 30
	// maxRuleLen bounds one rule string.
	maxRuleLen = 8 << 10
	// maxDeltasPerInit bounds the deltas applied for one lookup.
	maxDeltasPerInit = 1 << 14
)

var le = binary.LittleEndian

type initRec struct {
	start, size, rules, deltaFirst, deltaN uint32
}
type deltaRec struct{ addr, rules uint32 }

// cfiBuilder collects CFI records while the file streams by. Rule strings go
// to an unlinked temporary file; only fixed-width index records stay in memory.
type cfiBuilder struct {
	tmpDir  string
	f       *os.File
	w       *bufio.Writer
	strLen  uint64
	inits   []initRec
	deltas  []deltaRec
	dropped int
}

func (c *cfiBuilder) store(rules []byte) (uint32, bool) {
	if len(rules) > maxRuleLen || c.strLen+uint64(len(rules))+4 > maxCFIBytes {
		return 0, false
	}
	if c.f == nil {
		f, err := os.CreateTemp(c.tmpDir, "breakpad-cfi-*")
		if err != nil {
			return 0, false
		}
		_ = os.Remove(f.Name())
		c.f, c.w = f, bufio.NewWriterSize(f, 256<<10)
	}
	off := uint32(c.strLen)
	var h [4]byte
	le.PutUint32(h[:], uint32(len(rules)))
	_, _ = c.w.Write(h[:])
	_, _ = c.w.Write(rules)
	c.strLen += uint64(len(rules)) + 4
	return off, true
}

func (c *cfiBuilder) full() bool { return len(c.inits)+len(c.deltas) >= maxCFIRecords }

func (c *cfiBuilder) addInit(start, size uint64, rules []byte) {
	if size == 0 || start+size > 1<<32 || c.full() {
		c.dropped++
		return
	}
	off, ok := c.store(rules)
	if !ok {
		c.dropped++
		return
	}
	c.inits = append(c.inits, initRec{start: uint32(start), size: uint32(size), rules: off, deltaFirst: uint32(len(c.deltas))})
}

// addDelta attaches a rule change to the most recent INIT record.
func (c *cfiBuilder) addDelta(addr uint64, rules []byte) {
	if len(c.inits) == 0 || addr >= 1<<32 || c.full() {
		c.dropped++
		return
	}
	off, ok := c.store(rules)
	if !ok {
		c.dropped++
		return
	}
	c.deltas = append(c.deltas, deltaRec{addr: uint32(addr), rules: off})
	c.inits[len(c.inits)-1].deltaN++
}

func (c *cfiBuilder) close() {
	if c.f != nil {
		c.f.Close()
		c.f = nil
	}
}

// write appends the section to w, or nothing when there is no CFI at all.
func (c *cfiBuilder) write(w io.Writer) error {
	defer c.close()
	if len(c.inits) == 0 {
		return nil
	}
	// Deltas stay grouped per init at their original positions; sorting inside
	// a group keeps the indexes held by the init records valid.
	for i := range c.inits {
		g := c.deltas[c.inits[i].deltaFirst : c.inits[i].deltaFirst+c.inits[i].deltaN]
		slices.SortStableFunc(g, func(a, b deltaRec) int { return cmpU32(a.addr, b.addr) })
	}
	slices.SortStableFunc(c.inits, func(a, b initRec) int { return cmpU32(a.start, b.start) })
	if err := c.w.Flush(); err != nil {
		return err
	}
	nI, nD := uint64(len(c.inits)), uint64(len(c.deltas))
	initOff := uint64(cfiHeaderSize)
	deltaOff := initOff + nI*initRecSize
	strOff := deltaOff + nD*deltaRecSize
	if strOff+c.strLen > 1<<32 {
		return errors.New("breakpad: CFI section too large")
	}
	hdr := make([]byte, cfiHeaderSize)
	copy(hdr, cfiMagic)
	for i, v := range []uint32{cfiVersion, uint32(nI), uint32(nD), uint32(initOff), uint32(deltaOff), uint32(strOff), uint32(c.strLen)} {
		le.PutUint32(hdr[4+4*i:], v)
	}
	bw := bufio.NewWriterSize(w, 256<<10)
	_, _ = bw.Write(hdr)
	var rec [initRecSize]byte
	for _, r := range c.inits {
		for i, v := range []uint32{r.start, r.size, r.rules, r.deltaFirst, r.deltaN} {
			le.PutUint32(rec[4*i:], v)
		}
		_, _ = bw.Write(rec[:])
	}
	for _, d := range c.deltas {
		le.PutUint32(rec[0:], d.addr)
		le.PutUint32(rec[4:], d.rules)
		_, _ = bw.Write(rec[:deltaRecSize])
	}
	if _, err := c.f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if _, err := io.Copy(bw, c.f); err != nil {
		return err
	}
	return bw.Flush()
}

func cmpU32(a, b uint32) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// CFITable reads the call-frame rules of a Breakpad cache.
type CFITable struct {
	r                 io.ReaderAt
	base              int64 // section start in the file
	nInit, nDelta     uint64
	initOff, deltaOff uint64
	strOff, strLen    uint64
}

// ErrNoCFI means the cache has no call-frame section.
var ErrNoCFI = errors.New("breakpad: cache has no CFI section")

// OpenCFI reads the CFI section that follows the symcache tables of c.
func OpenCFI(c *symcache.Cache) (*CFITable, error) {
	r, end, size := c.Tail()
	if size-end < cfiHeaderSize {
		return nil, ErrNoCFI
	}
	hdr := make([]byte, cfiHeaderSize)
	if _, err := r.ReadAt(hdr, end); err != nil || string(hdr[:4]) != cfiMagic || le.Uint32(hdr[4:]) != cfiVersion {
		return nil, ErrNoCFI
	}
	u := func(i int) uint64 { return uint64(le.Uint32(hdr[4+4*i:])) }
	t := &CFITable{r: r, base: end, nInit: u(1), nDelta: u(2), initOff: u(3), deltaOff: u(4), strOff: u(5), strLen: u(6)}
	sec := uint64(size - end)
	if t.nInit > sec/initRecSize || t.nDelta > sec/deltaRecSize || t.initOff != cfiHeaderSize ||
		t.deltaOff != t.initOff+t.nInit*initRecSize || t.strOff != t.deltaOff+t.nDelta*deltaRecSize ||
		t.strOff+t.strLen > sec {
		return nil, ErrNoCFI
	}
	return t, nil
}

func (t *CFITable) readAt(off uint64, n int) ([]byte, bool) {
	b := make([]byte, n)
	_, err := t.r.ReadAt(b, t.base+int64(off))
	return b, err == nil
}

func (t *CFITable) initAt(i uint64) (initRec, bool) {
	b, ok := t.readAt(t.initOff+i*initRecSize, initRecSize)
	if !ok {
		return initRec{}, false
	}
	return initRec{le.Uint32(b), le.Uint32(b[4:]), le.Uint32(b[8:]), le.Uint32(b[12:]), le.Uint32(b[16:])}, true
}

func (t *CFITable) rules(off uint32) (string, bool) {
	o := uint64(off)
	if o+4 > t.strLen {
		return "", false
	}
	h, ok := t.readAt(t.strOff+o, 4)
	if !ok {
		return "", false
	}
	n := uint64(le.Uint32(h))
	if n > maxRuleLen || o+4+n > t.strLen {
		return "", false
	}
	b, ok := t.readAt(t.strOff+o+4, int(n))
	return string(b), ok
}

// RulesAt returns the effective rule string at an address relative to the
// image base: the INIT rules of the covering range with every delta at or
// before the address applied in order. ok is false when no range covers it.
func (t *CFITable) RulesAt(rel uint64) (string, bool) {
	if rel >= 1<<32 {
		return "", false
	}
	r32 := uint32(rel)
	i := uint64(sort.Search(int(t.nInit), func(i int) bool {
		r, ok := t.initAt(uint64(i))
		return !ok || r.start > r32
	}))
	if i == 0 {
		return "", false
	}
	in, ok := t.initAt(i - 1)
	if !ok || r32-in.start >= in.size {
		return "", false
	}
	base, ok := t.rules(in.rules)
	if !ok {
		return "", false
	}
	n := uint64(min(in.deltaN, maxDeltasPerInit))
	if uint64(in.deltaFirst)+n > t.nDelta {
		return "", false
	}
	var deltas []string
	if n > 0 {
		buf, ok := t.readAt(t.deltaOff+uint64(in.deltaFirst)*deltaRecSize, int(n)*deltaRecSize)
		if !ok {
			return "", false
		}
		for j := uint64(0); j < n; j++ {
			if le.Uint32(buf[j*deltaRecSize:]) > r32 {
				break
			}
			s, ok := t.rules(le.Uint32(buf[j*deltaRecSize+4:]))
			if !ok {
				return "", false
			}
			deltas = append(deltas, s)
		}
	}
	return foldRules(base, deltas), true
}

type rule struct{ reg, expr string }

// parseRules merges "reg: expr reg: expr" text into rs: a register named again
// replaces its expression in place, a new one is appended.
func parseRules(s string, rs []rule) []rule {
	var reg string
	var have bool
	var expr []string
	flush := func() {
		if !have {
			return
		}
		e := strings.Join(expr, " ")
		if j := slices.IndexFunc(rs, func(r rule) bool { return r.reg == reg }); j >= 0 {
			rs[j].expr = e
		} else {
			rs = append(rs, rule{reg, e})
		}
		expr = expr[:0]
	}
	for _, tok := range strings.Fields(s) {
		if len(tok) > 1 && strings.HasSuffix(tok, ":") {
			flush()
			reg, have = tok, true
			continue
		}
		expr = append(expr, tok)
	}
	flush()
	return rs
}

func foldRules(base string, deltas []string) string {
	rs := parseRules(base, nil)
	for _, d := range deltas {
		rs = parseRules(d, rs)
	}
	var b bytes.Buffer
	for i, r := range rs {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(r.reg)
		if r.expr != "" {
			b.WriteByte(' ')
			b.WriteString(r.expr)
		}
	}
	return b.String()
}
