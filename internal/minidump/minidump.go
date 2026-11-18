// Package minidump parses the Microsoft minidump container (MINIDUMP_HEADER and
// its streams) into the few facts crash symbolication needs: the operating
// system and CPU, the threads with their CPU contexts and stack memory, the
// loaded modules with their debug identifiers, the exception, and the captured
// memory ranges.
//
// The structures are re-implemented from Microsoft's public documentation of
// minidumpapiset.h (MINIDUMP_HEADER, MINIDUMP_DIRECTORY, MINIDUMP_THREAD,
// MINIDUMP_MODULE, MINIDUMP_EXCEPTION_STREAM, ...) and the platform and CPU
// constants Breakpad adds for non-Windows dumps.
//
// Parse never reads outside its input and never allocates more than a small
// multiple of len(b): every count and size from the file is checked against the
// bytes actually present before anything is allocated. The input slice is
// retained by the Dump (stack and memory views point into it).
package minidump

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"unicode/utf16"
)

// Limits on what one dump may declare (plan: 2d Global Constraints).
const (
	maxStreams = 4096
	maxThreads = 4096
	maxModules = 4096
	maxMemory  = 1000
	maxName    = 2048 // bytes of a module name read from the file
)

const (
	headerMagic = 0x504d444d // "MDMP"
	headerSize  = 32
	dirEntry    = 12
)

var le = binary.LittleEndian

// Dump is a parsed minidump.
type Dump struct {
	OS, CPU     string // "linux","mac","windows","android","ios"; "amd64","arm64","x86","arm"
	Crash       *Exception
	CrashThread *Thread
	Threads     []Thread
	Modules     []Module // sorted by Base
	mem         []memRange
}

// Thread is one thread of the crashed process.
type Thread struct {
	ID         uint32
	Context    Context
	StackStart uint64
	Stack      []byte
}

// Module is one loaded image.
type Module struct {
	Base, Size uint64
	CodeFile   string
	DebugFile  string
	DebugID    string
	CodeID     string
}

// Exception describes the crash.
type Exception struct {
	ThreadID uint32
	Code     uint32
	Flags    uint32
	Address  uint64
	Reason   string
}

type memRange struct {
	start uint64
	data  []byte
}

// ErrInvalid wraps every parse failure.
var ErrInvalid = errors.New("minidump: invalid")

func bad(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// sub returns b[off:off+n] when it lies inside b, without integer overflow.
func sub(b []byte, off, n uint64) ([]byte, bool) {
	if off > uint64(len(b)) || n > uint64(len(b))-off {
		return nil, false
	}
	return b[off : off+n : off+n], true
}

// Parse decodes a minidump.
func Parse(b []byte) (*Dump, error) {
	if len(b) < headerSize {
		return nil, bad("short header")
	}
	if le.Uint32(b) != headerMagic {
		return nil, bad("bad magic")
	}
	n := le.Uint32(b[8:])
	rva := le.Uint32(b[12:])
	if n > maxStreams {
		return nil, bad("%d streams", n)
	}
	dir, ok := sub(b, uint64(rva), uint64(n)*dirEntry)
	if !ok {
		return nil, bad("stream directory outside file")
	}
	streams := make(map[uint32][]byte, 8)
	for i := 0; i < int(n); i++ {
		e := dir[i*dirEntry:]
		typ := le.Uint32(e)
		if _, dup := streams[typ]; dup {
			continue
		}
		switch typ {
		case streamThreadList, streamModuleList, streamMemoryList, streamException, streamSystemInfo, streamMemory64List:
			data, ok := sub(b, uint64(le.Uint32(e[8:])), uint64(le.Uint32(e[4:])))
			if !ok {
				return nil, bad("stream %d outside file", typ)
			}
			streams[typ] = data
		}
	}

	d := &Dump{}
	if s, ok := streams[streamSystemInfo]; ok {
		if err := d.parseSystemInfo(s); err != nil {
			return nil, err
		}
	}
	cache := &ctxCache{}
	if s, ok := streams[streamThreadList]; ok {
		if err := d.parseThreads(b, s, cache); err != nil {
			return nil, err
		}
	}
	if s, ok := streams[streamModuleList]; ok {
		if err := d.parseModules(b, s); err != nil {
			return nil, err
		}
	}
	if s, ok := streams[streamMemoryList]; ok {
		if err := d.parseMemoryList(b, s); err != nil {
			return nil, err
		}
	}
	if s, ok := streams[streamMemory64List]; ok {
		if err := d.parseMemory64List(b, s); err != nil {
			return nil, err
		}
	}
	if s, ok := streams[streamException]; ok {
		if err := d.parseException(b, s, cache); err != nil {
			return nil, err
		}
	}
	for i := range d.Threads {
		t := &d.Threads[i]
		if len(t.Stack) > 0 {
			d.mem = append(d.mem, memRange{t.StackStart, t.Stack})
		}
	}
	sort.SliceStable(d.mem, func(i, j int) bool { return d.mem[i].start < d.mem[j].start })
	return d, nil
}

// ReadMemory returns n bytes at addr when one captured range holds all of them.
func (d *Dump) ReadMemory(addr uint64, n int) ([]byte, bool) {
	if n < 0 {
		return nil, false
	}
	i := sort.Search(len(d.mem), func(i int) bool { return d.mem[i].start > addr })
	// Ranges can overlap (a thread stack is also listed in the memory list),
	// so look at a few candidates at or before addr.
	for k := i - 1; k >= 0 && k >= i-8; k-- {
		r := d.mem[k]
		off := addr - r.start
		if off <= uint64(len(r.data)) && uint64(n) <= uint64(len(r.data))-off {
			return r.data[off : off+uint64(n)], true
		}
	}
	return nil, false
}

// ModuleAt returns the module whose [Base, Base+Size) holds addr.
func (d *Dump) ModuleAt(addr uint64) (*Module, bool) {
	i := sort.Search(len(d.Modules), func(i int) bool { return d.Modules[i].Base > addr })
	for k := i - 1; k >= 0 && k >= i-4; k-- {
		m := &d.Modules[k]
		if addr-m.Base < m.Size {
			return m, true
		}
	}
	return nil, false
}

// utf16String decodes a MINIDUMP_STRING (u32 byte length, UTF-16LE) at rva.
func utf16String(b []byte, rva uint32) (string, bool) {
	hdr, ok := sub(b, uint64(rva), 4)
	if !ok {
		return "", false
	}
	n := uint64(le.Uint32(hdr)) &^ 1
	if n > maxName {
		n = maxName
	}
	raw, ok := sub(b, uint64(rva)+4, n)
	if !ok {
		return "", false
	}
	u := make([]uint16, len(raw)/2)
	for i := range u {
		u[i] = le.Uint16(raw[i*2:])
	}
	return string(utf16.Decode(u)), true
}
