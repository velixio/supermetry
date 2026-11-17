package sentryingest

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/debugfile"
)

// CacheFor returns the cache of the newest finished debug file of the kind and
// debug id that the site may use. The rows visible to a site are its own
// (site_id equal) and the organization-wide ones (site_id NULL, from uploads
// that name no project); another project's upload is never served, so a
// project cannot replace what a sibling project symbolicates with, even when
// both upload the same debug id. Within that scope a newer upload wins, which
// is how a corrected re-upload takes effect. The query shape matches
// app_debug_files_lookup (org_id, kind, debug_id, id DESC).
func (s *PGArtifactStore) CacheFor(ctx context.Context, siteID int32, kind, debugID string) (string, bool, error) {
	id := debugfile.NormalizeDebugID(debugID)
	if id == "" {
		return "", false, nil
	}
	var key string
	err := s.pool.QueryRow(ctx, `select f.cache_key from app_debug_files f
		where `+visible+` and f.kind = $2 and f.debug_id = $3 and f.cache_state = 'ok' and f.cache_key is not null
		order by f.id desc limit 1`, siteID, kind, id).Scan(&key)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return key, true, nil
}

var _ DebugFileStore = (*PGArtifactStore)(nil)
