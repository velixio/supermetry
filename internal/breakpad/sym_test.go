package breakpad

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"reflect"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"supermetry/internal/symcache"
)

func buildFile(t testing.TB, data []byte) (*symcache.Cache, []string) {
	t.Helper()
	var out bytes.Buffer
	feats, err := Build(bytes.NewReader(data), &out, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c, err := symcache.Open(bytes.NewReader(out.Bytes()), int64(out.Len()))
	if err != nil {
		t.Fatal(err)
	}
	return c, feats
}

func TestParseModule(t *testing.T) {
	cases := []struct {
		in                 string
		os, arch, id, name string
		bad                bool
	}{
		{in: "MODULE Linux x86_64 7A3C9E1055B24B8E8A532F0C8D4E9A010 app", os: "Linux", arch: "x86_64", id: "7a3c9e10-55b2-4b8e-8a53-2f0c8d4e9a01", name: "app"},
		{in: "MODULE windows x86_64 9C2A902B6FDF40AD8308588A41D572A01 dbghelp.pdb", os: "windows", arch: "x86_64", id: "9c2a902b-6fdf-40ad-8308-588a41d572a0-1", name: "dbghelp.pdb"},
		{in: "MODULE mac arm64 DE7FADA356F73CE7B81E0B6E964459C1A my app\r\n", os: "mac", arch: "arm64", id: "de7fada3-56f7-3ce7-b81e-0b6e964459c1-a", name: "my app"},
		{in: "MODULE Linux x86_64 DE7FADA356F73CE7B81E0B6E964459C10", os: "Linux", arch: "x86_64", id: "de7fada3-56f7-3ce7-b81e-0b6e964459c1"},
		{in: "MODULE Linux x86_64 XYZ app", bad: true},
		{in: "MODULE Linux x86_64 7A3C9E1055B24B8E8A532F0C8D4E9A01", os: "Linux", arch: "x86_64", id: "7a3c9e10-55b2-4b8e-8a53-2f0c8d4e9a01"},
		{in: "MODULE Linux x86_64", bad: true},
		{in: "FUNC 1 2 3 x", bad: true},
		{in: "", bad: true},
	}
	for _, c := range cases {
		o, a, id, n, err := ParseModule(c.in)
		if c.bad {
			if err == nil {
				t.Errorf("%q accepted", c.in)
			}
			continue
		}
		if err != nil || o != c.os || a != c.arch || id != c.id || n != c.name {
			t.Errorf("%q = %q %q %q %q %v", c.in, o, a, id, n, err)
		}
	}
}

func fixture(t testing.TB) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/app.sym")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestBuildFixture(t *testing.T) {
	c, feats := buildFile(t, fixture(t))
	if !reflect.DeepEqual(feats, []string{"symtab", "debug", "unwind"}) || c.Arch() != "x86_64" {
		t.Fatalf("feats %v arch %q", feats, c.Arch())
	}
	loc := func(f string, file string, l uint32) symcache.Location {
		return symcache.Location{Function: f, File: file, Line: l}
	}
	for _, tc := range []struct {
		rel  uint64
		want []symcache.Location
		ok   bool
	}{
		{0x123, []symcache.Location{loc("crash_here", "app.c", 13)}, true},
		{0x12a, []symcache.Location{loc("check_ptr", "app.c", 13), loc("crash_here", "app.c", 13)}, true},
		{0x24f, []symcache.Location{loc("main", "app.c", 31)}, true},
		{0x310, []symcache.Location{loc("helper", "", 0)}, true},
		{0x150, nil, false},
		{0x20000, nil, false},
	} {
		got, ok := c.Lookup(tc.rel)
		if ok != tc.ok || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("Lookup(%#x) = %v %v, want %v", tc.rel, got, ok, tc.want)
		}
	}
}

func TestPublicSizing(t *testing.T) {
	// A is cut off by B; B runs on to the FUNC after it; the FUNC wins over a PUBLIC inside it.
	sym := "MODULE Linux x86_64 7A3C9E1055B24B8E8A532F0C8D4E9A010 app\nPUBLIC 100 0 A\nPUBLIC 180 0 B\nFUNC 200 10 0 F\nPUBLIC 204 0 Inside\n"
	c, _ := buildFile(t, []byte(sym))
	for rel, want := range map[uint64]string{0x100: "A", 0x17f: "A", 0x180: "B", 0x1ff: "B", 0x200: "F", 0x204: "F"} {
		got, ok := c.Lookup(rel)
		if !ok || got[len(got)-1].Function != want {
			t.Errorf("Lookup(%#x) = %v %v, want %s", rel, got, ok, want)
		}
	}
	if _, ok := c.Lookup(0x210); ok {
		t.Error("address after FUNC resolved")
	}
}

func TestCFI(t *testing.T) {
	c, _ := buildFile(t, fixture(t))
	tab, err := OpenCFI(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		rel  uint64
		want string
		ok   bool
	}{
		{0x110, ".cfa: $rsp 8 + .ra: .cfa -8 + ^", true},
		{0x202, ".cfa: $rsp 8 + .ra: .cfa -8 + ^", true},
		{0x203, ".cfa: $rsp 8 + .ra: .cfa -8 + ^", true},
		{0x204, ".cfa: $rsp 16 + .ra: .cfa -8 + ^ $rbp: .cfa -16 + ^", true},
		{0x210, ".cfa: $rsp 16 + .ra: .cfa -8 + ^ $rbp: .cfa -16 + ^", true},
		{0x27f, ".cfa: $rsp 16 + .ra: .cfa -8 + ^ $rbp: .cfa -16 + ^", true},
		{0x280, "", false},
		{0xff, "", false},
	} {
		got, ok := tab.RulesAt(tc.rel)
		if ok != tc.ok || got != tc.want {
			t.Errorf("RulesAt(%#x) = %q %v, want %q %v", tc.rel, got, ok, tc.want, tc.ok)
		}
	}
	if got, _ := tab.RulesAt(0x202); strings.Contains(got, "$rbp") {
		t.Errorf("rbp rule before its delta: %q", got)
	}
}

func TestCFIDeltasFoldedAndUnsorted(t *testing.T) {
	sym := "MODULE Linux arm64 7A3C9E1055B24B8E8A532F0C8D4E9A010 app\n" +
		"STACK CFI INIT 1000 100 .cfa: sp 0 + .ra: x30\n" +
		"STACK CFI 1010 .cfa: sp 32 +\n" +
		"STACK CFI 1008 .cfa: sp 16 + x29: .cfa -16 + ^\n" +
		"STACK CFI 1010 x30: .cfa -8 + ^\n" +
		"STACK CFI 1008 x19: .cfa -24 + ^\n" +
		"STACK CFI 2000 .cfa: sp 99 +\n" // before any INIT it covers: attaches to the last INIT, outside its range
	c, feats := buildFile(t, []byte(sym))
	if !reflect.DeepEqual(feats, []string{"unwind"}) {
		t.Fatal(feats)
	}
	tab, err := OpenCFI(c)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := tab.RulesAt(0x1008)
	if want := ".cfa: sp 16 + .ra: x30 x29: .cfa -16 + ^ x19: .cfa -24 + ^"; got != want {
		t.Errorf("0x1008 = %q", got)
	}
	got, _ = tab.RulesAt(0x1020)
	if want := ".cfa: sp 32 + .ra: x30 x29: .cfa -16 + ^ x19: .cfa -24 + ^ x30: .cfa -8 + ^"; got != want {
		t.Errorf("0x1020 = %q", got)
	}
}

func TestOpenCFIAbsent(t *testing.T) {
	c, _ := buildFile(t, []byte("MODULE Linux x86_64 7A3C9E1055B24B8E8A532F0C8D4E9A010 a\nFUNC 10 10 0 f\n"))
	if _, err := OpenCFI(c); err != ErrNoCFI {
		t.Fatalf("err = %v", err)
	}
}

func TestBuildRejects(t *testing.T) {
	for _, in := range []string{"", "FUNC 1 2 0 x\n", "MODULE Linux x86_64 nothex app\nFUNC 1 2 0 x\n", "MODULE Linux x86_64 7A3C9E1055B24B8E8A532F0C8D4E9A010 a\n"} {
		if _, err := Build(strings.NewReader(in), io.Discard, t.TempDir()); err == nil {
			t.Errorf("%q accepted", in)
		}
	}
}

func TestBuildHostileLines(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("MODULE Linux x86_64 7A3C9E1055B24B8E8A532F0C8D4E9A010 app\n")
	sb.WriteString("FUNC zz 10 0 bad\nFUNC 10 -5 0 neg\nFUNC 10 0 0 empty\nFUNC ffffffffffffffff 10 0 wrap\n")
	sb.WriteString("FUNC 20 10 0 " + strings.Repeat("x", 1<<20) + "\n") // 1 MiB line: skipped
	sb.WriteString("20 10 5 7\n99999999999 1 1 0\nSTACK CFI 5 .cfa: x\nSTACK WIN 4 1 2 3\nINLINE 0 1 2\n")
	sb.WriteString("FUNC 400 10 0 ok\n400 10 1 0\n")
	c, _ := buildFile(t, []byte(sb.String()))
	if got, ok := c.Lookup(0x405); !ok || got[0].Function != "ok" || got[0].Line != 1 {
		t.Fatalf("Lookup = %v %v", got, ok)
	}
	if _, ok := c.Lookup(0x25); ok {
		t.Fatal("skipped function resolved")
	}
}

func TestBuildUnsortedFuncs(t *testing.T) {
	sym := "MODULE Linux x86_64 7A3C9E1055B24B8E8A532F0C8D4E9A010 a\nFUNC 300 10 0 c\nFUNC 100 10 0 a\nFUNC 200 10 0 b\nFUNC 205 10 0 overlap\n"
	c, _ := buildFile(t, []byte(sym))
	for rel, want := range map[uint64]string{0x100: "a", 0x200: "b", 0x300: "c"} {
		if got, ok := c.Lookup(rel); !ok || got[0].Function != want {
			t.Errorf("%#x = %v", rel, got)
		}
	}
}

// genSym streams a synthetic symbol file of about n lines.
func genSym(w io.Writer, n int) {
	fmt.Fprintf(w, "MODULE Linux x86_64 7A3C9E1055B24B8E8A532F0C8D4E9A010 big\n")
	for i := 0; i < 64; i++ {
		fmt.Fprintf(w, "FILE %d /src/dir%d/file%d.c\n", i, i, i)
	}
	lines, addr := 65, 0x1000
	for lines < n {
		fmt.Fprintf(w, "FUNC %x 40 0 fn_%d\n", addr, addr)
		fmt.Fprintf(w, "STACK CFI INIT %x 40 .cfa: $rsp 8 + .ra: .cfa -8 + ^\n", addr)
		fmt.Fprintf(w, "STACK CFI %x .cfa: $rsp 16 + $rbp: .cfa -16 + ^\n", addr+4)
		for l := 0; l < 8; l++ {
			fmt.Fprintf(w, "%x 8 %d %d\n", addr+8*l, 10+l, l%64)
		}
		fmt.Fprintf(w, "PUBLIC %x 0 pub_%d\n", addr+0x40, addr)
		lines += 12
		addr += 0x50
	}
}

func TestBuildLargeBoundedHeap(t *testing.T) {
	if testing.Short() {
		t.Skip("5M-line build")
	}
	pr, pw := io.Pipe()
	go func() {
		genSym(pw, 5_000_000)
		pw.Close()
	}()
	var peak atomic.Uint64
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		var m runtime.MemStats
		for {
			runtime.ReadMemStats(&m)
			if m.HeapAlloc > peak.Load() {
				peak.Store(m.HeapAlloc)
			}
			select {
			case <-stop:
				return
			case <-time.After(20 * time.Millisecond):
			}
		}
	}()
	// HeapAlloc includes garbage not yet collected, so with the default GC
	// target the peak depends on GC timing. A lower target keeps the sample
	// close to the live heap the bound is about.
	defer debug.SetGCPercent(debug.SetGCPercent(25))
	runtime.GC()
	f, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	feats, err := Build(pr, f, t.TempDir())
	close(stop)
	<-done
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(feats, []string{"symtab", "debug", "unwind"}) {
		t.Fatal(feats)
	}
	if mb := peak.Load() >> 20; mb > 128 {
		t.Fatalf("peak heap %d MiB > 128", mb)
	} else {
		t.Logf("peak heap %d MiB", mb)
	}
	st, _ := f.Stat()
	c, err := symcache.Open(f, st.Size())
	if err != nil {
		t.Fatal(err)
	}
	got, ok := c.Lookup(0x1000 + 0x50*1000 + 0x12)
	if !ok || got[0].Function != fmt.Sprintf("fn_%d", 0x1000+0x50*1000) {
		t.Fatalf("lookup = %v %v", got, ok)
	}
	if tab, err := OpenCFI(c); err != nil {
		t.Fatal(err)
	} else if r, ok := tab.RulesAt(0x1000 + 0x50*1000 + 8); !ok || !strings.Contains(r, "$rbp") {
		t.Fatalf("rules = %q %v", r, ok)
	}
}
