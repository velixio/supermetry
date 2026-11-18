package symcache

import (
	"bytes"
	"math/rand/v2"
	"os"
	"runtime"
	"testing"
)

func buildHand(t testing.TB) *Cache {
	t.Helper()
	b := NewBuilder("arm64", t.TempDir())
	b.AddFunction(0x100, 0x140, "f")
	b.AddFunction(0x140, 0x200, "g")
	b.AddLine(0x100, "a.c", 10)
	b.AddLine(0x120, "a.c", 11)
	b.AddLine(0x128, "b.h", 5)
	b.AddLine(0x130, "a.c", 12)
	b.AddLine(0x140, "a.c", 20)
	b.AddInline(0x128, 0x130, 1, "h", "a.c", 11)
	var buf bytes.Buffer
	if err := b.Write(&buf); err != nil {
		t.Fatal(err)
	}
	c, err := Open(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestHandBuiltLookup(t *testing.T) {
	c := buildHand(t)
	if c.Arch() != "arm64" {
		t.Fatalf("arch %q", c.Arch())
	}
	cases := []struct {
		rel  uint64
		want []Location
		ok   bool
	}{
		{0x12c, []Location{{"h", "b.h", 5}, {"f", "a.c", 11}}, true},
		{0x13f, []Location{{"f", "a.c", 12}}, true},
		{0x100, []Location{{"f", "a.c", 10}}, true},
		{0x140, []Location{{"g", "a.c", 20}}, true},
		{0x1ff, []Location{{"g", "a.c", 20}}, true},
		{0x200, nil, false},
		{0xff, nil, false},
		{1 << 33, nil, false},
	}
	for _, tc := range cases {
		got, ok := c.Lookup(tc.rel)
		if ok != tc.ok || len(got) != len(tc.want) {
			t.Fatalf("Lookup(%#x) = %v %v, want %v %v", tc.rel, got, ok, tc.want, tc.ok)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("Lookup(%#x)[%d] = %+v, want %+v", tc.rel, i, got[i], tc.want[i])
			}
		}
	}
}

func TestOverlapAndTerminator(t *testing.T) {
	b := NewBuilder("x86_64", t.TempDir())
	b.AddFunction(0x10, 0x30, "outer")
	b.AddFunction(0x20, 0x28, "inside") // overlaps: dropped
	b.AddFunction(0x40, 0x40, "empty")  // empty: dropped
	b.AddLine(0x10, "a.c", 1)
	b.AddLine(0x30, "", 0) // end of sequence
	b.AddLine(0x30, "b.c", 7)
	b.AddLine(0x34, "b.c", 7) // same position as before: collapsed
	b.AddFunction(0x30, 0x40, "next")
	var buf bytes.Buffer
	if err := b.Write(&buf); err != nil {
		t.Fatal(err)
	}
	c, err := Open(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if fn, ln, _ := c.Counts(); fn != 2 || ln != 2 {
		t.Fatalf("counts funcs=%d lines=%d, want 2 and 2", fn, ln)
	}
	if got, _ := c.Lookup(0x24); got[0].Function != "outer" {
		t.Fatalf("got %+v", got)
	}
	if got, _ := c.Lookup(0x30); got[0] != (Location{"next", "b.c", 7}) {
		t.Fatalf("a run that ends where the next starts: %+v", got)
	}
}

func TestOpenRejectsHostile(t *testing.T) {
	c := buildHand(t)
	_ = c
	var buf bytes.Buffer
	b := NewBuilder("arm64", t.TempDir())
	b.AddFunction(0, 8, "f")
	if err := b.Write(&buf); err != nil {
		t.Fatal(err)
	}
	good := buf.Bytes()
	if _, err := Open(bytes.NewReader(good[:40]), 40); err == nil {
		t.Fatal("truncated header accepted")
	}
	bad := bytes.Clone(good)
	le.PutUint64(bad[16+8*3:], 1<<40) // funcN far beyond the file
	if _, err := Open(bytes.NewReader(bad), int64(len(bad))); err == nil {
		t.Fatal("oversized count accepted")
	}
	bad = bytes.Clone(good)
	bad[0] = 'X'
	if _, err := Open(bytes.NewReader(bad), int64(len(bad))); err == nil {
		t.Fatal("bad magic accepted")
	}
}

// TestSpillMerge forces several spilled runs and checks the merged order and
// the keep-the-last rule across runs.
func TestSpillMerge(t *testing.T) {
	if testing.Short() {
		t.Skip("builds several million records")
	}
	b := NewBuilder("arm64", t.TempDir())
	const n = 2*runRecords + 1000
	b.AddFunction(0, n*4, "all")
	for i := n - 1; i >= 0; i-- { // reverse order: every run needs sorting
		b.AddLine(uint64(i)*4, "f.c", uint32(i%1000)+1+uint32(i/1000))
	}
	b.AddLine(8, "f.c", 99999) // later insertion at an existing address wins
	var buf bytes.Buffer
	if err := b.Write(&buf); err != nil {
		t.Fatal(err)
	}
	c, err := Open(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := c.Lookup(8); got[0].Line != 99999 {
		t.Fatalf("last duplicate lost: %+v", got)
	}
	for _, i := range []uint64{0, 1, 12345, runRecords, n - 1} {
		want := uint32(i%1000) + 1 + uint32(i/1000)
		if got, ok := c.Lookup(i * 4); !ok || got[0].Line != want {
			t.Fatalf("Lookup(%d) = %+v want line %d", i*4, got, want)
		}
	}
}

// TestBuilderHeap builds 5M random lines and requires the heap to stay under
// 128 MiB.
func TestBuilderHeap(t *testing.T) {
	if testing.Short() {
		t.Skip("builds 5M records")
	}
	b := NewBuilder("arm64", t.TempDir())
	rng := rand.New(rand.NewPCG(1, 2))
	b.AddFunction(0, 1<<31, "big")
	files := []string{"a.c", "b.c", "c.h", "d.go", "e.swift"}
	var peak uint64
	var ms runtime.MemStats
	for i := 0; i < 5_000_000; i++ {
		b.AddLine(uint64(rng.Uint32N(1<<31)), files[rng.IntN(len(files))], uint32(rng.IntN(5000))+1)
		if i%250_000 == 0 {
			runtime.ReadMemStats(&ms)
			peak = max(peak, ms.HeapAlloc)
		}
	}
	f, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := b.Write(f); err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&ms)
	peak = max(peak, ms.HeapAlloc)
	t.Logf("peak sampled heap %d MiB", peak>>20)
	if peak > 128<<20 {
		t.Fatalf("heap %d MiB over 128 MiB", peak>>20)
	}
	st, _ := f.Stat()
	c, err := Open(f, st.Size())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Lookup(12345); !ok {
		t.Fatal("lookup failed")
	}
}
