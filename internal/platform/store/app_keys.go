package store

import (
	"context"
	"crypto/sha256"
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
	if _, err := tx.Exec(ctx, `delete from app_ingest_keys where site_id = $1 and kind = 'smk' and expires_at <= $2`, siteID, now); err != nil {
		return nil, fmt.Errorf("delete expired ingest keys: %w", err)
	}
	grace := now.Add(AppKeyGrace)
	tag, err := tx.Exec(ctx, `update app_ingest_keys set expires_at = $2 where site_id = $1 and kind = 'smk' and expires_at is null`, siteID, grace)
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
		where site_id = $1 and kind = 'smk' and (expires_at is null or expires_at > now()) order by created_at desc, id desc`, siteID)
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
		where kind = 'smk' and (expires_at is null or expires_at > now())`)
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

// AppSettings returns the raw settings->'app' JSON of a site, or nil when unset.
func (s *Store) AppSettings(ctx context.Context, siteID int32) ([]byte, error) {
	var raw []byte
	err := s.Pool.QueryRow(ctx, `select settings->'app' from sites where id = $1 and deleted_at is null`, siteID).Scan(&raw)
	if err != nil {
		return nil, fmt.Errorf("load app settings: %w", err)
	}
	return raw, nil
}

// AppSettingsBySite returns settings->'app' for every live app site that has one.
func (s *Store) AppSettingsBySite(ctx context.Context) (map[int32][]byte, error) {
	rows, err := s.Pool.Query(ctx, `select id, settings->'app' from sites where kind = 'app' and deleted_at is null and settings ? 'app'`)
	if err != nil {
		return nil, fmt.Errorf("load app settings: %w", err)
	}
	defer rows.Close()
	out := make(map[int32][]byte)
	for rows.Next() {
		var id int32
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, fmt.Errorf("scan app settings: %w", err)
		}
		out[id] = raw
	}
	return out, rows.Err()
}

// SetAppSettings stores settings->'app' inside the caller's transaction.
func (s *Store) SetAppSettings(ctx context.Context, tx pgx.Tx, siteID int32, raw []byte) error {
	if _, err := tx.Exec(ctx, `update sites set settings = jsonb_set(settings, '{app}', $2::jsonb, true) where id = $1`, siteID, raw); err != nil {
		return fmt.Errorf("save app settings: %w", err)
	}
	return nil
}

// AppHashSecrets returns the HMAC secret of every app that has one, for the registry.
func (s *Store) AppHashSecrets(ctx context.Context) (map[int32][]byte, error) {
	rows, err := s.Pool.Query(ctx, `select site_id, secret from app_hash_secrets`)
	if err != nil {
		return nil, fmt.Errorf("load app hash secrets: %w", err)
	}
	defer rows.Close()
	out := make(map[int32][]byte)
	for rows.Next() {
		var id int32
		var secret []byte
		if err := rows.Scan(&id, &secret); err != nil {
			return nil, fmt.Errorf("scan app hash secret: %w", err)
		}
		out[id] = secret
	}
	return out, rows.Err()
}

// AppLinksBySite returns the app project each live web site sends diagnostic reports to.
func (s *Store) AppLinksBySite(ctx context.Context) (map[int32]int32, error) {
	rows, err := s.Pool.Query(ctx, `select id, (settings->>'app_link')::integer from sites
		where kind = 'web' and deleted_at is null and settings ? 'app_link'`)
	if err != nil {
		return nil, fmt.Errorf("load app links: %w", err)
	}
	defer rows.Close()
	out := make(map[int32]int32)
	for rows.Next() {
		var id, target int32
		if err := rows.Scan(&id, &target); err != nil {
			return nil, fmt.Errorf("scan app link: %w", err)
		}
		out[id] = target
	}
	return out, rows.Err()
}

// AppLink returns the app project a web site is linked to, or 0 when unlinked.
func (s *Store) AppLink(ctx context.Context, siteID int32) (int32, error) {
	var target *int32
	err := s.Pool.QueryRow(ctx, `select (settings->>'app_link')::integer from sites where id = $1 and deleted_at is null`, siteID).Scan(&target)
	if err != nil {
		return 0, fmt.Errorf("load app link: %w", err)
	}
	if target == nil {
		return 0, nil
	}
	return *target, nil
}

// IsAppInOrg reports whether appSiteID is a live app project of org.
func (s *Store) IsAppInOrg(ctx context.Context, orgID int64, appSiteID int32) (bool, error) {
	var ok bool
	err := s.Pool.QueryRow(ctx, `select exists(select 1 from sites where id = $1 and org_id = $2 and kind = 'app' and deleted_at is null)`, appSiteID, orgID).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("check app project: %w", err)
	}
	return ok, nil
}

// SetAppLink links a web site to an app project, or unlinks it when appSiteID
// is nil, inside the caller's transaction.
func (s *Store) SetAppLink(ctx context.Context, tx pgx.Tx, siteID int32, appSiteID *int32) error {
	var err error
	if appSiteID == nil {
		_, err = tx.Exec(ctx, `update sites set settings = settings - 'app_link' where id = $1`, siteID)
	} else {
		_, err = tx.Exec(ctx, `update sites set settings = jsonb_set(settings, '{app_link}', to_jsonb($2::integer), true) where id = $1`, siteID, *appSiteID)
	}
	if err != nil {
		return fmt.Errorf("save app link: %w", err)
	}
	return nil
}

// DSNKeyRow is a DSN public key as stored.
type DSNKeyRow struct {
	SiteID         int32
	PublicKey      string
	Label          string
	AllowedOrigins []string
	RatePerMin     *int32
	CreatedAt      time.Time
	ExpiresAt      *time.Time
}

// CreateDSNKeyTx stores a new DSN key inside the caller's transaction. The
// hash column holds sha256(public_key) and prefix its first 8 characters.
func (s *Store) CreateDSNKeyTx(ctx context.Context, tx pgx.Tx, row DSNKeyRow, createdBy *int64) error {
	hash := sha256.Sum256([]byte(row.PublicKey))
	origins := row.AllowedOrigins
	if origins == nil {
		origins = []string{}
	}
	if _, err := tx.Exec(ctx, `insert into app_ingest_keys(site_id, prefix, hash, created_by, created_at, expires_at, kind, public_key, allowed_origins, label, rate_limit_per_min)
		values ($1,$2,$3,$4,$5,$6,'dsn',$7,$8,$9,$10)`,
		row.SiteID, row.PublicKey[:8], hash[:], createdBy, row.CreatedAt, row.ExpiresAt, row.PublicKey, origins, row.Label, row.RatePerMin); err != nil {
		return fmt.Errorf("insert dsn key: %w", err)
	}
	return nil
}

// ListDSNKeys returns a site's unrevoked, unexpired DSN keys, newest first.
func (s *Store) ListDSNKeys(ctx context.Context, siteID int32) ([]DSNKeyRow, error) {
	return s.queryDSNKeys(ctx, `select site_id, public_key, label, allowed_origins, rate_limit_per_min, created_at, expires_at
		from app_ingest_keys where site_id = $1 and kind = 'dsn' and revoked_at is null and (expires_at is null or expires_at > now())
		order by created_at desc, id desc`, siteID)
}

// ActiveDSNKeys returns every unrevoked, unexpired DSN key of every site, for the registry.
func (s *Store) ActiveDSNKeys(ctx context.Context) ([]DSNKeyRow, error) {
	return s.queryDSNKeys(ctx, `select site_id, public_key, label, allowed_origins, rate_limit_per_min, created_at, expires_at
		from app_ingest_keys where kind = 'dsn' and revoked_at is null and (expires_at is null or expires_at > now())`)
}

func (s *Store) queryDSNKeys(ctx context.Context, query string, args ...any) ([]DSNKeyRow, error) {
	rows, err := s.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("load dsn keys: %w", err)
	}
	defer rows.Close()
	var out []DSNKeyRow
	for rows.Next() {
		var row DSNKeyRow
		if err := rows.Scan(&row.SiteID, &row.PublicKey, &row.Label, &row.AllowedOrigins, &row.RatePerMin, &row.CreatedAt, &row.ExpiresAt); err != nil {
			return nil, fmt.Errorf("scan dsn key: %w", err)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// RevokeDSNKeyTx revokes the site's active DSN key with the given 8-character
// prefix and reports whether one was found.
func (s *Store) RevokeDSNKeyTx(ctx context.Context, tx pgx.Tx, siteID int32, prefix string, now time.Time) (bool, error) {
	tag, err := tx.Exec(ctx, `update app_ingest_keys set revoked_at = $3 where site_id = $1 and kind = 'dsn' and prefix = $2 and revoked_at is null`, siteID, prefix, now)
	if err != nil {
		return false, fmt.Errorf("revoke dsn key: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}
