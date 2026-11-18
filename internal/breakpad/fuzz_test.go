package breakpad

import (
	"bytes"
	"io"
	"testing"

	"supermetry/internal/symcache"
)

// FuzzBuild: arbitrary input yields an error or a cache that opens and
// answers lookups, never a panic.
func FuzzBuild(f *testing.F) {
	seed := fixtureBytes(f)
	f.Add(seed)
	f.Add(seed[:len(seed)/2])
	f.Add([]byte("MODULE Linux x86_64 7A3C9E1055B24B8E8A532F0C8D4E9A010 a\nFUNC ffffffff ffffffff 0 x\nFUNC -1 -1 0 y\n0 ffffffffffffffff 1 0\nINLINE 0 1 0 0 0 ffffffff\nSTACK CFI INIT 0 ffffffff .cfa: \nSTACK CFI 5\nPUBLIC ffffffffff 0 z\n"))
	f.Add(append([]byte("MODULE Linux x86_64 7A3C9E1055B24B8E8A532F0C8D4E9A010 a\nFUNC 1 1 0 "), bytes.Repeat([]byte{'x'}, 1<<20)...))
	f.Fuzz(func(t *testing.T, data []byte) {
		var out bytes.Buffer
		if _, err := Build(bytes.NewReader(data), &out, t.TempDir()); err != nil {
			return
		}
		c, err := symcache.Open(bytes.NewReader(out.Bytes()), int64(out.Len()))
		if err != nil {
			t.Fatalf("built cache does not open: %v", err)
		}
		for _, rel := range []uint64{0, 1, 0x100, 0xffffffff} {
			c.Lookup(rel)
			if tab, err := OpenCFI(c); err == nil {
				tab.RulesAt(rel)
			}
		}
	})
}

// FuzzOpenCFI: a damaged trailing section never panics RulesAt.
func FuzzOpenCFI(f *testing.F) {
	var out bytes.Buffer
	if _, err := Build(bytes.NewReader(fixtureBytes(f)), &out, f.TempDir()); err != nil {
		f.Fatal(err)
	}
	f.Add(out.Bytes(), uint64(0x204))
	f.Fuzz(func(t *testing.T, data []byte, rel uint64) {
		c, err := symcache.Open(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return
		}
		if tab, err := OpenCFI(c); err == nil {
			tab.RulesAt(rel)
		}
	})
}

func fixtureBytes(f testing.TB) []byte {
	f.Helper()
	b := fixture(f)
	return append([]byte(nil), b...)
}

var _ = io.Discard
