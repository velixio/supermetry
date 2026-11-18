package sentryingest

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha1" //nolint:gosec // the upload protocol hashes chunks with sha1
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/appdebug"
	"supermetry/internal/appingest"
	"supermetry/internal/db"
	"supermetry/internal/debugfile"
	"supermetry/internal/platform/authz"
	"supermetry/internal/platform/keys"
	"supermetry/internal/platform/store"
	"supermetry/internal/replay"
	"supermetry/internal/sentryapi"
	"supermetry/internal/sites"
	"supermetry/internal/symcache"
	"supermetry/internal/testpg"
)

// e2eRegistry serves one app site by public id, id and DSN key.
type e2eRegistry struct{ site sites.Site }

func (r e2eRegistry) ByPublicID(id string) (sites.Site, bool) { return r.site, id == r.site.PublicID }
func (r e2eRegistry) Site(id int32) (sites.Site, bool)        { return r.site, id == r.site.ID }
func (r e2eRegistry) All() []sites.Site                       { return []sites.Site{r.site} }
func (r e2eRegistry) ByDSNKey(k string) (sites.Site, sites.DSNKey, bool) {
	for _, dk := range r.site.App.DSNKeys {
		if dk.PublicKey == k {
			return r.site, dk, true
		}
	}
	return sites.Site{}, sites.DSNKey{}, false
}

// nativeRig runs the whole path in process: the upload API with its cache
// worker, the envelope handler with the symbolicator, the writers and
// Postgres. Only the lookup cache is switched off, so an upload is visible to
// the very next event (production keeps a 30 s lookup TTL).
type nativeRig struct {
	t      *testing.T
	pool   *pgxpool.Pool
	api    *httptest.Server
	token  string
	h      *Handler
	misses *appdebug.MissRecorder
}

func sha1hex(b []byte) string {
	s := sha1.Sum(b) //nolint:gosec // protocol hash
	return hex.EncodeToString(s[:])
}

func (r *nativeRig) apiDo(method, p string, body io.Reader, ct string) (int, []byte) {
	r.t.Helper()
	req, _ := http.NewRequest(method, r.api.URL+p, body)
	req.Header.Set("Authorization", "Bearer "+r.token)
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		r.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data
}

// uploadDIF runs the sentry-cli sequence: assemble (missing chunks), chunk
// upload, assemble again, poll until the file is assembled; it returns the
// final state and the dif object.
func (r *nativeRig) uploadDIF(name string, data []byte) (string, map[string]any) {
	r.t.Helper()
	sum := sha1hex(data)
	req, _ := json.Marshal(map[string]map[string]any{sum: {"name": name, "chunks": []string{sum}}})
	assemble := func() map[string]any {
		status, raw := r.apiDo("POST", "/api/0/projects/acme/appsite00042/files/difs/assemble/", bytes.NewReader(req), "application/json")
		if status != 200 {
			r.t.Fatalf("assemble %d: %s", status, raw)
		}
		var out map[string]map[string]any
		if err := json.Unmarshal(raw, &out); err != nil {
			r.t.Fatal(err)
		}
		return out[sum]
	}
	if got := assemble(); got["state"] != "not_found" {
		r.t.Fatalf("first assemble = %v", got)
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, _ := mw.CreateFormFile("file", sum)
	_, _ = part.Write(data)
	_ = mw.Close()
	if status, raw := r.apiDo("POST", "/api/0/organizations/acme/chunk-upload/", &buf, mw.FormDataContentType()); status != 200 {
		r.t.Fatalf("chunk upload %d: %s", status, raw)
	}
	for i := 0; i < 400; i++ {
		got := assemble()
		switch got["state"] {
		case "ok", "error":
			dif, _ := got["dif"].(map[string]any)
			return got["state"].(string), dif
		case "not_found":
			r.t.Fatalf("state not_found after the chunk arrived: %v", got)
		}
		time.Sleep(25 * time.Millisecond)
	}
	r.t.Fatal("debug file did not finish")
	return "", nil
}

func envelopeFor(eventID, sdkName string, event []byte) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, `{"event_id":%q,"sent_at":"2026-11-17T10:00:05.000Z","sdk":{"name":%q,"version":"1.0.0"}}`+"\n", eventID, sdkName)
	fmt.Fprintf(&b, `{"content_type":"application/json","type":"event","length":%d}`+"\n", len(event))
	b.Write(event)
	b.WriteByte('\n')
	return b.Bytes()
}

func (r *nativeRig) post(env []byte) {
	r.t.Helper()
	req := httptest.NewRequest("POST", fdPath+fdQuery, bytes.NewReader(env))
	req.Header.Set("Content-Type", "application/x-sentry-envelope")
	w := httptest.NewRecorder()
	r.h.ServeHTTP(w, req)
	if w.Code != 200 {
		r.t.Fatalf("envelope status %d: %s", w.Code, w.Body)
	}
}

type storedEvent struct {
	Symbolication string
	IssueID       int64
	Fingerprint   string
	Frames        []map[string]any
	Missing       []map[string]any
}

// stored waits for the event row and its body, then returns what was stored.
func (r *nativeRig) stored(eventID string) storedEvent {
	r.t.Helper()
	for i := 0; i < 400; i++ {
		var se storedEvent
		var body []byte
		var issue *int64
		err := r.pool.QueryRow(context.Background(), `
select coalesce(e.symbolication,''), e.issue_id, e.fingerprint, b.body::text::bytea
from app_events e join app_event_bodies b on b.site_id = e.site_id and b.event_id = e.event_id
where e.site_id = $1 and e.event_id = $2`, fdSite, eventID).Scan(&se.Symbolication, &issue, &se.Fingerprint, &body)
		if err == nil {
			var doc struct {
				Exceptions []struct {
					Frames []map[string]any `json:"frames"`
				} `json:"exceptions"`
				MissingDebug []map[string]any `json:"missing_debug"`
			}
			if err := json.Unmarshal(body, &doc); err != nil {
				r.t.Fatal(err)
			}
			if len(doc.Exceptions) > 0 {
				se.Frames = doc.Exceptions[0].Frames
			}
			se.Missing = doc.MissingDebug
			if issue != nil {
				se.IssueID = *issue
			}
			return se
		}
		time.Sleep(25 * time.Millisecond)
	}
	r.t.Fatalf("event %s was not stored", eventID)
	return storedEvent{}
}

func newNativeRig(t *testing.T) *nativeRig {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var uid, oid int64
	if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash) values ('owner@example.test','U','h') returning id`).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('acme','acme') returning id`).Scan(&oid); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into memberships(org_id,user_id,role) values ($1,$2,'owner')`, oid, uid); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into sites(id,org_id,public_id,name,timezone,retention_class,kind) overriding system value
values($1,$2,'appsite00042','App','UTC',30,'app')`, fdSite, oid); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsurePartitions(ctx, pool, fdNow.AddDate(0, 0, -10), fdNow.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	site := appSite(fdSite, sites.DSNKey{PublicKey: fdKey})
	site.OrgID, site.PublicID, site.App.MaxReportAgeDays = oid, "appsite00042", 30
	reg := e2eRegistry{site}

	blobs, err := replay.NewDiskStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	k := keys.New(&store.Store{Pool: pool})
	t.Cleanup(k.Close)
	tmp := t.TempDir()
	nativeCache := sentryapi.CacheBuilder{Version: symcache.Version, Build: symcache.BuildFunc(filepath.Join(tmp, "symcache-tmp"))}
	api := sentryapi.New(sentryapi.Deps{Pool: pool, Keys: k, Sites: reg, Blobs: blobs, TempDir: tmp, IPPerMin: -1, TokenPerMin: -1,
		Builders: map[string]sentryapi.CacheBuilder{"proguard": {Version: 1, Build: BuildProguardCache}, "elf": nativeCache, "macho": nativeCache}})
	api.Start(ctx)
	t.Cleanup(api.Close)
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	token, _, err := k.Create(ctx, authz.Principal{UserID: uid, OrgID: oid, Role: authz.RoleOwner}, "t", []string{sentryapi.Scope}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	intake := appingest.NewIntake(appingest.Config{})
	appWriter := appingest.NewWriter(pool, intake, appingest.WriterConfig{FlushEvery: 10 * time.Millisecond})
	bodies := appingest.NewBodyWriter(pool, appingest.BodyWriterConfig{FlushEvery: 10 * time.Millisecond})
	wctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{}, 2)
	go func() { _ = appWriter.Run(wctx); done <- struct{}{} }()
	go func() { _ = bodies.Run(wctx); done <- struct{}{} }()
	t.Cleanup(func() { cancel(); <-done; <-done })

	misses := appdebug.NewMissRecorder(pool, time.Hour, appdebug.DefaultMaxKeys)
	h := NewHandler(reg, intake, bodies, fixedGeo("DE"), nil, func() time.Time { return fdNow })
	h.WithIPLimit(-1)
	symb := NewSymbolicator(NewPGArtifactStore(pool), blobs, SymbolicatorConfig{
		Workers: 2, QueueSize: 16, CacheBytes: 64 << 20, Stats: h.Stats(), LookupTTL: -1,
		Disk:      debugfile.NewDiskCache(filepath.Join(tmp, "symcache"), 64<<20),
		Resolvers: []Resolver{JSResolver(), JVMResolver(), NativeResolver()}, DebugFiles: NewPGArtifactStore(pool), Misses: misses,
	})
	t.Cleanup(symb.Close)
	h.WithSymbolicator(symb)
	return &nativeRig{t: t, pool: pool, api: srv, token: token, h: h, misses: misses}
}

// nativeCase is one fixture: the debug file to upload and an envelope builder
// that takes the event id.
type nativeCase struct {
	name       string
	uploadName string
	upload     []byte
	kind       string // app_missing_debug_files.kind
	debugID    string
	event      func(id string) []byte
	// wantFrame is the hand-checked crash frame (the last one, exact address).
	wantFn, wantFile string
	wantLine         int
	frames           int
	skipBefore       bool // already uploaded by an earlier case
}

func gunzipFixture(t *testing.T, p string) []byte {
	t.Helper()
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func nativeEventJSON(errType, platform, sdk, osName, arch string, frames []map[string]any, images []map[string]any) func(string) []byte {
	return func(id string) []byte {
		ev := map[string]any{
			"event_id": id, "timestamp": "2026-11-17T09:59:58.000Z", "platform": platform, "level": "fatal",
			"release": "com.example.fixture@1.0.0+1", "environment": "fixture", "user": map[string]any{"id": "fixture-user-1"},
			"sdk":      map[string]any{"name": sdk, "version": "1.0.0"},
			"contexts": map[string]any{"os": map[string]any{"name": osName, "version": "1"}, "device": map[string]any{"arch": arch}},
			"exception": map[string]any{"values": []any{map[string]any{
				"type": errType, "value": "Segmentation fault", "thread_id": 1,
				"mechanism":  map[string]any{"type": "signalhandler", "handled": false},
				"stacktrace": map[string]any{"frames": frames},
			}}},
			"threads":    []any{map[string]any{"id": 1, "name": "main", "crashed": true, "current": true}},
			"debug_meta": map[string]any{"images": images},
		}
		b, _ := json.Marshal(ev)
		return envelopeFor(id, sdk, b)
	}
}

func hexAddr(a uint64) string { return fmt.Sprintf("0x%x", a) }

// objectFor finds the object of a committed fixture (an arch picks a slice of a fat file).
func objectFor(t *testing.T, file, arch string) (debugfile.Object, []byte) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(nativeTestdata, file))
	if err != nil {
		t.Fatal(err)
	}
	objs, err := debugfile.Detect(bytes.NewReader(data), int64(len(data)), file)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range objs {
		if arch == "" || o.Arch == arch {
			return o, data
		}
	}
	t.Fatalf("no %s object in %s", arch, file)
	return debugfile.Object{}, nil
}

func TestNativeAndMobileUploadSymbolicatesEndToEnd(t *testing.T) {
	exp := loadNativeExpected(t)
	var cases []nativeCase

	// Android obfuscated Java: the recorded SDK-shaped envelope and its mapping.
	mapping, err := os.ReadFile("testdata/android/mapping.txt")
	if err != nil {
		t.Fatal(err)
	}
	jvmEnv := gunzipFixture(t, "testdata/android/jvm-crash.envelope")
	cases = append(cases, nativeCase{
		name: "android-jvm", uploadName: "/proguard/" + jvmMappingUUID + ".txt", upload: mapping, kind: "proguard", debugID: jvmMappingUUID,
		event: func(id string) []byte {
			return bytes.ReplaceAll(jvmEnv, []byte("000000000000000000000000000000a1"), []byte(id))
		},
		wantFn: "validate", wantFile: "CheckoutActivity.kt", wantLine: 42, frames: 4,
	})

	// Android NDK: an ELF shared library; 0x1111 is one past fix_outer's call
	// and the first byte of fix_plain.
	elf, elfData := objectFor(t, "libfix.so", "")
	const soBase = 0x7f0000000000
	soFile := "/data/app/~~x/com.example.fixture/lib/arm64/libfix.so"
	cases = append(cases, nativeCase{
		name: "android-ndk", uploadName: "libfix.so", upload: elfData, kind: elf.Kind, debugID: elf.DebugID,
		event: nativeEventJSON("AndroidNdkCrash", "native", "sentry.native", "Android", "arm64",
			[]map[string]any{{"instruction_addr": hexAddr(soBase + 0x1111), "package": soFile}, {"instruction_addr": hexAddr(soBase + 0x1111), "package": soFile}},
			[]map[string]any{{"type": "elf", "debug_id": elf.DebugID, "code_file": soFile, "image_addr": hexAddr(soBase), "image_size": 0x4000, "arch": "arm64"}}),
		wantFn: "fix_plain", wantFile: "fixture.c", wantLine: 16, frames: 2,
	})

	// iOS and macOS: one fat Mach-O holds both slices; each event must pick its own.
	machoCase := func(name, arch, osName, codeFile, key string, upload bool) nativeCase {
		o, data := objectFor(t, "fix-darwin-fat", arch)
		eo := exp[key]
		const slide = 0x4000
		var frames []map[string]any
		for _, c := range eo.Cases[:3] {
			frames = append(frames, map[string]any{"instruction_addr": hexAddr(c.Addr + slide), "package": codeFile})
		}
		last := eo.Cases[2].Frames[0]
		nc := nativeCase{
			name: name, kind: o.Kind, debugID: o.DebugID,
			event: nativeEventJSON("Crash"+osName, "cocoa", "sentry.cocoa", osName, arch, frames,
				[]map[string]any{{"type": "macho", "debug_id": o.DebugID, "code_file": codeFile, "image_addr": hexAddr(eo.Base + slide), "image_size": 0x100000, "arch": arch}}),
			wantFn: last.Function, wantFile: path.Base(last.File), wantLine: int(last.Line), frames: 3,
		}
		if upload {
			nc.uploadName, nc.upload = "Fix.dSYM/Contents/Resources/DWARF/Fix", data
		} else {
			nc.skipBefore = true
		}
		return nc
	}
	cases = append(cases,
		machoCase("ios", "arm64", "iOS", "/private/var/containers/Bundle/Application/X/Fix.app/Fix", "fix-darwin-arm64", true),
		// The fat file is already uploaded by the iOS case: the macOS event
		// resolves from the x86_64 slice without another upload.
		machoCase("macos", "x86_64", "macOS", "/Applications/Fix.app/Contents/MacOS/Fix", "fix-darwin-amd64", false))

	// Linux: a plain ELF executable with DWARF.
	lin, linData := objectFor(t, "fix-linux-amd64", "")
	leo := exp["fix-linux-amd64"]
	var lframes []map[string]any
	for _, c := range leo.Cases[:3] {
		lframes = append(lframes, map[string]any{"instruction_addr": hexAddr(c.Addr), "package": "/opt/fix/fix"})
	}
	llast := leo.Cases[2].Frames[0]
	cases = append(cases, nativeCase{
		name: "linux-native", uploadName: "fix-linux-amd64", upload: linData, kind: lin.Kind, debugID: lin.DebugID,
		event: nativeEventJSON("LinuxCrash", "native", "sentry.native", "Linux", "x86_64", lframes,
			[]map[string]any{{"type": "elf", "debug_id": lin.DebugID, "code_file": "/opt/fix/fix", "image_addr": hexAddr(leo.Base), "image_size": 0x200000, "arch": "x86_64"}}),
		wantFn: llast.Function, wantFile: path.Base(llast.File), wantLine: int(llast.Line), frames: 3,
	})

	r := newNativeRig(t)
	ctx := context.Background()
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r.t = t
			id := func(n int) string { return fmt.Sprintf("%030x%02x", i+1, n) }

			// Before the upload the event is stored unresolved, names the missing file,
			// and the recorder counts the miss for the banner and settings page.
			var before storedEvent
			var n int
			if !c.skipBefore {
				r.post(c.event(id(1)))
				before = r.stored(id(1))
				if before.Symbolication != "missing" && before.Symbolication != "partial" {
					t.Fatalf("before upload symbolication = %q", before.Symbolication)
				}
				if len(before.Missing) == 0 {
					t.Fatalf("no missing_debug on the event: %+v", before)
				}
				if err := r.misses.Flush(ctx); err != nil {
					t.Fatal(err)
				}
				if err := r.pool.QueryRow(ctx, `select count(*) from app_missing_debug_files where site_id=$1 and debug_id=$2`, fdSite, c.debugID).Scan(&n); err != nil || n != 1 {
					t.Fatalf("missing row for %s: %d %v", c.debugID, n, err)
				}
			}

			// Upload, unless an earlier case already uploaded this file.
			if c.upload != nil {
				state, dif := r.uploadDIF(c.uploadName, c.upload)
				if state != "ok" {
					t.Fatalf("upload state %q: %v", state, dif)
				}
			}
			if err := r.pool.QueryRow(ctx, `select count(*) from app_debug_files where site_id=$1 and debug_id=$2 and cache_state='ok'`, fdSite, c.debugID).Scan(&n); err != nil || n != 1 {
				t.Fatalf("debug file %s cache ok rows = %d %v", c.debugID, n, err)
			}
			if err := r.pool.QueryRow(ctx, `select count(*) from app_missing_debug_files where site_id=$1 and debug_id=$2`, fdSite, c.debugID).Scan(&n); err != nil || n != 0 {
				t.Fatalf("missing row survived the upload: %d %v", n, err)
			}

			// Twice, to prove they group into one issue from the readable frames.
			r.post(c.event(id(2)))
			r.post(c.event(id(3)))
			a, b := r.stored(id(2)), r.stored(id(3))
			for _, se := range []storedEvent{a, b} {
				if se.Symbolication != "ok" {
					t.Fatalf("symbolication = %q, want ok", se.Symbolication)
				}
				if len(se.Frames) != c.frames {
					t.Fatalf("%d frames: %+v", len(se.Frames), se.Frames)
				}
			}
			// The hand-checked crash frame: the last one for native events, the first
			// for the JVM fixture (frames are listed top first there).
			top := a.Frames[len(a.Frames)-1]
			if c.kind == "proguard" {
				top = a.Frames[0]
			}
			if top["function"] != c.wantFn || path.Base(fmt.Sprint(top["filename"])) != c.wantFile || int(top["lineno"].(float64)) != c.wantLine {
				t.Fatalf("top frame %v, want %s %s:%d", top, c.wantFn, c.wantFile, c.wantLine)
			}
			if a.IssueID == 0 || a.IssueID != b.IssueID || a.Fingerprint != b.Fingerprint {
				t.Fatalf("issues %d/%d, fingerprints %s/%s", a.IssueID, b.IssueID, a.Fingerprint, b.Fingerprint)
			}
			var cnt int64
			if err := r.pool.QueryRow(ctx, `select event_count from app_issues where id=$1`, a.IssueID).Scan(&cnt); err != nil || cnt != 2 {
				t.Fatalf("issue event_count = %d %v", cnt, err)
			}
			if !c.skipBefore && a.Fingerprint == before.Fingerprint {
				t.Fatal("symbolication did not change the grouping")
			}

			// Later uploads never rewrite history.
			if !c.skipBefore {
				if again := r.stored(id(1)); again.Symbolication != before.Symbolication {
					t.Fatalf("stored event rewritten: %q -> %q", before.Symbolication, again.Symbolication)
				}
			}
		})
	}
}
