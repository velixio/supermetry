package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type SharedLink struct {
	ID           int64
	SiteID       int32
	OrgID        int64
	SlugHash     []byte
	PasswordHash *string
	CreatedBy    int64
	CreatedAt    time.Time
	ExpiresAt    *time.Time
	RevokedAt    *time.Time
	Config       []byte
}

func CreateSharedLink(ctx context.Context, tx pgx.Tx, orgID int64, link SharedLink) (SharedLink, error) {
	if len(link.Config) == 0 {
		link.Config = []byte(`{}`)
	}
	err := tx.QueryRow(ctx, `
		insert into shared_links (site_id, slug_hash, password_hash, created_by, expires_at, config)
		select s.id, $3, $4, $5, $6, $7
		from sites s
		where s.id = $1 and s.org_id = $2 and s.deleted_at is null
		returning id, created_at, expires_at, config`,
		link.SiteID, orgID, link.SlugHash, link.PasswordHash, link.CreatedBy, link.ExpiresAt, link.Config,
	).Scan(&link.ID, &link.CreatedAt, &link.ExpiresAt, &link.Config)
	if err != nil {
		return SharedLink{}, fmt.Errorf("insert shared link: %w", err)
	}
	link.OrgID = orgID
	return link, nil
}

func SharedLinkBySlugHash(ctx context.Context, data *Store, slugHash []byte) (SharedLink, error) {
	var link SharedLink
	err := data.Pool.QueryRow(ctx, `
		select l.id, l.site_id, s.org_id, l.slug_hash, l.password_hash,
		       l.created_by, l.created_at, l.expires_at, l.revoked_at, l.config
		from shared_links l
		join sites s on s.id = l.site_id
		join orgs o on o.id = s.org_id
		where l.slug_hash = $1 and s.deleted_at is null and o.deleted_at is null`, slugHash).Scan(
		&link.ID, &link.SiteID, &link.OrgID, &link.SlugHash,
		&link.PasswordHash, &link.CreatedBy, &link.CreatedAt, &link.ExpiresAt, &link.RevokedAt, &link.Config,
	)
	if err != nil {
		return SharedLink{}, fmt.Errorf("query shared link: %w", err)
	}
	return link, nil
}

func SharedLinkByIDForUpdate(ctx context.Context, tx pgx.Tx, orgID, id int64) (SharedLink, error) {
	var link SharedLink
	err := tx.QueryRow(ctx, `
		select l.id, l.site_id, s.org_id, l.slug_hash, l.password_hash,
		       l.created_by, l.created_at, l.expires_at, l.revoked_at, l.config
		from shared_links l
		join sites s on s.id = l.site_id
		where l.id = $1 and s.org_id = $2 and s.deleted_at is null
		for update of l`, id, orgID).Scan(
		&link.ID, &link.SiteID, &link.OrgID, &link.SlugHash,
		&link.PasswordHash, &link.CreatedBy, &link.CreatedAt, &link.ExpiresAt, &link.RevokedAt, &link.Config,
	)
	if err != nil {
		return SharedLink{}, fmt.Errorf("query shared link by ID: %w", err)
	}
	return link, nil
}

func ChangeSharedLinkExpiry(ctx context.Context, tx pgx.Tx, orgID, id int64, expiresAt *time.Time) error {
	command, err := tx.Exec(ctx, `
		update shared_links l
		set expires_at = $3
		from sites s
		where l.id = $1 and l.site_id = s.id and s.org_id = $2
		  and s.deleted_at is null`, id, orgID, expiresAt)
	if err != nil {
		return fmt.Errorf("change shared link expiry: %w", err)
	}
	if command.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func RevokeSharedLink(ctx context.Context, tx pgx.Tx, orgID, id int64) error {
	command, err := tx.Exec(ctx, `
		update shared_links l
		set revoked_at = now()
		from sites s
		where l.id = $1 and l.site_id = s.id and s.org_id = $2
		  and s.deleted_at is null and l.revoked_at is null`, id, orgID)
	if err != nil {
		return fmt.Errorf("revoke shared link: %w", err)
	}
	if command.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}
