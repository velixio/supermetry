package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// AppKeyGrace is how long a replaced ingest key keeps working.
const AppKeyGrace = 7 * 24 * time.Hour

// AppKeyRow is an ingest key as stored; Hash and SiteID are set only by ActiveAppKeys.
type AppKeyRow struct {
	SiteID    int32
	Prefix    string
	Hash      [32]byte
	CreatedAt time.Time
	ExpiresAt *time.Time
}

// RotateAppKey adds a new current key for a site. Keys past their grace are
// deleted and the previous current key gets a grace period. It returns the end
// of that grace, or nil when no key was replaced.
func (s *Store) RotateAppKey(ctx context.Context, siteID int32, createdBy *int64, prefix string, hash [32]byte, now time.Time) (*time.Time, error) {
	var previous *time.Time
	err := s.Tx(ctx, func(tx pgx.Tx) (err error) {
		previous, err = s.RotateAppKeyTx(ctx, tx, siteID, createdBy, prefix, hash, now)
		return err
	})
	if err != nil {
		return nil, err
	}
	return previous, nil
}

// RotateAppKeyTx is RotateAppKey inside the caller's transaction, so the caller
// can write an audit row atomically.
func (s *Store) RotateAppKeyTx(ctx context.Context, tx pgx.Tx, siteID int32, createdBy *int64, prefix string, hash [32]byte, now time.Time) (*time.Time, error) {
	// Serialise rotations of one site so only one key stays current.
	var locked int
	if err := tx.QueryRow(ctx, `select 1 from sites where id = $1 for update`, siteID).Scan(&locked); err != nil {
		return nil, fmt.Errorf("lock site for key rotation: %w", err)
	}
	if _, err := tx.Exec(ctx, `delete from app_ingest_keys where site_id = $1 and expires_at <= $2`, siteID, now); err != nil {
		return nil, fmt.Errorf("delete expired ingest keys: %w", err)
	}
	grace := now.Add(AppKeyGrace)
	tag, err := tx.Exec(ctx, `update app_ingest_keys set expires_at = $2 where site_id = $1 and expires_at is null`, siteID, grace)
	if err != nil {
		return nil, fmt.Errorf("demote ingest key: %w", err)
	}
	if _, err := tx.Exec(ctx, `insert into app_ingest_keys(site_id, prefix, hash, created_by, created_at) values ($1,$2,$3,$4,$5)`,
		siteID, prefix, hash[:], createdBy, now); err != nil {
		return nil, fmt.Errorf("insert ingest key: %w", err)
	}
	if tag.RowsAffected() > 0 {
		return &grace, nil
	}
	return nil, nil
}

// ListAppKeys returns a site's unexpired keys, newest first.
func (s *Store) ListAppKeys(ctx context.Context, siteID int32) ([]AppKeyRow, error) {
	rows, err := s.Pool.Query(ctx, `select prefix, created_at, expires_at from app_ingest_keys
		where site_id = $1 and (expires_at is null or expires_at > now()) order by created_at desc, id desc`, siteID)
	if err != nil {
		return nil, fmt.Errorf("list ingest keys: %w", err)
	}
	defer rows.Close()
	var out []AppKeyRow
	for rows.Next() {
		row := AppKeyRow{SiteID: siteID}
		if err := rows.Scan(&row.Prefix, &row.CreatedAt, &row.ExpiresAt); err != nil {
			return nil, fmt.Errorf("scan ingest key: %w", err)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// ActiveAppKeys returns every unexpired key of every site, for the registry.
func (s *Store) ActiveAppKeys(ctx context.Context) ([]AppKeyRow, error) {
	rows, err := s.Pool.Query(ctx, `select site_id, prefix, hash, created_at, expires_at from app_ingest_keys
		where expires_at is null or expires_at > now()`)
	if err != nil {
		return nil, fmt.Errorf("load ingest keys: %w", err)
	}
	defer rows.Close()
	var out []AppKeyRow
	for rows.Next() {
		var row AppKeyRow
		var hash []byte
		if err := rows.Scan(&row.SiteID, &row.Prefix, &hash, &row.CreatedAt, &row.ExpiresAt); err != nil {
			return nil, fmt.Errorf("scan ingest key: %w", err)
		}
		copy(row.Hash[:], hash)
		out = append(out, row)
	}
	return out, rows.Err()
}
