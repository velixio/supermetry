package sentryapi

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/replay"
)

// ChunkGrace is how long an unreferenced chunk blob is kept. An upload stores
// chunks before the assemble call that references them, so a younger blob may
// belong to an upload in flight.
const ChunkGrace = 24 * time.Hour

// SweepResult counts what one sweep removed.
type SweepResult struct {
	Bundles     int
	DIFs        int
	Chunks      int
	ChunkBytes  int64
	BundleBytes int64
}

// Sweep removes artifact data nothing can reach any more:
//
//  1. bundles whose organization was erased, or whose projects were all
//     erased, with their file index rows (by cascade) and per-file blobs;
//  2. debug file uploads in the same situation, with their raw and cache blobs;
//  3. chunk blobs that no bundle or debug file row lists and that are older
//     than grace.
//
// A bundle row keeps its chunks referenced from the moment the assemble call
// creates it, so created and assembling bundles protect their chunks, and the
// grace period covers chunks uploaded before that call. It is safe to run on
// several instances at once: deletes are idempotent.
func Sweep(ctx context.Context, pool *pgxpool.Pool, blobs replay.BlobStore, now time.Time, grace time.Duration) (SweepResult, error) {
	var res SweepResult
	if blobs == nil {
		return res, nil
	}
	if err := sweepBundles(ctx, pool, blobs, &res); err != nil {
		return res, err
	}
	if err := sweepDIFs(ctx, pool, blobs, &res); err != nil {
		return res, err
	}
	if err := sweepChunks(ctx, pool, blobs, now, grace, &res); err != nil {
		return res, err
	}
	if res.Bundles > 0 || res.DIFs > 0 || res.Chunks > 0 {
		slog.Info("sentryapi: swept artifact storage", "bundles", res.Bundles, "debug_files", res.DIFs, "chunks", res.Chunks, "chunk_bytes", res.ChunkBytes)
	}
	return res, nil
}

func sweepBundles(ctx context.Context, pool *pgxpool.Pool, blobs replay.BlobStore, res *SweepResult) error {
	rows, err := pool.Query(ctx, `
		select b.id, b.org_id, b.checksum, b.size
		from app_artifact_bundles b
		where b.state in ('ok','error')
		  and (
		    exists (select 1 from orgs o where o.id = b.org_id and o.erased_at is not null)
		    or (cardinality(b.site_ids) > 0
		        and not exists (select 1 from sites s where s.id = any(b.site_ids) and s.erased_at is null))
		  )
		order by b.id
		limit 1000`)
	if err != nil {
		return fmt.Errorf("list orphan bundles: %w", err)
	}
	type orphan struct {
		id, org, size int64
		checksum      string
	}
	var orphans []orphan
	for rows.Next() {
		var o orphan
		if err := rows.Scan(&o.id, &o.org, &o.checksum, &o.size); err != nil {
			rows.Close()
			return fmt.Errorf("scan orphan bundle: %w", err)
		}
		orphans = append(orphans, o)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("list orphan bundles: %w", err)
	}
	for _, o := range orphans {
		if _, err := blobs.DeletePrefix(ctx, fmt.Sprintf("bundles/%d/%s/", o.org, o.checksum)); err != nil {
			return fmt.Errorf("delete bundle blobs %d: %w", o.id, err)
		}
		if _, err := pool.Exec(ctx, `delete from app_artifact_bundles where id=$1`, o.id); err != nil {
			return fmt.Errorf("delete bundle %d: %w", o.id, err)
		}
		res.Bundles++
		res.BundleBytes += o.size
	}
	return nil
}

// sweepDIFs removes debug file uploads whose organization was erased or whose
// projects were all erased: the raw blob, each cache blob no other upload
// still uses, and the rows (the object rows go by cascade).
func sweepDIFs(ctx context.Context, pool *pgxpool.Pool, blobs replay.BlobStore, res *SweepResult) error {
	rows, err := pool.Query(ctx, `
		select u.id, u.org_id, u.checksum, u.size
		from app_dif_uploads u
		where u.state in ('ok','error')
		  and (
		    exists (select 1 from orgs o where o.id = u.org_id and o.erased_at is not null)
		    or (cardinality(u.site_ids) > 0
		        and not exists (select 1 from sites s where s.id = any(u.site_ids) and s.erased_at is null))
		  )
		order by u.id
		limit 1000`)
	if err != nil {
		return fmt.Errorf("list orphan debug files: %w", err)
	}
	type orphan struct {
		id, org, size int64
		checksum      string
	}
	var orphans []orphan
	for rows.Next() {
		var o orphan
		if err := rows.Scan(&o.id, &o.org, &o.checksum, &o.size); err != nil {
			rows.Close()
			return fmt.Errorf("scan orphan debug file: %w", err)
		}
		orphans = append(orphans, o)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("list orphan debug files: %w", err)
	}
	for _, o := range orphans {
		keys, err := difCacheKeys(ctx, pool, o.id)
		if err != nil {
			return err
		}
		for _, key := range keys {
			if _, err := blobs.Delete(ctx, key); err != nil {
				return fmt.Errorf("delete debug file cache %d: %w", o.id, err)
			}
		}
		if _, err := blobs.Delete(ctx, fmt.Sprintf("difs/%d/%s", o.org, o.checksum)); err != nil {
			return fmt.Errorf("delete debug file %d: %w", o.id, err)
		}
		if _, err := pool.Exec(ctx, `delete from app_dif_uploads where id=$1`, o.id); err != nil {
			return fmt.Errorf("delete debug file upload %d: %w", o.id, err)
		}
		res.DIFs++
		res.BundleBytes += o.size
	}
	return nil
}

// difCacheKeys lists the cache blobs only this upload references: two uploads
// of the same debug id share one blob.
func difCacheKeys(ctx context.Context, pool *pgxpool.Pool, uploadID int64) ([]string, error) {
	rows, err := pool.Query(ctx, `
		select distinct f.cache_key from app_debug_files f
		where f.upload_id=$1 and f.cache_key is not null
		  and not exists (select 1 from app_debug_files g where g.cache_key=f.cache_key and g.upload_id<>$1)`, uploadID)
	if err != nil {
		return nil, fmt.Errorf("list debug file caches: %w", err)
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, fmt.Errorf("scan debug file cache: %w", err)
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

func sweepChunks(ctx context.Context, pool *pgxpool.Pool, blobs replay.BlobStore, now time.Time, grace time.Duration, res *SweepResult) error {
	cutoff := now.Add(-grace)
	type candidate struct {
		sha  string
		size int64
	}
	var old []candidate
	visit := func(b replay.BlobInfo) error {
		sha, ok := strings.CutPrefix(b.Key, "chunks/")
		if ok && validSHA1(sha) && b.Modified.Before(cutoff) {
			old = append(old, candidate{sha, b.Size})
		}
		return nil
	}
	if w, ok := blobs.(replay.BlobWalker); ok {
		if err := w.Walk(ctx, "chunks/", visit); err != nil {
			return fmt.Errorf("walk chunks: %w", err)
		}
	} else {
		list, err := blobs.List(ctx, "chunks/")
		if err != nil {
			return fmt.Errorf("list chunks: %w", err)
		}
		for _, b := range list {
			_ = visit(b)
		}
	}
	if len(old) == 0 {
		return nil
	}
	// Read the references after the listing, so a bundle created while the
	// store was being listed still protects its chunks.
	refs := map[string]struct{}{}
	rows, err := pool.Query(ctx, `select unnest(chunks) from app_artifact_bundles union select unnest(chunks) from app_dif_uploads`)
	if err != nil {
		return fmt.Errorf("list chunk references: %w", err)
	}
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			rows.Close()
			return fmt.Errorf("scan chunk reference: %w", err)
		}
		refs[c] = struct{}{}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("list chunk references: %w", err)
	}
	for _, c := range old {
		if _, held := refs[c.sha]; held {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := blobs.Delete(ctx, chunkKey(c.sha)); err != nil {
			return fmt.Errorf("delete chunk: %w", err)
		}
		res.Chunks++
		res.ChunkBytes += c.size
	}
	return nil
}
