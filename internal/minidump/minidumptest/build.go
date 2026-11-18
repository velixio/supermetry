// Package minidumptest writes small synthetic minidumps for tests. It is the
// inverse of the parser in package minidump, built from the same public
// structure documentation, and is not used outside tests.
package minidumptest

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf16"

	"supermetry/internal/minidump"
)

var le = binary.LittleEndian

type module struct {
	name      string
	base      uint64
	size      uint32
	timestamp uint32
	cv        []byte
}

type thread struct {
	id         uint32
	ctx        minidump.Context
	stackStart uint64
	stack      []byte
}

type mem struct {
	start uint64
	data  []byte
}

// Builder assembles a minidump. Set OS ("linux","mac","windows","android") and
// CPU ("amd64","arm64","x86","arm") before calling Bytes; the zero values mean
// linux and amd64.
type Builder struct {
	OS, CPU string
	// ARM64Old writes arm64 contexts with the older flag value (0x80000000,
	// 64-bit flags field) instead of 0x00400000.
	ARM64Old bool

	modules []module
	threads []thread
	mem     []mem
	mem64   []mem
	exc     *exception
}

type exception struct {
	threadID    uint32
	code, flags uint32
	addr        uint64
	info        []uint64
	ctx         *minidump.Context
}

// AddModule adds an ELF module with a build id (a BpEL CodeView record).
func (b *Builder) AddModule(name string, base, size uint64, buildID []byte) {
	cv := append([]byte{'B', 'p', 'E', 'L'}, buildID...)
	b.modules = append(b.modules, module{name: name, base: base, size: uint32(size), cv: cv})
}

// AddModulePE adds a module with a PDB 7.0 (RSDS) CodeView record. guid is the
// textual GUID ("9C2A902B-6FDF-40AD-8308-588A41D572A0").
func (b *Builder) AddModulePE(name string, base, size uint64, timestamp uint32, guid string, age uint32, pdb string) {
	h, err := hex.DecodeString(strings.ReplaceAll(strings.Trim(guid, "{}"), "-", ""))
	if err != nil || len(h) != 16 {
		panic("minidumptest: bad guid " + guid)
	}
	cv := make([]byte, 24, 24+len(pdb)+1)
	copy(cv, "RSDS")
	le.PutUint32(cv[4:], le.Uint32(swap4(h[0:4])))
	le.PutUint16(cv[8:], uint16(h[4])<<8|uint16(h[5]))
	le.PutUint16(cv[10:], uint16(h[6])<<8|uint16(h[7]))
	copy(cv[12:20], h[8:16])
	le.PutUint32(cv[20:], age)
	cv = append(append(cv, pdb...), 0)
	b.modules = append(b.modules, module{name: name, base: base, size: uint32(size), timestamp: timestamp, cv: cv})
}

// swap4 returns the 4 bytes reversed, so a big-endian textual field can be
// stored as the little-endian integer it stands for.
func swap4(p []byte) []byte { return []byte{p[3], p[2], p[1], p[0]} }

// AddThread adds a thread with its context and stack memory.
func (b *Builder) AddThread(id uint32, ctx minidump.Context, stackStart uint64, stack []byte) {
	b.threads = append(b.threads, thread{id, ctx, stackStart, stack})
}

// SetException records the crash on a thread. For linux flags is si_code.
func (b *Builder) SetException(threadID, code, flags uint32, addr uint64) {
	b.exc = &exception{threadID: threadID, code: code, flags: flags, addr: addr}
}

// SetExceptionInfo sets ExceptionInformation (at most 15 words) of the crash.
func (b *Builder) SetExceptionInfo(info ...uint64) { b.exc.info = info }

// SetExceptionContext gives the exception stream its own thread context.
func (b *Builder) SetExceptionContext(c minidump.Context) { b.exc.ctx = &c }

// AddMemory adds a range to the MemoryListStream.
func (b *Builder) AddMemory(start uint64, data []byte) { b.mem = append(b.mem, mem{start, data}) }

// AddMemory64 adds a range to the Memory64ListStream.
func (b *Builder) AddMemory64(start uint64, data []byte) { b.mem64 = append(b.mem64, mem{start, data}) }

type writer struct{ buf []byte }

// put appends data (4-byte aligned) and returns its RVA.
func (w *writer) put(data []byte) uint32 {
	for len(w.buf)%4 != 0 {
		w.buf = append(w.buf, 0)
	}
	rva := uint32(len(w.buf))
	w.buf = append(w.buf, data...)
	return rva
}

func (w *writer) str(s string) uint32 {
	u := utf16.Encode([]rune(s))
	d := make([]byte, 4+len(u)*2+2)
	le.PutUint32(d, uint32(len(u)*2))
	for i, c := range u {
		le.PutUint16(d[4+i*2:], c)
	}
	return w.put(d)
}

type dirent struct {
	typ, size, rva uint32
}

// Bytes returns the minidump.
func (b *Builder) Bytes() []byte {
	osName, cpu := b.OS, b.CPU
	if osName == "" {
		osName = "linux"
	}
	if cpu == "" {
		cpu = "amd64"
	}
	w := &writer{buf: make([]byte, 32)} // header, patched below
	var dir []dirent

	// System info.
	si := make([]byte, 56)
	le.PutUint16(si, map[string]uint16{"x86": 0, "arm": 5, "amd64": 9, "arm64": 12}[cpu])
	le.PutUint32(si[24:], map[string]uint32{"windows": 2, "mac": 0x8101, "ios": 0x8102, "linux": 0x8201, "android": 0x8203}[osName])
	dir = append(dir, dirent{7, 56, w.put(si)})

	// Threads.
	if len(b.threads) > 0 {
		tl := make([]byte, 4+48*len(b.threads))
		le.PutUint32(tl, uint32(len(b.threads)))
		for i, t := range b.threads {
			e := tl[4+48*i:]
			le.PutUint32(e, t.id)
			le.PutUint64(e[24:], t.stackStart)
			if len(t.stack) > 0 {
				le.PutUint32(e[32:], uint32(len(t.stack)))
				le.PutUint32(e[36:], w.put(t.stack))
			}
			ctx := encodeContext(cpu, b.ARM64Old, t.ctx)
			le.PutUint32(e[40:], uint32(len(ctx)))
			le.PutUint32(e[44:], w.put(ctx))
		}
		dir = append(dir, dirent{3, uint32(len(tl)), w.put(tl)})
	}

	// Modules.
	if len(b.modules) > 0 {
		ml := make([]byte, 4+108*len(b.modules))
		le.PutUint32(ml, uint32(len(b.modules)))
		for i, m := range b.modules {
			e := ml[4+108*i:]
			le.PutUint64(e, m.base)
			le.PutUint32(e[8:], m.size)
			le.PutUint32(e[16:], m.timestamp)
			le.PutUint32(e[20:], w.str(m.name))
			le.PutUint32(e[76:], uint32(len(m.cv)))
			le.PutUint32(e[80:], w.put(m.cv))
		}
		dir = append(dir, dirent{4, uint32(len(ml)), w.put(ml)})
	}

	// Exception.
	if b.exc != nil {
		ex := make([]byte, 168)
		le.PutUint32(ex, b.exc.threadID)
		le.PutUint32(ex[8:], b.exc.code)
		le.PutUint32(ex[12:], b.exc.flags)
		le.PutUint64(ex[24:], b.exc.addr)
		le.PutUint32(ex[32:], uint32(len(b.exc.info)))
		for i, v := range b.exc.info {
			le.PutUint64(ex[40+i*8:], v)
		}
		if b.exc.ctx != nil {
			ctx := encodeContext(cpu, b.ARM64Old, *b.exc.ctx)
			le.PutUint32(ex[160:], uint32(len(ctx)))
			le.PutUint32(ex[164:], w.put(ctx))
		}
		dir = append(dir, dirent{6, 168, w.put(ex)})
	}

	// Memory list.
	if len(b.mem) > 0 {
		ml := make([]byte, 4+16*len(b.mem))
		le.PutUint32(ml, uint32(len(b.mem)))
		for i, m := range b.mem {
			e := ml[4+16*i:]
			le.PutUint64(e, m.start)
			le.PutUint32(e[8:], uint32(len(m.data)))
			le.PutUint32(e[12:], w.put(m.data))
		}
		dir = append(dir, dirent{5, uint32(len(ml)), w.put(ml)})
	}

	// Memory64 list: descriptors, then one contiguous data area.
	if len(b.mem64) > 0 {
		ml := make([]byte, 16+16*len(b.mem64))
		le.PutUint64(ml, uint64(len(b.mem64)))
		var data []byte
		for i, m := range b.mem64 {
			e := ml[16+16*i:]
			le.PutUint64(e, m.start)
			le.PutUint64(e[8:], uint64(len(m.data)))
			data = append(data, m.data...)
		}
		le.PutUint64(ml[8:], uint64(w.put(data)))
		dir = append(dir, dirent{9, uint32(len(ml)), w.put(ml)})
	}

	// Directory and header.
	d := make([]byte, 12*len(dir))
	for i, e := range dir {
		le.PutUint32(d[i*12:], e.typ)
		le.PutUint32(d[i*12+4:], e.size)
		le.PutUint32(d[i*12+8:], e.rva)
	}
	dirRVA := w.put(d)
	copy(w.buf, "MDMP")
	le.PutUint32(w.buf[4:], 0xa793)
	le.PutUint32(w.buf[8:], uint32(len(dir)))
	le.PutUint32(w.buf[12:], dirRVA)
	return w.buf
}

var amd64Offsets = map[string]int{
	"rax": 0x78, "rcx": 0x80, "rdx": 0x88, "rbx": 0x90, "rsp": 0x98, "rbp": 0xa0, "rsi": 0xa8, "rdi": 0xb0,
	"r8": 0xb8, "r9": 0xc0, "r10": 0xc8, "r11": 0xd0, "r12": 0xd8, "r13": 0xe0, "r14": 0xe8, "r15": 0xf0, "rip": 0xf8,
}

func encodeContext(cpu string, arm64Old bool, c minidump.Context) []byte {
	switch cpu {
	case "amd64":
		raw := make([]byte, 1232)
		le.PutUint32(raw, 0x10001f)
		for name, v := range c.Regs {
			if off, ok := amd64Offsets[name]; ok {
				le.PutUint64(raw[off:], v)
			}
		}
		put := func(off int, v uint64) {
			if v != 0 {
				le.PutUint64(raw[off:], v)
			}
		}
		put(0xf8, c.PC)
		put(0x98, c.SP)
		put(0xa0, c.FP)
		return raw
	case "arm64":
		raw := make([]byte, 912)
		if arm64Old {
			le.PutUint64(raw, 0x80000000|0x2)
		} else {
			le.PutUint32(raw, 0x00400000|0x2)
		}
		for i := 0; i < 31; i++ {
			if v, ok := c.Regs[fmt.Sprintf("x%d", i)]; ok {
				le.PutUint64(raw[8+i*8:], v)
			}
		}
		if c.FP != 0 {
			le.PutUint64(raw[8+29*8:], c.FP)
		}
		if c.LR != 0 {
			le.PutUint64(raw[8+30*8:], c.LR)
		}
		le.PutUint64(raw[256:], c.SP)
		le.PutUint64(raw[264:], c.PC)
		return raw
	case "x86":
		raw := make([]byte, 716)
		le.PutUint32(raw[0xb8:], uint32(c.PC))
		le.PutUint32(raw[0xc4:], uint32(c.SP))
		le.PutUint32(raw[0xb4:], uint32(c.FP))
		return raw
	case "arm":
		raw := make([]byte, 4+16*4+64)
		le.PutUint32(raw[4+11*4:], uint32(c.FP))
		le.PutUint32(raw[4+13*4:], uint32(c.SP))
		le.PutUint32(raw[4+14*4:], uint32(c.LR))
		le.PutUint32(raw[4+15*4:], uint32(c.PC))
		return raw
	}
	return nil
}
