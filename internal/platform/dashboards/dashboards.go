package dashboards

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"supermetry/internal/api"
	"supermetry/internal/platform/audit"
	"supermetry/internal/platform/authz"
	"supermetry/internal/platform/store"
)

const nameConstraint = "dashboards_site_id_name_lower_key"

var (
	// ErrNotFound means no saved dashboard has the given id.
	ErrNotFound = errors.New("dashboard not found")
	// ErrNameTaken means the site already has a dashboard with this name.
	ErrNameTaken = errors.New("a dashboard with this name already exists")
	// ErrLimit means the site reached MaxPerSite.
	ErrLimit = fmt.Errorf("a site can keep at most %d dashboards", MaxPerSite)
	// ErrStale means the caller edited an older version than the stored one.
	ErrStale = errors.New("the dashboard changed since it was loaded; reload and retry")
)

// Service reads and writes the dashboards table.
type Service struct{ store *store.Store }

// New returns a service backed by data.
func New(data *store.Store) *Service { return &Service{store: data} }

const selectColumns = `id,site_id,name,layout,is_default,version,created_by,created_at,updated_at`

func scan(fn func(...any) error) (api.Dashboard, error) {
	var dashboard api.Dashboard
	var layout []byte
	if err := fn(&dashboard.Id, &dashboard.SiteId, &dashboard.Name, &layout, &dashboard.IsDefault, &dashboard.Version,
		&dashboard.CreatedBy, &dashboard.CreatedAt, &dashboard.UpdatedAt); err != nil {
		return api.Dashboard{}, err
	}
	if err := json.Unmarshal(layout, &dashboard.Layout); err != nil {
		return api.Dashboard{}, fmt.Errorf("decode dashboard layout: %w", err)
	}
	return dashboard, nil
}

// List returns a site's saved dashboards by name, or the built-in dashboard
// alone when the site has none.
func (s *Service) List(ctx context.Context, siteID int32) ([]api.Dashboard, error) {
	rows, err := s.store.Pool.Query(ctx, `select `+selectColumns+` from dashboards where site_id=$1 order by lower(name),id limit $2`, siteID, MaxPerSite)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]api.Dashboard, 0)
	for rows.Next() {
		dashboard, err := scan(rows.Scan)
		if err != nil {
			return nil, err
		}
		result = append(result, dashboard)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(result) == 0 {
		result = append(result, Builtin(siteID))
	}
	return result, nil
}

// Get returns one saved dashboard.
func (s *Service) Get(ctx context.Context, siteID int32, id int64) (api.Dashboard, error) {
	dashboard, err := scan(func(dest ...any) error {
		return s.store.Pool.QueryRow(ctx, `select `+selectColumns+` from dashboards where id=$1 and site_id=$2`, id, siteID).Scan(dest...)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return api.Dashboard{}, ErrNotFound
	}
	return dashboard, err
}

// Default returns the site's flagged dashboard, or the built-in one.
func (s *Service) Default(ctx context.Context, siteID int32) (api.Dashboard, error) {
	dashboard, err := scan(func(dest ...any) error {
		return s.store.Pool.QueryRow(ctx, `select `+selectColumns+` from dashboards where site_id=$1 and is_default`, siteID).Scan(dest...)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Builtin(siteID), nil
	}
	return dashboard, err
}

// Create saves a new dashboard and records an audit entry.
func (s *Service) Create(ctx context.Context, principal authz.Principal, siteID int32, name string, layout api.DashboardLayout) (api.Dashboard, error) {
	name, err := ValidateName(name)
	if err != nil {
		return api.Dashboard{}, err
	}
	_, encoded, err := NormalizeLayout(layout)
	if err != nil {
		return api.Dashboard{}, err
	}
	return s.insert(ctx, principal, siteID, name, encoded, "dashboard.created", nil)
}

func (s *Service) insert(ctx context.Context, principal authz.Principal, siteID int32, name string, layout []byte, action string, extra map[string]any) (api.Dashboard, error) {
	var created api.Dashboard
	err := s.store.Tx(ctx, func(tx pgx.Tx) error {
		// Serialize writers per site so the cap cannot be raced past.
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtextextended('dashboards:'||$1::text,0))`, siteID); err != nil {
			return err
		}
		var count int
		if err := tx.QueryRow(ctx, `select count(*) from dashboards where site_id=$1`, siteID).Scan(&count); err != nil {
			return err
		}
		if count >= MaxPerSite {
			return ErrLimit
		}
		var scanErr error
		created, scanErr = scan(func(dest ...any) error {
			return tx.QueryRow(ctx, `insert into dashboards(site_id,name,layout,created_by) values ($1,$2,$3,nullif($4,0)) returning `+selectColumns,
				siteID, name, layout, principal.UserID).Scan(dest...)
		})
		if scanErr != nil {
			return scanErr
		}
		metadata := map[string]any{"site_id": siteID}
		for key, value := range extra {
			metadata[key] = value
		}
		return audit.Write(ctx, tx, principal, action, "dashboard", created.Id, metadata)
	})
	return created, nameError(err)
}

// Update renames and re-lays out a dashboard when expectedVersion matches the
// stored version, and increments the version.
func (s *Service) Update(ctx context.Context, principal authz.Principal, siteID int32, id int64, name string, layout api.DashboardLayout, expectedVersion int) (api.Dashboard, error) {
	name, err := ValidateName(name)
	if err != nil {
		return api.Dashboard{}, err
	}
	_, encoded, err := NormalizeLayout(layout)
	if err != nil {
		return api.Dashboard{}, err
	}
	var updated api.Dashboard
	err = s.store.Tx(ctx, func(tx pgx.Tx) error {
		var current int
		if err := tx.QueryRow(ctx, `select version from dashboards where id=$1 and site_id=$2 for update`, id, siteID).Scan(&current); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if current != expectedVersion {
			return ErrStale
		}
		var scanErr error
		updated, scanErr = scan(func(dest ...any) error {
			return tx.QueryRow(ctx, `update dashboards set name=$2,layout=$3,version=version+1,updated_at=now() where id=$1 and site_id=$4 returning `+selectColumns,
				id, name, encoded, siteID).Scan(dest...)
		})
		if scanErr != nil {
			return scanErr
		}
		return audit.Write(ctx, tx, principal, "dashboard.updated", "dashboard", id, map[string]any{"site_id": updated.SiteId, "version": updated.Version})
	})
	return updated, nameError(err)
}

// Delete removes a dashboard; deleting the default returns the site to the
// built-in default.
func (s *Service) Delete(ctx context.Context, principal authz.Principal, siteID int32, id int64) error {
	return s.store.Tx(ctx, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `delete from dashboards where id=$1 and site_id=$2 returning site_id`, id, siteID).Scan(&siteID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		return audit.Write(ctx, tx, principal, "dashboard.deleted", "dashboard", id, map[string]any{"site_id": siteID})
	})
}

// Duplicate copies a dashboard as "<name> (copy)", numbering the copy when taken.
func (s *Service) Duplicate(ctx context.Context, principal authz.Principal, siteID int32, id int64) (api.Dashboard, error) {
	source, err := s.Get(ctx, siteID, id)
	if err != nil {
		return api.Dashboard{}, err
	}
	_, encoded, err := NormalizeLayout(source.Layout)
	if err != nil {
		return api.Dashboard{}, err
	}
	for attempt := 1; attempt <= 20; attempt++ {
		suffix := " (copy)"
		if attempt > 1 {
			suffix = " (copy " + strconv.Itoa(attempt) + ")"
		}
		name := truncateRunes(source.Name, maxNameRunes-utf8.RuneCountInString(suffix)) + suffix
		copied, err := s.insert(ctx, principal, siteID, name, encoded, "dashboard.duplicated", map[string]any{"source_id": id})
		if errors.Is(err, ErrNameTaken) {
			continue
		}
		return copied, err
	}
	return api.Dashboard{}, ErrNameTaken
}

// SetDefault makes a dashboard the site default, replacing any previous one.
func (s *Service) SetDefault(ctx context.Context, principal authz.Principal, siteID int32, id int64) (api.Dashboard, error) {
	var result api.Dashboard
	err := s.store.Tx(ctx, func(tx pgx.Tx) error {
		var found int32
		if err := tx.QueryRow(ctx, `select site_id from dashboards where id=$1 and site_id=$2`, id, siteID).Scan(&found); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtextextended('dashboards:'||$1::text,0))`, siteID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `update dashboards set is_default=false where site_id=$1 and is_default and id<>$2`, siteID, id); err != nil {
			return err
		}
		var scanErr error
		result, scanErr = scan(func(dest ...any) error {
			return tx.QueryRow(ctx, `update dashboards set is_default=true where id=$1 and site_id=$2 returning `+selectColumns, id, siteID).Scan(dest...)
		})
		if errors.Is(scanErr, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if scanErr != nil {
			return scanErr
		}
		return audit.Write(ctx, tx, principal, "dashboard.default_set", "dashboard", id, map[string]any{"site_id": siteID})
	})
	return result, err
}

// ClearDefault unflags the site's default so the built-in dashboard is used.
func (s *Service) ClearDefault(ctx context.Context, principal authz.Principal, siteID int32) error {
	return s.store.Tx(ctx, func(tx pgx.Tx) error {
		var id int64
		err := tx.QueryRow(ctx, `update dashboards set is_default=false where site_id=$1 and is_default returning id`, siteID).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		return audit.Write(ctx, tx, principal, "dashboard.default_cleared", "dashboard", id, map[string]any{"site_id": siteID})
	})
}

func truncateRunes(value string, limit int) string {
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	return string([]rune(value)[:limit])
}

func nameError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == nameConstraint {
		return ErrNameTaken
	}
	return err
}
