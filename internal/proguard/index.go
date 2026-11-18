package proguard

import (
	"bufio"
	"container/heap"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
)

// Index file layout, all little-endian:
//
//	header  48 bytes: "SMPG", version u32, classCount u32, memberCount u32,
//	        classOff u64, memberOff u64, strOff u64, strLen u64 (only the
//	        low 8 bytes of the last field are used; the rest pads)
//	classes classCount x 32 bytes, sorted by (hash(obf), seq):
//	        hash u64, obf u32, orig u32, src u32, memStart u32, memCount u32, seq u32
//	members memberCount x 32 bytes, grouped by class and sorted inside it by
//	        (hash(obfName), obfStart, file order):
//	        obfName u32, nameHash u32, obfStart u32, obfEnd u32,
//	        origStart u32, origEnd u32, origClass u32, origName u32
//	strings u32 length + bytes, addressed by offset from strOff; offset 0 is ""
//
// Records are fixed width so lookups binary-search through io.ReaderAt without
// loading the file.
const (
	indexVersion = 1
	headerSize   = 48
	classRecSize = 32
	memRecSize   = 32
)

// memberRunBytes is the in-memory size of one sorted run of member records.
var memberRunBytes = 24 << 20

var magic = [4]byte{'S', 'M', 'P', 'G'}

// ErrTooLarge means the mapping needs more than the index format can address.
var ErrTooLarge = errors.New("proguard: mapping too large")

func hash64(s string) uint64 {
	h := uint64(14695981039346656037)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return h
}

func hash32(s string) uint32 { h := hash64(s); return uint32(h ^ h>>32) }

// BuildIndex reads a mapping file and writes its index to w. Memory use is
// bounded whatever the input size: records spill to temporary files in
// os.TempDir and are merged by an external sort.
func BuildIndex(r io.Reader, w io.Writer) error { return BuildIndexIn(r, w, "") }

// BuildIndexIn is BuildIndex with an explicit temporary directory ("" means
// the system default).
func BuildIndexIn(r io.Reader, w io.Writer, tmpDir string) error {
	b := &builder{tmp: tmpDir, dedupe: map[string]uint32{}}
	defer b.cleanup()
	return b.build(r, w)
}

type builder struct {
	tmp     string
	files   []*os.File
	strs    *bufio.Writer
	strsF   *os.File
	strPos  uint64
	dedupe  map[string]uint32
	classes *bufio.Writer
	classF  *os.File
	nClass  uint32
	members *sorter
	nMember uint64
	seq     uint32
}

func (b *builder) tempFile(pattern string) (*os.File, error) {
	f, err := os.CreateTemp(b.tmp, pattern)
	if err != nil {
		return nil, err
	}
	b.files = append(b.files, f)
	return f, nil
}

func (b *builder) cleanup() {
	for _, f := range b.files {
		_ = f.Close()
		_ = os.Remove(f.Name())
	}
	if b.members != nil {
		b.members.cleanup()
	}
}

// str appends s to the string file (deduplicating recent strings) and returns its offset.
func (b *builder) str(s string) (uint32, error) {
	if s == "" {
		return 0, nil
	}
	if off, ok := b.dedupe[s]; ok {
		return off, nil
	}
	if b.strPos+uint64(len(s))+4 >= 1<<32 {
		return 0, ErrTooLarge
	}
	off := uint32(b.strPos)
	var l [4]byte
	binary.LittleEndian.PutUint32(l[:], uint32(len(s)))
	if _, err := b.strs.Write(l[:]); err != nil {
		return 0, err
	}
	if _, err := b.strs.WriteString(s); err != nil {
		return 0, err
	}
	b.strPos += uint64(len(s)) + 4
	if len(b.dedupe) >= 1<<17 { // bounded: forget old strings rather than grow
		clear(b.dedupe)
	}
	b.dedupe[s] = off
	return off, nil
}

type curClass struct {
	orig, obf, src string
	seq            uint32
}

func (b *builder) build(r io.Reader, w io.Writer) error {
	var err error
	if b.strsF, err = b.tempFile("pgstr-*"); err != nil {
		return err
	}
	b.strs = bufio.NewWriterSize(b.strsF, 1<<16)
	b.strPos = 4 // offset 0 is the empty string
	if _, err := b.strs.Write([]byte{0, 0, 0, 0}); err != nil {
		return err
	}
	if b.classF, err = b.tempFile("pgcls-*"); err != nil {
		return err
	}
	b.classes = bufio.NewWriterSize(b.classF, 1<<16)
	b.members = newSorter(40, memberRunBytes, b.tmp, func(x, y []byte) bool {
		// classSeq, nameHash, obfStart, file order
		if a, c := binary.LittleEndian.Uint32(x), binary.LittleEndian.Uint32(y); a != c {
			return a < c
		}
		if a, c := binary.LittleEndian.Uint32(x[12:]), binary.LittleEndian.Uint32(y[12:]); a != c {
			return a < c
		}
		if a, c := binary.LittleEndian.Uint32(x[16:]), binary.LittleEndian.Uint32(y[16:]); a != c {
			return a < c
		}
		return binary.LittleEndian.Uint32(x[4:]) < binary.LittleEndian.Uint32(y[4:])
	})

	var cur *curClass
	flush := func() error {
		if cur == nil {
			return nil
		}
		var rec [24]byte
		binary.LittleEndian.PutUint64(rec[0:], hash64(cur.obf))
		for i, s := range []string{cur.obf, cur.orig, cur.src} {
			off, err := b.str(s)
			if err != nil {
				return err
			}
			binary.LittleEndian.PutUint32(rec[8+4*i:], off)
		}
		_, err := b.classes.Write(rec[:])
		b.nClass++
		cur = nil
		return err
	}

	br := bufio.NewReaderSize(r, 1<<16)
	for {
		line, err := readLine(br)
		if err != nil && err != io.EOF {
			return err
		}
		if t := trimLeft(line); len(t) > 0 {
			switch {
			case t[0] == '#':
				if cur != nil && cur.src == "" {
					if f, ok := sourceFile(string(t)); ok {
						cur.src = f
					}
				}
			case len(t) == len(line): // not indented: a class line
				if err := flush(); err != nil {
					return err
				}
				if orig, obf, ok := classLine(string(t)); ok {
					if b.nClass == 1<<31 {
						return ErrTooLarge
					}
					cur = &curClass{orig: orig, obf: obf, seq: b.nClass}
				}
			case cur != nil:
				if m, ok := methodLine(string(t)); ok {
					if err := b.addMember(cur.seq, m); err != nil {
						return err
					}
				}
			}
		}
		if err == io.EOF {
			break
		}
	}
	if err := flush(); err != nil {
		return err
	}
	if err := b.classes.Flush(); err != nil {
		return err
	}
	if err := b.strs.Flush(); err != nil {
		return err
	}
	return b.finish(w)
}

func (b *builder) addMember(classSeq uint32, m method) error {
	var rec [40]byte
	binary.LittleEndian.PutUint32(rec[0:], classSeq)
	binary.LittleEndian.PutUint32(rec[4:], b.seq)
	b.seq++
	if b.seq == 0 {
		return ErrTooLarge
	}
	name, err := b.str(m.obfName)
	if err != nil {
		return err
	}
	oc, err := b.str(m.origClass)
	if err != nil {
		return err
	}
	on, err := b.str(m.origName)
	if err != nil {
		return err
	}
	f := rec[8:]
	binary.LittleEndian.PutUint32(f[0:], name)
	binary.LittleEndian.PutUint32(f[4:], hash32(m.obfName))
	binary.LittleEndian.PutUint32(f[8:], m.obfStart)
	binary.LittleEndian.PutUint32(f[12:], m.obfEnd)
	binary.LittleEndian.PutUint32(f[16:], m.origStart)
	binary.LittleEndian.PutUint32(f[20:], m.origEnd)
	binary.LittleEndian.PutUint32(f[24:], oc)
	binary.LittleEndian.PutUint32(f[28:], on)
	b.nMember++
	return b.members.add(rec[:])
}

// finish assigns member ranges to classes, sorts the classes by obfuscated
// name hash and writes the index.
func (b *builder) finish(w io.Writer) error {
	memIt, err := b.members.iterate()
	if err != nil {
		return err
	}
	memF, err := b.tempFile("pgmem-*")
	if err != nil {
		return err
	}
	memW := bufio.NewWriterSize(memF, 1<<16)
	clsSort := newSorter(classRecSize, 16<<20, b.tmp, func(x, y []byte) bool {
		if a, c := binary.LittleEndian.Uint64(x), binary.LittleEndian.Uint64(y); a != c {
			return a < c
		}
		return binary.LittleEndian.Uint32(x[28:]) < binary.LittleEndian.Uint32(y[28:])
	})
	defer clsSort.cleanup()

	if _, err := b.classF.Seek(0, io.SeekStart); err != nil {
		return err
	}
	cr := bufio.NewReaderSize(b.classF, 1<<16)
	var memIdx, classIdx uint32
	var start, count uint32
	var crec [24]byte
	haveClass := false
	nextClass := func() error {
		if _, err := io.ReadFull(cr, crec[:]); err != nil {
			return err
		}
		haveClass = true
		start, count = memIdx, 0
		return nil
	}
	emit := func() error {
		var out [classRecSize]byte
		copy(out[0:20], crec[0:20])
		binary.LittleEndian.PutUint32(out[20:], start)
		binary.LittleEndian.PutUint32(out[24:], count)
		binary.LittleEndian.PutUint32(out[28:], classIdx)
		classIdx++
		haveClass = false
		return clsSort.add(out[:])
	}
	for {
		rec, ok, err := memIt.next()
		if err != nil {
			return err
		}
		if !ok {
			break
		}
		seq := binary.LittleEndian.Uint32(rec)
		for classIdx < seq || !haveClass {
			if haveClass {
				if err := emit(); err != nil {
					return err
				}
			}
			if err := nextClass(); err != nil {
				return err
			}
		}
		if _, err := memW.Write(rec[8:]); err != nil {
			return err
		}
		memIdx++
		count++
	}
	for classIdx < b.nClass {
		if !haveClass {
			if err := nextClass(); err != nil {
				return err
			}
		}
		if err := emit(); err != nil {
			return err
		}
	}
	if err := memW.Flush(); err != nil {
		return err
	}
	clsIt, err := clsSort.iterate()
	if err != nil {
		return err
	}

	classOff := uint64(headerSize)
	memberOff := classOff + uint64(b.nClass)*classRecSize
	strOff := memberOff + uint64(memIdx)*memRecSize
	var h [headerSize]byte
	copy(h[0:], magic[:])
	binary.LittleEndian.PutUint32(h[4:], indexVersion)
	binary.LittleEndian.PutUint32(h[8:], b.nClass)
	binary.LittleEndian.PutUint32(h[12:], memIdx)
	binary.LittleEndian.PutUint64(h[16:], classOff)
	binary.LittleEndian.PutUint64(h[24:], memberOff)
	binary.LittleEndian.PutUint64(h[32:], strOff)
	binary.LittleEndian.PutUint64(h[40:], b.strPos)
	bw := bufio.NewWriterSize(w, 1<<16)
	if _, err := bw.Write(h[:]); err != nil {
		return err
	}
	for {
		rec, ok, err := clsIt.next()
		if err != nil {
			return err
		}
		if !ok {
			break
		}
		if _, err := bw.Write(rec); err != nil {
			return err
		}
	}
	if _, err := memF.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if _, err := io.Copy(bw, memF); err != nil {
		return err
	}
	if _, err := b.strsF.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if _, err := io.Copy(bw, b.strsF); err != nil {
		return err
	}
	return bw.Flush()
}

// readLine returns the next line without its terminator. A line longer than
// maxLine is consumed and returned as empty. io.EOF comes with the last line.
func readLine(br *bufio.Reader) ([]byte, error) {
	line, err := br.ReadSlice('\n')
	if err == bufio.ErrBufferFull {
		for err == bufio.ErrBufferFull {
			_, err = br.ReadSlice('\n')
		}
		return nil, err
	}
	n := len(line)
	for n > 0 && (line[n-1] == '\n' || line[n-1] == '\r') {
		n--
	}
	line = line[:n]
	if len(line) > maxLine {
		line = nil
	}
	return line, err
}

func trimLeft(b []byte) []byte {
	for len(b) > 0 && (b[0] == ' ' || b[0] == '\t') {
		b = b[1:]
	}
	return b
}

// sorter is an external sort of fixed-size records: runs are sorted in memory
// and spilled to temporary files, then merged.
type sorter struct {
	size  int
	limit int
	tmp   string
	less  func(a, b []byte) bool
	buf   []byte
	runs  []*os.File
}

func newSorter(size, limit int, tmp string, less func(a, b []byte) bool) *sorter {
	return &sorter{size: size, limit: limit - limit%size, tmp: tmp, less: less}
}

func (s *sorter) add(rec []byte) error {
	s.buf = append(s.buf, rec...)
	if len(s.buf) >= s.limit {
		return s.spill()
	}
	return nil
}

type recs struct {
	buf  []byte
	size int
	less func(a, b []byte) bool
	tmp  []byte
}

func (r *recs) Len() int { return len(r.buf) / r.size }
func (r *recs) Less(i, j int) bool {
	return r.less(r.buf[i*r.size:(i+1)*r.size], r.buf[j*r.size:(j+1)*r.size])
}
func (r *recs) Swap(i, j int) {
	a, b := r.buf[i*r.size:(i+1)*r.size], r.buf[j*r.size:(j+1)*r.size]
	copy(r.tmp, a)
	copy(a, b)
	copy(b, r.tmp)
}

func (s *sorter) sortBuf() {
	sort.Sort(&recs{buf: s.buf, size: s.size, less: s.less, tmp: make([]byte, s.size)})
}

func (s *sorter) spill() error {
	s.sortBuf()
	f, err := os.CreateTemp(s.tmp, "pgrun-*")
	if err != nil {
		return err
	}
	s.runs = append(s.runs, f)
	if _, err := f.Write(s.buf); err != nil {
		return err
	}
	s.buf = s.buf[:0]
	return nil
}

func (s *sorter) cleanup() {
	for _, f := range s.runs {
		_ = f.Close()
		_ = os.Remove(f.Name())
	}
	s.runs = nil
}

type recIter interface {
	next() ([]byte, bool, error)
}

// iterate sorts what remains and returns the merged stream.
func (s *sorter) iterate() (recIter, error) {
	if len(s.runs) == 0 {
		s.sortBuf()
		return &memIter{buf: s.buf, size: s.size}, nil
	}
	if len(s.buf) > 0 {
		if err := s.spill(); err != nil {
			return nil, err
		}
	}
	m := &merger{less: s.less}
	for _, f := range s.runs {
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return nil, err
		}
		src := &runReader{r: bufio.NewReaderSize(f, 1<<16), rec: make([]byte, s.size)}
		ok, err := src.advance()
		if err != nil {
			return nil, err
		}
		if ok {
			m.h = append(m.h, src)
		}
	}
	heap.Init(m)
	return m, nil
}

type memIter struct {
	buf  []byte
	size int
}

func (m *memIter) next() ([]byte, bool, error) {
	if len(m.buf) < m.size {
		return nil, false, nil
	}
	rec := m.buf[:m.size]
	m.buf = m.buf[m.size:]
	return rec, true, nil
}

type runReader struct {
	r   *bufio.Reader
	rec []byte
}

func (s *runReader) advance() (bool, error) {
	_, err := io.ReadFull(s.r, s.rec)
	if err == io.EOF {
		return false, nil
	}
	return err == nil, err
}

type merger struct {
	h    []*runReader
	less func(a, b []byte) bool
	out  []byte
	err  error
}

func (m *merger) Len() int           { return len(m.h) }
func (m *merger) Less(i, j int) bool { return m.less(m.h[i].rec, m.h[j].rec) }
func (m *merger) Swap(i, j int)      { m.h[i], m.h[j] = m.h[j], m.h[i] }
func (m *merger) Push(x any)         { m.h = append(m.h, x.(*runReader)) }
func (m *merger) Pop() any {
	n := len(m.h)
	x := m.h[n-1]
	m.h = m.h[:n-1]
	return x
}

func (m *merger) next() ([]byte, bool, error) {
	if len(m.h) == 0 {
		return nil, false, nil
	}
	top := m.h[0]
	m.out = append(m.out[:0], top.rec...)
	ok, err := top.advance()
	if err != nil {
		return nil, false, err
	}
	if ok {
		heap.Fix(m, 0)
	} else {
		heap.Pop(m)
	}
	return m.out, true, nil
}

// Index answers deobfuscation queries from a built index through io.ReaderAt.
type Index struct {
	r                           io.ReaderAt
	nClass, nMember             uint32
	classOff, memberOff, strOff uint64
	strLen                      uint64
}

// OpenIndex validates the header and bounds of an index; counts larger than
// the file are rejected.
func OpenIndex(r io.ReaderAt, size int64) (*Index, error) {
	if size < headerSize {
		return nil, errors.New("proguard: index too short")
	}
	var h [headerSize]byte
	if _, err := r.ReadAt(h[:], 0); err != nil {
		return nil, fmt.Errorf("proguard: read header: %w", err)
	}
	if [4]byte(h[0:4]) != magic || binary.LittleEndian.Uint32(h[4:]) != indexVersion {
		return nil, errors.New("proguard: not an index of this version")
	}
	ix := &Index{
		r:         r,
		nClass:    binary.LittleEndian.Uint32(h[8:]),
		nMember:   binary.LittleEndian.Uint32(h[12:]),
		classOff:  binary.LittleEndian.Uint64(h[16:]),
		memberOff: binary.LittleEndian.Uint64(h[24:]),
		strOff:    binary.LittleEndian.Uint64(h[32:]),
		strLen:    binary.LittleEndian.Uint64(h[40:]),
	}
	sz := uint64(size)
	if ix.classOff != headerSize ||
		ix.memberOff != ix.classOff+uint64(ix.nClass)*classRecSize ||
		ix.strOff != ix.memberOff+uint64(ix.nMember)*memRecSize ||
		ix.strLen > sz || ix.strOff > sz-ix.strLen || ix.strOff+ix.strLen != sz {
		return nil, errors.New("proguard: index sections do not fit the file")
	}
	return ix, nil
}

func (ix *Index) str(off uint32) (string, error) {
	if off == 0 {
		return "", nil
	}
	if uint64(off)+4 > ix.strLen {
		return "", errors.New("proguard: string offset out of range")
	}
	var l [4]byte
	if _, err := ix.r.ReadAt(l[:], int64(ix.strOff)+int64(off)); err != nil {
		return "", err
	}
	n := binary.LittleEndian.Uint32(l[:])
	if n > maxString || uint64(off)+4+uint64(n) > ix.strLen {
		return "", errors.New("proguard: string out of range")
	}
	buf := make([]byte, n)
	if _, err := ix.r.ReadAt(buf, int64(ix.strOff)+int64(off)+4); err != nil {
		return "", err
	}
	return string(buf), nil
}
