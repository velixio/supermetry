package symcache

import (
	"bytes"
	"testing"
)

func fuzzSeed(f *testing.F) {
	b := NewBuilder("arm64", f.TempDir())
	b.AddFunction(0x100, 0x140, "f")
	b.AddLine(0x100, "a.c", 10)
	b.AddLine(0x120, "b.h", 5)
	b.AddInline(0x118, 0x128, 1, "h", "a.c", 11)
	var buf bytes.Buffer
	if err := b.Write(&buf); err != nil {
		f.Fatal(err)
	}
	f.Add(buf.Bytes(), uint64(0x11c))
	f.Add(buf.Bytes()[:60], uint64(0))
}

// FuzzOpen: arbitrary bytes never panic and counts larger than the file fail.
func FuzzOpen(f *testing.F) {
	fuzzSeed(f)
	f.Fuzz(func(t *testing.T, data []byte, rel uint64) {
		c, err := Open(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return
		}
		fn, ln, in := c.Counts()
		if uint64(fn)*funcRecSize > uint64(len(data)) || uint64(ln)*lineRecSize > uint64(len(data)) || uint64(in)*inlRecSize > uint64(len(data)) {
			t.Fatalf("counts %d %d %d exceed %d bytes", fn, ln, in, len(data))
		}
		c.Lookup(rel)
	})
}

// FuzzLookup: any address on a valid cache never panics.
func FuzzLookup(f *testing.F) {
	fuzzSeed(f)
	f.Fuzz(func(t *testing.T, data []byte, rel uint64) {
		c, err := Open(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return
		}
		c.Lookup(rel)
		c.Lookup(rel &^ 0xffff)
		c.Lookup(0)
	})
}
