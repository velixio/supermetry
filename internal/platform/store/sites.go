package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

func SiteReplaySettings(ctx context.Context, data *Store, orgID int64, id int32) ([]byte, error) {
	var raw []byte
	err := data.Pool.QueryRow(ctx, `
		select coalesce(settings->'replay', '{}'::jsonb)
		from sites where id=$1 and org_id=$2 and deleted_at is null`, id, orgID).Scan(&raw)
	if err != nil {
		return nil, fmt.Errorf("load site replay settings: %w", err)
	}
	return raw, nil
}

func SetSiteReplaySettings(ctx context.Context, data *Store, orgID int64, id int32, settings []byte) error {
	tag, err := data.Pool.Exec(ctx, `
		update sites
		set settings=jsonb_set(settings, '{replay}', $3::jsonb)
		where id=$1 and org_id=$2 and deleted_at is null`, id, orgID, settings)
	if err != nil {
		return fmt.Errorf("update site replay settings: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return pgx.ErrNoRows
	}
	return nil
}

func InstanceReplayStorage(ctx context.Context, data *Store) ([]byte, error) {
	var raw []byte
	err := data.Pool.QueryRow(ctx, `select coalesce(settings->'replay_storage', '{}'::jsonb) from instance_settings where id=true`).Scan(&raw)
	if err != nil {
		return nil, fmt.Errorf("load instance replay storage: %w", err)
	}
	return raw, nil
}

func SetInstanceReplayStorage(ctx context.Context, data *Store, settings []byte, migration any) error {
	if migration == nil {
		_, err := data.Pool.Exec(ctx, `
			update instance_settings
			set settings=jsonb_set(settings, '{replay_storage}', $1::jsonb), updated_at=now()
			where id=true`, settings)
		if err != nil {
			return fmt.Errorf("update instance replay storage: %w", err)
		}
		return nil
	}
	migrationJSON, err := json.Marshal(migration)
	if err != nil {
		return fmt.Errorf("encode replay migration request: %w", err)
	}
	_, err = data.Pool.Exec(ctx, `
		update instance_settings
		set settings=jsonb_set(
			jsonb_set(settings, '{replay_storage}', $1::jsonb),
			'{replay_migration}', $2::jsonb
		), updated_at=now()
		where id=true`, settings, migrationJSON)
	if err != nil {
		return fmt.Errorf("update instance replay storage: %w", err)
	}
	return nil
}

type Site struct {
	ID                    int32
	OrgID                 int64
	PublicID              string
	Name                  string
	AllowedHosts          []string
	Timezone              string
	RetentionClass        int16
	ExcludeIPs            []string
	ExcludePaths          []string
	ExcludeCountries      []string
	Tracker               []byte
	ProfilesEnabled       bool
	HeatmapsEnabled       bool
	RespectPrivacySignals bool
	StrictOrigin          bool
	BlockedReferrers      []string
	IconURL               string
	IconCheckedAt         string
	RevenueCurrency       string
	RevenueCurrencyState  string
	CreatedAt             time.Time
	DeletedAt             *time.Time
}

func CreateSite(ctx context.Context, tx pgx.Tx, site Site) (Site, error) {
	err := tx.QueryRow(ctx, `
		insert into sites (org_id, public_id, name, allowed_hosts, timezone, retention_class, revenue_currency, revenue_currency_state)
		values ($1, $2, $3, $4, $5, $6, $7, 'configured')
		on conflict (public_id) do nothing
		returning id, created_at`,
		site.OrgID, site.PublicID, site.Name, site.AllowedHosts, site.Timezone, site.RetentionClass, site.RevenueCurrency,
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
		       coalesce(settings->'respect_privacy_signals' = 'true'::jsonb, true),
		       coalesce(settings->'strict_origin' = 'true'::jsonb, false),
		       coalesce(settings->'blocked_referrers', '[]'::jsonb),
		       coalesce(settings->>'icon_url', ''), coalesce(settings->>'icon_checked_at', ''),
		       coalesce(revenue_currency::text, ''), revenue_currency_state, created_at, deleted_at
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
		        jsonb_build_object(
		            'heatmaps_enabled', $12::boolean,
		            'respect_privacy_signals', $13::boolean,
		            'strict_origin', $14::boolean,
		            'blocked_referrers', coalesce(to_jsonb($15::text[]), '[]'::jsonb))
		where id = $1 and org_id = $2 and deleted_at is null
		returning id, org_id, public_id, name, allowed_hosts, timezone, retention_class,
		          coalesce(settings->'exclude_ips', '[]'::jsonb),
		          coalesce(settings->'exclude_paths', '[]'::jsonb),
		          coalesce(settings->'exclude_countries', '[]'::jsonb),
		          coalesce(settings->'tracker', '{}'::jsonb),
		          coalesce(settings->'profiles_enabled' = 'true'::jsonb, false),
		          coalesce(settings->'heatmaps_enabled' = 'true'::jsonb, false),
		          coalesce(settings->'respect_privacy_signals' = 'true'::jsonb, true),
		          coalesce(settings->'strict_origin' = 'true'::jsonb, false),
		          coalesce(settings->'blocked_referrers', '[]'::jsonb),
		          coalesce(settings->>'icon_url', ''), coalesce(settings->>'icon_checked_at', ''),
		          coalesce(revenue_currency::text, ''), revenue_currency_state, created_at, deleted_at`,
		site.ID, site.OrgID, site.Name, site.AllowedHosts, site.Timezone, site.RetentionClass,
		site.ExcludeIPs, site.ExcludePaths, site.ExcludeCountries, string(site.Tracker), site.ProfilesEnabled, site.HeatmapsEnabled,
		site.RespectPrivacySignals, site.StrictOrigin, site.BlockedReferrers,
	).Scan(
		&site.ID, &site.OrgID, &site.PublicID, &site.Name, &site.AllowedHosts,
		&site.Timezone, &site.RetentionClass, &site.ExcludeIPs, &site.ExcludePaths, &site.ExcludeCountries, &site.Tracker, &site.ProfilesEnabled,
		&site.HeatmapsEnabled, &site.RespectPrivacySignals, &site.StrictOrigin, &site.BlockedReferrers, &site.IconURL, &site.IconCheckedAt,
		&site.RevenueCurrency, &site.RevenueCurrencyState,
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
		select s.id, s.org_id, s.public_id, s.name, s.allowed_hosts, s.timezone, s.retention_class,
		       coalesce(s.settings->'exclude_ips', '[]'::jsonb),
		       coalesce(s.settings->'exclude_paths', '[]'::jsonb),
		       coalesce(s.settings->'exclude_countries', '[]'::jsonb),
		       coalesce(s.settings->'tracker', '{}'::jsonb),
		       coalesce(s.settings->'profiles_enabled' = 'true'::jsonb, false),
		       coalesce(s.settings->'heatmaps_enabled' = 'true'::jsonb, false),
		       coalesce(s.settings->'respect_privacy_signals' = 'true'::jsonb, true),
		       coalesce(s.settings->'strict_origin' = 'true'::jsonb, false),
		       coalesce(s.settings->'blocked_referrers', '[]'::jsonb),
		       coalesce(s.settings->>'icon_url', ''), coalesce(s.settings->>'icon_checked_at', ''),
		       coalesce(s.revenue_currency::text, ''), s.revenue_currency_state, s.created_at, s.deleted_at
		from sites s
		join orgs o on o.id = s.org_id
		where s.deleted_at is null and o.deleted_at is null
		order by s.id`)
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
		       coalesce(settings->'respect_privacy_signals' = 'true'::jsonb, true),
		       coalesce(settings->'strict_origin' = 'true'::jsonb, false),
		       coalesce(settings->'blocked_referrers', '[]'::jsonb),
		       coalesce(settings->>'icon_url', ''), coalesce(settings->>'icon_checked_at', ''),
		       coalesce(revenue_currency::text, ''), revenue_currency_state, created_at, deleted_at
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
		&site.HeatmapsEnabled, &site.RespectPrivacySignals, &site.StrictOrigin, &site.BlockedReferrers, &site.IconURL, &site.IconCheckedAt,
		&site.RevenueCurrency, &site.RevenueCurrencyState,
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
		          coalesce(settings->'respect_privacy_signals' = 'true'::jsonb, true),
		          coalesce(settings->'strict_origin' = 'true'::jsonb, false),
		          coalesce(settings->'blocked_referrers', '[]'::jsonb),
		          coalesce(settings->>'icon_url', ''), coalesce(settings->>'icon_checked_at', ''),
		          coalesce(revenue_currency::text, ''), revenue_currency_state, created_at, deleted_at`,
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
