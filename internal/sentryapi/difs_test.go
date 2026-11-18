package sentryapi

import (
	"bytes"
	"context"
	"io"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"supermetry/internal/debugfile"
)

const (
	proguardName = "/proguard/6a1b8c0e-3d5f-3e2a-9b47-0c1d2e3f4a5b.txt"
	proguardID   = "6a1b8c0e-3d5f-3e2a-9b47-0c1d2e3f4a5b"
)

var proguardFile = []byte("# compiler: R8\ncom.example.Main -> a.a:\n    void run() -> a\n")

func (e *env) difs(t *testing.T, token, project string, files map[string]map[string]any) (int, map[string]any) {
	t.Helper()
	return e.json(t, "POST", "/api/0/projects/acme/"+project+"/files/difs/assemble/", token, files)
}

func (e *env) difReq(data []byte, name string) (string, map[string]any) {
	return sha(data), map[string]any{"name": name, "chunks": []string{sha(data)}}
}

// waitDIF polls until the file leaves created/assembling.
func (e *env) waitDIF(t *testing.T, token, project, sum string, req map[string]any) map[string]any {
	t.Helper()
	for i := 0; i < 200; i++ {
		status, resp := e.difs(t, token, project, map[string]map[string]any{sum: req})
		if status != 200 {
			t.Fatalf("status %d: %v", status, resp)
		}
		got := resp[sum].(map[string]any)
		if s := got["state"]; s == "ok" || s == "error" {
			return got
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("debug file did not finish")
	return nil
}

func TestDifsAssembleFlow(t *testing.T) {
	e := newEnv(t, nil)
	token := e.svc(t, e.owner, []string{Scope}, nil)
	other := []byte("some other bytes")
	sa, ra := e.difReq(proguardFile, proguardName)
	sb, rb := e.difReq(other, "blob.bin")
	req := map[string]map[string]any{sa: ra, sb: rb}

	status, resp := e.difs(t, token, "appsite00010", req)
	if status != 200 {
		t.Fatalf("status %d", status)
	}
	for _, s := range []string{sa, sb} {
		got := resp[s].(map[string]any)
		if got["state"] != "not_found" || len(got["missingChunks"].([]any)) != 1 {
			t.Fatalf("%s = %v", s, got)
		}
	}
	if e.upload(t, token, "file", sha(proguardFile), proguardFile) != 200 {
		t.Fatal("upload")
	}
	_, resp = e.difs(t, token, "appsite00010", req)
	if got := resp[sa].(map[string]any); got["state"] != "created" && got["state"] != "assembling" && got["state"] != "ok" {
		t.Fatalf("a = %v", got)
	}
	if got := resp[sb].(map[string]any); got["state"] != "not_found" {
		t.Fatalf("b = %v", got)
	}
	final := e.waitDIF(t, token, "appsite00010", sa, ra)
	dif := final["dif"].(map[string]any)
	if final["state"] != "ok" || dif["debugId"] != proguardID || dif["uuid"] != proguardID || dif["sha1"] != sa || final["missingChunks"] == nil {
		t.Fatalf("final = %v", final)
	}
	if feats := dif["data"].(map[string]any)["features"].([]any); len(feats) != 1 || feats[0] != "mapping" {
		t.Fatalf("features = %v", feats)
	}
	if !e.hasBlob(t, "difs/"+itoa(e.owner.OrgID)+"/"+sa) {
		t.Fatal("raw file not stored")
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// Review Focus 2: while the cache worker builds, polls never report not_found.
func TestDifsPollWhileBuilding(t *testing.T) {
	gate := make(chan struct{})
	started := make(chan struct{}, 1)
	var calls atomic.Int32
	builders := map[string]CacheBuilder{"proguard": {Version: 1, Build: func(_ context.Context, o debugfile.Object, raw io.ReaderAt, size int64, out io.Writer) ([]string, error) {
		calls.Add(1)
		select {
		case started <- struct{}{}:
		default:
		}
		<-gate
		_, err := out.Write([]byte("cache of " + o.DebugID))
		return []string{"mapping", "indexed"}, err
	}}}
	e := newEnv(t, func(d *Deps) { d.Builders = builders })
	token := e.svc(t, e.owner, []string{Scope}, nil)
	sum, req := e.difReq(proguardFile, proguardName)
	if status, _ := e.difs(t, token, "appsite00010", map[string]map[string]any{sum: req}); status != 200 {
		t.Fatal("first poll")
	}
	e.upload(t, token, "file", sum, proguardFile)
	_, _ = e.difs(t, token, "appsite00010", map[string]map[string]any{sum: req})
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("builder did not start")
	}
	for i := 0; i < 5; i++ {
		_, resp := e.difs(t, token, "appsite00010", map[string]map[string]any{sum: req})
		got := resp[sum].(map[string]any)
		if got["state"] != "assembling" || got["dif"] != nil {
			t.Fatalf("poll %d during build = %v", i, got)
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(gate)
	final := e.waitDIF(t, token, "appsite00010", sum, req)
	if final["state"] != "ok" {
		t.Fatalf("final = %v", final)
	}
	var state, key string
	var version int16
	var csize int64
	var features []string
	if err := e.h.deps.Pool.QueryRow(context.Background(), `select cache_state, cache_key, cache_version, cache_size, features from app_debug_files where upload_id=(select id from app_dif_uploads where checksum=$1) and site_id=10`, sum).
		Scan(&state, &key, &version, &csize, &features); err != nil {
		t.Fatal(err)
	}
	wantKey := "difcache/" + itoa(e.owner.OrgID) + "/" + sum + "/" + proguardID + "/proguard-v1"
	if state != "ok" || key != wantKey || version != 1 || csize != int64(len("cache of "+proguardID)) || len(features) != 2 {
		t.Fatalf("row = %s %s v%d %d %v", state, key, version, csize, features)
	}
	rc, err := e.blobs.Open(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(rc)
	_ = rc.Close()
	if !bytes.Equal(data, []byte("cache of "+proguardID)) {
		t.Fatalf("cache blob = %q", data)
	}
}

func TestDifsLimitsAndHostileFiles(t *testing.T) {
	e := newEnv(t, func(d *Deps) { d.DIFMaxBytes = 64; d.OrgCapBytes = 200 })
	token := e.svc(t, e.owner, []string{Scope}, nil)
	ctx := context.Background()
	run := func(name string, data []byte) map[string]any {
		t.Helper()
		sum, req := e.difReq(data, name)
		e.difs(t, token, "appsite00010", map[string]map[string]any{sum: req})
		e.upload(t, token, "file", sum, data)
		return e.waitDIF(t, token, "appsite00010", sum, req)
	}

	big := run("big.bin", bytes.Repeat([]byte{7}, 100))
	if big["state"] != "error" || big["detail"] != "debug file exceeds the size limit" {
		t.Fatalf("oversize = %v", big)
	}
	// Truncated ELF, Mach-O with 2^31 load commands: rejected with a short detail.
	elf := run("a.so", append([]byte("\x7fELF\x02\x01\x01"), make([]byte, 20)...))
	macho := run("a.dylib", append([]byte{0xcf, 0xfa, 0xed, 0xfe, 12, 0, 0, 1, 0, 0, 0, 0, 10, 0, 0, 0, 0, 0, 0, 0x80, 0, 0, 0, 0}, make([]byte, 16)...))
	for _, got := range []map[string]any{elf, macho} {
		d, _ := got["detail"].(string)
		if got["state"] != "error" || !strings.HasPrefix(d, "invalid debug file") || len(d) > 300 {
			t.Fatalf("hostile = %v", got)
		}
	}
	// A file whose chunks do not hash to its checksum.
	part := []byte("not the file")
	wrong := strings.Repeat("a", 40)
	e.difs(t, token, "appsite00010", map[string]map[string]any{wrong: {"name": "x", "chunks": []string{sha(part)}}})
	e.upload(t, token, "file", sha(part), part)
	if got := e.waitDIF(t, token, "appsite00010", wrong, map[string]any{"name": "x", "chunks": []string{sha(part)}}); got["state"] != "error" || !strings.Contains(got["detail"].(string), "checksum") {
		t.Fatalf("mismatch = %v", got)
	}
	// Org cap: fill most of the 200 bytes, then exceed it.
	if ok := run("fill.bin", bytes.Repeat([]byte{1}, 60)); ok["state"] != "ok" {
		t.Fatalf("fill = %v", ok)
	}
	if ok := run("fill2.bin", bytes.Repeat([]byte{2}, 60)); ok["state"] != "ok" {
		t.Fatalf("fill2 = %v", ok)
	}
	if ok := run("fill3.bin", bytes.Repeat([]byte{3}, 60)); ok["state"] != "ok" {
		t.Fatalf("fill3 = %v", ok)
	}
	capped := run("fill4.bin", bytes.Repeat([]byte{4}, 60))
	if capped["state"] != "error" || !strings.Contains(capped["detail"].(string), "SUPERMETRY_SENTRY_ORG_CAP") {
		t.Fatalf("cap = %v", capped)
	}
	var n int
	_ = e.h.deps.Pool.QueryRow(ctx, `select count(*) from app_debug_files where upload_id in (select id from app_dif_uploads where state='error')`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d object rows kept for failed uploads", n)
	}
}

func TestDifsAuth(t *testing.T) {
	e := newEnv(t, nil)
	good := e.svc(t, e.owner, []string{Scope}, nil)
	noScope := e.svc(t, e.owner, []string{"stats:read"}, nil)
	restricted := e.svc(t, e.owner, []string{Scope}, []int32{10})
	sum, req := e.difReq(proguardFile, proguardName)
	body := map[string]map[string]any{sum: req}
	for name, tc := range map[string]struct {
		token, project string
		want           int
	}{
		"no token":        {"", "appsite00010", 401},
		"missing scope":   {noScope, "appsite00010", 403},
		"other org site":  {good, "appsite00030", 404},
		"unknown project": {good, "nosuch", 404},
		"restricted ok":   {restricted, "appsite00010", 200},
		"restricted deny": {restricted, "appsite00020", 403},
		"numeric project": {good, "20", 200},
	} {
		if s, _ := e.difs(t, tc.token, tc.project, body); s != tc.want {
			t.Errorf("%s: %d, want %d", name, s, tc.want)
		}
	}
	for name, b := range map[string]any{
		"empty":     map[string]any{},
		"bad sha":   map[string]any{"zz": map[string]any{"chunks": []string{sum}}},
		"no chunks": map[string]any{sum: map[string]any{"chunks": []string{}}},
		"bad chunk": map[string]any{sum: map[string]any{"chunks": []string{"x"}}},
	} {
		if s, _ := e.json(t, "POST", "/api/0/projects/acme/appsite00010/files/difs/assemble/", good, b); s != 400 {
			t.Errorf("%s: %d, want 400", name, s)
		}
	}
}

func TestDifsSecondProjectGetsRows(t *testing.T) {
	e := newEnv(t, nil)
	token := e.svc(t, e.owner, []string{Scope}, nil)
	sum, req := e.difReq(proguardFile, proguardName)
	e.difs(t, token, "appsite00010", map[string]map[string]any{sum: req})
	e.upload(t, token, "file", sum, proguardFile)
	if got := e.waitDIF(t, token, "appsite00010", sum, req); got["state"] != "ok" {
		t.Fatal(got)
	}
	// The same file for another project is already there; no re-upload.
	_, resp := e.difs(t, token, "appsite00020", map[string]map[string]any{sum: req})
	if got := resp[sum].(map[string]any); got["state"] != "ok" || got["dif"] == nil {
		t.Fatalf("second project = %v", got)
	}
	var n int
	_ = e.h.deps.Pool.QueryRow(context.Background(), `select count(*) from app_debug_files where site_id in (10,20)`).Scan(&n)
	if n != 2 {
		t.Fatalf("%d rows, want one per project", n)
	}
}

// A restart re-queues interrupted uploads and caches older than their builder.
func TestDifsRequeueOnStart(t *testing.T) {
	build := func(_ context.Context, o debugfile.Object, _ io.ReaderAt, _ int64, out io.Writer) ([]string, error) {
		_, err := out.Write([]byte("v"))
		return nil, err
	}
	builders := map[string]CacheBuilder{"proguard": {Version: 1, Build: build}}
	e := newEnv(t, func(d *Deps) { d.Builders = builders })
	token := e.svc(t, e.owner, []string{Scope}, nil)
	ctx := context.Background()
	sum, req := e.difReq(proguardFile, proguardName)
	e.difs(t, token, "appsite00010", map[string]map[string]any{sum: req})
	e.upload(t, token, "file", sum, proguardFile)
	if got := e.waitDIF(t, token, "appsite00010", sum, req); got["state"] != "ok" {
		t.Fatal(got)
	}

	// An upload left assembling by a crash, its chunk present.
	sum2, _ := e.difReq([]byte("MODULE Linux x86_64 DE7FADA356F73CE7B81E0B6E964459C10 a\n"), "a.sym")
	data2 := []byte("MODULE Linux x86_64 DE7FADA356F73CE7B81E0B6E964459C10 a\n")
	e.upload(t, token, "file", sum2, data2)
	if _, err := e.h.deps.Pool.Exec(ctx, `insert into app_dif_uploads(org_id, site_ids, checksum, name, chunks, state) values ($1, '{10}', $2, 'a.sym', $3, 'assembling')`,
		e.owner.OrgID, sum2, []string{sum2}); err != nil {
		t.Fatal(err)
	}
	// A builder upgrade: the stored cache is older than v2.
	builders["proguard"] = CacheBuilder{Version: 2, Build: build}
	e.h.requeueDIFs(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for {
		var v int16
		var st string
		_ = e.h.deps.Pool.QueryRow(ctx, `select (select cache_version from app_debug_files where debug_id=$2 and site_id=10), (select state from app_dif_uploads where checksum=$1)`, sum2, proguardID).Scan(&v, &st)
		if v == 2 && st == "ok" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("not rebuilt: version %d, interrupted upload %s", v, st)
		}
		time.Sleep(20 * time.Millisecond)
	}
	var key string
	_ = e.h.deps.Pool.QueryRow(ctx, `select cache_key from app_debug_files where debug_id=$1 and site_id=10`, proguardID).Scan(&key)
	if !strings.HasSuffix(key, "/proguard-v2") || !e.hasBlob(t, key) {
		t.Fatalf("cache key %q", key)
	}
}
