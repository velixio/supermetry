package symcache

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"testing"
	"time"

	"supermetry/internal/debugfile"
)

const testdata = "../debugfile/testdata"

type expFrame struct {
	Function string `json:"function"`
	File     string `json:"file"`
	Line     uint32 `json:"line"`
}

type expCase struct {
	Addr   uint64     `json:"addr"`
	Frames []expFrame `json:"frames"`
}

type expObject struct {
	File  string    `json:"file"`
	Base  uint64    `json:"base"`
	Cases []expCase `json:"cases"`
}

func loadExpected(t testing.TB) []expObject {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(testdata, "expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	var e struct{ Objects []expObject }
	if err := json.Unmarshal(b, &e); err != nil {
		t.Fatal(err)
	}
	return e.Objects
}

// source returns the bytes and the debugfile object of a fixture; the thin
// Mach-O names are slices of the committed fat file.
func source(t testing.TB, name string) ([]byte, debugfile.Object) {
	t.Helper()
	file, arch := name, ""
	switch name {
	case "fix-darwin-arm64":
		file, arch = "fix-darwin-fat", "arm64"
	case "fix-darwin-amd64":
		file, arch = "fix-darwin-fat", "x86_64"
	}
	data, err := os.ReadFile(filepath.Join(testdata, file))
	if err != nil {
		t.Fatal(err)
	}
	objs, err := debugfile.Detect(bytes.NewReader(data), int64(len(data)), file)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range objs {
		if arch == "" || o.Arch == arch {
			return data, o
		}
	}
	t.Fatalf("%s: no object with arch %q in %+v", name, arch, objs)
	return nil, debugfile.Object{}
}

func buildFixture(t testing.TB, name string) (*Cache, []string) {
	t.Helper()
	data, obj := source(t, name)
	var out bytes.Buffer
	feats, err := BuildFromObject(obj, bytes.NewReader(data), int64(len(data)), &out, t.TempDir())
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	c, err := Open(bytes.NewReader(out.Bytes()), int64(out.Len()))
	if err != nil {
		t.Fatal(err)
	}
	return c, feats
}

func TestBuildFromFixtures(t *testing.T) {
	for _, eo := range loadExpected(t) {
		t.Run(eo.File, func(t *testing.T) {
			c, feats := buildFixture(t, eo.File)
			if len(feats) == 0 {
				t.Fatal("no features")
			}
			if len(eo.Cases) == 0 {
				t.Fatal("no oracle cases")
			}
			for _, tc := range eo.Cases {
				got, ok := c.Lookup(tc.Addr - eo.Base)
				if !ok {
					t.Fatalf("%#x: not found, want %+v", tc.Addr, tc.Frames)
				}
				if len(got) != len(tc.Frames) {
					t.Fatalf("%#x: got %+v, want %+v", tc.Addr, got, tc.Frames)
				}
				for i, g := range got {
					w := tc.Frames[i]
					if g.Function != w.Function || path.Base(g.File) != path.Base(w.File) || g.Line != w.Line {
						t.Fatalf("%#x frame %d: got %+v, want %+v", tc.Addr, i, g, w)
					}
				}
			}
		})
	}
}

// TestFatSliceSelection is Review Focus 1: each slice of the fat Mach-O
// answers its own architecture's addresses, and the two are not the same.
func TestFatSliceSelection(t *testing.T) {
	byName := map[string]expObject{}
	for _, o := range loadExpected(t) {
		byName[o.File] = o
	}
	arm, amd := byName["fix-darwin-arm64"], byName["fix-darwin-amd64"]
	if arm.Base == amd.Base {
		t.Fatal("fixture bases must differ")
	}
	cArm, _ := buildFixture(t, "fix-darwin-arm64")
	cAmd, _ := buildFixture(t, "fix-darwin-amd64")
	if cArm.Arch() != "arm64" || cAmd.Arch() != "x86_64" {
		t.Fatalf("arches %q %q", cArm.Arch(), cAmd.Arch())
	}
	// Same relative address, different code: the slices must not be mixed up.
	for i, tc := range arm.Cases {
		got, ok := cArm.Lookup(tc.Addr - arm.Base)
		if !ok || got[0].Function != tc.Frames[0].Function || got[0].Line != tc.Frames[0].Line {
			t.Fatalf("arm64 case %d: %+v", i, got)
		}
	}
	for i, tc := range amd.Cases {
		got, ok := cAmd.Lookup(tc.Addr - amd.Base)
		if !ok || got[0].Function != tc.Frames[0].Function || got[0].Line != tc.Frames[0].Line {
			t.Fatalf("x86_64 case %d: %+v", i, got)
		}
	}
	differs := false
	for _, tc := range arm.Cases {
		a, _ := cArm.Lookup(tc.Addr - arm.Base)
		b, _ := cAmd.Lookup(tc.Addr - arm.Base)
		if len(a) != len(b) || (len(a) > 0 && a[0] != b[0]) {
			differs = true
		}
	}
	if !differs {
		t.Fatal("the two slices answered every arm64 address identically")
	}
}

func TestSymtabFallback(t *testing.T) {
	// libfix.so stripped of DWARF would have symbols only; emulate with the Go
	// object by forcing the symtab path on a function DWARF does not cover.
	o := newObjBuilder("x86_64", t.TempDir(), 0x1000)
	o.covered = [][2]uint64{{0x1100, 0x1140}}
	o.addSymbols([]symFn{{0x1000, 0, "a"}, {0x1100, 0x40, "covered"}, {0x1140, 0, "c"}, {0x1180, 0x10, "d"}}, 0x1200)
	var buf bytes.Buffer
	if _, err := o.finish(&buf); err != nil {
		t.Fatal(err)
	}
	c, err := Open(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	// "a" is sized by the next symbol but clipped at DWARF coverage (0x100).
	if got, ok := c.Lookup(0xff); !ok || got[0].Function != "a" {
		t.Fatalf("a: %+v %v", got, ok)
	}
	if _, ok := c.Lookup(0x100); ok {
		t.Fatal("covered range must not come from the symbol table")
	}
	if got, ok := c.Lookup(0x140); !ok || got[0].Function != "c" || got[0].Line != 0 {
		t.Fatalf("c: %+v", got)
	}
	if got, ok := c.Lookup(0x18f); !ok || got[0].Function != "d" {
		t.Fatalf("d: %+v", got)
	}
}

func TestBuildRejectsHostile(t *testing.T) {
	data, obj := source(t, "fix-linux-amd64")
	cases := map[string][]byte{
		"truncated elf": data[:len(data)/3],
		"garbage":       bytes.Repeat([]byte{0x7f}, 4096),
	}
	for name, b := range cases {
		var out bytes.Buffer
		if _, err := BuildFromObject(obj, bytes.NewReader(b), int64(len(b)), &out, t.TempDir()); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// Mach-O with 2^31 load commands.
	fat, mobj := source(t, "fix-darwin-arm64")
	off, n, err := machoSlice(bytes.NewReader(fat), int64(len(fat)), mobj.Slice)
	if err != nil {
		t.Fatal(err)
	}
	hostile := bytes.Clone(fat[off : off+n])
	hostile[16], hostile[17], hostile[18], hostile[19] = 0, 0, 0, 0x80 // ncmds = 2^31
	mobj.Slice = 0
	var out bytes.Buffer
	if _, err := BuildFromObject(mobj, bytes.NewReader(hostile), int64(len(hostile)), &out, t.TempDir()); err == nil {
		t.Error("2^31 load commands accepted")
	}
	// Unsupported kind.
	if _, err := BuildFromObject(debugfile.Object{Kind: "pe"}, bytes.NewReader(nil), 0, &out, t.TempDir()); err == nil {
		t.Error("pe accepted")
	}
}

// TestBuildLargeObject measures a build of the file named by SYMCACHE_BENCH_FILE
// (an ELF); run the test binary under /usr/bin/time -v for peak RSS.
func TestBuildLargeObject(t *testing.T) {
	p := os.Getenv("SYMCACHE_BENCH_FILE")
	if p == "" {
		t.Skip("set SYMCACHE_BENCH_FILE")
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	st, _ := f.Stat()
	out, err := os.CreateTemp(t.TempDir(), "cache")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	start := time.Now()
	feats, err := BuildFromObject(debugfile.Object{Kind: "elf", Arch: "x86_64"}, f, st.Size(), out, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cst, _ := out.Stat()
	took := time.Since(start)
	c, err := Open(out, cst.Size())
	if err != nil {
		t.Fatal(err)
	}
	nf, nl, ni := c.Counts()
	t.Logf("input %d MiB, cache %d MiB (%d functions, %d lines, %d inlines), features %v, build %v", st.Size()>>20, cst.Size()>>20, nf, nl, ni, feats, took)
}

func FuzzBuildFromObject(f *testing.F) {
	for _, n := range []string{"libfix.so", "fix-linux-arm64"} {
		d, err := os.ReadFile(filepath.Join(testdata, n))
		if err != nil {
			f.Fatal(err)
		}
		if len(d) > 64<<10 {
			d = d[:64<<10] // a truncated Go binary still exercises the parsers
		}
		f.Add(d, true)
	}
	f.Fuzz(func(t *testing.T, data []byte, elfKind bool) {
		kind := "macho"
		if elfKind {
			kind = "elf"
		}
		var out bytes.Buffer
		_, _ = BuildFromObject(debugfile.Object{Kind: kind, Arch: "x"}, bytes.NewReader(data), int64(len(data)), &out, t.TempDir())
	})
}

// TestStrippedELFUsesSymtab strips the DWARF of the C fixture: the cache must
// still name functions, with the symtab feature and no lines.
func TestStrippedELFUsesSymtab(t *testing.T) {
	strip, err := exec.LookPath("strip")
	if err != nil {
		t.Skip("strip not installed")
	}
	dst := filepath.Join(t.TempDir(), "stripped.so")
	if out, err := exec.Command(strip, "--strip-debug", "-o", dst, filepath.Join(testdata, "libfix.so")).CombinedOutput(); err != nil {
		t.Skipf("strip: %v %s", err, out)
	}
	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	feats, err := BuildFromObject(debugfile.Object{Kind: "elf", Arch: "x86_64"}, bytes.NewReader(data), int64(len(data)), &out, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(feats) != 1 || feats[0] != FeatureSymtab {
		t.Fatalf("features %v, want [symtab]", feats)
	}
	c, err := Open(bytes.NewReader(out.Bytes()), int64(out.Len()))
	if err != nil {
		t.Fatal(err)
	}
	got, ok := c.Lookup(0x1117)
	if !ok || got[0].Function != "fix_plain" || got[0].Line != 0 {
		t.Fatalf("got %+v %v", got, ok)
	}
}
