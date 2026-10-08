package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type Site struct {
	ID               int32
	OrgID            int64
	PublicID         string
	Name             string
	AllowedHosts     []string
	Timezone         string
	RetentionClass   int16
	ExcludeIPs       []string
	ExcludePaths     []string
	ExcludeCountries []string
	Tracker          []byte
	ProfilesEnabled  bool
	HeatmapsEnabled  bool
	IconURL          string
	IconCheckedAt    string
	CreatedAt        time.Time
	DeletedAt        *time.Time
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
		select id, org_id, public_id, name, allowed_hosts, timezone, retention_class,
		       coalesce(settings->'exclude_ips', '[]'::jsonb),
		       coalesce(settings->'exclude_paths', '[]'::jsonb),
		       coalesce(settings->'exclude_countries', '[]'::jsonb),
		       coalesce(settings->'tracker', '{}'::jsonb),
		       coalesce(settings->'profiles_enabled' = 'true'::jsonb, false),
		       coalesce(settings->'heatmaps_enabled' = 'true'::jsonb, false),
		       coalesce(settings->>'icon_url', ''), coalesce(settings->>'icon_checked_at', ''), created_at, deleted_at
		from sites
		where id = $1 and org_id = $2 and deleted_at is null
		for update`, id, orgID)
	return scanSite(row)
}

func UpdateSite(ctx context.Context, tx pgx.Tx, site Site) (Site, error) {
	err := tx.QueryRow(ctx, `
		update sites
		set name = $3, allowed_hosts = $4, timezone = $5, retention_class = $6,
		    settings = jsonb_set(
		        jsonb_set(
		            jsonb_set(
		                jsonb_set(
		                    jsonb_set(settings, '{exclude_ips}', coalesce(to_jsonb($7::text[]), '[]'::jsonb)),
		                    '{exclude_paths}', coalesce(to_jsonb($8::text[]), '[]'::jsonb)),
		                '{exclude_countries}', coalesce(to_jsonb($9::text[]), '[]'::jsonb)),
		            '{tracker}', coalesce($10::jsonb, '{}'::jsonb)),
		        '{profiles_enabled}', to_jsonb($11::boolean)) ||
		        jsonb_build_object('heatmaps_enabled', $12::boolean)
		where id = $1 and org_id = $2 and deleted_at is null
		returning id, org_id, public_id, name, allowed_hosts, timezone, retention_class,
		          coalesce(settings->'exclude_ips', '[]'::jsonb),
		          coalesce(settings->'exclude_paths', '[]'::jsonb),
		          coalesce(settings->'exclude_countries', '[]'::jsonb),
		          coalesce(settings->'tracker', '{}'::jsonb),
		          coalesce(settings->'profiles_enabled' = 'true'::jsonb, false),
		          coalesce(settings->'heatmaps_enabled' = 'true'::jsonb, false),
		          coalesce(settings->>'icon_url', ''), coalesce(settings->>'icon_checked_at', ''), created_at, deleted_at`,
		site.ID, site.OrgID, site.Name, site.AllowedHosts, site.Timezone, site.RetentionClass,
		site.ExcludeIPs, site.ExcludePaths, site.ExcludeCountries, string(site.Tracker), site.ProfilesEnabled, site.HeatmapsEnabled,
	).Scan(
		&site.ID, &site.OrgID, &site.PublicID, &site.Name, &site.AllowedHosts,
		&site.Timezone, &site.RetentionClass, &site.ExcludeIPs, &site.ExcludePaths, &site.ExcludeCountries, &site.Tracker, &site.ProfilesEnabled,
		&site.HeatmapsEnabled, &site.IconURL, &site.IconCheckedAt,
		&site.CreatedAt, &site.DeletedAt,
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
		select id, org_id, public_id, name, allowed_hosts, timezone, retention_class,
		       coalesce(settings->'exclude_ips', '[]'::jsonb),
		       coalesce(settings->'exclude_paths', '[]'::jsonb),
		       coalesce(settings->'exclude_countries', '[]'::jsonb),
		       coalesce(settings->'tracker', '{}'::jsonb),
		       coalesce(settings->'profiles_enabled' = 'true'::jsonb, false),
		       coalesce(settings->'heatmaps_enabled' = 'true'::jsonb, false),
		       coalesce(settings->>'icon_url', ''), coalesce(settings->>'icon_checked_at', ''), created_at, deleted_at
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
		select id, org_id, public_id, name, allowed_hosts, timezone, retention_class,
		       coalesce(settings->'exclude_ips', '[]'::jsonb),
		       coalesce(settings->'exclude_paths', '[]'::jsonb),
		       coalesce(settings->'exclude_countries', '[]'::jsonb),
		       coalesce(settings->'tracker', '{}'::jsonb),
		       coalesce(settings->'profiles_enabled' = 'true'::jsonb, false),
		       coalesce(settings->'heatmaps_enabled' = 'true'::jsonb, false),
		       coalesce(settings->>'icon_url', ''), coalesce(settings->>'icon_checked_at', ''), created_at, deleted_at
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
		&site.Timezone, &site.RetentionClass, &site.ExcludeIPs, &site.ExcludePaths, &site.ExcludeCountries, &site.Tracker, &site.ProfilesEnabled,
		&site.HeatmapsEnabled, &site.IconURL, &site.IconCheckedAt,
		&site.CreatedAt, &site.DeletedAt,
	); err != nil {
		return Site{}, err
	}
	return site, nil
}

func SetSiteIcon(ctx context.Context, data *Store, orgID int64, id int32, iconURL string, checkedAt time.Time) (Site, error) {
	row := data.Pool.QueryRow(ctx, `
		update sites
		set settings = settings ||
			case when $3::text = '' then '{}'::jsonb else jsonb_build_object('icon_url', $3::text) end ||
			jsonb_build_object('icon_checked_at', $4::text)
		where id = $1 and org_id = $2 and deleted_at is null
		returning id, org_id, public_id, name, allowed_hosts, timezone, retention_class,
		          coalesce(settings->'exclude_ips', '[]'::jsonb),
		          coalesce(settings->'exclude_paths', '[]'::jsonb),
		          coalesce(settings->'exclude_countries', '[]'::jsonb),
		          coalesce(settings->'tracker', '{}'::jsonb),
		          coalesce(settings->'profiles_enabled' = 'true'::jsonb, false),
		          coalesce(settings->'heatmaps_enabled' = 'true'::jsonb, false),
		          coalesce(settings->>'icon_url', ''), coalesce(settings->>'icon_checked_at', ''), created_at, deleted_at`,
		id, orgID, iconURL, checkedAt.UTC().Format(time.RFC3339),
	)
	site, err := scanSite(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Site{}, pgx.ErrNoRows
	}
	if err != nil {
		return Site{}, fmt.Errorf("update site icon: %w", err)
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
