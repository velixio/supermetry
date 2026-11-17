package sentryapi

import (
	"context"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	"supermetry/internal/debugfile"
)

func TestSweepKeepsChunksOfDebugFileUploads(t *testing.T) {
	e := newEnv(t, nil)
	token := e.svc(t, e.owner, []string{Scope}, nil)
	ctx := context.Background()
	pool := e.h.deps.Pool
	held, orphan := []byte("held by a debug file upload"), []byte("held by nothing")
	for _, c := range [][]byte{held, orphan} {
		if e.upload(t, token, "file", sha(c), c) != 200 {
			t.Fatal("upload")
		}
	}
	if _, err := pool.Exec(ctx, `insert into app_dif_uploads(org_id, checksum, chunks, state) values ($1, $2, $3, 'created')`,
		e.owner.OrgID, strings.Repeat("e", 40), []string{sha(held)}); err != nil {
		t.Fatal(err)
	}
	res, err := Sweep(ctx, pool, e.blobs, time.Now().Add(ChunkGrace+time.Hour), ChunkGrace)
	if err != nil || res.Chunks != 1 {
		t.Fatalf("sweep = %+v, %v", res, err)
	}
	if !e.hasBlob(t, chunkKey(sha(held))) || e.hasBlob(t, chunkKey(sha(orphan))) {
		t.Fatal("the debug file's chunk must survive and the orphan go")
	}
}

func TestSweepDebugFilesAfterOrgErasure(t *testing.T) {
	e := newEnv(t, func(d *Deps) {
		d.Builders = map[string]CacheBuilder{"proguard": {Version: 1, Build: func(_ context.Context, _ debugfile.Object, _ io.ReaderAt, _ int64, out io.Writer) ([]string, error) {
			_, err := out.Write([]byte("cache"))
			return nil, err
		}}}
	})
	token := e.svc(t, e.owner, []string{Scope}, nil)
	ctx := context.Background()
	pool := e.h.deps.Pool
	sum, req := e.difReq(proguardFile, proguardName)
	e.difs(t, token, "appsite00010", map[string]map[string]any{sum: req})
	e.upload(t, token, "file", sum, proguardFile)
	if got := e.waitDIF(t, token, "appsite00010", sum, req); got["state"] != "ok" {
		t.Fatal(got)
	}
	var key string
	if err := pool.QueryRow(ctx, `select cache_key from app_debug_files limit 1`).Scan(&key); err != nil {
		t.Fatal(err)
	}
	raw := "difs/" + strconv.FormatInt(e.owner.OrgID, 10) + "/" + sum
	if !e.hasBlob(t, key) || !e.hasBlob(t, raw) {
		t.Fatal("expected raw and cache blobs")
	}
	if _, err := pool.Exec(ctx, `update orgs set deleted_at=now(), erased_at=now() where id=$1`, e.owner.OrgID); err != nil {
		t.Fatal(err)
	}
	if res, err := Sweep(ctx, pool, e.blobs, time.Now(), ChunkGrace); err != nil || res.DIFs != 1 {
		t.Fatalf("sweep = %+v, %v", res, err)
	}
	if e.hasBlob(t, key) || e.hasBlob(t, raw) {
		t.Fatal("blobs survived the erasure sweep")
	}
	var n int
	_ = pool.QueryRow(ctx, `select (select count(*) from app_debug_files) + (select count(*) from app_dif_uploads)`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d rows left", n)
	}
}

// Two uploads with the same debug id and different content keep separate
// caches: neither can replace or delete the other's.
func TestDifsSameDebugIDDifferentContentKeepsSeparateCaches(t *testing.T) {
	e := newEnv(t, func(d *Deps) {
		d.Builders = map[string]CacheBuilder{"proguard": {Version: 1, Build: func(_ context.Context, _ debugfile.Object, raw io.ReaderAt, size int64, out io.Writer) ([]string, error) {
			b := make([]byte, size)
			_, _ = raw.ReadAt(b, 0)
			_, err := out.Write(b)
			return nil, err
		}}}
	})
	token := e.svc(t, e.owner, []string{Scope}, nil)
	ctx := context.Background()
	v1 := proguardFile
	v2 := append(append([]byte{}, proguardFile...), []byte("    void fixed() -> b\n")...)
	for _, c := range []struct {
		project string
		data    []byte
	}{{"appsite00010", v1}, {"appsite00010", v2}, {"appsite00020", v2}} {
		sum, req := e.difReq(c.data, proguardName)
		e.difs(t, token, c.project, map[string]map[string]any{sum: req})
		e.upload(t, token, "file", sum, c.data)
		if got := e.waitDIF(t, token, c.project, sum, req); got["state"] != "ok" {
			t.Fatal(got)
		}
	}
	rows, err := e.h.deps.Pool.Query(ctx, `select site_id, cache_key from app_debug_files order by id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	keys := map[string]bool{}
	for rows.Next() {
		var site int32
		var key string
		if err := rows.Scan(&site, &key); err != nil {
			t.Fatal(err)
		}
		keys[key] = true
		if !e.hasBlob(t, key) {
			t.Fatalf("blob %s missing", key)
		}
	}
	if len(keys) != 2 {
		t.Fatalf("want one cache per distinct content, got %v", keys)
	}
}
