// Package breakpad converts Breakpad text symbol files (.sym) into the
// symcache format plus a call-frame-information section the stack walker reads.
//
// The .sym record grammar follows Breakpad's public documentation
// (docs/symbol_files.md, BSD-3-Clause); the parser and the cache layout are
// original. See NOTICE.
package breakpad

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"supermetry/internal/debugfile"
)

const (
	// maxLine is the longest line the parser looks at; longer lines are skipped
	// without being buffered, so a hostile file cannot grow the heap.
	maxLine = 64 << 10
	// maxFiles bounds the FILE and INLINE_ORIGIN tables.
	maxFiles = 4 << 20
	// publicCap is the longest span given to a PUBLIC symbol that has no
	// following symbol to end it.
	publicCap = 64 << 10
)

// ErrFormat marks input that is not a Breakpad symbol file.
var ErrFormat = errors.New("breakpad: invalid symbol file")

// ParseModule parses a "MODULE <os> <arch> <id> [name]" line. The id is the
// 32-digit GUID followed by the age in hex; the result is the debug id form
// used everywhere else, with an age of 0 dropped.
func ParseModule(line string) (os, arch, id, name string, err error) {
	f := strings.SplitN(strings.TrimRight(line, "\r\n"), " ", 5)
	if len(f) < 4 || f[0] != "MODULE" {
		return "", "", "", "", fmt.Errorf("%w: bad MODULE line", ErrFormat)
	}
	id = debugfile.BreakpadDebugID(f[3])
	if id == "" {
		return "", "", "", "", fmt.Errorf("%w: bad module id", ErrFormat)
	}
	if len(f) == 5 {
		name = strings.TrimSpace(f[4])
	}
	return f[1], f[2], id, name, nil
}

// lineReader yields lines up to maxLine bytes; longer ones are reported as
// skipped after being drained.
type lineReader struct{ r *bufio.Reader }

func newLineReader(r io.Reader) *lineReader {
	return &lineReader{r: bufio.NewReaderSize(r, maxLine)}
}

func (l *lineReader) next() (line []byte, skipped bool, err error) {
	b, err := l.r.ReadSlice('\n')
	if err == bufio.ErrBufferFull {
		for err == bufio.ErrBufferFull {
			_, err = l.r.ReadSlice('\n')
		}
		if err != nil && err != io.EOF {
			return nil, true, err
		}
		return nil, true, nil
	}
	if err != nil && (err != io.EOF || len(b) == 0) {
		return nil, false, err
	}
	return bytes.TrimRight(b, "\r\n"), false, nil
}

// field splits the next space-separated token off b.
func field(b []byte) (tok, rest []byte) {
	b = bytes.TrimLeft(b, " ")
	i := bytes.IndexByte(b, ' ')
	if i < 0 {
		return b, nil
	}
	return b[:i], b[i+1:]
}

func hexNum(b []byte) (uint64, bool) {
	if len(b) == 0 || len(b) > 16 {
		return 0, false
	}
	var v uint64
	for _, c := range b {
		switch {
		case c >= '0' && c <= '9':
			c -= '0'
		case c >= 'a' && c <= 'f':
			c = c - 'a' + 10
		case c >= 'A' && c <= 'F':
			c = c - 'A' + 10
		default:
			return 0, false
		}
		v = v<<4 | uint64(c)
	}
	return v, true
}

// decNum parses a decimal that fits 32 bits.
func decNum(b []byte) (uint64, bool) {
	if len(b) == 0 || len(b) > 10 {
		return 0, false
	}
	var v uint64
	for _, c := range b {
		if c < '0' || c > '9' {
			return 0, false
		}
		v = v*10 + uint64(c-'0')
	}
	return v, v < 1<<32
}

// baseName is the last path element, for both / and \ separators.
func baseName(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		p = p[i+1:]
	}
	return p
}
