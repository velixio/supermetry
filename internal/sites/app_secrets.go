package sites

import (
	"context"
	"crypto/rand"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const hashSecretBytes = 32

// InsertAppHashSecret stores a fresh random secret for an app site inside the
// caller's transaction. An existing secret is kept. pgcrypto is not enabled
// and random() is not a CSPRNG, so the bytes come from crypto/rand.
func InsertAppHashSecret(ctx context.Context, tx pgx.Tx, siteID int32) error {
	secret := make([]byte, hashSecretBytes)
	if _, err := rand.Read(secret); err != nil {
		return fmt.Errorf("generate app hash secret: %w", err)
	}
	if _, err := tx.Exec(ctx, `insert into app_hash_secrets(site_id, secret) values ($1, $2) on conflict (site_id) do nothing`, siteID, secret); err != nil {
		return fmt.Errorf("insert app hash secret: %w", err)
	}
	return nil
}

// EnsureAppHashSecrets gives every live app site without a secret one and
// returns how many it created. It runs before the first registry load, so
// every app site in the registry carries App.HashSecret.
func EnsureAppHashSecrets(ctx context.Context, pool *pgxpool.Pool) (created int, err error) {
	rows, err := pool.Query(ctx, `select s.id from sites s
		where s.kind = 'app' and s.deleted_at is null
		  and not exists (select 1 from app_hash_secrets h where h.site_id = s.id)`)
	if err != nil {
		return 0, fmt.Errorf("list app sites without secret: %w", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int32])
	if err != nil {
		return 0, fmt.Errorf("scan app sites without secret: %w", err)
	}
	for _, id := range ids {
		secret := make([]byte, hashSecretBytes)
		if _, err := rand.Read(secret); err != nil {
			return created, fmt.Errorf("generate app hash secret: %w", err)
		}
		tag, err := pool.Exec(ctx, `insert into app_hash_secrets(site_id, secret) values ($1, $2) on conflict (site_id) do nothing`, id, secret)
		if err != nil {
			return created, fmt.Errorf("insert app hash secret: %w", err)
		}
		created += int(tag.RowsAffected())
	}
	return created, nil
}
