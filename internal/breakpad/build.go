package breakpad

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"

	"supermetry/internal/debugfile"
	"supermetry/internal/symcache"
)

// Feature names stored with a built cache.
const (
	FeatureSymtab = "symtab" // functions
	FeatureDebug  = "debug"  // line records
	FeatureUnwind = "unwind" // call-frame rules
)

const (
	// maxPublics and maxPublicBytes bound the PUBLIC symbols held in memory
	// until their sizes can be derived; further ones are dropped.
	maxPublics     = 2 << 20
	maxPublicBytes = 64 << 20
)

type span struct{ start, end uint32 }

type pub struct {
	addr uint32
	off  uint32 // into the name arena
	n    uint16
}

// Build streams a Breakpad .sym file into a symcache followed by a CFI
// section. Lines it cannot make sense of are skipped, so a damaged file yields
// a partial cache; a file with no MODULE header or no usable record is an
// error. Spill files go to tmpDir ("" means the system temporary directory).
func Build(r io.Reader, out io.Writer, tmpDir string) (features []string, err error) {
	defer func() {
		if p := recover(); p != nil {
			features, err = nil, fmt.Errorf("breakpad: malformed symbol file: %v", p)
		}
	}()
	lr := newLineReader(r)
	var arch string
	for {
		line, skipped, rerr := lr.next()
		if rerr != nil {
			return nil, fmt.Errorf("%w: no MODULE line", ErrFormat)
		}
		if skipped {
			return nil, fmt.Errorf("%w: oversized first line", ErrFormat)
		}
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		_, a, _, _, perr := ParseModule(string(line))
		if perr != nil {
			return nil, perr
		}
		arch = a
		break
	}

	b := symcache.NewBuilder(arch, tmpDir)
	defer b.Close()
	cfi := &cfiBuilder{tmpDir: tmpDir}
	defer cfi.close()

	files := map[uint32]string{}
	origins := map[uint32]string{}
	var funcs []span
	var pubs []pub
	var arena []byte
	var havePend bool
	var pendEnd uint64
	var nFunc, nPub, nLine int

	flushLine := func() {
		if havePend {
			b.AddLine(pendEnd, "", 0)
			havePend = false
		}
	}

	for {
		line, skipped, rerr := lr.next()
		if rerr != nil {
			if rerr != io.EOF {
				return nil, rerr
			}
			break
		}
		if skipped || len(line) == 0 {
			continue
		}
		kw, rest := field(line)
		switch string(kw) {
		case "FILE":
			n, rest2 := field(rest)
			id, ok := decNum(n)
			if ok && len(files) < maxFiles {
				files[uint32(id)] = baseName(string(bytes.TrimSpace(rest2)))
			}
		case "INLINE_ORIGIN":
			n, rest2 := field(rest)
			id, ok := decNum(n)
			if ok && len(origins) < maxFiles {
				origins[uint32(id)] = string(bytes.TrimSpace(rest2))
			}
		case "FUNC":
			flushLine()
			t, rest2 := field(rest)
			if string(t) == "m" {
				t, rest2 = field(rest2)
			}
			size, rest3 := field(rest2)
			_, name := field(rest3)
			addr, ok1 := hexNum(t)
			sz, ok2 := hexNum(size)
			name = bytes.TrimSpace(name)
			if !ok1 || !ok2 || sz == 0 || len(name) == 0 || addr+sz < addr {
				continue
			}
			b.AddFunction(addr, addr+sz, string(name))
			nFunc++
			if addr+sz <= 1<<32 && len(funcs) < maxFiles {
				funcs = append(funcs, span{uint32(addr), uint32(addr + sz)})
			}
		case "PUBLIC":
			t, rest2 := field(rest)
			if string(t) == "m" {
				t, rest2 = field(rest2)
			}
			addr, ok := hexNum(t)
			_, name := field(rest2)
			name = bytes.TrimSpace(name)
			if !ok || len(name) == 0 || addr >= 1<<32 {
				continue
			}
			if len(pubs) >= maxPublics || len(arena)+len(name) > maxPublicBytes {
				continue
			}
			name = name[:min(len(name), 1024)]
			pubs = append(pubs, pub{uint32(addr), uint32(len(arena)), uint16(len(name))})
			arena = append(arena, name...)
		case "INLINE":
			parseInline(rest, files, origins, b)
		case "STACK":
			parseStack(rest, cfi)
		case "MODULE", "INFO":
		default:
			addr, ok1 := hexNum(kw)
			sizeTok, rest2 := field(rest)
			lineTok, rest3 := field(rest2)
			fileTok, _ := field(rest3)
			sz, ok2 := hexNum(sizeTok)
			ln, ok3 := decNum(lineTok)
			fid, ok4 := decNum(fileTok)
			if !ok1 || !ok2 || !ok3 || !ok4 || addr >= 1<<32 {
				continue
			}
			if havePend && pendEnd != addr {
				b.AddLine(pendEnd, "", 0)
			}
			b.AddLine(addr, files[uint32(fid)], uint32(ln))
			nLine++
			havePend, pendEnd = sz > 0, addr+sz
		}
	}
	flushLine()
	if len(funcs) == nFunc {
		addPublics(b, funcs, pubs, arena, &nPub)
	}

	if nFunc == 0 && nPub == 0 && nLine == 0 && len(cfi.inits) == 0 {
		return nil, fmt.Errorf("%w: no symbols", ErrFormat)
	}
	if err := b.Write(out); err != nil {
		return nil, err
	}
	if err := cfi.write(out); err != nil {
		return nil, err
	}
	if nFunc+nPub > 0 {
		features = append(features, FeatureSymtab)
	}
	if nLine > 0 {
		features = append(features, FeatureDebug)
	}
	if len(cfi.inits) > 0 {
		features = append(features, FeatureUnwind)
	}
	return features, nil
}

func parseInline(rest []byte, files, origins map[uint32]string, b *symcache.Builder) {
	var nums [4]uint64
	for i := range nums {
		var t []byte
		t, rest = field(rest)
		v, ok := decNum(t)
		if !ok {
			return
		}
		nums[i] = v
	}
	depth, callLine, callFile, origin := nums[0], nums[1], nums[2], nums[3]
	if depth >= 1<<15 {
		return
	}
	for {
		var a, s []byte
		a, rest = field(rest)
		s, rest = field(rest)
		addr, ok1 := hexNum(a)
		size, ok2 := hexNum(s)
		if !ok1 || !ok2 {
			return
		}
		if size > 0 {
			b.AddInline(addr, addr+size, uint16(depth+1), origins[uint32(origin)], files[uint32(callFile)], uint32(callLine))
		}
	}
}

func parseStack(rest []byte, c *cfiBuilder) {
	kind, rest := field(rest)
	if string(kind) != "CFI" { // STACK WIN is x86 only
		return
	}
	t, rest2 := field(rest)
	if string(t) == "INIT" {
		a, rest3 := field(rest2)
		s, rules := field(rest3)
		addr, ok1 := hexNum(a)
		size, ok2 := hexNum(s)
		if ok1 && ok2 {
			c.addInit(addr, size, bytes.TrimSpace(rules))
		}
		return
	}
	if addr, ok := hexNum(t); ok {
		c.addDelta(addr, bytes.TrimSpace(rest2))
	}
}

// addPublics adds PUBLIC symbols that no FUNC covers, each sized to the next
// symbol (capped at publicCap).
func addPublics(b *symcache.Builder, funcs []span, pubs []pub, arena []byte, n *int) {
	slices.SortStableFunc(funcs, func(x, y span) int { return cmpU32(x.start, y.start) })
	slices.SortStableFunc(pubs, func(x, y pub) int { return cmpU32(x.addr, y.addr) })
	for i, p := range pubs {
		if i > 0 && pubs[i-1].addr == p.addr {
			continue
		}
		a := p.addr
		// the last function starting at or before a
		fi := sort.Search(len(funcs), func(j int) bool { return funcs[j].start > a })
		if fi > 0 && a < funcs[fi-1].end {
			continue
		}
		end := uint64(a) + publicCap
		if fi < len(funcs) {
			end = min(end, uint64(funcs[fi].start))
		}
		for j := i + 1; j < len(pubs); j++ {
			if pubs[j].addr > a {
				end = min(end, uint64(pubs[j].addr))
				break
			}
		}
		b.AddFunction(uint64(a), end, string(arena[p.off:p.off+uint32(p.n)]))
		*n++
	}
}

// BuildFunc returns a function with the signature of sentryapi.CacheBuilder.Build
// for Breakpad symbol files, spilling to tmpDir.
func BuildFunc(tmpDir string) func(ctx context.Context, obj debugfile.Object, raw io.ReaderAt, size int64, out io.Writer) ([]string, error) {
	return func(ctx context.Context, obj debugfile.Object, raw io.ReaderAt, size int64, out io.Writer) ([]string, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if obj.Kind != "breakpad" {
			return nil, errors.New("breakpad: not a symbol file")
		}
		if tmpDir != "" {
			if err := os.MkdirAll(tmpDir, 0o700); err != nil {
				return nil, err
			}
		}
		return Build(io.NewSectionReader(raw, 0, size), out, tmpDir)
	}
}
