package sentryapi

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha1" //nolint:gosec // test fixtures use the protocol's hash
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"supermetry/internal/db"
	"supermetry/internal/platform/authz"
	"supermetry/internal/platform/keys"
	"supermetry/internal/platform/store"
	"supermetry/internal/replay"
	"supermetry/internal/sites"
	"supermetry/internal/sourcemap"
	"supermetry/internal/testpg"
)

type fakeSites struct{ all []sites.Site }

func (f fakeSites) ByPublicID(id string) (sites.Site, bool) {
	for _, s := range f.all {
		if s.PublicID == id {
			return s, true
		}
	}
	return sites.Site{}, false
}
func (f fakeSites) Site(id int32) (sites.Site, bool) {
	for _, s := range f.all {
		if s.ID == id {
			return s, true
		}
	}
	return sites.Site{}, false
}
func (f fakeSites) All() []sites.Site { return f.all }

type env struct {
	h       *Handler
	srv     *httptest.Server
	blobs   *replay.DiskStore
	keys    *keys.Keys
	owner   authz.Principal
	other   authz.Principal
	pool    interface{ Close() }
	deps    Deps
	svc     func(t *testing.T, p authz.Principal, scopes []string, sites []int32) string
	orgSlug string
}

func newEnv(t *testing.T, mod func(*Deps)) *env {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	mk := func(email, slug string) authz.Principal {
		var uid, oid int64
		if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash) values ($1,'U','h') returning id`, email).Scan(&uid); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ($1,$1) returning id`, slug).Scan(&oid); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `insert into memberships(org_id,user_id,role) values ($1,$2,'owner')`, oid, uid); err != nil {
			t.Fatal(err)
		}
		return authz.Principal{UserID: uid, OrgID: oid, Role: authz.RoleOwner}
	}
	owner, other := mk("a@example.test", "acme"), mk("b@example.test", "other")
	if _, err := pool.Exec(ctx, `insert into sites(id,org_id,public_id,name,timezone,retention_class) overriding system value values (10,$1,'appsite00010','Ten','UTC',30),(20,$1,'appsite00020','Twenty','UTC',30),(30,$2,'appsite00030','Thirty','UTC',30)`, owner.OrgID, other.OrgID); err != nil {
		t.Fatal(err)
	}
	reg := fakeSites{all: []sites.Site{
		{ID: 10, OrgID: owner.OrgID, PublicID: "appsite00010", Kind: sites.KindApp}, {ID: 20, OrgID: owner.OrgID, PublicID: "appsite00020", Kind: sites.KindApp}, {ID: 30, OrgID: other.OrgID, PublicID: "appsite00030", Kind: sites.KindApp},
	}}
	blobs, err := replay.NewDiskStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	k := keys.New(&store.Store{Pool: pool})
	t.Cleanup(k.Close)
	deps := Deps{Pool: pool, Keys: k, Sites: reg, Blobs: blobs, TempDir: t.TempDir()}
	if mod != nil {
		mod(&deps)
	}
	h := New(deps)
	h.Start(ctx)
	t.Cleanup(h.Close)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &env{h: h, srv: srv, blobs: blobs, keys: k, owner: owner, other: other, deps: deps, orgSlug: "acme",
		svc: func(t *testing.T, p authz.Principal, scopes []string, sites []int32) string {
			t.Helper()
			plain, _, err := k.Create(ctx, p, "t", scopes, sites, nil)
			if err != nil {
				t.Fatal(err)
			}
			return plain
		}}
}

func (e *env) do(t *testing.T, method, path, token string, body io.Reader, headers ...string) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequest(method, e.srv.URL+path, body)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data
}

func (e *env) json(t *testing.T, method, path, token string, body any) (int, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	status, data := e.do(t, method, path, token, reader, "Content-Type", "application/json")
	var out map[string]any
	_ = json.Unmarshal(data, &out)
	return status, out
}

func sha(b []byte) string {
	s := sha1.Sum(b) //nolint:gosec // see import
	return hex.EncodeToString(s[:])
}

func multipartBody(t *testing.T, field, filename string, data []byte) (io.Reader, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile(field, filename)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(data)
	_ = mw.Close()
	return &buf, mw.FormDataContentType()
}

func (e *env) upload(t *testing.T, token, field, filename string, data []byte) int {
	t.Helper()
	body, ct := multipartBody(t, field, filename, data)
	status, _ := e.do(t, "POST", "/api/0/organizations/acme/chunk-upload/", token, body, "Content-Type", ct)
	return status
}

func fixtureBundle(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("../sourcemap/testdata/vite/bundle.zip")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func (e *env) waitState(t *testing.T, token string, req map[string]any) map[string]any {
	t.Helper()
	for i := 0; i < 100; i++ {
		_, resp := e.json(t, "POST", "/api/0/organizations/acme/artifactbundle/assemble/", token, req)
		if s := resp["state"]; s == "ok" || s == "error" {
			return resp
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("assembly did not finish")
	return nil
}

func TestDiscoveryShapesAndRouting(t *testing.T) {
	e := newEnv(t, nil)
	token := e.svc(t, e.owner, []string{Scope}, nil)
	if s, body := e.json(t, "GET", "/api/0/", token, nil); s != 200 || len(body) != 0 {
		t.Fatalf("root = %d %v", s, body)
	}
	_, org := e.json(t, "GET", "/api/0/organizations/acme/", token, nil)
	if !reflect.DeepEqual(org, map[string]any{"slug": "acme", "name": "acme"}) {
		t.Fatalf("org = %v", org)
	}
	_, opts := e.json(t, "GET", "/api/0/organizations/acme/chunk-upload/", token, nil)
	want := map[string]any{"url": e.srv.URL + "/api/0/organizations/acme/chunk-upload/", "chunkSize": 8388608.0, "chunksPerRequest": 64.0, "maxFileSize": 2147483648.0, "maxRequestSize": 33554432.0,
		"concurrency": 4.0, "hashAlgorithm": "sha1", "compression": []any{"gzip"}, "accept": []any{"artifact_bundles", "release_files", "sources", "debug_files", "proguard"}}
	if !reflect.DeepEqual(opts, want) {
		t.Fatalf("options = %v", opts)
	}
	if s, body := e.json(t, "GET", "/api/0/nope/", token, nil); s != 404 || body["detail"] != "not found" {
		t.Fatalf("unknown = %d %v", s, body)
	}
	if s, body := e.json(t, "GET", "/api/0/organizations/acme/repos/", token, nil); s != 200 || body != nil {
		t.Fatalf("repos = %d %v", s, body)
	}
	if s, _ := e.json(t, "GET", "/api/0/organizations/acme/releases/1.0/previous-with-commits/", token, nil); s != 404 {
		t.Fatalf("previous = %d", s)
	}
}

func TestAuthMatrix(t *testing.T) {
	e := newEnv(t, nil)
	good := e.svc(t, e.owner, []string{Scope}, nil)
	statsOnly := e.svc(t, e.owner, []string{"stats:read"}, nil)
	otherOrg := e.svc(t, e.other, []string{Scope}, nil)
	restricted := e.svc(t, e.owner, []string{Scope}, []int32{10})
	chunk := "/api/0/organizations/acme/chunk-upload/"
	for name, tc := range map[string]struct {
		token string
		want  int
	}{"none": {"", 401}, "garbage": {"pnl_aaaaaaaa_x", 401}, "wrong scope": {statsOnly, 403}, "other org": {otherOrg, 404}, "good": {good, 200}} {
		if s, _ := e.json(t, "GET", chunk, tc.token, nil); s != tc.want {
			t.Errorf("%s: %d, want %d", name, s, tc.want)
		}
	}
	assemble := func(token string, projects []string) int {
		s, _ := e.json(t, "POST", "/api/0/organizations/acme/artifactbundle/assemble/", token, map[string]any{"checksum": strings.Repeat("a", 40), "chunks": []string{strings.Repeat("a", 40)}, "projects": projects})
		return s
	}
	if s := assemble(restricted, []string{"appsite00020"}); s != 403 {
		t.Errorf("restricted site = %d", s)
	}
	if s := assemble(restricted, []string{"appsite00010"}); s != 200 {
		t.Errorf("allowed site = %d", s)
	}
	if s := assemble(good, []string{"appsite00030"}); s != 404 {
		t.Errorf("other org's project = %d", s)
	}
	if s := assemble(good, []string{"nosuch"}); s != 404 {
		t.Errorf("unknown project = %d", s)
	}
	if s := assemble(good, []string{"20"}); s != 200 {
		t.Errorf("numeric project = %d", s)
	}
}

func TestSentryOrgToken(t *testing.T) {
	e := newEnv(t, nil)
	plain := e.svc(t, e.owner, []string{Scope}, nil)
	payload, _ := json.Marshal(map[string]any{"iat": 1, "url": "http://x", "region_url": "http://x", "org": "acme"})
	compact := strings.ReplaceAll(strings.TrimPrefix(plain, "pnl_"), "_", "")
	token := "sntrys_" + base64.StdEncoding.EncodeToString(payload) + "_" + compact
	if s, _ := e.json(t, "GET", "/api/0/organizations/acme/", token, nil); s != 200 {
		t.Fatalf("sntrys token = %d", s)
	}
	for _, bad := range []string{"sntrys_" + base64.StdEncoding.EncodeToString(payload) + "_" + compact[:len(compact)-2] + "ZZ", "sntrys_" + base64.StdEncoding.EncodeToString(payload) + "_secret", "sntrys_"} {
		if s, _ := e.json(t, "GET", "/api/0/organizations/acme/", bad, nil); s != 401 {
			t.Fatalf("bad sntrys token = %d", s)
		}
	}
	if got := ParseToken(token); got != plain {
		t.Fatalf("ParseToken = %q", got)
	}
}

func TestChunkUpload(t *testing.T) {
	e := newEnv(t, nil)
	token := e.svc(t, e.owner, []string{Scope}, nil)
	data := []byte("hello chunk")
	if s := e.upload(t, token, "file", strings.Repeat("0", 40), data); s != 400 {
		t.Fatalf("sha1 mismatch = %d", s)
	}
	if s := e.upload(t, token, "file", "notahash", data); s != 400 {
		t.Fatalf("bad name = %d", s)
	}
	if s := e.upload(t, token, "file", sha(data), data); s != 200 {
		t.Fatalf("plain = %d", s)
	}
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	gdata := []byte("gzipped chunk")
	_, _ = zw.Write(gdata)
	_ = zw.Close()
	if s := e.upload(t, token, "file_gzip", sha(gdata), gz.Bytes()); s != 200 {
		t.Fatalf("gzip = %d", s)
	}
	rc, err := e.blobs.Open(context.Background(), "chunks/"+sha(gdata))
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := io.ReadAll(rc)
	_ = rc.Close()
	if !bytes.Equal(stored, gdata) {
		t.Fatalf("stored gzip chunk = %q (must be decompressed)", stored)
	}
	if s := e.upload(t, token, "file_gzip", sha(gdata), []byte("not gzip")); s != 400 {
		t.Fatalf("bad gzip = %d", s)
	}
	big := bytes.Repeat([]byte{1}, chunkSize+1)
	if s := e.upload(t, token, "file", sha(big), big); s != 400 {
		t.Fatalf("oversize chunk = %d", s)
	}
}

func TestAssembleFlow(t *testing.T) {
	e := newEnv(t, nil)
	token := e.svc(t, e.owner, []string{Scope}, nil)
	bundle := fixtureBundle(t)
	half := len(bundle) / 2
	c1, c2 := bundle[:half], bundle[half:]
	req := map[string]any{"checksum": sha(bundle), "chunks": []string{sha(c1), sha(c2)}, "projects": []string{"appsite00010"}, "version": "1.2.3", "dist": "7"}

	_, resp := e.json(t, "POST", "/api/0/organizations/acme/artifactbundle/assemble/", token, req)
	if resp["state"] != "not_found" || len(resp["missingChunks"].([]any)) != 2 || resp["detail"] != nil {
		t.Fatalf("first = %v", resp)
	}
	if e.upload(t, token, "file", sha(c1), c1) != 200 {
		t.Fatal("upload c1")
	}
	_, resp = e.json(t, "POST", "/api/0/organizations/acme/artifactbundle/assemble/", token, req)
	if resp["state"] != "not_found" || !reflect.DeepEqual(resp["missingChunks"], []any{sha(c2)}) {
		t.Fatalf("partial = %v (only the missing chunk is reported)", resp)
	}
	if e.upload(t, token, "file", sha(c2), c2) != 200 {
		t.Fatal("upload c2")
	}
	resp = e.waitState(t, token, req)
	if resp["state"] != "ok" || resp["detail"] != nil || len(resp["missingChunks"].([]any)) != 0 {
		t.Fatalf("final = %v", resp)
	}
	// Idempotent: a repeat call reports the same state and adds no rows.
	_, again := e.json(t, "POST", "/api/0/organizations/acme/artifactbundle/assemble/", token, req)
	if !reflect.DeepEqual(again, resp) {
		t.Fatalf("repeat = %v, want %v", again, resp)
	}
	ctx := context.Background()
	var bundles, files, withDebug int
	_ = e.deps.Pool.QueryRow(ctx, `select (select count(*) from app_artifact_bundles), (select count(*) from app_artifact_files), (select count(*) from app_artifact_files where debug_id is not null and site_id=10 and release='1.2.3' and dist='7')`).Scan(&bundles, &files, &withDebug)
	if bundles != 1 || files == 0 || withDebug == 0 {
		t.Fatalf("rows: bundles=%d files=%d withDebug=%d", bundles, files, withDebug)
	}
	var key string
	if err := e.deps.Pool.QueryRow(ctx, `select blob_key from app_artifact_files where type='minified_source' limit 1`).Scan(&key); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(key, "bundles/") || !strings.Contains(key, sha(bundle)) {
		t.Fatalf("blob key = %q", key)
	}
	if rc, err := e.blobs.Open(ctx, key); err != nil {
		t.Fatal(err)
	} else {
		_ = rc.Close()
	}
	// Another organization uploading the same bytes gets its own bundle.
	otherToken := e.svc(t, e.other, []string{Scope}, nil)
	if s, _ := e.json(t, "POST", "/api/0/organizations/other/artifactbundle/assemble/", otherToken, map[string]any{"checksum": sha(bundle), "chunks": []string{sha(c1), sha(c2)}}); s != 200 {
		t.Fatalf("other org = %d", s)
	}
	otherResp := map[string]any{}
	for i := 0; i < 100 && otherResp["state"] != "ok"; i++ {
		_, otherResp = e.json(t, "POST", "/api/0/organizations/other/artifactbundle/assemble/", otherToken, map[string]any{"checksum": sha(bundle), "chunks": []string{sha(c1), sha(c2)}})
		time.Sleep(50 * time.Millisecond)
	}
	var orgs int
	_ = e.deps.Pool.QueryRow(ctx, `select count(distinct org_id) from app_artifact_files`).Scan(&orgs)
	if orgs != 2 {
		t.Fatalf("files for %d orgs, want 2", orgs)
	}
}

func TestAssembleErrors(t *testing.T) {
	t.Run("wrong checksum", func(t *testing.T) {
		e := newEnv(t, nil)
		token := e.svc(t, e.owner, []string{Scope}, nil)
		data := []byte("not a bundle")
		e.upload(t, token, "file", sha(data), data)
		resp := e.waitState(t, token, map[string]any{"checksum": strings.Repeat("b", 40), "chunks": []string{sha(data)}})
		if resp["state"] != "error" || resp["detail"] == nil {
			t.Fatalf("resp = %v", resp)
		}
	})
	t.Run("not a zip", func(t *testing.T) {
		e := newEnv(t, nil)
		token := e.svc(t, e.owner, []string{Scope}, nil)
		data := []byte("not a bundle")
		e.upload(t, token, "file", sha(data), data)
		resp := e.waitState(t, token, map[string]any{"checksum": sha(data), "chunks": []string{sha(data)}})
		if resp["state"] != "error" {
			t.Fatalf("resp = %v", resp)
		}
	})
	t.Run("file cap", func(t *testing.T) {
		e := newEnv(t, func(d *Deps) { d.Caps = sourcemap.Caps{MaxTotal: 1 << 20, MaxFiles: 1, MaxFile: 1 << 20} })
		token := e.svc(t, e.owner, []string{Scope}, nil)
		b := fixtureBundle(t)
		e.upload(t, token, "file", sha(b), b)
		resp := e.waitState(t, token, map[string]any{"checksum": sha(b), "chunks": []string{sha(b)}})
		if resp["state"] != "error" || !strings.Contains(resp["detail"].(string), "cap") {
			t.Fatalf("resp = %v", resp)
		}
	})
	t.Run("org storage cap", func(t *testing.T) {
		b := fixtureBundle(t)
		e := newEnv(t, func(d *Deps) { d.OrgCapBytes = int64(len(b)) + 10 })
		token := e.svc(t, e.owner, []string{Scope}, nil)
		e.upload(t, token, "file", sha(b), b)
		resp := e.waitState(t, token, map[string]any{"checksum": sha(b), "chunks": []string{sha(b)}})
		if resp["state"] != "ok" {
			t.Fatalf("first bundle fits the cap: %v", resp)
		}
		b2 := append(append([]byte{}, b...), 0) // trailing byte: a different checksum
		e.upload(t, token, "file", sha(b2), b2)
		resp = e.waitState(t, token, map[string]any{"checksum": sha(b2), "chunks": []string{sha(b2)}})
		if resp["state"] != "error" || !strings.Contains(resp["detail"].(string), "storage limit reached") || !strings.Contains(resp["detail"].(string), "SUPERMETRY_SENTRY_ORG_CAP") {
			t.Fatalf("resp = %v", resp)
		}
	})
	t.Run("org storage cap is per organization", func(t *testing.T) {
		b := fixtureBundle(t)
		e := newEnv(t, func(d *Deps) { d.OrgCapBytes = int64(len(b)) + 10 })
		token := e.svc(t, e.owner, []string{Scope}, nil)
		e.upload(t, token, "file", sha(b), b)
		if resp := e.waitState(t, token, map[string]any{"checksum": sha(b), "chunks": []string{sha(b)}}); resp["state"] != "ok" {
			t.Fatalf("owner bundle: %v", resp)
		}
		// The other organization has its own budget, and a failed bundle uses none.
		otherToken := e.svc(t, e.other, []string{Scope}, nil)
		e.upload(t, otherToken, "file", sha(b), b)
		_, resp := e.json(t, "POST", "/api/0/organizations/other/artifactbundle/assemble/", otherToken, map[string]any{"checksum": sha(b), "chunks": []string{sha(b)}})
		for i := 0; i < 100 && resp["state"] != "ok"; i++ {
			time.Sleep(50 * time.Millisecond)
			_, resp = e.json(t, "POST", "/api/0/organizations/other/artifactbundle/assemble/", otherToken, map[string]any{"checksum": sha(b), "chunks": []string{sha(b)}})
		}
		if resp["state"] != "ok" {
			t.Fatalf("other organization bundle: %v", resp)
		}
	})
	t.Run("bundle larger than the cap", func(t *testing.T) {
		b := fixtureBundle(t)
		e := newEnv(t, func(d *Deps) { d.OrgCapBytes = int64(len(b)) - 1 })
		token := e.svc(t, e.owner, []string{Scope}, nil)
		e.upload(t, token, "file", sha(b), b)
		resp := e.waitState(t, token, map[string]any{"checksum": sha(b), "chunks": []string{sha(b)}})
		if resp["state"] != "error" || !strings.Contains(resp["detail"].(string), "storage limit reached") {
			t.Fatalf("resp = %v", resp)
		}
	})
	t.Run("bad request", func(t *testing.T) {
		e := newEnv(t, nil)
		token := e.svc(t, e.owner, []string{Scope}, nil)
		if s, _ := e.json(t, "POST", "/api/0/organizations/acme/artifactbundle/assemble/", token, map[string]any{"checksum": "x", "chunks": []string{}}); s != 400 {
			t.Fatalf("status = %d", s)
		}
	})
}

func TestReleaseRoundTrip(t *testing.T) {
	e := newEnv(t, nil)
	token := e.svc(t, e.owner, []string{Scope}, nil)
	base := "/api/0/organizations/acme/releases/"
	if s, _ := e.json(t, "GET", base+"1.0.0/", token, nil); s != 404 {
		t.Fatalf("missing release = %d", s)
	}
	s, rel := e.json(t, "POST", base, token, map[string]any{"version": "1.0.0", "projects": []string{"appsite00010"}})
	if s != 201 || rel["version"] != "1.0.0" || rel["dateCreated"] == nil || rel["dateReleased"] != nil || len(rel["projects"].([]any)) != 1 {
		t.Fatalf("create = %d %v", s, rel)
	}
	if s, _ := e.json(t, "POST", base, token, map[string]any{"version": "1.0.0"}); s != 208 {
		t.Fatalf("repeat create = %d", s)
	}
	released := time.Now().UTC().Format(time.RFC3339)
	s, rel = e.json(t, "PUT", base+"1.0.0/", token, map[string]any{"dateReleased": released})
	if s != 200 || rel["dateReleased"] == nil {
		t.Fatalf("finalize = %d %v", s, rel)
	}
	s, rel = e.json(t, "GET", base+"1.0.0/", token, nil)
	if s != 200 || rel["dateReleased"] == nil {
		t.Fatalf("get = %d %v", s, rel)
	}
	s, dep := e.json(t, "POST", base+"1.0.0/deploys/", token, map[string]any{"environment": "production", "name": "ci-1"})
	if s != 201 || dep["environment"] != "production" || dep["id"] == nil {
		t.Fatalf("deploy = %d %v", s, dep)
	}
	if s, _ := e.json(t, "POST", base+"1.0.0/deploys/", token, map[string]any{"name": "x"}); s != 400 {
		t.Fatalf("deploy without environment = %d", s)
	}
	if s, _ := e.json(t, "POST", base+"9.9/deploys/", token, map[string]any{"environment": "x"}); s != 404 {
		t.Fatalf("deploy on missing release = %d", s)
	}
	_, data := e.do(t, "GET", base+"1.0.0/deploys/", token, nil)
	var list []map[string]any
	_ = json.Unmarshal(data, &list)
	if len(list) != 1 {
		t.Fatalf("deploys = %s", data)
	}
	// Versions with a slash arrive percent-encoded.
	if s, _ := e.json(t, "POST", base, token, map[string]any{"version": "app-1.0/rc"}); s != 201 {
		t.Fatalf("slash version create = %d", s)
	}
	if s, got := e.json(t, "GET", base+"app-1.0%2Frc/", token, nil); s != 200 || got["version"] != "app-1.0/rc" {
		t.Fatalf("slash version get = %d %v", s, got)
	}
	// Another organization cannot see it.
	otherToken := e.svc(t, e.other, []string{Scope}, nil)
	if s, _ := e.json(t, "GET", "/api/0/organizations/other/releases/1.0.0/", otherToken, nil); s != 404 {
		t.Fatalf("cross-org release = %d", s)
	}
}

func TestReleaseSiteRestrictionMatrix(t *testing.T) {
	e := newEnv(t, nil)
	full := e.svc(t, e.owner, []string{Scope}, nil)
	restricted := e.svc(t, e.owner, []string{Scope}, []int32{10})
	base := "/api/0/organizations/acme/releases/"
	for version, projects := range map[string][]string{"site-b": {"appsite00020"}, "org-wide": nil, "site-a": {"appsite00010"}} {
		if s, _ := e.json(t, "POST", base, full, map[string]any{"version": version, "projects": projects}); s != 201 {
			t.Fatalf("create %s = %d", version, s)
		}
	}
	for _, version := range []string{"site-b", "org-wide"} {
		for name, call := range map[string]func() int{
			"get": func() int { s, _ := e.json(t, "GET", base+version+"/", restricted, nil); return s },
			"update": func() int {
				s, _ := e.json(t, "PUT", base+version+"/", restricted, map[string]any{"url": "http://x"})
				return s
			},
			"deploy create": func() int {
				s, _ := e.json(t, "POST", base+version+"/deploys/", restricted, map[string]any{"environment": "e"})
				return s
			},
			"deploy list":     func() int { s, _ := e.json(t, "GET", base+version+"/deploys/", restricted, nil); return s },
			"create conflict": func() int { s, _ := e.json(t, "POST", base, restricted, map[string]any{"version": version}); return s },
		} {
			if got := call(); got != 404 {
				t.Errorf("restricted %s %s = %d, want 404", name, version, got)
			}
		}
		if s, _ := e.json(t, "GET", base+version+"/", full, nil); s != 200 {
			t.Errorf("unrestricted get %s = %d", version, s)
		}
		if s, _ := e.json(t, "POST", base+version+"/deploys/", full, map[string]any{"environment": "e"}); s != 201 {
			t.Errorf("unrestricted deploy %s = %d", version, s)
		}
	}
	// Its own site works end to end.
	if s, _ := e.json(t, "GET", base+"site-a/", restricted, nil); s != 200 {
		t.Errorf("own release get = %d", s)
	}
	if s, _ := e.json(t, "POST", base+"site-a/deploys/", restricted, map[string]any{"environment": "e"}); s != 201 {
		t.Errorf("own deploy = %d", s)
	}
	// A restricted key cannot attach a release to a site it may not use.
	if s, _ := e.json(t, "PUT", base+"site-a/", restricted, map[string]any{"projects": []string{"appsite00020"}}); s != 403 {
		t.Errorf("update with foreign project = %d, want 403", s)
	}
	// A restricted key's release without projects is bound to its own sites.
	if s, _ := e.json(t, "POST", base, restricted, map[string]any{"version": "mine"}); s != 201 {
		t.Fatalf("restricted create = %d", s)
	}
	if s, _ := e.json(t, "GET", base+"mine/", restricted, nil); s != 200 {
		t.Errorf("restricted get own = %d", s)
	}
}

func TestAssembleHidesBundlesOfOtherSites(t *testing.T) {
	e := newEnv(t, nil)
	full := e.svc(t, e.owner, []string{Scope}, nil)
	restricted := e.svc(t, e.owner, []string{Scope}, []int32{10})
	b := fixtureBundle(t)
	e.upload(t, full, "file", sha(b), b)
	req := map[string]any{"checksum": sha(b), "chunks": []string{sha(b)}, "projects": []string{"appsite00020"}}
	if resp := e.waitState(t, full, req); resp["state"] != "ok" {
		t.Fatalf("resp = %v", resp)
	}
	if s, _ := e.json(t, "POST", "/api/0/organizations/acme/artifactbundle/assemble/", restricted, map[string]any{"checksum": sha(b), "chunks": []string{sha(b)}}); s != 404 {
		t.Fatalf("restricted key vs another site's bundle = %d, want 404", s)
	}
}
