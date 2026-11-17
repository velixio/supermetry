package sentryapi

import (
	"context"
	"errors"
	"io/fs"
	"strconv"
	"strings"
	"testing"
	"time"

	"supermetry/internal/retention"
)

func (e *env) hasBlob(t *testing.T, key string) bool {
	t.Helper()
	rc, err := e.blobs.Open(context.Background(), key)
	if err == nil {
		_ = rc.Close()
		return true
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	return false
}

func TestSweepChunks(t *testing.T) {
	e := newEnv(t, nil)
	token := e.svc(t, e.owner, []string{Scope}, nil)
	ctx := context.Background()
	pool := e.h.deps.Pool
	real := time.Now()
	later := real.Add(ChunkGrace + time.Hour)

	orphan, inflight, pending := []byte("orphan chunk"), []byte("in flight chunk"), []byte("pending chunk")
	for _, c := range [][]byte{orphan, inflight, pending} {
		if e.upload(t, token, "file", sha(c), c) != 200 {
			t.Fatal("upload")
		}
	}
	// A bundle row that has not been assembled yet still owns its chunks.
	if _, err := pool.Exec(ctx, `insert into app_artifact_bundles(org_id, checksum, chunks, state) values ($1, $2, $3, 'created')`,
		e.owner.OrgID, strings.Repeat("c", 40), []string{sha(pending)}); err != nil {
		t.Fatal(err)
	}

	// Inside the grace period nothing goes, referenced or not: an upload stores
	// its chunks before the assemble call that references them.
	res, err := Sweep(ctx, pool, e.blobs, real, ChunkGrace)
	if err != nil || res.Chunks != 0 {
		t.Fatalf("in-flight sweep = %+v, %v", res, err)
	}
	for _, c := range [][]byte{orphan, inflight, pending} {
		if !e.hasBlob(t, chunkKey(sha(c))) {
			t.Fatalf("chunk %s swept during grace", sha(c))
		}
	}

	// A late assemble for the in-flight chunk lands before the sweep: it is kept.
	if _, err := pool.Exec(ctx, `insert into app_artifact_bundles(org_id, checksum, chunks, state) values ($1, $2, $3, 'assembling')`,
		e.owner.OrgID, strings.Repeat("d", 40), []string{sha(inflight)}); err != nil {
		t.Fatal(err)
	}
	res, err = Sweep(ctx, pool, e.blobs, later, ChunkGrace)
	if err != nil {
		t.Fatal(err)
	}
	if res.Chunks != 1 || res.ChunkBytes != int64(len(orphan)) {
		t.Fatalf("sweep = %+v, want only the orphan", res)
	}
	if e.hasBlob(t, chunkKey(sha(orphan))) {
		t.Fatal("orphan chunk survived")
	}
	if !e.hasBlob(t, chunkKey(sha(inflight))) || !e.hasBlob(t, chunkKey(sha(pending))) {
		t.Fatal("referenced chunk was swept")
	}
	// Idempotent.
	if res, err = Sweep(ctx, pool, e.blobs, later, ChunkGrace); err != nil || res.Chunks != 0 {
		t.Fatalf("second sweep = %+v, %v", res, err)
	}
}

func TestSweepAfterSiteErasure(t *testing.T) {
	e := newEnv(t, nil)
	token := e.svc(t, e.owner, []string{Scope}, nil)
	ctx := context.Background()
	pool := e.h.deps.Pool
	bundle := fixtureBundle(t)
	siteBundle := map[string]any{"checksum": sha(bundle), "chunks": []string{sha(bundle)}, "projects": []string{"appsite00010"}, "version": "1"}
	e.upload(t, token, "file", sha(bundle), bundle)
	if resp := e.waitState(t, token, siteBundle); resp["state"] != "ok" {
		t.Fatalf("resp = %v", resp)
	}
	// A second bundle for every project of the organization.
	b2 := append(append([]byte{}, bundle...), 0)
	e.upload(t, token, "file", sha(b2), b2)
	if resp := e.waitState(t, token, map[string]any{"checksum": sha(b2), "chunks": []string{sha(b2)}, "version": "2"}); resp["state"] != "error" && resp["state"] != "ok" {
		t.Fatalf("resp = %v", resp)
	}
	var files int
	pool.QueryRow(ctx, `select count(*) from app_artifact_files where site_id=10`).Scan(&files)
	if files == 0 {
		t.Fatal("expected index rows for site 10")
	}
	list, _ := e.blobs.List(ctx, "bundles/")
	if len(list) == 0 {
		t.Fatal("expected bundle blobs")
	}

	// Site 10 is erased the way the site-erasure job does it.
	if _, err := pool.Exec(ctx, `update sites set deleted_at=now(), erased_at=now() where id=10`); err != nil {
		t.Fatal(err)
	}
	if err := retention.Erase(ctx, pool, 10, 100, 0); err != nil {
		t.Fatal(err)
	}
	pool.QueryRow(ctx, `select count(*) from app_artifact_files where site_id=10`).Scan(&files)
	if files != 0 {
		t.Fatalf("%d index rows left for the erased site", files)
	}

	res, err := Sweep(ctx, pool, e.blobs, time.Now().Add(ChunkGrace+time.Hour), ChunkGrace)
	if err != nil {
		t.Fatal(err)
	}
	if res.Bundles != 1 {
		t.Fatalf("sweep = %+v, want the erased site's bundle removed", res)
	}
	var rows, orgWide int
	pool.QueryRow(ctx, `select count(*) from app_artifact_bundles where checksum=$1`, sha(bundle)).Scan(&rows)
	pool.QueryRow(ctx, `select count(*) from app_artifact_bundles where checksum=$1`, sha(b2)).Scan(&orgWide)
	if rows != 0 || orgWide != 1 {
		t.Fatalf("bundle rows: site=%d org-wide=%d", rows, orgWide)
	}
	prefix := "bundles/" + strconv.FormatInt(e.owner.OrgID, 10) + "/" + sha(bundle) + "/"
	if left, _ := e.blobs.List(ctx, prefix); len(left) != 0 {
		t.Fatalf("blobs left: %v", left)
	}
	if e.hasBlob(t, chunkKey(sha(bundle))) {
		t.Fatal("chunk of the erased bundle survived the sweep")
	}
	if !e.hasBlob(t, chunkKey(sha(b2))) {
		t.Fatal("chunk of a live bundle was swept")
	}
}

func TestSweepAfterOrgErasure(t *testing.T) {
	e := newEnv(t, nil)
	token := e.svc(t, e.owner, []string{Scope}, nil)
	ctx := context.Background()
	pool := e.h.deps.Pool
	b := fixtureBundle(t)
	e.upload(t, token, "file", sha(b), b)
	if resp := e.waitState(t, token, map[string]any{"checksum": sha(b), "chunks": []string{sha(b)}}); resp["state"] != "ok" {
		t.Fatalf("resp = %v", resp)
	}
	if _, err := pool.Exec(ctx, `update orgs set deleted_at=now(), erased_at=now() where id=$1`, e.owner.OrgID); err != nil {
		t.Fatal(err)
	}
	if res, err := Sweep(ctx, pool, e.blobs, time.Now(), ChunkGrace); err != nil || res.Bundles != 1 {
		t.Fatalf("sweep = %+v, %v", res, err)
	}
	var n int
	pool.QueryRow(ctx, `select count(*) from app_artifact_files`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d index rows left", n)
	}
}
