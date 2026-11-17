package sourcemap

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"strings"
	"testing"
)

type entry struct {
	name string
	data string
}

func buildZip(t testing.TB, prefix []byte, entries ...entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	buf.Write(prefix)
	w := zip.NewWriter(&buf)
	if len(prefix) > 0 {
		w.SetOffset(int64(len(prefix))) // absolute offsets, like a stream written after a header
	}
	for _, e := range entries {
		f, err := w.Create(e.name)
		if err != nil {
			t.Fatal(err)
		}
		f.Write([]byte(e.data))
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sysb(version uint32) []byte {
	h := make([]byte, 8)
	copy(h, "SYSB")
	binary.LittleEndian.PutUint32(h[4:], version)
	return h
}

func parse(t testing.TB, b []byte, caps Caps) (*Bundle, error) {
	return ParseBundle(bytes.NewReader(b), int64(len(b)), caps)
}

const id1 = "11111111-2222-3333-4444-555555555555"

func TestViteBundleFixture(t *testing.T) {
	b := readFixture(t, "bundle.zip")
	if string(b[:4]) != "SYSB" {
		t.Fatal("fixture should carry the SYSB header")
	}
	bun, err := parse(t, b, DefaultCaps())
	if err != nil {
		t.Fatal(err)
	}
	e := loadExpected(t)
	if bun.Org != "fixture-org" || bun.Project != "fixture-project" || bun.Release != "fixture-1.0.0" || len(bun.Files) != 2 {
		t.Fatalf("%+v", bun)
	}
	var js, mp BundleFile
	for _, f := range bun.Files {
		switch f.Type {
		case "minified_source":
			js = f
		case "source_map":
			mp = f
		}
	}
	if js.DebugID != e.DebugID || js.SourceMapRef != "app.js.map" || js.URL != "~/assets/app.js" || mp.DebugID != e.DebugID {
		t.Fatalf("js=%+v map=%+v", js, mp)
	}
	src, err := bun.ReadFile(js.Path)
	if err != nil || DebugIDFromSource(src) != e.DebugID {
		t.Fatalf("source: %v", err)
	}
	mb, err := bun.ReadFile(mp.Path)
	if err != nil {
		t.Fatal(err)
	}
	m, err := ParseMap(mb)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := m.Lookup(e.Minified.Line, e.Minified.NewCol); !ok || got.Line != 12 {
		t.Fatalf("lookup through bundle: %+v %v", got, ok)
	}
}

// The Rust CLI (symbolic SourceBundleWriter) nests the descriptive fields in
// "attributes" and uses CamelCase file types.
func TestAttributesManifestLayout(t *testing.T) {
	manifest := `{"attributes":{"debug_id":"` + id1 + `","org":"o","project":"p","release":"r1","dist":"d1"},
	"files":{
	  "_/_/a.js":{"type":"MinifiedSource","url":"~/a.js","headers":{"Debug-Id":"` + strings.ToUpper(id1) + `","SourceMap":"a.js.map"}},
	  "_/_/a.js.map":{"type":"SourceMap","url":"~/a.js.map","headers":{"debug-id":"` + id1 + `"}},
	  "_/_/src.ts":{"type":"source","url":"~/src.ts"},
	  "_/_/ram":{"type":"IndexedRamBundle","url":"~/ram"},
	  "_/_/missing.js":{"type":"source","url":"~/missing.js"}
	}}`
	data := buildZip(t, sysb(2),
		entry{"manifest.json", manifest}, entry{"_/_/a.js", "x"}, entry{"_/_/a.js.map", "{}"},
		entry{"_/_/src.ts", "s"}, entry{"_/_/ram", "r"})
	bun, err := parse(t, data, DefaultCaps())
	if err != nil {
		t.Fatal(err)
	}
	if bun.Org != "o" || bun.Project != "p" || bun.Release != "r1" || bun.Dist != "d1" {
		t.Fatalf("attributes not read: %+v", bun)
	}
	if len(bun.Files) != 4 {
		t.Fatalf("files (the entry absent from the ZIP is skipped): %+v", bun.Files)
	}
	want := map[string]BundleFile{
		"_/_/a.js":     {Path: "_/_/a.js", URL: "~/a.js", Type: "minified_source", DebugID: id1, SourceMapRef: "a.js.map", Size: 1},
		"_/_/a.js.map": {Path: "_/_/a.js.map", URL: "~/a.js.map", Type: "source_map", DebugID: id1, Size: 2},
		"_/_/ram":      {Path: "_/_/ram", URL: "~/ram", Type: "indexed_ram_bundle", Size: 1},
		"_/_/src.ts":   {Path: "_/_/src.ts", URL: "~/src.ts", Type: "source", Size: 1},
	}
	for _, f := range bun.Files {
		if f != want[f.Path] {
			t.Errorf("%s: got %+v want %+v", f.Path, f, want[f.Path])
		}
	}
}

func TestBundleWithoutPrefixAndRelativeOffsets(t *testing.T) {
	manifest := `{"org":"o","project":"p","files":{"a.js":{"type":"source","url":"~/a.js"}}}`
	plain := buildZip(t, nil, entry{"manifest.json", manifest}, entry{"a.js", "x"})
	if bun, err := parse(t, plain, DefaultCaps()); err != nil || len(bun.Files) != 1 {
		t.Fatalf("no prefix: %v", err)
	}
	// ZIP with zip-relative offsets behind the header.
	rel := append(sysb(2), plain...)
	if bun, err := parse(t, rel, DefaultCaps()); err != nil || len(bun.Files) != 1 {
		t.Fatalf("relative offsets: %v", err)
	}
	if _, err := parse(t, append(sysb(3), plain...), DefaultCaps()); err == nil {
		t.Error("SYSB version 3 must be rejected")
	}
}

func TestBundleErrors(t *testing.T) {
	for name, b := range map[string][]byte{
		"empty":       nil,
		"garbage":     bytes.Repeat([]byte("x"), 100),
		"no manifest": buildZip(t, nil, entry{"a.js", "x"}),
		"bad json":    buildZip(t, nil, entry{"manifest.json", "{"}),
	} {
		if _, err := parse(t, b, DefaultCaps()); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestBundleCaps(t *testing.T) {
	m := entry{"manifest.json", `{"files":{}}`}
	big := strings.Repeat("a", 1<<20) // compresses to about 1 KiB
	zeros := buildZip(t, nil, m, entry{"big.js", big})
	if len(zeros) > 4096 {
		t.Fatalf("bomb too large: %d", len(zeros))
	}
	caps := Caps{MaxTotal: 1 << 30, MaxFiles: 10, MaxFile: 1 << 10}
	if _, err := parse(t, zeros, caps); !errors.Is(err, ErrCap) {
		t.Errorf("per-file cap: %v", err)
	}
	caps = Caps{MaxTotal: 1 << 10, MaxFiles: 10, MaxFile: 1 << 30}
	if _, err := parse(t, zeros, caps); !errors.Is(err, ErrCap) {
		t.Errorf("total cap: %v", err)
	}
	caps = Caps{MaxTotal: 1 << 30, MaxFiles: 2, MaxFile: 1 << 30}
	many := buildZip(t, nil, m, entry{"1", "x"}, entry{"2", "x"}, entry{"3", "x"})
	if _, err := parse(t, many, caps); !errors.Is(err, ErrCap) {
		t.Errorf("file count cap: %v", err)
	}
	if _, err := parse(t, zeros, DefaultCaps()); err != nil {
		t.Errorf("within default caps: %v", err)
	}
}

// A few bytes of data in an archive that declares 10 GiB must be refused from
// the headers alone, before anything is decompressed.
func TestZipBombDeclaredSize(t *testing.T) {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	f, _ := w.Create("manifest.json")
	f.Write([]byte(`{"files":{}}`))
	raw, err := w.CreateRaw(&zip.FileHeader{
		Name: "bomb.js", Method: zip.Store, CRC32: crc32.ChecksumIEEE([]byte("x")),
		CompressedSize64: 1, UncompressedSize64: 10 << 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	raw.Write([]byte("x"))
	w.Close()
	if buf.Len() > 1024 {
		t.Fatalf("archive too large: %d", buf.Len())
	}
	if _, err := parse(t, buf.Bytes(), DefaultCaps()); !errors.Is(err, ErrCap) {
		t.Fatalf("expected ErrCap, got %v", err)
	}
}

func TestReadFileEnforcesCapOnActualBytes(t *testing.T) {
	data := buildZip(t, nil, entry{"manifest.json", `{"files":{"a.js":{"type":"source"}}}`}, entry{"a.js", strings.Repeat("a", 100)})
	bun, err := parse(t, data, Caps{MaxTotal: 1 << 20, MaxFiles: 10, MaxFile: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	bun.caps.MaxFile = 10 // as if the header had under-declared
	if _, err := bun.ReadFile("a.js"); !errors.Is(err, ErrCap) {
		t.Fatalf("expected ErrCap, got %v", err)
	}
	if _, err := bun.ReadFile("nope"); err == nil {
		t.Fatal("missing file should fail")
	}
}

func TestDebugIDFromSource(t *testing.T) {
	for in, want := range map[string]string{
		"x\n//# debugId=" + id1 + "\n":                        id1,
		"x\n//# debugId=" + strings.ToUpper(id1):              id1,
		"//# debugId=11111111222233334444555555555555\n":      id1,
		"//# debugId=" + id1 + "\n//# sourceMappingURL=a.map": id1,
		"//# debugId=zzz\n":                                   "",
		"nothing here":                                        "",
		"":                                                    "",
	} {
		if got := DebugIDFromSource([]byte(in)); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func FuzzParseBundle(f *testing.F) {
	f.Add(readFixture(f, "bundle.zip"))
	f.Add(buildZip(f, sysb(2), entry{"manifest.json", `{"attributes":{"org":"o"},"files":{"a":{"type":"source","headers":{"debug-id":"` + id1 + `"}}}}`}, entry{"a", "x"}))
	f.Add(buildZip(f, nil, entry{"manifest.json", `{"files":{}}`}))
	f.Add([]byte("SYSB\x02\x00\x00\x00PK"))
	caps := Caps{MaxTotal: 8 << 20, MaxFiles: 100, MaxFile: 4 << 20}
	f.Fuzz(func(t *testing.T, b []byte) {
		bun, err := ParseBundle(bytes.NewReader(b), int64(len(b)), caps)
		if err != nil {
			return
		}
		for _, bf := range bun.Files {
			bun.ReadFile(bf.Path)
		}
	})
}
