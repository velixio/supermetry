package sentryingest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"

	"supermetry/internal/appingest"
	"supermetry/internal/debugfile"
	"supermetry/internal/testpg"
)

func TestParseDebugImagesNativeFixtures(t *testing.T) {
	_, ndk := fixtureEvent(t, "android/ndk-crash")
	im := ndk.DebugImages[0]
	if im.Type != "elf" || im.DebugID != "7a3c9e10-55b2-4b8e-8a53-2f0c8d4e9a01" || im.CodeID != "109e3c7ab2558e4b8a532f0c8d4e9a01c0ffee00" ||
		im.Arch != "arm64" || im.ImageAddr != 524288000000 || im.ImageSize != 1048576 {
		t.Fatalf("ndk image %+v", im)
	}
	_, cocoa := fixtureEvent(t, "cocoa/crash")
	im = cocoa.DebugImages[0]
	if im.Type != "macho" || im.ImageAddr != 4328521728 || im.ImageSize != 1638400 || im.ImageVMAddr != 4294967296 {
		t.Fatalf("cocoa image %+v", im)
	}
	_, jvm := fixtureEvent(t, "android/jvm-crash")
	if jvm.DebugImages[0].Type != "proguard" || jvm.DebugImages[0].UUID != "6a1b8c0e-3d5f-3e2a-9b47-0c1d2e3f4a5b" {
		t.Fatalf("proguard image %+v", jvm.DebugImages[0])
	}
}

func TestParseAddrForms(t *testing.T) {
	cases := map[string]uint64{
		`"0x7fff00001000"`: 0x7fff00001000, `"0XFF"`: 255, `"4096"`: 4096, `4096`: 4096, `1048576`: 1048576,
		`"0xffffffffffffffff"`: 1<<64 - 1, `"0x10000000000000000"`: 0, `"-1"`: 0, `-5`: 0, `"zz"`: 0, `1.5`: 0, `null`: 0, `{}`: 0,
		`"` + strings.Repeat("9", 30) + `"`: 0,
	}
	for in, want := range cases {
		var v any
		if err := json.Unmarshal([]byte(in), &v); err != nil {
			t.Fatal(err)
		}
		if got := parseAddr(v); got != want {
			t.Errorf("parseAddr(%s) = %d, want %d", in, got, want)
		}
	}
}

func TestParseDebugImagesCaps(t *testing.T) {
	var imgs []any
	for i := 0; i < 600; i++ {
		imgs = append(imgs, map[string]any{"type": "macho", "debug_id": fmt.Sprintf("%032x", i)})
	}
	meta := map[string]any{"images": imgs}
	if n := len(parseDebugImages(meta, "cocoa")); n != 500 {
		t.Fatalf("cocoa kept %d", n)
	}
	if n := len(parseDebugImages(meta, "native")); n != 500 {
		t.Fatalf("native kept %d", n)
	}
	if n := len(parseDebugImages(meta, "javascript")); n != 50 {
		t.Fatalf("javascript kept %d", n)
	}
}

type fakeNative struct{ resolved int }

func (fakeNative) Wants(b appingest.Body) bool {
	for _, im := range b.DebugImages {
		if im.Type == "macho" || im.Type == "elf" {
			return true
		}
	}
	return false
}

func (f fakeNative) Resolve(_ context.Context, _ appingest.Event, b *appingest.Body, _ *Symbolicator) (int, int) {
	for i := range b.Exceptions {
		for k := range b.Exceptions[i].Frames {
			b.Exceptions[i].Frames[k].Function = "native_resolved"
		}
	}
	return 1, 1
}

func TestResolverChainNativeWants(t *testing.T) {
	_, body := fixtureEvent(t, "cocoa/crash")
	ev, _ := fixtureEvent(t, "cocoa/crash")
	if needsSymbolication(body) {
		t.Fatal("a native event is not a JavaScript one")
	}
	// Default chain: the JS resolver only, so the event is not taken.
	s, _ := newSymb(t, newFakeArtifacts(), newMemBlobs())
	if s.Submit(ev, body, nil) {
		t.Fatal("no resolver wants a native event, yet it was queued")
	}
	// With a native resolver registered next to the JS one, it is.
	s2, col := newSymb(t, newFakeArtifacts(), newMemBlobs(), func(c *SymbolicatorConfig) {
		c.Resolvers = []Resolver{JSResolver(), fakeNative{}}
	})
	if !s2.Submit(ev, body, nil) {
		t.Fatal("the native resolver wants the event")
	}
	gev, gb := col.wait(t)
	if gev.Symbolication != "ok" || gb.Exceptions[0].Frames[0].Function != "native_resolved" {
		t.Fatalf("symbolication %q, frame %+v", gev.Symbolication, gb.Exceptions[0].Frames[0])
	}
	if body.Exceptions[0].Frames[0].Function == "native_resolved" {
		t.Fatal("the caller's body was modified in place")
	}
}

func TestSymbolicatorCacheReadsThroughDisk(t *testing.T) {
	blobs := newMemBlobs()
	blobs.files["difcache/k"] = []byte("cache bytes")
	disk := debugfile.NewDiskCache(t.TempDir(), 1<<20)
	s, _ := newSymb(t, newFakeArtifacts(), blobs, func(c *SymbolicatorConfig) { c.Disk = disk })
	for i := 0; i < 2; i++ {
		f, size, err := s.Cache(context.Background(), "difcache/k")
		if err != nil || size != 11 {
			t.Fatalf("cache: %v size %d", err, size)
		}
		data, _ := io.ReadAll(f)
		_ = f.Close()
		if string(data) != "cache bytes" {
			t.Fatalf("data %q", data)
		}
	}
	if blobs.opens["difcache/k"] != 1 {
		t.Fatalf("blob opened %d times, want once", blobs.opens["difcache/k"])
	}
	s2, _ := newSymb(t, newFakeArtifacts(), blobs)
	if _, _, err := s2.Cache(context.Background(), "difcache/k"); err == nil {
		t.Fatal("Cache without a disk cache must fail")
	}
}

func TestPGDebugFileStoreScopesAndPrefersNewest(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	const id = "7a3c9e10-55b2-4b8e-8a53-2f0c8d4e9a01"
	if _, err := pool.Exec(ctx, `
insert into orgs(name,slug) values('A','a'),('B','b');
insert into sites(id,org_id,public_id,name,timezone,retention_class,kind) overriding system value values
 (1,1,'appdbgf0001a','A1','UTC',30,'app'),(2,1,'appdbgf0002a','A2','UTC',30,'app'),(3,2,'appdbgf0003b','B1','UTC',30,'app');
insert into app_dif_uploads(id,org_id,site_ids,checksum,state) overriding system value values
 (1,1,'{1}','`+rep("1")+`','ok'),(2,1,'{1}','`+rep("2")+`','ok'),(3,1,'{2}','`+rep("3")+`','ok'),(4,2,'{}','`+rep("4")+`','ok'),(5,1,'{}','`+rep("5")+`','ok');
insert into app_debug_files(upload_id,org_id,site_id,debug_id,kind,cache_state,cache_key) values
 (1,1,1,'`+id+`','elf','ok','k/old-site1'),
 (2,1,1,'`+id+`','elf','ok','k/new-site1'),
 (3,1,2,'`+id+`','elf','ok','k/site2'),
 (4,2,NULL,'`+id+`','elf','ok','k/orgb'),
 (5,1,NULL,'88888888-8888-8888-8888-888888888888','elf','ok','k/orgwide'),
 (2,1,1,'99999999-9999-9999-9999-999999999999','elf','pending',NULL)`); err != nil {
		t.Fatal(err)
	}
	st := NewPGArtifactStore(pool)
	get := func(site int32, kind, dbg string) string {
		t.Helper()
		k, ok, err := st.CacheFor(ctx, site, kind, dbg)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			return ""
		}
		return k
	}
	// A corrected re-upload (newer id) replaces what the project is served.
	if k := get(1, "elf", id); k != "k/new-site1" {
		t.Fatalf("site 1 = %q, want the newest upload", k)
	}
	// Project 2's upload of the same debug id is never served to project 1, and vice versa.
	if k := get(2, "elf", id); k != "k/site2" {
		t.Fatalf("site 2 = %q", k)
	}
	// Another organization's file is invisible.
	if k := get(3, "elf", id); k != "k/orgb" {
		t.Fatalf("site 3 = %q", k)
	}
	// An org-wide upload (no project) serves every project of its organization.
	for _, site := range []int32{1, 2} {
		if k := get(site, "elf", "88888888-8888-8888-8888-888888888888"); k != "k/orgwide" {
			t.Fatalf("site %d org-wide = %q", site, k)
		}
	}
	if k := get(3, "elf", "88888888-8888-8888-8888-888888888888"); k != "" {
		t.Fatalf("org-wide file leaked to another organization: %q", k)
	}
	// Pending caches, other kinds and malformed ids find nothing.
	if k := get(1, "elf", "99999999-9999-9999-9999-999999999999"); k != "" {
		t.Fatalf("pending cache served: %q", k)
	}
	if get(1, "macho", id) != "" || get(1, "elf", "nope") != "" {
		t.Fatal("wrong kind or malformed id matched")
	}
}
