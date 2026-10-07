package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type APIKey struct {
	ID         int64
	OrgID      int64
	Name       string
	Prefix     string
	Hash       []byte
	Scopes     []string
	SiteIDs    []int32
	CreatedBy  int64
	CreatedAt  time.Time
	LastUsedAt *time.Time
	ExpiresAt  *time.Time
	RevokedAt  *time.Time
}

func CreateAPIKey(ctx context.Context, tx pgx.Tx, key APIKey) (APIKey, error) {
	err := tx.QueryRow(ctx, `
		insert into api_keys (org_id, name, prefix, hash, scopes, site_ids, created_by, expires_at)
		values ($1, $2, $3, $4, $5, $6, $7, $8)
		returning id, created_at`,
		key.OrgID, key.Name, key.Prefix, key.Hash, key.Scopes, key.SiteIDs,
		key.CreatedBy, key.ExpiresAt,
	).Scan(&key.ID, &key.CreatedAt)
	if err != nil {
		return APIKey{}, fmt.Errorf("insert API key: %w", err)
	}
	return key, nil
}

func APIKeysByPrefix(ctx context.Context, data *Store, prefix string) ([]APIKey, error) {
	rows, err := data.Pool.Query(ctx, `
		select id, org_id, name, prefix, hash, scopes, site_ids, created_by,
		       created_at, last_used_at, expires_at, revoked_at
		from api_keys
		where prefix = $1`, prefix)
	if err != nil {
		return nil, fmt.Errorf("query API key prefix: %w", err)
	}
	defer rows.Close()

	keys := make([]APIKey, 0, 1)
	for rows.Next() {
		key, err := scanAPIKey(rows)
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate API key prefix: %w", err)
	}
	return keys, nil
}

func ListAPIKeys(ctx context.Context, data *Store, orgID int64) ([]APIKey, error) {
	rows, err := data.Pool.Query(ctx, `
		select id, org_id, name, prefix, hash, scopes, site_ids, created_by,
		       created_at, last_used_at, expires_at, revoked_at
		from api_keys
		where org_id = $1
		order by created_at desc, id desc`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list API keys: %w", err)
	}
	defer rows.Close()

	keys := make([]APIKey, 0)
	for rows.Next() {
		key, err := scanAPIKey(rows)
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate API keys: %w", err)
	}
	return keys, nil
}

func RevokeAPIKey(ctx context.Context, tx pgx.Tx, orgID, id int64) error {
	command, err := tx.Exec(ctx, `
		update api_keys
		set revoked_at = now()
		where id = $1 and org_id = $2 and revoked_at is null`, id, orgID)
	if err != nil {
		return fmt.Errorf("revoke API key: %w", err)
	}
	if command.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func TouchAPIKeys(ctx context.Context, data *Store, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := data.Pool.Exec(ctx, `
		update api_keys
		set last_used_at = now()
		where id = any($1)
		  and (last_used_at is null or last_used_at <= now() - interval '1 minute')`, ids)
	if err != nil {
		return fmt.Errorf("update API key last use: %w", err)
	}
	return nil
}

type apiKeyScanner interface {
	Scan(dest ...any) error
}

func scanAPIKey(row apiKeyScanner) (APIKey, error) {
	var key APIKey
	if err := row.Scan(
		&key.ID, &key.OrgID, &key.Name, &key.Prefix, &key.Hash,
		&key.Scopes, &key.SiteIDs, &key.CreatedBy, &key.CreatedAt,
		&key.LastUsedAt, &key.ExpiresAt, &key.RevokedAt,
	); err != nil {
		return APIKey{}, fmt.Errorf("scan API key: %w", err)
	}
	return key, nil
}
