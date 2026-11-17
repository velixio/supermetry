package debugfile

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// Object is one debug object found in an uploaded file.
type Object struct {
	Kind     string // macho, elf, pe, breakpad, proguard, sourcebundle, other
	DebugID  string
	CodeID   string
	Arch     string
	Name     string
	Features []string
	// Slice is the index of the architecture in a fat Mach-O, else 0.
	Slice int
}

const (
	// maxTable bounds any header table read into memory (load commands, note
	// segments, section name tables, program headers).
	maxTable = 8 << 20
	// maxObjects bounds the architectures of a fat Mach-O.
	maxObjects = 64
	// maxSections bounds the section headers Detect walks.
	maxSections = 4096
	// sniffLen is how much of a text file is read to recognise it.
	sniffLen = 8 << 10
	// foldLen is how much of .text an ELF without a build-id is hashed over.
	foldLen = 4096
	// maxNameLen clips the upload name kept on an object.
	maxNameLen = 1024
)

// ErrUnsupported marks a file whose format is recognised but malformed.
var ErrUnsupported = errors.New("malformed debug file")

var (
	proguardNameRE = regexp.MustCompile(`(?:^|/)proguard/([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})\.txt$`)
	proguardLineRE = regexp.MustCompile(`^[^\s:]+ -> [^\s:]+:$`)
)

// Detect identifies the debug objects in a file of the given size. It never
// reads more than a few MiB regardless of what the headers claim, and returns
// an error for a recognised-but-broken file or one with no usable object.
// An unrecognised file is a single object of kind "other".
func Detect(r io.ReaderAt, size int64, uploadName string) ([]Object, error) {
	if size < 0 {
		return nil, fmt.Errorf("%w: negative size", ErrUnsupported)
	}
	name := clipName(uploadName)
	if m := proguardNameRE.FindStringSubmatch(uploadName); m != nil {
		return []Object{{Kind: "proguard", DebugID: NormalizeDebugID(m[1]), Name: name, Features: []string{"mapping"}}}, nil
	}
	var magic [4]byte
	n, _ := r.ReadAt(magic[:], 0)
	if int64(n) > size {
		n = int(size)
	}
	if n < 4 {
		return []Object{{Kind: "other", Name: name}}, nil
	}
	be := binary.BigEndian.Uint32(magic[:])
	le := binary.LittleEndian.Uint32(magic[:])
	switch {
	case string(magic[:]) == "\x7fELF":
		o, err := detectELF(r, size, name)
		if err != nil {
			return nil, err
		}
		return []Object{o}, nil
	case be == 0xcafebabe || be == 0xcafebabf:
		if objs, ok, err := detectFat(r, size, name, be == 0xcafebabf); ok || err != nil {
			return objs, err
		}
	case be == 0xfeedface || be == 0xfeedfacf || le == 0xfeedface || le == 0xfeedfacf:
		o, err := detectMacho(r, size, name, 0)
		if err != nil {
			return nil, err
		}
		return []Object{o}, nil
	case string(magic[:]) == "SYSB":
		return []Object{{Kind: "sourcebundle", Name: name, Features: []string{"sources"}}}, nil
	case magic[0] == 'M' && magic[1] == 'Z':
		if isPE(r, size) {
			return []Object{{Kind: "pe", Name: name}}, nil
		}
	}
	head, err := readHead(r, size, sniffLen)
	if err != nil {
		return nil, err
	}
	if bytes.HasPrefix(head, []byte("MODULE ")) {
		return detectBreakpad(head, name)
	}
	if looksLikeProguard(head) {
		return []Object{{Kind: "proguard", Name: name, Features: []string{"mapping"}}}, nil
	}
	return []Object{{Kind: "other", Name: name}}, nil
}

func clipName(s string) string {
	if len(s) > maxNameLen {
		s = s[:maxNameLen]
	}
	return strings.ToValidUTF8(strings.ReplaceAll(s, "\x00", ""), "")
}

func readHead(r io.ReaderAt, size, n int64) ([]byte, error) {
	n = min(n, size)
	buf := make([]byte, n)
	got, err := r.ReadAt(buf, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return buf[:got], nil
}

// readAt reads exactly n bytes at off, failing when the range leaves the file
// or n exceeds the table cap. The size is checked before allocating.
func readAt(r io.ReaderAt, size, off, n int64) ([]byte, error) {
	if off < 0 || n < 0 || n > maxTable || off > size || n > size-off {
		return nil, fmt.Errorf("%w: truncated", ErrUnsupported)
	}
	buf := make([]byte, n)
	if _, err := r.ReadAt(buf, off); err != nil && !(errors.Is(err, io.EOF) && n == 0) {
		return nil, fmt.Errorf("%w: truncated", ErrUnsupported)
	}
	return buf, nil
}

func isPE(r io.ReaderAt, size int64) bool {
	buf, err := readAt(r, size, 0x3c, 4)
	if err != nil {
		return false
	}
	off := int64(binary.LittleEndian.Uint32(buf))
	sig, err := readAt(r, size, off, 4)
	return err == nil && string(sig) == "PE\x00\x00"
}

func detectBreakpad(head []byte, name string) ([]Object, error) {
	line, _, _ := bytes.Cut(head, []byte("\n"))
	f := strings.Fields(string(line))
	// MODULE <os> <arch> <id> [name]
	if len(f) < 4 {
		return nil, fmt.Errorf("%w: invalid breakpad module header", ErrUnsupported)
	}
	id := NormalizeDebugID(f[3])
	if id == "" {
		return nil, fmt.Errorf("%w: invalid breakpad debug id", ErrUnsupported)
	}
	return []Object{{Kind: "breakpad", DebugID: id, Arch: clipArch(f[2]), Name: name, Features: []string{"symtab"}}}, nil
}

func looksLikeProguard(head []byte) bool {
	for _, line := range bytes.Split(head, []byte("\n")) {
		s := strings.TrimRight(string(line), "\r")
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		return proguardLineRE.MatchString(s)
	}
	return false
}

func clipArch(s string) string {
	if len(s) > 32 {
		s = s[:32]
	}
	return strings.ToValidUTF8(s, "")
}

// ---------------------------------------------------------------- ELF

var elfArch = map[uint16]string{
	3: "x86", 62: "x86_64", 40: "arm", 183: "arm64", 243: "riscv", 8: "mips", 20: "ppc", 21: "ppc64", 22: "s390x", 258: "loongarch",
}

func detectELF(r io.ReaderAt, size int64, name string) (Object, error) {
	hdr, err := readAt(r, size, 0, 64)
	if err != nil {
		hdr, err = readAt(r, size, 0, min(size, 52))
		if err != nil || len(hdr) < 52 {
			return Object{}, fmt.Errorf("%w: truncated ELF header", ErrUnsupported)
		}
	}
	var bo binary.ByteOrder
	switch hdr[5] {
	case 1:
		bo = binary.LittleEndian
	case 2:
		bo = binary.BigEndian
	default:
		return Object{}, fmt.Errorf("%w: unknown ELF byte order", ErrUnsupported)
	}
	var phoff, shoff int64
	var phentsize, phnum, shentsize, shnum, shstrndx uint16
	switch hdr[4] {
	case 1: // 32-bit
		phoff, shoff = int64(bo.Uint32(hdr[28:])), int64(bo.Uint32(hdr[32:]))
		phentsize, phnum = bo.Uint16(hdr[42:]), bo.Uint16(hdr[44:])
		shentsize, shnum, shstrndx = bo.Uint16(hdr[46:]), bo.Uint16(hdr[48:]), bo.Uint16(hdr[50:])
	case 2: // 64-bit
		if len(hdr) < 64 {
			return Object{}, fmt.Errorf("%w: truncated ELF header", ErrUnsupported)
		}
		o1, o2 := bo.Uint64(hdr[32:]), bo.Uint64(hdr[40:])
		if o1 > uint64(size) || o2 > uint64(size) {
			return Object{}, fmt.Errorf("%w: ELF table offset beyond the file", ErrUnsupported)
		}
		phoff, shoff = int64(o1), int64(o2)
		phentsize, phnum = bo.Uint16(hdr[54:]), bo.Uint16(hdr[56:])
		shentsize, shnum, shstrndx = bo.Uint16(hdr[58:]), bo.Uint16(hdr[60:]), bo.Uint16(hdr[62:])
	default:
		return Object{}, fmt.Errorf("%w: unknown ELF class", ErrUnsupported)
	}
	is64 := hdr[4] == 2
	arch := elfArch[bo.Uint16(hdr[18:])]
	if arch == "" {
		arch = "unknown"
	}
	obj := Object{Kind: "elf", Arch: arch, Name: name}

	// Program headers: the PT_NOTE segments hold the GNU build-id.
	var buildID []byte
	if phnum > 0 && phentsize > 0 {
		minEnt := int64(32)
		if is64 {
			minEnt = 56
		}
		if int64(phentsize) < minEnt {
			return Object{}, fmt.Errorf("%w: ELF program header size", ErrUnsupported)
		}
		table, err := readAt(r, size, phoff, int64(phnum)*int64(phentsize))
		if err != nil {
			return Object{}, fmt.Errorf("%w: ELF program headers beyond the file", ErrUnsupported)
		}
		for i := 0; i < int(phnum) && buildID == nil; i++ {
			e := table[i*int(phentsize):]
			var typ uint32 = bo.Uint32(e)
			var off, filesz int64
			if is64 {
				off, filesz = int64(bo.Uint64(e[8:])&(1<<62-1)), int64(bo.Uint64(e[32:])&(1<<62-1))
			} else {
				off, filesz = int64(bo.Uint32(e[4:])), int64(bo.Uint32(e[16:]))
			}
			if typ != 4 || filesz <= 0 || filesz > maxTable {
				continue
			}
			notes, err := readAt(r, size, off, filesz)
			if err != nil {
				continue // a lying note segment is ignored; the sections may still tell
			}
			buildID = findBuildID(notes, bo)
		}
	}

	// Section headers: features, and the .note.gnu.build-id / .text fallbacks.
	var text struct{ off, size int64 }
	if shnum > 0 && shentsize >= 40 && shoff > 0 {
		if int(shnum) > maxSections {
			return Object{}, fmt.Errorf("%w: too many ELF sections", ErrUnsupported)
		}
		table, err := readAt(r, size, shoff, int64(shnum)*int64(shentsize))
		if err != nil {
			return Object{}, fmt.Errorf("%w: ELF section headers beyond the file", ErrUnsupported)
		}
		type sec struct {
			name, typ  uint32
			off, size  int64
			nameOffset uint32
		}
		secs := make([]sec, shnum)
		for i := range secs {
			e := table[i*int(shentsize):]
			s := sec{name: bo.Uint32(e), typ: bo.Uint32(e[4:])}
			if is64 && int(shentsize) >= 64 {
				s.off, s.size = int64(bo.Uint64(e[24:])&(1<<62-1)), int64(bo.Uint64(e[32:])&(1<<62-1))
			} else if !is64 {
				s.off, s.size = int64(bo.Uint32(e[16:])), int64(bo.Uint32(e[20:]))
			}
			secs[i] = s
		}
		var strtab []byte
		if int(shstrndx) < len(secs) && secs[shstrndx].typ != 8 {
			if st := secs[shstrndx]; st.size > 0 && st.size <= maxTable {
				strtab, _ = readAt(r, size, st.off, st.size)
			}
		}
		secName := func(off uint32) string {
			if int64(off) >= int64(len(strtab)) {
				return ""
			}
			b := strtab[off:]
			if i := bytes.IndexByte(b, 0); i >= 0 {
				b = b[:i]
			}
			if len(b) > 64 {
				b = b[:64]
			}
			return string(b)
		}
		feat := map[string]bool{}
		for _, s := range secs {
			n := secName(s.name)
			nobits := s.typ == 8
			switch {
			case s.typ == 2 && s.size > 0: // SHT_SYMTAB
				feat["symtab"] = true
			case n == ".debug_info" && s.size > 0 && !nobits:
				feat["debug"] = true
			case (n == ".eh_frame" || n == ".debug_frame") && s.size > 0 && !nobits:
				feat["unwind"] = true
			case n == ".text" && s.size > 0 && !nobits:
				feat["code"] = true
				text.off, text.size = s.off, s.size
			case n == ".note.gnu.build-id" && buildID == nil && s.size > 0 && s.size <= maxTable && !nobits:
				if notes, err := readAt(r, size, s.off, s.size); err == nil {
					buildID = findBuildID(notes, bo)
				}
			}
		}
		for _, f := range []string{"symtab", "debug", "unwind", "code"} {
			if feat[f] {
				obj.Features = append(obj.Features, f)
			}
		}
	}

	switch {
	case len(buildID) > 0:
		obj.DebugID = DebugIDFromBuildID(buildID)
		obj.CodeID = fmt.Sprintf("%x", buildID)
	case text.size > 0:
		n := min(text.size, foldLen)
		data, err := readAt(r, size, text.off, n)
		if err != nil {
			return Object{}, fmt.Errorf("%w: ELF .text beyond the file", ErrUnsupported)
		}
		var fold [16]byte
		for i, b := range data {
			fold[i%16] ^= b
		}
		obj.DebugID = formatUUID(swapUUID(fold))
	default:
		return Object{}, fmt.Errorf("%w: ELF has no build-id and no code section", ErrUnsupported)
	}
	return obj, nil
}

// findBuildID walks an ELF note blob for the GNU build-id note.
func findBuildID(notes []byte, bo binary.ByteOrder) []byte {
	for len(notes) >= 12 {
		namesz, descsz, typ := int64(bo.Uint32(notes)), int64(bo.Uint32(notes[4:])), bo.Uint32(notes[8:])
		nameEnd := 12 + (namesz+3)&^3
		descEnd := nameEnd + (descsz+3)&^3
		if namesz < 0 || descsz < 0 || nameEnd > int64(len(notes)) || nameEnd+descsz > int64(len(notes)) {
			return nil
		}
		if typ == 3 && namesz == 4 && string(notes[12:16]) == "GNU\x00" && descsz > 0 && descsz <= 64 {
			return append([]byte(nil), notes[nameEnd:nameEnd+descsz]...)
		}
		if descEnd > int64(len(notes)) {
			return nil
		}
		notes = notes[descEnd:]
	}
	return nil
}

// ------------------------------------------------------------- Mach-O

func machoArch(cpu, sub uint32) string {
	switch cpu {
	case 7:
		return "x86"
	case 0x01000007:
		return "x86_64"
	case 12:
		switch sub & 0xff {
		case 9:
			return "armv7"
		case 11:
			return "armv7s"
		case 12:
			return "armv7k"
		case 6:
			return "armv6"
		}
		return "arm"
	case 0x0100000c:
		if sub&0xff == 2 {
			return "arm64e"
		}
		return "arm64"
	case 0x0200000c:
		return "arm64_32"
	case 18, 0x01000012:
		return "ppc"
	}
	return "unknown"
}

func detectFat(r io.ReaderAt, size int64, name string, is64 bool) ([]Object, bool, error) {
	hdr, err := readAt(r, size, 0, 8)
	if err != nil {
		return nil, false, nil
	}
	n := int64(binary.BigEndian.Uint32(hdr[4:]))
	if n == 0 || n >= 45 {
		// 0xcafebabe is also the Java class magic; a version-like count is not a fat file.
		return nil, false, nil
	}
	entSize := int64(20)
	if is64 {
		entSize = 32
	}
	table, err := readAt(r, size, 8, n*entSize)
	if err != nil {
		return nil, true, fmt.Errorf("%w: fat Mach-O header beyond the file", ErrUnsupported)
	}
	var out []Object
	var firstErr error
	for i := int64(0); i < n; i++ {
		e := table[i*entSize:]
		var off, sz int64
		if is64 {
			o, s := binary.BigEndian.Uint64(e[8:]), binary.BigEndian.Uint64(e[16:])
			if o > uint64(size) || s > uint64(size) {
				if firstErr == nil {
					firstErr = fmt.Errorf("%w: fat slice beyond the file", ErrUnsupported)
				}
				continue
			}
			off, sz = int64(o), int64(s)
		} else {
			off, sz = int64(binary.BigEndian.Uint32(e[8:])), int64(binary.BigEndian.Uint32(e[12:]))
		}
		if off < 0 || sz < 0 || off > size || sz > size-off {
			if firstErr == nil {
				firstErr = fmt.Errorf("%w: fat slice beyond the file", ErrUnsupported)
			}
			continue
		}
		obj, err := detectMacho(io.NewSectionReader(r, off, sz), sz, name, int(i))
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		out = append(out, obj)
	}
	if len(out) == 0 {
		if firstErr == nil {
			firstErr = fmt.Errorf("%w: empty fat Mach-O", ErrUnsupported)
		}
		return nil, true, firstErr
	}
	return out, true, nil
}

func detectMacho(r io.ReaderAt, size int64, name string, slice int) (Object, error) {
	head, err := readAt(r, size, 0, min(size, 32))
	if err != nil || len(head) < 28 {
		return Object{}, fmt.Errorf("%w: truncated Mach-O header", ErrUnsupported)
	}
	var bo binary.ByteOrder
	var is64 bool
	switch m := binary.LittleEndian.Uint32(head); m {
	case 0xfeedface:
		bo = binary.LittleEndian
	case 0xfeedfacf:
		bo, is64 = binary.LittleEndian, true
	default:
		switch binary.BigEndian.Uint32(head) {
		case 0xfeedface:
			bo = binary.BigEndian
		case 0xfeedfacf:
			bo, is64 = binary.BigEndian, true
		default:
			return Object{}, fmt.Errorf("%w: not a Mach-O slice", ErrUnsupported)
		}
	}
	hdrLen := int64(28)
	if is64 {
		hdrLen = 32
	}
	if int64(len(head)) < hdrLen {
		return Object{}, fmt.Errorf("%w: truncated Mach-O header", ErrUnsupported)
	}
	cpu, sub := bo.Uint32(head[4:]), bo.Uint32(head[8:])
	ncmds, sizeofcmds := int64(bo.Uint32(head[16:])), int64(bo.Uint32(head[20:]))
	// Bound the counts by the real size before reading: every command is at
	// least 8 bytes, and the command area must fit in the slice and the cap.
	if sizeofcmds > maxTable || hdrLen+sizeofcmds > size || ncmds*8 > sizeofcmds {
		return Object{}, fmt.Errorf("%w: Mach-O load commands exceed the file", ErrUnsupported)
	}
	cmds, err := readAt(r, size, hdrLen, sizeofcmds)
	if err != nil {
		return Object{}, err
	}
	obj := Object{Kind: "macho", Arch: machoArch(cpu, sub), Name: name, Slice: slice}
	var uuid [16]byte
	haveUUID := false
	feat := map[string]bool{}
	for i := int64(0); i < ncmds; i++ {
		if len(cmds) < 8 {
			return Object{}, fmt.Errorf("%w: truncated Mach-O load command", ErrUnsupported)
		}
		cmd, cmdsize := bo.Uint32(cmds), int64(bo.Uint32(cmds[4:]))
		if cmdsize < 8 || cmdsize > int64(len(cmds)) {
			return Object{}, fmt.Errorf("%w: invalid Mach-O load command size", ErrUnsupported)
		}
		body := cmds[:cmdsize]
		switch cmd {
		case 0x1b: // LC_UUID
			if cmdsize >= 24 {
				copy(uuid[:], body[8:24])
				haveUUID = true
			}
		case 0x2: // LC_SYMTAB
			if cmdsize >= 24 && bo.Uint32(body[12:]) > 0 {
				feat["symtab"] = true
			}
		case 0x1, 0x19: // LC_SEGMENT, LC_SEGMENT_64
			segHdr, secLen := int64(56), int64(68)
			if cmd == 0x19 {
				segHdr, secLen = 72, 80
			}
			if cmdsize < segHdr {
				break
			}
			nsects := int64(bo.Uint32(body[segHdr-8:]))
			if nsects > (cmdsize-segHdr)/secLen {
				return Object{}, fmt.Errorf("%w: Mach-O segment section count exceeds its command", ErrUnsupported)
			}
			for s := int64(0); s < nsects; s++ {
				sec := body[segHdr+s*secLen:]
				sect, seg := cstr(sec[0:16]), cstr(sec[16:32])
				var ssize uint64
				if cmd == 0x19 {
					ssize = bo.Uint64(sec[40:])
				} else {
					ssize = uint64(bo.Uint32(sec[36:]))
				}
				if ssize == 0 {
					continue
				}
				switch {
				case seg == "__DWARF" && sect == "__debug_info":
					feat["debug"] = true
				case seg == "__TEXT" && (sect == "__unwind_info" || sect == "__eh_frame"):
					feat["unwind"] = true
				case seg == "__TEXT" && sect == "__text":
					feat["code"] = true
				}
			}
		}
		cmds = cmds[cmdsize:]
	}
	if !haveUUID {
		return Object{}, fmt.Errorf("%w: Mach-O has no LC_UUID", ErrUnsupported)
	}
	obj.DebugID = DebugIDFromMachoUUID(uuid)
	for _, f := range []string{"symtab", "debug", "unwind", "code"} {
		if feat[f] {
			obj.Features = append(obj.Features, f)
		}
	}
	return obj, nil
}

func cstr(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return string(b)
}
