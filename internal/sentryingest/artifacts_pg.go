package sentryingest

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/sourcemap"
)

// PGArtifactStore is the Postgres ArtifactStore over app_artifact_files. A
// file is visible to a site when it belongs to the site's organization and its
// bundle covers the site (site_id equal) or the whole organization (NULL).
type PGArtifactStore struct{ pool *pgxpool.Pool }

// NewPGArtifactStore wraps a pool.
func NewPGArtifactStore(pool *pgxpool.Pool) *PGArtifactStore { return &PGArtifactStore{pool: pool} }

const visible = `f.org_id = (select org_id from sites where id = $1) and (f.site_id = $1 or f.site_id is null)`

// MapByDebugID returns the newest source map with the debug id: a source_map
// file carrying it, else the map the matching minified file points to.
func (s *PGArtifactStore) MapByDebugID(ctx context.Context, siteID int32, debugID string) (string, bool, error) {
	id := sourcemap.NormalizeDebugID(debugID)
	if id == "" {
		return "", false, nil
	}
	var key string
	err := s.pool.QueryRow(ctx, `select f.blob_key from app_artifact_files f
		where `+visible+` and f.debug_id = $2::uuid and f.type = 'source_map'
		order by f.bundle_id desc limit 1`, siteID, id).Scan(&key)
	if err == nil {
		return key, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", false, err
	}
	var bundle int64
	var ref, url string
	err = s.pool.QueryRow(ctx, `select f.bundle_id, f.sourcemap_ref, f.url from app_artifact_files f
		where `+visible+` and f.debug_id = $2::uuid and f.type = 'minified_source'
		order by f.bundle_id desc limit 1`, siteID, id).Scan(&bundle, &ref, &url)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return s.mapInBundle(ctx, siteID, bundle, ref, url)
}

// MapByURL returns the map of the newest upload for the release, dist and
// script name (a URL or "~/path").
func (s *PGArtifactStore) MapByURL(ctx context.Context, siteID int32, release, dist, name string) (string, bool, error) {
	var bundle int64
	var ref, url string
	err := s.pool.QueryRow(ctx, `select f.bundle_id, f.sourcemap_ref, f.url from app_artifact_files f
		where `+visible+` and f.release = $2 and f.dist = $3 and f.url = $4 and f.type in ('minified_source', 'source')
		order by f.bundle_id desc limit 1`, siteID, release, dist, name).Scan(&bundle, &ref, &url)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return s.mapInBundle(ctx, siteID, bundle, ref, url)
}

// MinifiedByDebugID returns the newest minified script with the debug id.
func (s *PGArtifactStore) MinifiedByDebugID(ctx context.Context, siteID int32, debugID string) (string, bool, error) {
	id := sourcemap.NormalizeDebugID(debugID)
	if id == "" {
		return "", false, nil
	}
	return s.minified(ctx, `select f.blob_key from app_artifact_files f
		where `+visible+` and f.debug_id = $2::uuid and f.type = 'minified_source'
		order by f.bundle_id desc limit 1`, siteID, id)
}

// MinifiedByURL returns the newest minified script for the release, dist and
// script name (a URL or "~/path").
func (s *PGArtifactStore) MinifiedByURL(ctx context.Context, siteID int32, release, dist, name string) (string, bool, error) {
	return s.minified(ctx, `select f.blob_key from app_artifact_files f
		where `+visible+` and f.release = $2 and f.dist = $3 and f.url = $4 and f.type = 'minified_source'
		order by f.bundle_id desc limit 1`, siteID, release, dist, name)
}

func (s *PGArtifactStore) minified(ctx context.Context, q string, args ...any) (string, bool, error) {
	var key string
	err := s.pool.QueryRow(ctx, q, args...).Scan(&key)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return key, true, nil
}

var _ ScopeSource = (*PGArtifactStore)(nil)

// mapInBundle finds the source_map file of a bundle that a script's
// sourceMappingURL reference (or "<url>.map") names.
func (s *PGArtifactStore) mapInBundle(ctx context.Context, siteID int32, bundle int64, ref, scriptURL string) (string, bool, error) {
	names := []string{scriptURL + ".map"}
	if ref != "" {
		base := ref
		if i := strings.LastIndexByte(scriptURL, '/'); i >= 0 && !strings.Contains(ref, "://") && !strings.HasPrefix(ref, "/") && !strings.HasPrefix(ref, "~") {
			base = scriptURL[:i+1] + ref
		}
		names = append([]string{base, ref}, names...)
	}
	var key string
	err := s.pool.QueryRow(ctx, `select f.blob_key from app_artifact_files f
		where `+visible+` and f.bundle_id = $2 and f.type = 'source_map' and f.url = any($3)
		order by array_position($3::text[], f.url) limit 1`, siteID, bundle, names).Scan(&key)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return key, true, nil
}
