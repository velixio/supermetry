package sentryingest

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"supermetry/internal/appingest"
	"supermetry/internal/breakpad"
	"supermetry/internal/debugfile"
	"supermetry/internal/minidump"
	"supermetry/internal/stackwalk"
)

// The real Electron dump: recorded from @sentry/electron 8.4.0 on Electron
// 40.10.6 (linux x64) by tools/sentry-fixtures/electron/native-crash.sh under
// env -i, from process.crash() in the main process. The module id below is the
// ELF build id of that electron binary in Breakpad form; the public symbol
// server serves its .sym at /electron/5E0613C660B3B9829F56F37E73E29B290/electron.sym.
const (
	electronDebugID  = "5e0613c6-60b3-b982-9f56-f37e73e29b29"
	electronTopFrame = "electron::ElectronBindings::Crash()"
)

func electronDump(t testing.TB) []byte {
	t.Helper()
	for _, f := range loadFixtures(t) {
		if f.Name != "electron/native-crash" {
			continue
		}
		for _, it := range f.Items {
			if it.Type == "attachment" && it.Header["attachment_type"] == "event.minidump" {
				return it.Payload
			}
		}
	}
	t.Fatal("electron/native-crash has no minidump attachment")
	return nil
}

type noSyms struct{}

func (noSyms) CFI(*minidump.Module) (*breakpad.CFITable, bool) { return nil, false }
func (noSyms) Covers(*minidump.Module, uint64) bool            { return true }

func TestElectronRealDumpParses(t *testing.T) {
	d, err := minidump.Parse(electronDump(t))
	if err != nil {
		t.Fatal(err)
	}
	if d.CPU != "amd64" || d.OS != "linux" {
		t.Fatalf("os/cpu = %s/%s", d.OS, d.CPU)
	}
	var electron *minidump.Module
	for i, m := range d.Modules {
		if strings.HasSuffix(m.CodeFile, "/electron") {
			electron = &d.Modules[i]
		}
	}
	if electron == nil || electron.DebugID != electronDebugID {
		t.Fatalf("electron module = %+v", electron)
	}
	if d.Crash == nil || d.Crash.Reason == "" {
		t.Fatalf("crash = %+v", d.Crash)
	}
	frames := stackwalk.Walk(d, d.CrashThread, noSyms{}, 64)
	if len(frames) < 3 {
		t.Fatalf("walked %d frames", len(frames))
	}
	if frames[0].Module == nil || frames[0].Module.DebugID != electronDebugID || frames[0].Rel != 0x2c1a0d4 {
		t.Fatalf("frame 0 = %+v", frames[0])
	}
}

func electronSymbolicator(t *testing.T) (*Symbolicator, *collector) {
	t.Helper()
	sym, err := os.ReadFile("testdata/electron/electron-excerpt.sym")
	if err != nil {
		t.Fatal(err)
	}
	var cache bytes.Buffer
	if _, err := breakpad.Build(bytes.NewReader(sym), &cache, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	blobs, df := newMemBlobs(), nativeDebugFiles{keys: map[string]string{}}
	blobs.files["difcache/1/electron/breakpad-v1"] = cache.Bytes()
	df.keys["breakpad|"+electronDebugID] = "difcache/1/electron/breakpad-v1"
	return newSymb(t, newFakeArtifacts(), blobs, func(c *SymbolicatorConfig) {
		c.Resolvers = []Resolver{JSResolver(), MinidumpResolver(), NativeResolver()}
		c.DebugFiles = df
		c.Disk = debugfile.NewDiskCache(t.TempDir(), 64<<20)
	})
}

// The top frame of the real dump symbolicates through the recorded excerpt of
// Electron's public .sym to the function the generator recorded for that
// address (sym-excerpt.py prints it).
func TestElectronRealDumpSymbolicatesTopFrame(t *testing.T) {
	s, col := electronSymbolicator(t)
	ev := appingest.Event{ID: "e1", Platform: "linux", Kind: "crash", Level: "fatal"}
	body := appingest.Body{SiteID: 7, Platform: "native", Level: "fatal", Minidump: electronDump(t)}
	if !s.Submit(ev, body, nil) {
		t.Fatal("not queued")
	}
	gev, gb := col.wait(t)
	if gev.Symbolication != "ok" && gev.Symbolication != "partial" {
		t.Fatalf("symbolication = %q", gev.Symbolication)
	}
	frames := gb.Exceptions[0].Frames
	if len(frames) < 3 {
		t.Fatalf("frames = %d", len(frames))
	}
	top := frames[len(frames)-1] // newest call last
	if top.Function != electronTopFrame {
		t.Fatalf("top frame = %q, want %q", top.Function, electronTopFrame)
	}
	if gb.Minidump != nil {
		t.Fatal("the body still carries the dump")
	}
}

// The three recorded Electron fixtures for one DSN land in one site as three
// issues, tagged main, renderer, main (2d-D6).
func TestElectronProcessesShareOneSite(t *testing.T) {
	e := newEnv(t)
	s, _ := electronSymbolicator(t)
	s.cfg.Deliver = nil
	e.h.WithSymbolicator(s)

	var fps []string
	var tags []string
	for _, name := range []string{"main-exception", "renderer-exception", "native-crash"} {
		raw, err := os.ReadFile("testdata/electron/" + name + ".envelope")
		if err != nil {
			t.Fatal(err)
		}
		side, _ := os.ReadFile("testdata/electron/" + name + ".json")
		var sc sidecar
		if err := json.Unmarshal(side, &sc); err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(sc.Method, sc.Path+"?"+sc.Query, bytes.NewReader(raw))
		for k, v := range sc.Headers {
			req.Header.Set(k, v)
		}
		if w := e.do(req); w.Code != 200 {
			t.Fatalf("%s = %d", name, w.Code)
		}
		q := recvQueued(t, e.intake)
		if q.SiteID != fdSite {
			t.Fatalf("%s: site %d, want %d", name, q.SiteID, fdSite)
		}
		fps = append(fps, q.Event.Fingerprint)
		tags = append(tags, q.Event.Tags["process"])
	}
	if strings.Join(tags, ",") != "main,renderer,main" {
		t.Fatalf("process tags = %v", tags)
	}
	if fps[0] == fps[1] || fps[0] == fps[2] || fps[1] == fps[2] {
		t.Fatalf("expected three distinct issues, fingerprints = %v", fps)
	}
}
