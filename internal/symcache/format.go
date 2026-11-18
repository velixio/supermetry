// Package symcache builds and reads compact native symbol caches: function,
// line and inline ranges keyed by address relative to the image base, laid out
// as fixed-width little-endian records so a lookup is a few binary searches
// through an io.ReaderAt and never loads the file.
//
// File layout (version 1):
//
//	header (80 bytes)
//	string table   entries of [len uint16][bytes]; offset 0 is the empty string
//	function table {start, end, name uint32}, sorted by start, non-overlapping
//	line table     {addr, file, line uint32}, sorted by addr; line 0 ends a run
//	inline table   {start, end uint32, depth uint16, pad uint16, name, callFile, callLine uint32} (24 bytes), sorted by start
package symcache

import (
	"encoding/binary"
	"errors"
)

const (
	// Version is the current cache format version; builders register it so a
	// format bump rebuilds every cache from the kept raw files.
	Version = 1

	magic      = "SMSC"
	headerSize = 80

	funcRecSize = 12
	lineRecSize = 12
	inlRecSize  = 24

	// maxStringLen clips any stored name so an entry's length fits a uint16.
	maxStringLen = 1024
	// maxStrings bounds the distinct strings of one cache.
	maxStrings = 4 << 20
	// maxStringBytes bounds the string table (offsets are uint32).
	maxStringBytes = 1 << 30
)

var (
	// ErrFormat marks a file that is not a valid cache.
	ErrFormat = errors.New("symcache: invalid cache")
	// ErrTooLarge marks an object whose code does not fit 32-bit relative addresses.
	ErrTooLarge = errors.New("symcache: image spans 4 GiB or more")
)

var le = binary.LittleEndian

type header struct {
	version      uint32
	arch         uint32 // string offset
	maxInlineLen uint32
	strOff       uint64
	strLen       uint64
	funcOff      uint64
	funcN        uint64
	lineOff      uint64
	lineN        uint64
	inlOff       uint64
	inlN         uint64
}

func (h header) encode() []byte {
	b := make([]byte, headerSize)
	copy(b, magic)
	le.PutUint32(b[4:], h.version)
	le.PutUint32(b[8:], h.arch)
	le.PutUint32(b[12:], h.maxInlineLen)
	for i, v := range []uint64{h.strOff, h.strLen, h.funcOff, h.funcN, h.lineOff, h.lineN, h.inlOff, h.inlN} {
		le.PutUint64(b[16+8*i:], v)
	}
	return b
}

func decodeHeader(b []byte) (header, bool) {
	if len(b) < headerSize || string(b[:4]) != magic {
		return header{}, false
	}
	h := header{version: le.Uint32(b[4:]), arch: le.Uint32(b[8:]), maxInlineLen: le.Uint32(b[12:])}
	f := func(i int) uint64 { return le.Uint64(b[16+8*i:]) }
	h.strOff, h.strLen, h.funcOff, h.funcN = f(0), f(1), f(2), f(3)
	h.lineOff, h.lineN, h.inlOff, h.inlN = f(4), f(5), f(6), f(7)
	return h, true
}

type funcRec struct{ start, end, name uint32 }
type lineRec struct{ addr, file, line uint32 }
type inlRec struct {
	start, end uint32
	depth      uint16
	name       uint32
	callFile   uint32
	callLine   uint32
}

func (r funcRec) put(b []byte) {
	le.PutUint32(b, r.start)
	le.PutUint32(b[4:], r.end)
	le.PutUint32(b[8:], r.name)
}

func getFunc(b []byte) funcRec {
	return funcRec{le.Uint32(b), le.Uint32(b[4:]), le.Uint32(b[8:])}
}

func (r lineRec) put(b []byte) {
	le.PutUint32(b, r.addr)
	le.PutUint32(b[4:], r.file)
	le.PutUint32(b[8:], r.line)
}

func getLine(b []byte) lineRec {
	return lineRec{le.Uint32(b), le.Uint32(b[4:]), le.Uint32(b[8:])}
}

func (r inlRec) put(b []byte) {
	le.PutUint32(b, r.start)
	le.PutUint32(b[4:], r.end)
	le.PutUint16(b[8:], r.depth)
	le.PutUint16(b[10:], 0)
	le.PutUint32(b[12:], r.name)
	le.PutUint32(b[16:], r.callFile)
	le.PutUint32(b[20:], r.callLine)
}

func getInl(b []byte) inlRec {
	return inlRec{le.Uint32(b), le.Uint32(b[4:]), le.Uint16(b[8:]), le.Uint32(b[12:]), le.Uint32(b[16:]), le.Uint32(b[20:])}
}
