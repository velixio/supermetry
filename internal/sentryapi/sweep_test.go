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

func TestSweepRemoteSymbolsEvictsLeastRecentlyUsed(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	pool := e.h.deps.Pool
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	put := func(id string, size int64, usedAgo time.Duration) {
		t.Helper()
		key := "remote-symbols/electron/" + id
		if _, err := e.blobs.PutIfAbsent(ctx, key, strings.NewReader(strings.Repeat("x", int(size)))); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `insert into app_remote_symbols(source,debug_file,debug_id,state,cache_key,size,fetched_at,last_used_at) values ('electron','electron',$1,'ok',$2,$3,$4,$4)`,
			id, key, size, now.Add(-usedAgo)); err != nil {
			t.Fatal(err)
		}
	}
	put("old", 400, 72*time.Hour)
	put("mid", 400, 24*time.Hour)
	put("new", 400, time.Hour)
	// A negative row past its time to live goes regardless of the byte cap.
	if _, err := pool.Exec(ctx, `insert into app_remote_symbols(source,debug_file,debug_id,state,fetched_at,last_used_at) values ('electron','electron','gone','missing',$1,$1),('electron','electron','fresh','missing',$2,$2)`,
		now.Add(-8*24*time.Hour), now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}

	n, err := SweepRemoteSymbols(ctx, pool, e.blobs, now, 1000)
	if err != nil || n != 1 {
		t.Fatalf("evicted %d, %v; want 1 (1200 bytes over a 1000 cap drops the oldest)", n, err)
	}
	if e.hasBlob(t, "remote-symbols/electron/old") || !e.hasBlob(t, "remote-symbols/electron/mid") || !e.hasBlob(t, "remote-symbols/electron/new") {
		t.Fatal("wrong blobs evicted")
	}
	var ids []string
	rows, err := pool.Query(ctx, `select debug_id from app_remote_symbols order by debug_id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if strings.Join(ids, ",") != "fresh,mid,new" {
		t.Fatalf("rows left = %v", ids)
	}
	if n, err := SweepRemoteSymbols(ctx, pool, e.blobs, now, 1000); err != nil || n != 0 {
		t.Fatalf("second sweep evicted %d, %v", n, err)
	}
	if n, err := SweepRemoteSymbols(ctx, pool, e.blobs, now, 500); err != nil || n != 1 {
		t.Fatalf("tighter cap evicted %d, %v; want 1 more (mid)", n, err)
	}
}
