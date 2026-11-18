package symcache

import (
	"context"
	"debug/dwarf"
	"debug/elf"
	"debug/macho"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"strings"

	"supermetry/internal/debugfile"
)

const (
	// maxDWARFBytes bounds the uncompressed DWARF the builder will load; the
	// stdlib parser needs the sections in memory.
	maxDWARFBytes = 2 << 30
	// maxLateItems bounds entries whose abstract origin appears after them in
	// another unit and so wait for the end of the walk.
	maxLateItems = 1 << 20
	// maxOriginHops bounds a chain of abstract_origin/specification references.
	maxOriginHops = 8
)

// Feature names stored with a built cache.
const (
	FeatureDebug  = "debug"  // DWARF functions and lines
	FeatureSymtab = "symtab" // symbol-table functions
)

// ErrNoSymbols marks an object with neither debug info nor symbols.
var ErrNoSymbols = errors.New("symcache: object has no symbols or debug info")

// BuildFromObject reads one ELF or Mach-O object (the slice obj.Slice of a fat
// Mach-O) and writes its symbol cache to w. Names come from DWARF
// (DW_AT_name, readable for C, Go and most Swift and C++) with the symbol table
// filling functions DWARF does not cover; symbol-table-only names stay mangled
// because no demangler is included. Split DWARF (.dwo/.dwp) is not followed,
// such an object yields a symbol-table-only cache.
func BuildFromObject(obj debugfile.Object, r io.ReaderAt, size int64, w io.Writer, tmpDir string) (features []string, err error) {
	defer func() {
		if p := recover(); p != nil {
			features, err = nil, fmt.Errorf("symcache: malformed object: %v", p)
		}
	}()
	switch obj.Kind {
	case "elf":
		return buildELF(obj, r, size, w, tmpDir)
	case "macho":
		return buildMacho(obj, r, size, w, tmpDir)
	}
	return nil, fmt.Errorf("symcache: cannot build a cache from kind %q", obj.Kind)
}

// objBuilder carries the state shared by the ELF and Mach-O front ends.
type objBuilder struct {
	b       *Builder
	base    uint64
	covered [][2]uint64 // function ranges DWARF reported (absolute addresses)
	debug   bool
	symtab  bool
	// names maps DWARF entries to their names, for abstract_origin and
	// specification references; origins chains entries without a name.
	names   map[dwarf.Offset]string
	origins map[dwarf.Offset]dwarf.Offset
	late    []item
}

type item struct {
	inline    bool
	ranges    [][2]uint64
	name      string
	origin    dwarf.Offset
	depth     uint16
	callFile  string
	callLine  uint32
	hasOrigin bool
}

func newObjBuilder(arch, tmpDir string, base uint64) *objBuilder {
	return &objBuilder{b: NewBuilder(arch, tmpDir), base: base,
		names: map[dwarf.Offset]string{}, origins: map[dwarf.Offset]dwarf.Offset{}}
}

func (o *objBuilder) rel(addr uint64) (uint64, bool) {
	if addr < o.base || addr-o.base >= 1<<32 {
		return 0, false
	}
	return addr - o.base, true
}

func (o *objBuilder) finish(w io.Writer) ([]string, error) {
	defer o.b.Close()
	nf, _, _ := o.b.Counts()
	if nf == 0 {
		return nil, ErrNoSymbols
	}
	if err := o.b.Write(w); err != nil {
		return nil, err
	}
	var feats []string
	if o.symtab {
		feats = append(feats, FeatureSymtab)
	}
	if o.debug {
		feats = append(feats, FeatureDebug)
	}
	return feats, nil
}

// nameOf follows origin references to a name; ok is false when the chain ends
// at an entry not seen yet.
func (o *objBuilder) nameOf(off dwarf.Offset) (string, bool) {
	for range maxOriginHops {
		if n, ok := o.names[off]; ok {
			return n, true
		}
		next, ok := o.origins[off]
		if !ok {
			return "", false
		}
		off = next
	}
	return "", false
}

func (o *objBuilder) emit(it item) {
	name := it.name
	if name == "" && it.hasOrigin {
		n, ok := o.nameOf(it.origin)
		if !ok {
			if len(o.late) < maxLateItems {
				o.late = append(o.late, it)
			}
			return
		}
		name = n
	}
	if name == "" {
		return
	}
	for _, rg := range it.ranges {
		lo, ok := o.rel(rg[0])
		if !ok || rg[1] <= rg[0] {
			continue
		}
		hi := rg[1] - o.base
		if it.inline {
			o.b.AddInline(lo, hi, it.depth, name, it.callFile, it.callLine)
		} else {
			o.b.AddFunction(lo, hi, name)
			o.covered = append(o.covered, rg)
			o.debug = true
		}
	}
}

// walkDWARF reads every compilation unit: its line program, then its
// subprograms and inlined calls.
func (o *objBuilder) walkDWARF(d *dwarf.Data) {
	rd := d.Reader()
	for {
		e, err := rd.Next()
		if err != nil || e == nil {
			break
		}
		if e.Tag != dwarf.TagCompileUnit {
			if e.Children {
				rd.SkipChildren()
			}
			continue
		}
		files := o.lines(d, e)
		if !e.Children {
			continue
		}
		var stack []bool // per open entry: true for an inlined subroutine
		inl := 0
	unit:
		for {
			c, err := rd.Next()
			if err != nil || c == nil {
				return
			}
			if c.Tag == 0 {
				if len(stack) == 0 {
					break unit
				}
				if stack[len(stack)-1] {
					inl--
				}
				stack = stack[:len(stack)-1]
				continue
			}
			switch c.Tag {
			case dwarf.TagSubprogram:
				o.subprogram(d, c)
			case dwarf.TagInlinedSubroutine:
				o.inlined(d, c, files, inl)
			}
			if c.Children {
				isInl := c.Tag == dwarf.TagInlinedSubroutine
				stack = append(stack, isInl)
				if isInl {
					inl++
				}
			}
		}
	}
	for _, it := range o.late {
		o.emitLate(it)
	}
	o.late = nil
}

func (o *objBuilder) emitLate(it item) {
	if n, ok := o.nameOf(it.origin); ok {
		it.name, it.hasOrigin = n, false
		o.emit(it)
	}
}

func ref(e *dwarf.Entry, attr dwarf.Attr) (dwarf.Offset, bool) {
	v, ok := e.Val(attr).(dwarf.Offset)
	return v, ok
}

func (o *objBuilder) subprogram(d *dwarf.Data, e *dwarf.Entry) {
	it := item{}
	if n, ok := e.Val(dwarf.AttrName).(string); ok && n != "" {
		o.names[e.Offset] = n
		it.name = n
	} else if off, ok := ref(e, dwarf.AttrAbstractOrigin); ok {
		o.origins[e.Offset] = off
		it.origin, it.hasOrigin = off, true
	} else if off, ok := ref(e, dwarf.AttrSpecification); ok {
		o.origins[e.Offset] = off
		it.origin, it.hasOrigin = off, true
	}
	rs, err := d.Ranges(e)
	if err != nil || len(rs) == 0 {
		return
	}
	it.ranges = rs
	o.emit(it)
}

func (o *objBuilder) inlined(d *dwarf.Data, e *dwarf.Entry, files []string, depth int) {
	it := item{inline: true, depth: uint16(min(depth+1, 0xffff))}
	off, ok := ref(e, dwarf.AttrAbstractOrigin)
	if !ok {
		return
	}
	it.origin, it.hasOrigin = off, true
	if i, ok := e.Val(dwarf.AttrCallFile).(int64); ok && i >= 0 && i < int64(len(files)) {
		it.callFile = files[i]
	}
	if l, ok := e.Val(dwarf.AttrCallLine).(int64); ok && l > 0 && l < 1<<32 {
		it.callLine = uint32(l)
	}
	rs, err := d.Ranges(e)
	if err != nil || len(rs) == 0 {
		return
	}
	it.ranges = rs
	o.emit(it)
}

// lines feeds the unit's line program to the builder and returns its file
// table, indexed by file number.
func (o *objBuilder) lines(d *dwarf.Data, cu *dwarf.Entry) []string {
	lr, err := d.LineReader(cu)
	if err != nil || lr == nil {
		return nil
	}
	var le dwarf.LineEntry
	skip, newSeq := false, true
	for {
		if err := lr.Next(&le); err != nil {
			break
		}
		if newSeq {
			// A sequence starting at 0 or below the image is dead code the
			// linker discarded.
			skip = le.Address == 0 || le.Address < o.base
			newSeq = false
		}
		if le.EndSequence {
			if !skip {
				if rel, ok := o.rel(le.Address); ok {
					o.b.AddLine(rel, "", 0)
				}
			}
			newSeq = true
			continue
		}
		// Rows that are not statement starts are kept: compilers emit them for
		// real code (binutils addr2line answers from them too).
		if skip || le.File == nil {
			continue
		}
		if rel, ok := o.rel(le.Address); ok {
			o.b.AddLine(rel, le.File.Name, uint32(max(le.Line, 0)))
			o.debug = true
		}
	}
	fs := lr.Files()
	names := make([]string, len(fs))
	for i, f := range fs {
		if f != nil {
			names[i] = f.Name
		}
	}
	return names
}

type symFn struct {
	start, size uint64
	name        string
}

// addSymbols adds functions from the symbol table that DWARF did not cover.
// A symbol without a size ends where the next one starts; limit is the end of
// the code, used for the last one.
func (o *objBuilder) addSymbols(syms []symFn, limit uint64) {
	slices.SortStableFunc(syms, func(a, b symFn) int {
		switch {
		case a.start < b.start:
			return -1
		case a.start > b.start:
			return 1
		}
		return 0
	})
	// merge DWARF ranges into disjoint, sorted spans
	cov := slices.Clone(o.covered)
	slices.SortFunc(cov, func(a, b [2]uint64) int {
		switch {
		case a[0] < b[0]:
			return -1
		case a[0] > b[0]:
			return 1
		}
		return 0
	})
	var merged [][2]uint64
	for _, c := range cov {
		if n := len(merged); n > 0 && c[0] <= merged[n-1][1] {
			merged[n-1][1] = max(merged[n-1][1], c[1])
		} else {
			merged = append(merged, c)
		}
	}
	for i, s := range syms {
		if i > 0 && syms[i-1].start == s.start {
			continue
		}
		end := s.start + s.size
		if s.size == 0 {
			end = limit
			for _, n := range syms[i+1:] {
				if n.start > s.start {
					end = n.start
					break
				}
			}
		}
		// a symbol inside DWARF coverage is already described
		j := sort.Search(len(merged), func(k int) bool { return merged[k][1] > s.start })
		if j < len(merged) {
			if merged[j][0] <= s.start {
				continue
			}
			end = min(end, merged[j][0])
		}
		rel, ok := o.rel(s.start)
		if !ok || end <= s.start || s.name == "" {
			continue
		}
		o.b.AddFunction(rel, end-o.base, s.name)
		o.symtab = true
	}
}

const maxSymbols = 8 << 20

func buildELF(obj debugfile.Object, r io.ReaderAt, size int64, w io.Writer, tmpDir string) ([]string, error) {
	f, err := elf.NewFile(io.NewSectionReader(r, 0, size))
	if err != nil {
		return nil, fmt.Errorf("symcache: %w", err)
	}
	defer f.Close()
	var base, end uint64
	haveBase := false
	for _, p := range f.Progs {
		if p.Type != elf.PT_LOAD {
			continue
		}
		if !haveBase || p.Vaddr < base {
			base, haveBase = p.Vaddr, true
		}
		if p.Flags&elf.PF_X != 0 {
			end = max(end, p.Vaddr+p.Memsz)
		}
	}
	if end == 0 {
		for _, s := range f.Sections { // relocatable or section-only objects
			if s.Flags&elf.SHF_EXECINSTR != 0 {
				end = max(end, s.Addr+s.Size)
			}
		}
	}
	if end > base && end-base >= 1<<32 {
		return nil, ErrTooLarge
	}
	o := newObjBuilder(obj.Arch, tmpDir, base)
	defer o.b.Close()
	if dwarfSize(f) <= maxDWARFBytes {
		if d, err := f.DWARF(); err == nil {
			o.walkDWARF(d)
		}
	}
	var syms []symFn
	for _, get := range []func() ([]elf.Symbol, error){f.Symbols, f.DynamicSymbols} {
		ss, _ := get()
		for _, s := range ss {
			t := elf.ST_TYPE(s.Info)
			if (t != elf.STT_FUNC && t != elf.STT_GNU_IFUNC) || s.Section == elf.SHN_UNDEF || s.Value == 0 {
				continue
			}
			if len(syms) >= maxSymbols {
				break
			}
			syms = append(syms, symFn{s.Value, s.Size, s.Name})
		}
	}
	o.addSymbols(syms, end)
	return o.finish(w)
}

// dwarfSize sums the uncompressed sizes of the DWARF sections.
func dwarfSize(f *elf.File) uint64 {
	var n uint64
	for _, s := range f.Sections {
		if (strings.HasPrefix(s.Name, ".debug_") || strings.HasPrefix(s.Name, ".zdebug_")) && s.Type != elf.SHT_NOBITS {
			n += s.Size
		}
	}
	return n
}

func buildMacho(obj debugfile.Object, r io.ReaderAt, size int64, w io.Writer, tmpDir string) ([]string, error) {
	off, n, err := machoSlice(r, size, obj.Slice)
	if err != nil {
		return nil, err
	}
	sr := io.NewSectionReader(r, off, n)
	if err := precheckMacho(sr, n); err != nil {
		return nil, err
	}
	f, err := macho.NewFile(sr)
	if err != nil {
		return nil, fmt.Errorf("symcache: %w", err)
	}
	defer f.Close()
	text := f.Segment("__TEXT")
	if text == nil {
		return nil, errors.New("symcache: Mach-O has no __TEXT segment")
	}
	base := text.Addr
	if text.Memsz >= 1<<32 {
		return nil, ErrTooLarge
	}
	o := newObjBuilder(obj.Arch, tmpDir, base)
	defer o.b.Close()
	var dw uint64
	for _, s := range f.Sections {
		if strings.HasPrefix(s.Name, "__debug_") || strings.HasPrefix(s.Name, "__zdebug_") {
			dw += s.Size
		}
	}
	if dw <= maxDWARFBytes {
		if d, err := f.DWARF(); err == nil {
			o.walkDWARF(d)
		}
	}
	var syms []symFn
	if f.Symtab != nil {
		for _, s := range f.Symtab.Syms {
			if s.Type&0xe0 != 0 || s.Type&0x0e != 0x0e || s.Sect == 0 || int(s.Sect) > len(f.Sections) {
				continue
			}
			sec := f.Sections[s.Sect-1]
			if sec.Flags&0x80000400 == 0 || s.Value == 0 { // pure or some instructions
				continue
			}
			if len(syms) >= maxSymbols {
				break
			}
			syms = append(syms, symFn{s.Value, 0, strings.TrimPrefix(s.Name, "_")})
		}
	}
	o.addSymbols(syms, text.Addr+text.Memsz)
	return o.finish(w)
}

// machoSlice locates the object inside a thin or fat Mach-O file.
func machoSlice(r io.ReaderAt, size int64, slice int) (off, n int64, err error) {
	var hdr [8]byte
	if _, err := r.ReadAt(hdr[:], 0); err != nil {
		return 0, 0, errors.New("symcache: truncated Mach-O")
	}
	be := binary.BigEndian
	magic := be.Uint32(hdr[:])
	if magic != 0xcafebabe && magic != 0xcafebabf {
		if slice != 0 {
			return 0, 0, errors.New("symcache: slice index on a thin Mach-O")
		}
		return 0, size, nil
	}
	count := be.Uint32(hdr[4:])
	if slice < 0 || uint32(slice) >= count || count > 64 {
		return 0, 0, errors.New("symcache: no such fat slice")
	}
	entry := int64(20)
	if magic == 0xcafebabf {
		entry = 32
	}
	e := make([]byte, entry)
	if _, err := r.ReadAt(e, 8+int64(slice)*entry); err != nil {
		return 0, 0, errors.New("symcache: truncated fat header")
	}
	if magic == 0xcafebabe {
		off, n = int64(be.Uint32(e[8:])), int64(be.Uint32(e[12:]))
	} else {
		o, s := be.Uint64(e[8:]), be.Uint64(e[16:])
		if o > uint64(size) || s > uint64(size) {
			return 0, 0, errors.New("symcache: fat slice outside the file")
		}
		off, n = int64(o), int64(s)
	}
	if off < 0 || n <= 0 || off > size || n > size-off {
		return 0, 0, errors.New("symcache: fat slice outside the file")
	}
	return off, n, nil
}

// precheckMacho rejects headers whose load-command table cannot fit the file,
// before the stdlib allocates for it.
func precheckMacho(r io.ReaderAt, size int64) error {
	var h [32]byte
	if _, err := r.ReadAt(h[:], 0); err != nil {
		return errors.New("symcache: truncated Mach-O")
	}
	var bo binary.ByteOrder = binary.LittleEndian
	switch m := binary.LittleEndian.Uint32(h[:]); m {
	case 0xfeedface, 0xfeedfacf:
	case 0xcefaedfe, 0xcffaedfe:
		bo = binary.BigEndian
	default:
		return errors.New("symcache: not a Mach-O")
	}
	ncmds, sizeofcmds := bo.Uint32(h[16:]), bo.Uint32(h[20:])
	if int64(sizeofcmds) > size || sizeofcmds > 8<<20 || int64(ncmds) > int64(sizeofcmds)/8 {
		return errors.New("symcache: Mach-O load commands exceed the file")
	}
	return nil
}

// BuildFunc returns a build function with the signature of
// sentryapi.CacheBuilder.Build for ELF and Mach-O objects, spilling to tmpDir.
func BuildFunc(tmpDir string) func(ctx context.Context, obj debugfile.Object, raw io.ReaderAt, size int64, out io.Writer) ([]string, error) {
	return func(ctx context.Context, obj debugfile.Object, raw io.ReaderAt, size int64, out io.Writer) ([]string, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if tmpDir != "" {
			if err := os.MkdirAll(tmpDir, 0o700); err != nil {
				return nil, err
			}
		}
		return BuildFromObject(obj, raw, size, out, tmpDir)
	}
}
