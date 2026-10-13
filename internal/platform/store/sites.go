package store

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"supermetry/internal/platform/lifecycle"
)

const demoPublicIDAttempts = 8

var (
	ErrDemoExists        = errors.New("demo site already exists")
	ErrNotRestorable     = errors.New("site is not restorable")
	demoPublicIDEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)
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
	Slug                  string
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
	IsDemo                bool
	DemoSeededAt          *time.Time
	DemoStartedAt         *time.Time
}

type DemoState struct {
	SiteID      int32
	SeededAt    *time.Time
	LiveThrough *time.Time
}

type DeletedSite struct {
	ID        int32
	PublicID  string
	Name      string
	DeletedAt time.Time
	PurgeAt   time.Time
	WithOrg   bool
}

func CreateSite(ctx context.Context, tx pgx.Tx, site Site) (Site, error) {
	err := tx.QueryRow(ctx, `
		insert into sites (org_id, public_id, name, allowed_hosts, timezone, retention_class, revenue_currency, revenue_currency_state, settings)
		values ($1, $2, $3, $4, $5, $6, $7, 'configured', '{"profiles_enabled": true}'::jsonb)
		on conflict (public_id) do nothing
		returning id, slug, created_at`,
		site.OrgID, site.PublicID, site.Name, site.AllowedHosts, site.Timezone, site.RetentionClass, site.RevenueCurrency,
	).Scan(&site.ID, &site.Slug, &site.CreatedAt)
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
		select id, org_id, public_id, slug, name, allowed_hosts, timezone, retention_class,
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
		       coalesce(revenue_currency::text, ''), revenue_currency_state, created_at, deleted_at,
		       is_demo, demo_seeded_at, demo_started_at
		from sites
		where id = $1 and org_id = $2 and deleted_at is null
		for update`, id, orgID)
	return scanSite(row)
}

func UpdateSite(ctx context.Context, tx pgx.Tx, site Site) (Site, error) {
	err := tx.QueryRow(ctx, `
		update sites
		set name = $3, allowed_hosts = $4, timezone = $5, retention_class = $6,
		    slug = coalesce(nullif($16::text, ''), slug),
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
		returning id, org_id, public_id, slug, name, allowed_hosts, timezone, retention_class,
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
		          coalesce(revenue_currency::text, ''), revenue_currency_state, created_at, deleted_at,
		          is_demo, demo_seeded_at, demo_started_at`,
		site.ID, site.OrgID, site.Name, site.AllowedHosts, site.Timezone, site.RetentionClass,
		site.ExcludeIPs, site.ExcludePaths, site.ExcludeCountries, string(site.Tracker), site.ProfilesEnabled, site.HeatmapsEnabled,
		site.RespectPrivacySignals, site.StrictOrigin, site.BlockedReferrers, site.Slug,
	).Scan(
		&site.ID, &site.OrgID, &site.PublicID, &site.Slug, &site.Name, &site.AllowedHosts,
		&site.Timezone, &site.RetentionClass, &site.ExcludeIPs, &site.ExcludePaths, &site.ExcludeCountries, &site.Tracker, &site.ProfilesEnabled,
		&site.HeatmapsEnabled, &site.RespectPrivacySignals, &site.StrictOrigin, &site.BlockedReferrers, &site.IconURL, &site.IconCheckedAt,
		&site.RevenueCurrency, &site.RevenueCurrencyState,
		&site.CreatedAt, &site.DeletedAt, &site.IsDemo, &site.DemoSeededAt, &site.DemoStartedAt,
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
	return SoftDeleteSiteAt(ctx, tx, orgID, id, time.Now())
}

func SoftDeleteSiteAt(ctx context.Context, tx pgx.Tx, orgID int64, id int32, deletedAt time.Time) error {
	command, err := tx.Exec(ctx, `
		update sites
		set deleted_at = $3, deleted_with_org = false,
		    deletion_notified_at = null, deletion_reminded_at = null
		where id = $1 and org_id = $2 and deleted_at is null`, id, orgID, deletedAt)
	if err != nil {
		return fmt.Errorf("soft-delete site: %w", err)
	}
	if command.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

type siteRowsQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func SitesDueForErasure(ctx context.Context, q siteRowsQuerier, now time.Time) ([]int32, error) {
	rows, err := q.Query(ctx, `
		select id
		from sites
		where deleted_at is not null
		  and erased_at is null
		  and (erasure_started_at is not null or is_demo or deleted_at <= $1::timestamptz - interval '7 days')
		order by id`, now)
	if err != nil {
		return nil, fmt.Errorf("query sites due for erasure: %w", err)
	}
	defer rows.Close()
	result := make([]int32, 0)
	for rows.Next() {
		var id int32
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan site due for erasure: %w", err)
		}
		result = append(result, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sites due for erasure: %w", err)
	}
	return result, nil
}

func ClaimSiteErasure(ctx context.Context, q DBTX, id int32, now time.Time) (bool, error) {
	var claimedID int32
	err := q.QueryRow(ctx, `
		update sites
		set erasure_started_at=coalesce(erasure_started_at,$2)
		where id=$1 and erased_at is null and deleted_at is not null
		  and (erasure_started_at is not null or is_demo or deleted_at <= $2::timestamptz - interval '7 days')
		returning id`, id, now).Scan(&claimedID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim site %d for erasure: %w", id, err)
	}
	return true, nil
}

func MarkSiteErased(ctx context.Context, q DBTX, id int32, at time.Time) error {
	tag, err := q.Exec(ctx, `
		update sites
		set erased_at=$2,
		    settings=jsonb_set(settings,'{erased_at}',to_jsonb($3::text))
		where id=$1 and erased_at is null`, id, at, at.UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("mark site %d erased: %w", id, err)
	}
	if tag.RowsAffected() != 1 {
		return pgx.ErrNoRows
	}
	return nil
}

func DeletedSites(ctx context.Context, q siteRowsQuerier, orgID int64, now time.Time) ([]DeletedSite, error) {
	rows, err := q.Query(ctx, `
		select s.id,s.public_id,s.name,s.deleted_at,s.deleted_with_org
		from sites s
		join orgs o on o.id=s.org_id
		where s.org_id=$1
		  and s.deleted_at is not null
		  and s.deleted_at > $2
		  and s.erased_at is null
		  and not s.is_demo
		  and o.deleted_at is null
		order by s.deleted_at,s.id`, orgID, now.Add(-lifecycle.DeletionGrace))
	if err != nil {
		return nil, fmt.Errorf("query deleted sites: %w", err)
	}
	defer rows.Close()
	result := make([]DeletedSite, 0)
	for rows.Next() {
		var site DeletedSite
		if err := rows.Scan(&site.ID, &site.PublicID, &site.Name, &site.DeletedAt, &site.WithOrg); err != nil {
			return nil, fmt.Errorf("scan deleted site: %w", err)
		}
		site.PurgeAt = lifecycle.PurgeAt(site.DeletedAt)
		result = append(result, site)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate deleted sites: %w", err)
	}
	return result, nil
}

func RestoreSite(ctx context.Context, tx pgx.Tx, orgID int64, id int32, now time.Time) error {
	var deletedAt *time.Time
	var erasedAt, erasureStartedAt *time.Time
	var demo bool
	err := tx.QueryRow(ctx, `
		select s.deleted_at,s.erased_at,s.erasure_started_at,s.is_demo
		from sites s join orgs o on o.id=s.org_id
		where s.id=$1 and s.org_id=$2 and o.deleted_at is null
		for update of s`, id, orgID).Scan(&deletedAt, &erasedAt, &erasureStartedAt, &demo)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotRestorable
	}
	if err != nil {
		return fmt.Errorf("lock site for restore: %w", err)
	}
	if deletedAt == nil || erasedAt != nil || erasureStartedAt != nil || demo || !deletedAt.After(now.Add(-lifecycle.DeletionGrace)) {
		return ErrNotRestorable
	}
	if _, err := tx.Exec(ctx, `
		update sites set deleted_at=null,deleted_with_org=false,
		deletion_notified_at=null,deletion_reminded_at=null
		where id=$1`, id); err != nil {
		return fmt.Errorf("restore site: %w", err)
	}
	return nil
}

func ActiveSites(ctx context.Context, data *Store) ([]Site, error) {
	rows, err := data.Pool.Query(ctx, `
		select s.id, s.org_id, s.public_id, s.slug, s.name, s.allowed_hosts, s.timezone, s.retention_class,
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
		       coalesce(s.revenue_currency::text, ''), s.revenue_currency_state, s.created_at, s.deleted_at,
		       s.is_demo, s.demo_seeded_at, s.demo_started_at
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
		select id, org_id, public_id, slug, name, allowed_hosts, timezone, retention_class,
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
		       coalesce(revenue_currency::text, ''), revenue_currency_state, created_at, deleted_at,
		       is_demo, demo_seeded_at, demo_started_at
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
		&site.ID, &site.OrgID, &site.PublicID, &site.Slug, &site.Name, &site.AllowedHosts,
		&site.Timezone, &site.RetentionClass, &site.ExcludeIPs, &site.ExcludePaths, &site.ExcludeCountries, &site.Tracker, &site.ProfilesEnabled,
		&site.HeatmapsEnabled, &site.RespectPrivacySignals, &site.StrictOrigin, &site.BlockedReferrers, &site.IconURL, &site.IconCheckedAt,
		&site.RevenueCurrency, &site.RevenueCurrencyState,
		&site.CreatedAt, &site.DeletedAt, &site.IsDemo, &site.DemoSeededAt, &site.DemoStartedAt,
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
		returning id, org_id, public_id, slug, name, allowed_hosts, timezone, retention_class,
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
		          coalesce(revenue_currency::text, ''), revenue_currency_state, created_at, deleted_at,
		          is_demo, demo_seeded_at, demo_started_at`,
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

// CreateDemoSite inserts the organization's single active demo project.
func CreateDemoSite(ctx context.Context, tx pgx.Tx, site Site, createdAt time.Time) (Site, error) {
	if site.AllowedHosts == nil {
		site.AllowedHosts = []string{}
	}
	for attempt := 0; attempt < demoPublicIDAttempts; attempt++ {
		publicID := site.PublicID
		if publicID == "" || attempt > 0 {
			var err error
			publicID, err = newDemoPublicID()
			if err != nil {
				return Site{}, err
			}
		}
		err := tx.QueryRow(ctx, `
			insert into sites (
				org_id, public_id, name, allowed_hosts, timezone, retention_class,
				settings, revenue_currency, revenue_currency_state, created_at, is_demo, demo_started_at
			)
			values (
				$1, $2, $3, $4, $5, $6,
				jsonb_build_object(
					'profiles_enabled', true,
					'heatmaps_enabled', true,
					'replay', jsonb_build_object('enabled', true, 'sample_rate', 100)
				),
				'USD', 'configured', $7, true, now()
			)
			on conflict (public_id) do nothing
			returning id, public_id, slug, created_at, demo_started_at`,
			site.OrgID, publicID, site.Name, site.AllowedHosts, site.Timezone, site.RetentionClass, createdAt,
		).Scan(&site.ID, &site.PublicID, &site.Slug, &site.CreatedAt, &site.DemoStartedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.ConstraintName == "sites_one_demo_per_org" {
				return Site{}, ErrDemoExists
			}
			return Site{}, fmt.Errorf("insert demo site: %w", err)
		}
		site.IsDemo = true
		site.ProfilesEnabled = true
		site.HeatmapsEnabled = true
		site.RevenueCurrency = "USD"
		site.RevenueCurrencyState = "configured"
		return site, nil
	}
	return Site{}, errors.New("generate unique demo site public ID")
}

func DemoSiteForOrg(ctx context.Context, q DBTX, orgID int64) (Site, bool, error) {
	row := q.QueryRow(ctx, `
		select id, org_id, public_id, slug, name, allowed_hosts, timezone, retention_class,
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
		       coalesce(revenue_currency::text, ''), revenue_currency_state, created_at, deleted_at,
		       is_demo, demo_seeded_at, demo_started_at
		from sites
		where org_id=$1 and is_demo and deleted_at is null`, orgID)
	site, err := scanSite(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Site{}, false, nil
	}
	if err != nil {
		return Site{}, false, fmt.Errorf("load organization demo site: %w", err)
	}
	return site, true, nil
}

func SetDemoSeeded(ctx context.Context, q DBTX, siteID int32, at time.Time) error {
	return updateDemoTimestamp(ctx, q, siteID, "demo_seeded_at", at)
}

func SetDemoLiveThrough(ctx context.Context, q DBTX, siteID int32, at time.Time) error {
	return updateDemoTimestamp(ctx, q, siteID, "demo_live_through", at)
}

func TouchDemoViewed(ctx context.Context, q DBTX, siteID int32, at time.Time) error {
	return updateDemoTimestamp(ctx, q, siteID, "demo_viewed_at", at)
}

func updateDemoTimestamp(ctx context.Context, q DBTX, siteID int32, column string, at time.Time) error {
	query := "update sites set " + column + "=$2 where id=$1 and is_demo and deleted_at is null"
	tag, err := q.Exec(ctx, query, siteID, at)
	if err != nil {
		return fmt.Errorf("update demo site state: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return pgx.ErrNoRows
	}
	return nil
}

type demoQuerier interface {
	DBTX
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

type ExpiredDemo struct {
	SiteID int32
	OrgID  int64
}

func ExpiredDemoSites(ctx context.Context, q demoQuerier, startedBefore time.Time) ([]ExpiredDemo, error) {
	rows, err := q.Query(ctx, `
		select s.id, s.org_id
		from sites s
		join orgs o on o.id=s.org_id
		where s.is_demo and coalesce(s.demo_started_at, s.created_at) < $1 and s.deleted_at is null and o.deleted_at is null
		order by s.id`, startedBefore)
	if err != nil {
		return nil, fmt.Errorf("query expired demo sites: %w", err)
	}
	defer rows.Close()
	result := make([]ExpiredDemo, 0)
	for rows.Next() {
		var expired ExpiredDemo
		if err := rows.Scan(&expired.SiteID, &expired.OrgID); err != nil {
			return nil, fmt.Errorf("scan expired demo site: %w", err)
		}
		result = append(result, expired)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate expired demo sites: %w", err)
	}
	return result, nil
}

func UnseededDemoSites(ctx context.Context, q demoQuerier) ([]int32, error) {
	rows, err := q.Query(ctx, `
		select s.id
		from sites s
		join orgs o on o.id=s.org_id
		where s.is_demo and s.demo_seeded_at is null and s.deleted_at is null and o.deleted_at is null
		order by s.id`)
	if err != nil {
		return nil, fmt.Errorf("query unseeded demo sites: %w", err)
	}
	defer rows.Close()
	result := make([]int32, 0)
	for rows.Next() {
		var id int32
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan unseeded demo site: %w", err)
		}
		result = append(result, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate unseeded demo sites: %w", err)
	}
	return result, nil
}

func ActiveDemoSites(ctx context.Context, q demoQuerier, viewedSince time.Time) ([]DemoState, error) {
	rows, err := q.Query(ctx, `
		select s.id, s.demo_seeded_at, s.demo_live_through
		from sites s
		join orgs o on o.id=s.org_id
		where s.is_demo and s.deleted_at is null and o.deleted_at is null
		  and s.demo_viewed_at >= $1
		order by s.id`, viewedSince)
	if err != nil {
		return nil, fmt.Errorf("query active demo sites: %w", err)
	}
	defer rows.Close()
	result := make([]DemoState, 0)
	for rows.Next() {
		var state DemoState
		if err := rows.Scan(&state.SiteID, &state.SeededAt, &state.LiveThrough); err != nil {
			return nil, fmt.Errorf("scan active demo site: %w", err)
		}
		result = append(result, state)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active demo sites: %w", err)
	}
	return result, nil
}

func DismissDemoPrompt(ctx context.Context, q DBTX, userID int64, at time.Time) error {
	tag, err := q.Exec(ctx, `
		update users set demo_prompt_dismissed_at=$2
		where id=$1 and deleted_at is null`, userID, at)
	if err != nil {
		return fmt.Errorf("dismiss demo prompt: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return pgx.ErrNoRows
	}
	return nil
}

func DemoPromptDismissed(ctx context.Context, q DBTX, userID int64) (bool, error) {
	var dismissed bool
	if err := q.QueryRow(ctx, `
		select demo_prompt_dismissed_at is not null
		from users where id=$1 and deleted_at is null`, userID).Scan(&dismissed); err != nil {
		return false, fmt.Errorf("load demo prompt state: %w", err)
	}
	return dismissed, nil
}

func newDemoPublicID() (string, error) {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("generate demo site public ID: %w", err)
	}
	return strings.ToLower(demoPublicIDEncoding.EncodeToString(random[:])[:12]), nil
}
