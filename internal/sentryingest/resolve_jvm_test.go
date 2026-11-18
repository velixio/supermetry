package sentryingest

import (
	"bytes"
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"supermetry/internal/appingest"
	"supermetry/internal/debugfile"
	"supermetry/internal/proguard"
)

const jvmMappingUUID = "6a1b8c0e-3d5f-3e2a-9b47-0c1d2e3f4a5b"

type fakeDebugFiles struct{ keys map[string]string } // "kind|id" -> cache key

func (f fakeDebugFiles) CacheFor(_ context.Context, _ int32, kind, id string) (string, bool, error) {
	k, ok := f.keys[kind+"|"+id]
	return k, ok, nil
}

type missRecorder struct {
	mu   sync.Mutex
	seen []appingest.DebugImage
	kind []string
}

func (m *missRecorder) Missing(_ int32, kind string, im appingest.DebugImage, _ time.Time) {
	m.mu.Lock()
	m.seen, m.kind = append(m.seen, im), append(m.kind, kind)
	m.mu.Unlock()
}

// jvmSymb wires a symbolicator whose mapping index for storedUUID is built
// from the fixture mapping.
func jvmSymb(t *testing.T, storedUUID string, misses MissSink) (*Symbolicator, *collector) {
	t.Helper()
	mapping, err := os.ReadFile("testdata/android/mapping.txt")
	if err != nil {
		t.Fatal(err)
	}
	var idx bytes.Buffer
	if err := proguard.BuildIndex(bytes.NewReader(mapping), &idx); err != nil {
		t.Fatal(err)
	}
	blobs := newMemBlobs()
	blobs.files["difcache/proguard-index"] = idx.Bytes()
	return newSymb(t, newFakeArtifacts(), blobs, func(c *SymbolicatorConfig) {
		c.Resolvers = []Resolver{JSResolver(), JVMResolver()}
		c.DebugFiles = fakeDebugFiles{keys: map[string]string{"proguard|" + storedUUID: "difcache/proguard-index"}}
		c.Disk = debugfile.NewDiskCache(t.TempDir(), 1<<20)
		c.Misses = misses
	})
}

func TestJVMResolverDeobfuscates(t *testing.T) {
	ev, body := fixtureEvent(t, "android/jvm-crash")
	rawFP := ev.Fingerprint
	s, col := jvmSymb(t, jvmMappingUUID, nil)
	if !s.Submit(ev, body, body.Row.SDKFingerprint) {
		t.Fatal("the JVM resolver wants this event")
	}
	got, gb := col.wait(t)
	if got.Symbolication != "ok" || gb.Symbolication != "ok" {
		t.Fatalf("symbolication %q", got.Symbolication)
	}
	frames := gb.Exceptions[0].Frames
	want := []struct {
		module, fn, file string
		line             int
	}{
		{"com.example.fixture.CheckoutActivity", "validate", "CheckoutActivity.kt", 42},
		{"com.example.fixture.CheckoutActivity", "checkAmount", "CheckoutActivity.kt", 90},
		{"com.example.fixture.Router", "run", "Router.java", 0},
		{"android.os.Looper", "loopOnce", "Looper.java", 232},
	}
	if len(frames) != len(want) {
		t.Fatalf("%d frames: %+v", len(frames), frames)
	}
	for i, w := range want {
		f := frames[i]
		if f.Module != w.module || f.Function != w.fn || f.Filename != w.file || f.Lineno != w.line {
			t.Errorf("frame %d = %+v, want %+v", i, f, w)
		}
	}
	if !frames[1].InApp || frames[3].InApp {
		t.Errorf("in_app not carried over: %+v", frames)
	}
	if got.Culprit != "com.example.fixture.Router.run" {
		t.Errorf("culprit %q", got.Culprit)
	}
	if got.Fingerprint == rawFP {
		t.Error("fingerprint unchanged by symbolication")
	}
	// The fingerprint equals the one computed from readable frames.
	readable := body
	readable.Exceptions = append([]appingest.Exception(nil), body.Exceptions...)
	readable.Exceptions[0].Frames = frames
	rev := ev
	rev.Frames = eventFrames(readable.Exceptions)
	rev.Culprit = culprit(readable.Exceptions, rev.Transaction)
	wantFP, _ := appingest.GroupingKeyRules(rev, readable, body.Row.SDKFingerprint, readable.Row.Rules)
	if got.Fingerprint != wantFP {
		t.Errorf("fingerprint %s, want %s", got.Fingerprint, wantFP)
	}
	if body.Exceptions[0].Frames[0].Module != "a.b.c.e" {
		t.Error("the caller's body was modified in place")
	}
}

func TestJVMResolverMappingForAnotherUUID(t *testing.T) {
	ev, body := fixtureEvent(t, "android/jvm-crash")
	misses := &missRecorder{}
	// The mapping is stored under UUID X; the app reports UUID Y.
	s, col := jvmSymb(t, "11111111-2222-3333-4444-555555555555", misses)
	if !s.Submit(ev, body, body.Row.SDKFingerprint) {
		t.Fatal("not queued")
	}
	got, gb := col.wait(t)
	if got.Symbolication != "missing" {
		t.Fatalf("symbolication %q, want missing", got.Symbolication)
	}
	if gb.Exceptions[0].Frames[0].Module != "a.b.c.e" || got.Fingerprint != ev.Fingerprint {
		t.Fatalf("frames changed without a mapping: %+v", gb.Exceptions[0].Frames[0])
	}
	if len(misses.seen) != 1 || misses.kind[0] != "proguard" || misses.seen[0].UUID != jvmMappingUUID {
		t.Fatalf("misses %+v %v", misses.seen, misses.kind)
	}
}

func TestJVMResolverWants(t *testing.T) {
	_, jvm := fixtureEvent(t, "android/jvm-crash")
	if !(jvmResolver{}).Wants(jvm) {
		t.Fatal("jvm crash with a proguard image must be wanted")
	}
	jvm.DebugImages = nil
	if (jvmResolver{}).Wants(jvm) {
		t.Fatal("no proguard image, nothing to do")
	}
	_, ndk := fixtureEvent(t, "android/ndk-crash")
	if (jvmResolver{}).Wants(ndk) {
		t.Fatal("native event is not a JVM one")
	}
}

func TestJVMResolverRemapsExceptionClass(t *testing.T) {
	ev, body := fixtureEvent(t, "android/jvm-crash")
	body.Exceptions = append([]appingest.Exception(nil), body.Exceptions...)
	body.Exceptions[0].Module, body.Exceptions[0].Type = "a.b.c", "f"
	s, col := jvmSymb(t, jvmMappingUUID, nil)
	s.Submit(ev, body, nil)
	_, gb := col.wait(t)
	if ex := gb.Exceptions[0]; ex.Module != "com.example.fixture" || ex.Type != "PaymentException" {
		t.Fatalf("exception %q.%q", ex.Module, ex.Type)
	}
}
