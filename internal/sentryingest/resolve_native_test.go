package sentryingest

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"supermetry/internal/appingest"
	"supermetry/internal/debugfile"
	"supermetry/internal/symcache"
)

const nativeTestdata = "../debugfile/testdata"

type nativeFixture struct {
	obj   debugfile.Object
	cache []byte
}

// fixtureCache builds the symbol cache of a committed fixture; the thin
// Mach-O names pick a slice of the fat file.
func fixtureCache(t testing.TB, name string) nativeFixture {
	t.Helper()
	file, arch := name, ""
	switch name {
	case "fix-darwin-arm64":
		file, arch = "fix-darwin-fat", "arm64"
	case "fix-darwin-amd64":
		file, arch = "fix-darwin-fat", "x86_64"
	}
	data, err := os.ReadFile(filepath.Join(nativeTestdata, file))
	if err != nil {
		t.Fatal(err)
	}
	objs, err := debugfile.Detect(bytes.NewReader(data), int64(len(data)), file)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range objs {
		if arch != "" && o.Arch != arch {
			continue
		}
		var out bytes.Buffer
		if _, err := symcache.BuildFromObject(o, bytes.NewReader(data), int64(len(data)), &out, t.TempDir()); err != nil {
			t.Fatal(err)
		}
		return nativeFixture{o, out.Bytes()}
	}
	t.Fatalf("no object for %s", name)
	return nativeFixture{}
}

type fakeDebugFiles struct{ keys map[string]string }

func (f fakeDebugFiles) CacheFor(_ context.Context, _ int32, kind, id string) (string, bool, error) {
	k, ok := f.keys[kind+"|"+id]
	return k, ok, nil
}

type missRecorder struct {
	mu   sync.Mutex
	seen []appingest.DebugImage
}

func (m *missRecorder) Missing(_ int32, _ string, im appingest.DebugImage, _ time.Time) {
	m.mu.Lock()
	m.seen = append(m.seen, im)
	m.mu.Unlock()
}

func nativeSymbolicator(t *testing.T, fixes ...nativeFixture) (*Symbolicator, *collector, *missRecorder) {
	t.Helper()
	blobs, df := newMemBlobs(), fakeDebugFiles{keys: map[string]string{}}
	for _, f := range fixes {
		key := "difcache/1/" + f.obj.DebugID + "/" + f.obj.Kind + "-v1"
		blobs.files[key] = f.cache
		df.keys[f.obj.Kind+"|"+f.obj.DebugID] = key
	}
	miss := &missRecorder{}
	s, col := newSymb(t, newFakeArtifacts(), blobs, func(c *SymbolicatorConfig) {
		c.Resolvers = []Resolver{JSResolver(), NativeResolver()}
		c.DebugFiles = df
		c.Disk = debugfile.NewDiskCache(t.TempDir(), 64<<20)
		c.Misses = miss
	})
	return s, col, miss
}

func nativeEvent(images []appingest.DebugImage, handled bool, frames ...appingest.Frame) (appingest.Event, appingest.Body) {
	h := handled
	b := appingest.Body{SiteID: 7, Platform: "native", DebugImages: images,
		Exceptions: []appingest.Exception{{Type: "SIGSEGV", Mechanism: &appingest.Mechanism{Type: "signal", Handled: &h}, Frames: frames}}}
	return appingest.Event{ID: "e1", Platform: "native", Kind: "crash"}, b
}

func addrStr(a uint64) string { return "0x" + strconv.FormatUint(a, 16) }

func TestNativeResolveELFReviewFocus3(t *testing.T) {
	fx := fixtureCache(t, "libfix.so")
	s, col, miss := nativeSymbolicator(t, fx)
	const base = 0x7f0000000000
	img := appingest.DebugImage{Type: "elf", DebugID: fx.obj.DebugID, CodeFile: "/data/app/~~x/lib/arm64/libfix.so", ImageAddr: base, ImageSize: 0x4000}
	// 0x1111 is the first byte of fix_plain and one past the last byte of
	// fix_outer: as a return address it belongs to fix_outer's call.
	ev, body := nativeEvent([]appingest.DebugImage{img}, false,
		appingest.Frame{InstructionAddr: addrStr(base + 0x1111), Package: img.CodeFile},
		appingest.Frame{InstructionAddr: addrStr(base + 0x1111), Package: img.CodeFile},
	)
	if !s.Submit(ev, body, nil) {
		t.Fatal("not queued")
	}
	gev, gb := col.wait(t)
	if gev.Symbolication != "ok" {
		t.Fatalf("symbolication %q", gev.Symbolication)
	}
	fr := gb.Exceptions[0].Frames
	if len(fr) != 2 {
		t.Fatalf("frames %+v", fr)
	}
	if fr[0].Function != "fix_outer" || fr[0].Lineno != 13 || path.Base(fr[0].Filename) != "fixture.c" {
		t.Fatalf("caller frame %+v", fr[0])
	}
	if fr[1].Function != "fix_plain" || fr[1].Lineno != 16 {
		t.Fatalf("crash frame %+v", fr[1])
	}
	if !fr[0].InApp || !fr[1].InApp {
		t.Fatal("/data/app frames are in_app")
	}
	if len(miss.seen) != 0 {
		t.Fatalf("unexpected misses %+v", miss.seen)
	}
	if gev.Fingerprint == "" {
		t.Fatal("grouping not recomputed")
	}
}

func TestNativeResolveInlineAndMissingImage(t *testing.T) {
	fx := fixtureCache(t, "libfix.so")
	s, col, miss := nativeSymbolicator(t, fx)
	const base = 0x7f0000000000
	img := appingest.DebugImage{Type: "elf", DebugID: fx.obj.DebugID, CodeFile: "/system/lib/libfix.so", ImageAddr: base, ImageSize: 0x4000}
	other := appingest.DebugImage{Type: "elf", DebugID: "11111111-2222-3333-4444-555555555555", CodeFile: "/system/lib/libother.so", ImageAddr: 0x7e0000000000, ImageSize: 0x4000}
	ev, body := nativeEvent([]appingest.DebugImage{img, other}, false,
		appingest.Frame{InstructionAddr: addrStr(0x7e0000000000 + 0x40), Package: other.CodeFile},
		appingest.Frame{InstructionAddr: addrStr(base + 0x10fc), Package: img.CodeFile},
	)
	s.Submit(ev, body, nil)
	gev, gb := col.wait(t)
	if gev.Symbolication != "partial" {
		t.Fatalf("symbolication %q, want partial", gev.Symbolication)
	}
	fr := gb.Exceptions[0].Frames
	// unresolved frame, then the inlined chain oldest first: fix_outer, helper
	if len(fr) != 3 || fr[0].Function != "" || fr[1].Function != "fix_outer" || fr[1].Lineno != 10 || fr[2].Function != "helper" || fr[2].Lineno != 3 {
		t.Fatalf("frames %+v", fr)
	}
	if fr[1].InApp || fr[2].InApp {
		t.Fatal("system library must not be in_app")
	}
	if len(miss.seen) != 1 || miss.seen[0].DebugID != other.DebugID {
		t.Fatalf("misses %+v", miss.seen)
	}
}

// TestNativeResolveFatMachoReviewFocus1 uploads a fat Mach-O (x86_64 first)
// and symbolicates an arm64 event: the arm64 slice answers.
func TestNativeResolveFatMachoReviewFocus1(t *testing.T) {
	arm, amd := fixtureCache(t, "fix-darwin-arm64"), fixtureCache(t, "fix-darwin-amd64")
	if arm.obj.DebugID == amd.obj.DebugID {
		t.Fatal("slices must have distinct uuids")
	}
	s, col, _ := nativeSymbolicator(t, amd, arm)
	exp := loadNativeExpected(t)
	eo := exp["fix-darwin-arm64"]
	const slide = 0x4000
	img := appingest.DebugImage{Type: "macho", DebugID: arm.obj.DebugID, Arch: "arm64", CodeFile: "/private/var/containers/Bundle/Application/X/Fix.app/Fix",
		ImageAddr: eo.Base + slide, ImageSize: 0x100000}
	var frames []appingest.Frame
	for _, c := range eo.Cases[:3] {
		frames = append(frames, appingest.Frame{InstructionAddr: addrStr(c.Addr + slide), Package: img.CodeFile})
	}
	ev, body := nativeEvent([]appingest.DebugImage{img}, false, frames...)
	body.Platform, ev.Platform = "cocoa", "cocoa"
	s.Submit(ev, body, nil)
	gev, gb := col.wait(t)
	if gev.Symbolication != "ok" {
		t.Fatalf("symbolication %q", gev.Symbolication)
	}
	got := gb.Exceptions[0].Frames
	if len(got) != 3 {
		t.Fatalf("frames %+v", got)
	}
	// the last frame is the crash frame (exact address); earlier ones use addr-1
	// which stays inside the same function for these mid-function addresses.
	last := eo.Cases[2]
	if got[2].Function != last.Frames[0].Function || got[2].Lineno != int(last.Frames[0].Line) {
		t.Fatalf("crash frame %+v, want %+v", got[2], last.Frames[0])
	}
	if !got[0].InApp {
		t.Fatal(".app images are in_app")
	}
}

type nativeExpCase struct {
	Addr   uint64 `json:"addr"`
	Frames []struct {
		Function string `json:"function"`
		File     string `json:"file"`
		Line     uint32 `json:"line"`
	} `json:"frames"`
}

type nativeExpObject struct {
	File  string          `json:"file"`
	Base  uint64          `json:"base"`
	Cases []nativeExpCase `json:"cases"`
}

func loadNativeExpected(t testing.TB) map[string]nativeExpObject {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(nativeTestdata, "expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	var e struct{ Objects []nativeExpObject }
	if err := json.Unmarshal(b, &e); err != nil {
		t.Fatal(err)
	}
	m := map[string]nativeExpObject{}
	for _, o := range e.Objects {
		m[o.File] = o
	}
	return m
}

func TestNativeWants(t *testing.T) {
	r := NativeResolver()
	frame := appingest.Frame{InstructionAddr: "0x1000"}
	ex := []appingest.Exception{{Frames: []appingest.Frame{frame}}}
	if !r.Wants(appingest.Body{Platform: "cocoa", Exceptions: ex}) {
		t.Fatal("cocoa with addresses")
	}
	if !r.Wants(appingest.Body{Platform: "other", DebugImages: []appingest.DebugImage{{Type: "elf"}}, Exceptions: ex}) {
		t.Fatal("elf image")
	}
	if r.Wants(appingest.Body{Platform: "cocoa", Exceptions: []appingest.Exception{{Frames: []appingest.Frame{{Function: "f"}}}}}) {
		t.Fatal("no instruction addresses")
	}
	if r.Wants(appingest.Body{Platform: "javascript", Exceptions: ex}) {
		t.Fatal("javascript")
	}
}

func TestNativeHandledExceptionAdjustsEveryFrame(t *testing.T) {
	fx := fixtureCache(t, "libfix.so")
	s, col, _ := nativeSymbolicator(t, fx)
	const base = 0x7f0000000000
	img := appingest.DebugImage{Type: "elf", DebugID: fx.obj.DebugID, CodeFile: "/x/libfix.so", ImageAddr: base, ImageSize: 0x4000}
	ev, body := nativeEvent([]appingest.DebugImage{img}, true,
		appingest.Frame{InstructionAddr: addrStr(base + 0x1111), Package: img.CodeFile})
	s.Submit(ev, body, nil)
	_, gb := col.wait(t)
	if f := gb.Exceptions[0].Frames[0]; f.Function != "fix_outer" {
		t.Fatalf("handled exception frames are return addresses: %+v", f)
	}
}
