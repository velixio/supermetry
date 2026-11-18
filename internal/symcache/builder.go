package symcache

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
)

// runRecords is how many records of each table sit in memory before they are
// sorted and spilled to a temporary file, which keeps the builder's heap near
// 50 MiB however large the input is.
const runRecords = 1 << 20

// spiller collects records of one table, spills sorted runs to unlinked
// temporary files and merges them back in order. The merge is stable: records
// that compare equal come out in insertion order.
type spiller[T any] struct {
	size   int
	put    func(T, []byte)
	get    func([]byte) T
	cmp    func(a, b T) int
	tmpDir string
	buf    []T
	runs   []*os.File
	n      int
}

func (s *spiller[T]) add(r T) error {
	s.buf = append(s.buf, r)
	s.n++
	if len(s.buf) >= runRecords {
		return s.spill()
	}
	return nil
}

func (s *spiller[T]) spill() error {
	slices.SortStableFunc(s.buf, s.cmp)
	f, err := os.CreateTemp(s.tmpDir, "symcache-run-*")
	if err != nil {
		return err
	}
	_ = os.Remove(f.Name()) // the open descriptor keeps the data; nothing is left behind on a crash
	w := bufio.NewWriterSize(f, 256<<10)
	rec := make([]byte, s.size)
	for _, r := range s.buf {
		s.put(r, rec)
		if _, err := w.Write(rec); err != nil {
			f.Close()
			return err
		}
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	s.runs = append(s.runs, f)
	s.buf = s.buf[:0]
	return nil
}

// merge feeds every record to emit in sorted order, then releases the runs.
func (s *spiller[T]) merge(emit func(T) error) (err error) {
	defer s.close()
	if len(s.runs) == 0 {
		slices.SortStableFunc(s.buf, s.cmp)
		for _, r := range s.buf {
			if err := emit(r); err != nil {
				return err
			}
		}
		return nil
	}
	if len(s.buf) > 0 {
		if err := s.spill(); err != nil {
			return err
		}
	}
	type head struct {
		r   *bufio.Reader
		cur T
		ok  bool
	}
	heads := make([]head, len(s.runs))
	rec := make([]byte, s.size)
	next := func(h *head) error {
		if _, err := io.ReadFull(h.r, rec); err != nil {
			if err == io.EOF {
				h.ok = false
				return nil
			}
			return err
		}
		h.cur, h.ok = s.get(rec), true
		return nil
	}
	for i, f := range s.runs {
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return err
		}
		heads[i].r = bufio.NewReaderSize(f, 64<<10)
		if err := next(&heads[i]); err != nil {
			return err
		}
	}
	for {
		best := -1
		for i := range heads {
			if heads[i].ok && (best < 0 || s.cmp(heads[i].cur, heads[best].cur) < 0) {
				best = i
			}
		}
		if best < 0 {
			return nil
		}
		if err := emit(heads[best].cur); err != nil {
			return err
		}
		if err := next(&heads[best]); err != nil {
			return err
		}
	}
}

func (s *spiller[T]) close() {
	for _, f := range s.runs {
		f.Close()
	}
	s.runs, s.buf = nil, nil
}

// Builder accumulates the functions, lines and inlined calls of one image and
// writes them as a cache. Addresses are relative to the image base. Records
// are spilled to temporary files and merge-sorted, so the heap stays bounded
// by the run size and the distinct-string table, not by the input size.
// A Builder is single use and not safe for concurrent use.
type Builder struct {
	arch   string
	tmpDir string

	strs   map[string]uint32
	strTmp *os.File
	strW   *bufio.Writer
	strLen uint64

	funcs spiller[funcRec]
	lines spiller[lineRec]
	inls  spiller[inlRec]

	err     error
	dropped int
}

// NewBuilder returns a Builder for an image of the given architecture whose
// spill files live in tmpDir ("" means the system temporary directory).
func NewBuilder(arch string, tmpDir string) *Builder {
	b := &Builder{arch: arch, tmpDir: tmpDir, strs: map[string]uint32{"": 0}}
	b.funcs = spiller[funcRec]{size: funcRecSize, tmpDir: tmpDir,
		put: func(r funcRec, p []byte) { r.put(p) }, get: getFunc,
		cmp: func(a, c funcRec) int {
			if a.start != c.start {
				return cmpU32(a.start, c.start)
			}
			return cmpU32(c.end, a.end) // the wider range first
		}}
	b.lines = spiller[lineRec]{size: lineRecSize, tmpDir: tmpDir,
		put: func(r lineRec, p []byte) { r.put(p) }, get: getLine,
		cmp: func(a, c lineRec) int { return cmpU32(a.addr, c.addr) }}
	b.inls = spiller[inlRec]{size: inlRecSize, tmpDir: tmpDir,
		put: func(r inlRec, p []byte) { r.put(p) }, get: getInl,
		cmp: func(a, c inlRec) int {
			if a.start != c.start {
				return cmpU32(a.start, c.start)
			}
			if a.end != c.end {
				return cmpU32(c.end, a.end)
			}
			return cmpU32(uint32(a.depth), uint32(c.depth))
		}}
	return b
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

// Dropped reports how many records were ignored because their addresses were
// empty, inverted or beyond 32 bits.
func (b *Builder) Dropped() int { return b.dropped }

func (b *Builder) fail(err error) {
	if b.err == nil {
		b.err = err
	}
}

func (b *Builder) intern(s string) uint32 {
	if off, ok := b.strs[s]; ok {
		return off
	}
	if len(s) > maxStringLen {
		s = s[:maxStringLen]
		if off, ok := b.strs[s]; ok {
			return off
		}
	}
	if b.strTmp == nil {
		f, err := os.CreateTemp(b.tmpDir, "symcache-str-*")
		if err != nil {
			b.fail(err)
			return 0
		}
		_ = os.Remove(f.Name())
		b.strTmp, b.strW = f, bufio.NewWriterSize(f, 256<<10)
		_, _ = b.strW.Write([]byte{0, 0}) // offset 0: the empty string
		b.strLen = 2
	}
	if len(b.strs) >= maxStrings || b.strLen+uint64(len(s))+2 > maxStringBytes {
		b.fail(errors.New("symcache: too many distinct names"))
		return 0
	}
	off := uint32(b.strLen)
	var hdr [2]byte
	le.PutUint16(hdr[:], uint16(len(s)))
	_, _ = b.strW.Write(hdr[:])
	_, _ = b.strW.WriteString(s)
	b.strLen += uint64(len(s)) + 2
	b.strs[s] = off
	return off
}

func (b *Builder) ok(start, end uint64) bool {
	if end <= start || end > 1<<32 {
		b.dropped++
		return false
	}
	return b.err == nil
}

// AddFunction records a function covering [start, end).
func (b *Builder) AddFunction(start, end uint64, name string) {
	if !b.ok(start, end) {
		return
	}
	b.fail(b.funcs.add(funcRec{uint32(start), uint32(end), b.intern(name)}))
}

// AddLine records that code from addr up to the next line record belongs to
// file:line. A line of 0 ends the run: addresses after it have no line.
func (b *Builder) AddLine(addr uint64, file string, line uint32) {
	if addr >= 1<<32 {
		b.dropped++
		return
	}
	if b.err != nil {
		return
	}
	f := uint32(0)
	if line != 0 {
		f = b.intern(file)
	}
	b.fail(b.lines.add(lineRec{uint32(addr), f, line}))
}

// AddInline records a call inlined into the range [start, end) at the given
// nesting depth (1 for a call inlined directly into a function).
func (b *Builder) AddInline(start, end uint64, depth uint16, name, callFile string, callLine uint32) {
	if !b.ok(start, end) {
		return
	}
	b.fail(b.inls.add(inlRec{start: uint32(start), end: uint32(end), depth: depth,
		name: b.intern(name), callFile: b.intern(callFile), callLine: callLine}))
}

// Counts reports the records added so far.
func (b *Builder) Counts() (funcs, lines, inlines int) { return b.funcs.n, b.lines.n, b.inls.n }

// Close releases the temporary files; Write calls it.
func (b *Builder) Close() {
	b.funcs.close()
	b.lines.close()
	b.inls.close()
	if b.strTmp != nil {
		b.strTmp.Close()
		b.strTmp = nil
	}
}

// tmpTable is a merged, de-duplicated table spooled to an unlinked file.
type tmpTable struct {
	f *os.File
	w *bufio.Writer
	n uint64
}

func newTmpTable(dir string) (*tmpTable, error) {
	f, err := os.CreateTemp(dir, "symcache-tbl-*")
	if err != nil {
		return nil, err
	}
	_ = os.Remove(f.Name())
	return &tmpTable{f: f, w: bufio.NewWriterSize(f, 256<<10)}, nil
}

func (t *tmpTable) write(p []byte) error {
	t.n++
	_, err := t.w.Write(p)
	return err
}

func (t *tmpTable) finish() (int64, error) {
	if err := t.w.Flush(); err != nil {
		return 0, err
	}
	return int64(t.n), nil
}

// Write merges everything added and writes the cache. The Builder is spent
// afterwards.
func (b *Builder) Write(w io.Writer) (err error) {
	defer b.Close()
	if b.err != nil {
		return b.err
	}
	archOff := b.intern(b.arch)
	if b.err != nil {
		return b.err
	}
	if b.strTmp == nil {
		b.intern("-") // the string table always exists
		if b.err != nil {
			return b.err
		}
	}
	if err := b.strW.Flush(); err != nil {
		return err
	}

	ft, err := newTmpTable(b.tmpDir)
	if err != nil {
		return err
	}
	defer ft.f.Close()
	var prevEnd uint32
	rec := make([]byte, 24)
	if err := b.funcs.merge(func(r funcRec) error {
		if r.end <= r.start || r.start < prevEnd {
			return nil // empty, or inside the previous function
		}
		prevEnd = r.end
		r.put(rec)
		return ft.write(rec[:funcRecSize])
	}); err != nil {
		return err
	}

	lt, err := newTmpTable(b.tmpDir)
	if err != nil {
		return err
	}
	defer lt.f.Close()
	var pending, last lineRec
	var havePending, haveLast bool
	emitLine := func(r lineRec) error {
		if !haveLast && r.line == 0 {
			return nil
		}
		if haveLast && r.line == last.line && r.file == last.file {
			return nil
		}
		last, haveLast = r, true
		r.put(rec)
		return lt.write(rec[:lineRecSize])
	}
	if err := b.lines.merge(func(r lineRec) error {
		if havePending && r.addr == pending.addr {
			if r.line != 0 {
				pending = r
			}
			return nil
		}
		if havePending {
			if err := emitLine(pending); err != nil {
				return err
			}
		}
		pending, havePending = r, true
		return nil
	}); err != nil {
		return err
	}
	if havePending {
		if err := emitLine(pending); err != nil {
			return err
		}
	}

	it, err := newTmpTable(b.tmpDir)
	if err != nil {
		return err
	}
	defer it.f.Close()
	var maxLen uint32
	if err := b.inls.merge(func(r inlRec) error {
		if r.end <= r.start {
			return nil
		}
		maxLen = max(maxLen, r.end-r.start)
		r.put(rec)
		return it.write(rec[:inlRecSize])
	}); err != nil {
		return err
	}

	for _, t := range []*tmpTable{ft, lt, it} {
		if _, err := t.finish(); err != nil {
			return err
		}
	}
	h := header{version: Version, arch: archOff, maxInlineLen: maxLen,
		strOff: headerSize, strLen: b.strLen}
	h.funcOff, h.funcN = h.strOff+h.strLen, ft.n
	h.lineOff, h.lineN = h.funcOff+ft.n*funcRecSize, lt.n
	h.inlOff, h.inlN = h.lineOff+lt.n*lineRecSize, it.n

	bw := bufio.NewWriterSize(w, 256<<10)
	if _, err := bw.Write(h.encode()); err != nil {
		return err
	}
	for i, f := range []*os.File{b.strTmp, ft.f, lt.f, it.f} {
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return err
		}
		if _, err := io.Copy(bw, f); err != nil {
			return fmt.Errorf("symcache: write table %d: %w", i, err)
		}
	}
	return bw.Flush()
}
