package sentryingest

import (
	"context"
	"testing"

	"supermetry/internal/appingest"
)

// failNative attempts every frame and resolves none, as a native resolver does
// when no uploaded file covers the images.
type failNative struct{ partial bool }

func (failNative) Wants(appingest.Body) bool { return true }

func (f failNative) Resolve(_ context.Context, _ appingest.Event, b *appingest.Body, _ *Symbolicator) (int, int) {
	if f.partial {
		return 2, 1
	}
	return 1, 0
}

type onlyCache map[string]string

func (c onlyCache) CacheFor(_ context.Context, _ int32, kind, id string) (string, bool, error) {
	k, ok := c[kind+"|"+id]
	return k, ok, nil
}

func TestSymbolicationMissingNamesTheImagesTheFramesNeed(t *testing.T) {
	const have = "de7fada3-56f7-3ce7-b81e-0b6e964459c1"
	const lack = "7a3c9e10-55b2-4b8e-8a53-2f0c8d4e9a01"
	const sys = "11111111-2222-3333-4444-555555555555"
	const unused = "99999999-2222-3333-4444-555555555555"
	build := func() (appingest.Event, appingest.Body) {
		ev, body := fixtureEvent(t, "cocoa/crash")
		body.DebugImages = []appingest.DebugImage{
			{Type: "macho", DebugID: have, CodeFile: "/private/var/containers/Bundle/Application/X/App.app/App", ImageAddr: 0x1000, ImageSize: 0x1000, Arch: "arm64"},
			{Type: "macho", DebugID: lack, CodeFile: "/private/var/containers/Bundle/Application/X/App.app/Frameworks/Fix.framework/Fix", ImageAddr: 0x10000, ImageSize: 0x1000, Arch: "arm64"},
			{Type: "macho", DebugID: sys, CodeFile: "/System/Library/Frameworks/UIKit.framework/UIKit", ImageAddr: 0x20000, ImageSize: 0x1000},
			{Type: "macho", DebugID: unused, CodeFile: "/private/var/containers/Bundle/Application/X/App.app/Frameworks/Idle.framework/Idle", ImageAddr: 0x30000, ImageSize: 0x1000},
			{Type: "proguard", UUID: "6A1B8C0E-3D5F-3E2A-9B47-0C1D2E3F4A5B"},
		}
		body.Exceptions[0].Frames = []appingest.Frame{
			{InstructionAddr: "0x1100"}, {InstructionAddr: "0x10200"}, {InstructionAddr: "0x20010"},
		}
		return ev, body
	}
	run := func(r Resolver, store DebugFileStore) (appingest.Event, appingest.Body) {
		s, col := newSymb(t, newFakeArtifacts(), newMemBlobs(), func(c *SymbolicatorConfig) {
			c.Resolvers = []Resolver{r}
			c.DebugFiles = store
		})
		ev, body := build()
		if !s.Submit(ev, body, nil) {
			t.Fatal("not queued")
		}
		return col.wait(t)
	}

	// Nothing resolved: only images that hold a frame, no system image, no
	// image the stack never touches, and the ProGuard mapping of the event.
	ev, body := run(failNative{}, onlyCache{"macho|" + have: "k"})
	if ev.Symbolication != "missing" || body.Symbolication != "missing" {
		t.Fatalf("status = %q", ev.Symbolication)
	}
	want := []appingest.MissingDebug{
		{Kind: "macho", DebugID: lack, CodeFile: "Fix", Arch: "arm64"},
		{Kind: "proguard", DebugID: "6a1b8c0e-3d5f-3e2a-9b47-0c1d2e3f4a5b"},
	}
	if len(body.MissingDebug) != len(want) || body.MissingDebug[0] != want[0] || body.MissingDebug[1] != want[1] {
		t.Fatalf("missing = %+v, want %+v", body.MissingDebug, want)
	}

	// Review focus 4: a mapping uploaded for another UUID does not cover this one.
	_, body = run(failNative{}, onlyCache{"proguard|0e8c1b6a-3d5f-3e2a-9b47-0c1d2e3f4a5b": "k", "macho|" + have: "k", "macho|" + lack: "k"})
	if len(body.MissingDebug) != 1 || body.MissingDebug[0].Kind != "proguard" || body.MissingDebug[0].DebugID != "6a1b8c0e-3d5f-3e2a-9b47-0c1d2e3f4a5b" {
		t.Fatalf("missing = %+v", body.MissingDebug)
	}

	// Partly resolved events carry the list too; fully resolved ones do not.
	ev, body = run(failNative{partial: true}, onlyCache{"macho|" + have: "k"})
	if ev.Symbolication != "partial" || len(body.MissingDebug) != 2 {
		t.Fatalf("partial = %q %+v", ev.Symbolication, body.MissingDebug)
	}
	ev, body = run(fakeNative{}, onlyCache{})
	if ev.Symbolication != "ok" || len(body.MissingDebug) != 0 {
		t.Fatalf("ok = %q %+v", ev.Symbolication, body.MissingDebug)
	}
}
