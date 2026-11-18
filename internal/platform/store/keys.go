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
	// LastUsedCountry and LastUsedClient say where and with what the key was
	// last used: a two-letter country and a coarse client family. Neither an IP
	// address nor a raw User-Agent is ever stored.
	LastUsedCountry *string
	LastUsedClient  *string
	// RolledFromID is the key this one replaced, when it was made by a roll.
	RolledFromID *int64
}

func CreateAPIKey(ctx context.Context, tx pgx.Tx, key APIKey) (APIKey, error) {
	err := tx.QueryRow(ctx, `
		insert into api_keys (org_id, name, prefix, hash, scopes, site_ids, created_by, expires_at, rolled_from_id)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		returning id, created_at`,
		key.OrgID, key.Name, key.Prefix, key.Hash, key.Scopes, key.SiteIDs,
		key.CreatedBy, key.ExpiresAt, key.RolledFromID,
	).Scan(&key.ID, &key.CreatedAt)
	if err != nil {
		return APIKey{}, fmt.Errorf("insert API key: %w", err)
	}
	return key, nil
}

func APIKeysByPrefix(ctx context.Context, data *Store, prefix string) ([]APIKey, error) {
	rows, err := data.Pool.Query(ctx, `
		select id, org_id, name, prefix, hash, scopes, site_ids, created_by,
		       created_at, last_used_at, expires_at, revoked_at,
		       last_used_country, last_used_client, rolled_from_id
		from api_keys
		where prefix = $1 and exists (select 1 from orgs o where o.id=api_keys.org_id and o.deleted_at is null)`, prefix)
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

// APIKeyForDelegation locks and returns an active key while it creates a
// narrower child key. The lock keeps a concurrent revocation from committing
// between authorization and child creation.
func APIKeyForDelegation(ctx context.Context, tx pgx.Tx, orgID, id int64) (APIKey, error) {
	row := tx.QueryRow(ctx, `
		select id, org_id, name, prefix, hash, scopes, site_ids, created_by,
		       created_at, last_used_at, expires_at, revoked_at,
		       last_used_country, last_used_client, rolled_from_id
		from api_keys
		where id = $1 and org_id = $2 and revoked_at is null
		  and (expires_at is null or expires_at > now())
		for share`, id, orgID)
	key, err := scanAPIKey(row)
	if err != nil {
		return APIKey{}, err
	}
	return key, nil
}

func ListAPIKeys(ctx context.Context, data *Store, orgID int64) ([]APIKey, error) {
	rows, err := data.Pool.Query(ctx, `
		select id, org_id, name, prefix, hash, scopes, site_ids, created_by,
		       created_at, last_used_at, expires_at, revoked_at,
		       last_used_country, last_used_client, rolled_from_id
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

// APIKeyUse is one batched last-use record. Country is a two-letter code or
// empty; Client is a coarse family from keys.ClientFamily.
type APIKeyUse struct {
	ID      int64
	Country string
	Client  string
}

// TouchAPIKeys stamps last use for a batch. A key is written at most once a
// minute, however often it is used.
func TouchAPIKeys(ctx context.Context, data *Store, uses []APIKeyUse) error {
	if len(uses) == 0 {
		return nil
	}
	ids := make([]int64, len(uses))
	countries := make([]string, len(uses))
	clients := make([]string, len(uses))
	for i, use := range uses {
		ids[i], countries[i], clients[i] = use.ID, use.Country, use.Client
	}
	_, err := data.Pool.Exec(ctx, `
		update api_keys k
		set last_used_at = now(),
		    last_used_country = coalesce(nullif(u.country, ''), k.last_used_country),
		    last_used_client = coalesce(nullif(u.client, ''), k.last_used_client)
		from unnest($1::bigint[], $2::text[], $3::text[]) as u(id, country, client)
		where k.id = u.id
		  and (k.last_used_at is null or k.last_used_at <= now() - interval '1 minute')`, ids, countries, clients)
	if err != nil {
		return fmt.Errorf("update API key last use: %w", err)
	}
	return nil
}

// APIKeyForRoll locks a key for rolling, whatever its state: an expired key
// can still be rolled, a revoked one cannot.
func APIKeyForRoll(ctx context.Context, tx pgx.Tx, orgID, id int64) (APIKey, error) {
	row := tx.QueryRow(ctx, `
		select `+apiKeyColumns+`
		from api_keys
		where id = $1 and org_id = $2 and revoked_at is null
		for update`, id, orgID)
	return scanAPIKey(row)
}

// SetAPIKeyExpiry sets a key's expiry.
func SetAPIKeyExpiry(ctx context.Context, tx pgx.Tx, orgID, id int64, expires time.Time) error {
	command, err := tx.Exec(ctx, `update api_keys set expires_at = $3 where id = $1 and org_id = $2`, id, orgID, expires)
	if err != nil {
		return fmt.Errorf("set API key expiry: %w", err)
	}
	if command.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

const apiKeyColumns = `id, org_id, name, prefix, hash, scopes, site_ids, created_by,
		created_at, last_used_at, expires_at, revoked_at,
		last_used_country, last_used_client, rolled_from_id`

type apiKeyScanner interface {
	Scan(dest ...any) error
}

func scanAPIKey(row apiKeyScanner) (APIKey, error) {
	var key APIKey
	if err := row.Scan(
		&key.ID, &key.OrgID, &key.Name, &key.Prefix, &key.Hash,
		&key.Scopes, &key.SiteIDs, &key.CreatedBy, &key.CreatedAt,
		&key.LastUsedAt, &key.ExpiresAt, &key.RevokedAt,
		&key.LastUsedCountry, &key.LastUsedClient, &key.RolledFromID,
	); err != nil {
		return APIKey{}, fmt.Errorf("scan API key: %w", err)
	}
	return key, nil
}
