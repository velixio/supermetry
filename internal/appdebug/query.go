package appdebug

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/replay"
)

// Row limits keep the Symbols page bounded for an organization with many
// debug files.
const (
	maxFiles   = 500
	maxMissing = 200
)

// ErrNotFound reports a debug file the project cannot see.
var ErrNotFound = errors.New("debug file not found")

// File is one uploaded debug object a project can use.
type File struct {
	ID         int64     `json:"id"`
	Kind       string    `json:"kind"`
	DebugID    string    `json:"debug_id"`
	CodeID     string    `json:"code_id"`
	Arch       string    `json:"arch"`
	ObjectName string    `json:"object_name"`
	Features   []string  `json:"features"`
	CacheState string    `json:"cache_state"`
	Size       int64     `json:"size"`
	CreatedAt  time.Time `json:"created_at"`
}

// Missing is a debug image events needed and no upload covered.
type Missing struct {
	Kind     string    `json:"kind"`
	DebugID  string    `json:"debug_id"`
	CodeFile string    `json:"code_file"`
	Arch     string    `json:"arch"`
	LastSeen time.Time `json:"last_seen"`
	Events   int64     `json:"events"`
}

// Listing is the Symbols settings page's data.
type Listing struct {
	Files   []File    `json:"files"`
	Missing []Missing `json:"missing"`
}

// Store reads and deletes the debug files of a project.
type Store struct {
	pool  *pgxpool.Pool
	blobs replay.BlobStore
}

// NewStore returns a Store; blobs may be nil, then deletes keep the blobs for
// the sweeper (which removes what no row references).
func NewStore(pool *pgxpool.Pool, blobs replay.BlobStore) *Store {
	return &Store{pool: pool, blobs: blobs}
}

// List returns the project's debug files (its own and its organization's
// org-wide ones), newest first, and the images still missing.
func (s *Store) List(ctx context.Context, orgID int64, siteID int32) (Listing, error) {
	out := Listing{Files: []File{}, Missing: []Missing{}}
	rows, err := s.pool.Query(ctx, `
select f.id, f.kind, f.debug_id, f.code_id, f.arch, f.object_name, f.features, f.cache_state, u.size, f.created_at
from app_debug_files f join app_dif_uploads u on u.id = f.upload_id
where f.org_id = $1 and (f.site_id = $2 or f.site_id is null)
order by f.id desc limit $3`, orgID, siteID, maxFiles)
	if err != nil {
		return out, fmt.Errorf("list debug files: %w", err)
	}
	for rows.Next() {
		var f File
		if err := rows.Scan(&f.ID, &f.Kind, &f.DebugID, &f.CodeID, &f.Arch, &f.ObjectName, &f.Features, &f.CacheState, &f.Size, &f.CreatedAt); err != nil {
			rows.Close()
			return out, fmt.Errorf("scan debug file: %w", err)
		}
		if f.Features == nil {
			f.Features = []string{}
		}
		out.Files = append(out.Files, f)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, fmt.Errorf("list debug files: %w", err)
	}
	mrows, err := s.pool.Query(ctx, `
select kind, debug_id, code_file, arch, last_seen, events
from app_missing_debug_files where site_id = $1
order by last_seen desc, debug_id limit $2`, siteID, maxMissing)
	if err != nil {
		return out, fmt.Errorf("list missing debug files: %w", err)
	}
	defer mrows.Close()
	for mrows.Next() {
		var m Missing
		if err := mrows.Scan(&m.Kind, &m.DebugID, &m.CodeFile, &m.Arch, &m.LastSeen, &m.Events); err != nil {
			return out, fmt.Errorf("scan missing debug file: %w", err)
		}
		out.Missing = append(out.Missing, m)
	}
	return out, mrows.Err()
}

// Delete removes one debug file row of the project. An organization-wide row
// (no project) is shared by every project of the organization, so it goes only
// when allowOrgWide is set, which the caller grants after an organization-level
// permission check. When no other row refers to its upload the upload and its
// raw blob go too; a cache blob goes when no other row uses it. ErrNotFound
// means no such file the caller may delete.
func (s *Store) Delete(ctx context.Context, orgID int64, siteID int32, id int64, allowOrgWide bool) error {
	var uploadID int64
	var cacheKey *string
	var checksum string
	var otherRows int
	var cacheShared bool
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
delete from app_debug_files f where f.id = $1 and f.org_id = $2 and (f.site_id = $3 or ($4 and f.site_id is null))
returning f.upload_id, f.cache_key`, id, orgID, siteID, allowOrgWide).Scan(&uploadID, &cacheKey)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `select count(*) from app_debug_files where upload_id = $1`, uploadID).Scan(&otherRows); err != nil {
			return err
		}
		if cacheKey != nil {
			if err := tx.QueryRow(ctx, `select exists(select 1 from app_debug_files where cache_key = $1)`, *cacheKey).Scan(&cacheShared); err != nil {
				return err
			}
		}
		if otherRows == 0 {
			return tx.QueryRow(ctx, `delete from app_dif_uploads where id = $1 returning checksum`, uploadID).Scan(&checksum)
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return err
		}
		return fmt.Errorf("delete debug file: %w", err)
	}
	if s.blobs == nil {
		return nil
	}
	// Best effort after commit: a failure leaves an orphan the sweeper owns.
	if cacheKey != nil && !cacheShared {
		_, _ = s.blobs.Delete(ctx, *cacheKey)
	}
	if otherRows == 0 && checksum != "" {
		_, _ = s.blobs.Delete(ctx, fmt.Sprintf("difs/%d/%s", orgID, checksum))
	}
	return nil
}
