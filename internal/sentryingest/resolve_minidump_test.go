package sentryingest

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"supermetry/internal/appingest"
	"supermetry/internal/breakpad"
	"supermetry/internal/debugfile"
	"supermetry/internal/minidump"
	"supermetry/internal/minidump/minidumptest"
)

const appBreakpadID = "7a3c9e10-55b2-4b8e-8a53-2f0c8d4e9a01"

var appBuildID = []byte{0x10, 0x9e, 0x3c, 0x7a, 0xb2, 0x55, 0x8e, 0x4b, 0x8a, 0x53, 0x2f, 0x0c, 0x8d, 0x4e, 0x9a, 0x01, 0xc0, 0xff, 0xee, 0x00}

func stackWords(w ...uint64) []byte {
	b := make([]byte, 8*len(w))
	for i, v := range w {
		binary.LittleEndian.PutUint64(b[i*8:], v)
	}
	return b
}

// appDump is a Linux amd64 crash in module app at 0x400000: RIP 0x400123 and a
// return address 0x400250 on the stack.
func appDump(extra ...[]byte) []byte {
	b := &minidumptest.Builder{OS: "linux", CPU: "amd64"}
	b.AddModule("/opt/app/app", 0x400000, 0x10000, appBuildID)
	ctx := minidump.Context{CPU: "amd64", PC: 0x400123, SP: 0x7ff000, Regs: map[string]uint64{"rip": 0x400123, "rsp": 0x7ff000}}
	b.AddThread(7, ctx, 0x7ff000, stackWords(0x400250, 0, 0))
	b.SetException(7, 11, 1, 0)
	for i, e := range extra {
		b.AddMemory(0x900000+uint64(i)*0x1000, e)
	}
	return b.Bytes()
}

func appSymCache(t testing.TB) []byte {
	t.Helper()
	sym, err := os.ReadFile("../breakpad/testdata/app.sym")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if _, err := breakpad.Build(bytes.NewReader(sym), &out, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func dumpSymbolicator(t *testing.T, withSym bool, mut ...func(*SymbolicatorConfig)) (*Symbolicator, *collector, *nativeMissRecorder) {
	t.Helper()
	blobs, df := newMemBlobs(), nativeDebugFiles{keys: map[string]string{}}
	if withSym {
		blobs.files["difcache/1/app/breakpad-v1"] = appSymCache(t)
		df.keys["breakpad|"+appBreakpadID] = "difcache/1/app/breakpad-v1"
	}
	miss := &nativeMissRecorder{}
	s, col := newSymb(t, newFakeArtifacts(), blobs, append([]func(*SymbolicatorConfig){func(c *SymbolicatorConfig) {
		c.Resolvers = []Resolver{JSResolver(), MinidumpResolver(), NativeResolver()}
		c.DebugFiles = df
		c.Disk = debugfile.NewDiskCache(t.TempDir(), 64<<20)
		c.Misses = miss
	}}, mut...)...)
	return s, col, miss
}

func dumpEvent(dump []byte) (appingest.Event, appingest.Body) {
	ev := appingest.Event{ID: "d1", Platform: "linux", Kind: "crash", Level: "fatal"}
	return ev, appingest.Body{SiteID: 7, Platform: "native", Level: "fatal", Minidump: dump}
}

func frameNames(b appingest.Body) []string {
	var out []string
	for _, f := range b.Exceptions[0].Frames {
		out = append(out, f.Function)
	}
	return out
}

func TestMinidumpResolveSymbolicates(t *testing.T) {
	s, col, _ := dumpSymbolicator(t, true)
	ev, body := dumpEvent(appDump())
	if !s.Submit(ev, body, nil) {
		t.Fatal("not queued")
	}
	gev, gb := col.wait(t)
	if gev.Symbolication != "ok" || gb.Symbolication != "ok" {
		t.Fatalf("symbolication = %q", gev.Symbolication)
	}
	if got := frameNames(gb); strings.Join(got, ",") != "main,crash_here" {
		t.Fatalf("frames = %v", got)
	}
	ex := gb.Exceptions[0]
	if ex.Type != "SIGSEGV / SEGV_MAPERR" || ex.Value != "Fatal error at 0x0" || ex.Mechanism == nil || ex.Mechanism.Type != "minidump" || *ex.Mechanism.Handled {
		t.Fatalf("exception = %+v", ex)
	}
	top := ex.Frames[1]
	if top.Filename != "app.c" || top.Lineno != 13 || top.Trust != "context" || ex.Frames[0].Lineno != 31 || ex.Frames[0].Trust != "cfi" {
		t.Fatalf("frames = %+v", ex.Frames)
	}
	if gev.ErrorType != "SIGSEGV / SEGV_MAPERR" || gev.Signal != "SIGSEGV" || gev.Title != "SIGSEGV / SEGV_MAPERR: Fatal error at 0x0" || len(gev.MessageHash) == 0 {
		t.Fatalf("event = %+v", gev)
	}
	if gb.Minidump != nil || len(gb.MissingDebug) != 0 {
		t.Fatalf("body keeps the dump or reports missing symbols: %d bytes, %+v", len(gb.Minidump), gb.MissingDebug)
	}
	if gb.Contexts["os"]["name"] != "Linux" || gb.Contexts["device"]["arch"] != "amd64" {
		t.Fatalf("contexts = %v", gb.Contexts)
	}
	if gev.Frames == "" || !strings.Contains(gev.Frames, "crash_here") {
		t.Fatalf("event frames = %q", gev.Frames)
	}
}

func TestMinidumpWithoutSymbolsKeepsStableGrouping(t *testing.T) {
	s, col, miss := dumpSymbolicator(t, false)
	var fps []string
	for i := 0; i < 2; i++ {
		ev, body := dumpEvent(appDump())
		ev.ID = string(rune('a' + i))
		if !s.Submit(ev, body, nil) {
			t.Fatal("not queued")
		}
		gev, gb := col.wait(t)
		if gev.Symbolication != "missing" {
			t.Fatalf("symbolication = %q", gev.Symbolication)
		}
		if got := frameNames(gb); strings.Join(got, ",") != "app+0x250,app+0x123" {
			t.Fatalf("frames = %v", got)
		}
		if len(gb.MissingDebug) != 1 || gb.MissingDebug[0].Kind != "breakpad" || gb.MissingDebug[0].DebugID != appBreakpadID || gb.MissingDebug[0].CodeFile != "app" {
			t.Fatalf("missing = %+v", gb.MissingDebug)
		}
		fps = append(fps, gev.Fingerprint)
	}
	if fps[0] == "" || fps[0] != fps[1] {
		t.Fatalf("fingerprints differ: %v", fps)
	}
	miss.mu.Lock()
	defer miss.mu.Unlock()
	if len(miss.seen) == 0 || miss.seen[0].DebugID != appBreakpadID {
		t.Fatalf("misses = %+v", miss.seen)
	}
}

func TestMinidumpInvalidKeepsEventMissing(t *testing.T) {
	stats := &Stats{}
	s, col, _ := dumpSymbolicator(t, true, func(c *SymbolicatorConfig) { c.Stats = stats })
	ev, body := dumpEvent([]byte("MDMP-but-not-really-a-dump-at-all-xxxxxxxxxxxxxxxxxxxx"))
	if !s.Submit(ev, body, nil) {
		t.Fatal("not queued")
	}
	gev, gb := col.wait(t)
	if gev.Symbolication != "missing" || gb.Symbolication != "missing" || len(gb.Exceptions) != 0 || gb.Minidump != nil {
		t.Fatalf("event %+v body %+v", gev, gb)
	}
	if stats.Snapshot()["minidump_invalid"] != 1 {
		t.Fatalf("stats = %v", stats.Snapshot())
	}
}

type fakeRemote struct{ key string }

func (f fakeRemote) CacheFor(_ context.Context, _ int32, m appingest.DebugImage) (string, bool, error) {
	if m.DebugID == appBreakpadID {
		return f.key, true, nil
	}
	return "", false, nil
}

func TestMinidumpUsesRemoteSymbolsWhenUploadsMiss(t *testing.T) {
	blobs := newMemBlobs()
	blobs.files["remote/app"] = appSymCache(t)
	s, col, _ := dumpSymbolicator(t, false, func(c *SymbolicatorConfig) {
		c.Remote = fakeRemote{key: "remote/app"}
		c.Resolvers = []Resolver{MinidumpResolver(), NativeResolver()}
	})
	s.blobs = blobs
	ev, body := dumpEvent(appDump())
	s.Submit(ev, body, nil)
	gev, gb := col.wait(t)
	if gev.Symbolication != "ok" || strings.Join(frameNames(gb), ",") != "main,crash_here" {
		t.Fatalf("%q %v", gev.Symbolication, frameNames(gb))
	}
}

func TestMinidumpBytesAreNeverInTheDeliveredBody(t *testing.T) {
	s, col, _ := dumpSymbolicator(t, true)
	home := []byte("HOME=/home/alice and alice@example.com")
	ev, body := dumpEvent(appDump(home))
	s.Submit(ev, body, nil)
	gev, gb := col.wait(t)
	raw, _ := json.Marshal(gb)
	rawEv, _ := json.Marshal(gev)
	for _, s := range []string{"HOME=/home/alice", "alice@example.com", "/home/alice"} {
		if strings.Contains(string(raw), s) || strings.Contains(string(rawEv), s) {
			t.Fatalf("%q leaked", s)
		}
	}
}
