package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type Site struct {
	ID             int32
	OrgID          int64
	PublicID       string
	Name           string
	AllowedHosts   []string
	Timezone       string
	RetentionClass int16
	CreatedAt      time.Time
	DeletedAt      *time.Time
}

func CreateSite(ctx context.Context, tx pgx.Tx, site Site) (Site, error) {
	err := tx.QueryRow(ctx, `
		insert into sites (org_id, public_id, name, allowed_hosts, timezone, retention_class)
		values ($1, $2, $3, $4, $5, $6)
		on conflict (public_id) do nothing
		returning id, created_at`,
		site.OrgID, site.PublicID, site.Name, site.AllowedHosts, site.Timezone, site.RetentionClass,
	).Scan(&site.ID, &site.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Site{}, pgx.ErrNoRows
		}
		return Site{}, fmt.Errorf("insert site: %w", err)
	}
	return site, nil
}

func SiteForUpdate(ctx context.Context, tx pgx.Tx, orgID int64, id int32) (Site, error) {
	row := tx.QueryRow(ctx, `
		select id, org_id, public_id, name, allowed_hosts, timezone, retention_class, created_at, deleted_at
		from sites
		where id = $1 and org_id = $2 and deleted_at is null
		for update`, id, orgID)
	return scanSite(row)
}

func UpdateSite(ctx context.Context, tx pgx.Tx, site Site) (Site, error) {
	err := tx.QueryRow(ctx, `
		update sites
		set name = $3, allowed_hosts = $4, timezone = $5, retention_class = $6
		where id = $1 and org_id = $2 and deleted_at is null
		returning id, org_id, public_id, name, allowed_hosts, timezone, retention_class, created_at, deleted_at`,
		site.ID, site.OrgID, site.Name, site.AllowedHosts, site.Timezone, site.RetentionClass,
	).Scan(
		&site.ID, &site.OrgID, &site.PublicID, &site.Name, &site.AllowedHosts,
		&site.Timezone, &site.RetentionClass, &site.CreatedAt, &site.DeletedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Site{}, pgx.ErrNoRows
		}
		return Site{}, fmt.Errorf("update site: %w", err)
	}
	return site, nil
}

func SoftDeleteSite(ctx context.Context, tx pgx.Tx, orgID int64, id int32) error {
	command, err := tx.Exec(ctx, `
		update sites
		set deleted_at = now()
		where id = $1 and org_id = $2 and deleted_at is null`, id, orgID)
	if err != nil {
		return fmt.Errorf("soft-delete site: %w", err)
	}
	if command.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func ActiveSites(ctx context.Context, data *Store) ([]Site, error) {
	rows, err := data.Pool.Query(ctx, `
		select id, org_id, public_id, name, allowed_hosts, timezone, retention_class, created_at, deleted_at
		from sites
		where deleted_at is null
		order by id`)
	if err != nil {
		return nil, fmt.Errorf("query active sites: %w", err)
	}
	defer rows.Close()
	return scanSites(rows)
}

func ListSites(ctx context.Context, data *Store, orgID int64, siteIDs []int32, after int32, limit int) ([]Site, error) {
	rows, err := data.Pool.Query(ctx, `
		select id, org_id, public_id, name, allowed_hosts, timezone, retention_class, created_at, deleted_at
		from sites
		where org_id = $1 and deleted_at is null and id > $2
		  and ($3::integer[] is null or id = any($3))
		order by id
		limit $4`, orgID, after, siteIDs, limit)
	if err != nil {
		return nil, fmt.Errorf("list sites: %w", err)
	}
	defer rows.Close()
	return scanSites(rows)
}

type siteScanner interface {
	Scan(dest ...any) error
}

func scanSite(row siteScanner) (Site, error) {
	var site Site
	if err := row.Scan(
		&site.ID, &site.OrgID, &site.PublicID, &site.Name, &site.AllowedHosts,
		&site.Timezone, &site.RetentionClass, &site.CreatedAt, &site.DeletedAt,
	); err != nil {
		return Site{}, err
	}
	return site, nil
}

func scanSites(rows pgx.Rows) ([]Site, error) {
	sites := make([]Site, 0)
	for rows.Next() {
		site, err := scanSite(rows)
		if err != nil {
			return nil, fmt.Errorf("scan site: %w", err)
		}
		sites = append(sites, site)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sites: %w", err)
	}
	return sites, nil
}
