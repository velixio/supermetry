// Package cohorts stores saved audience definitions. A cohort keeps only its
// conditions; membership is evaluated on demand by the query engine against
// retained data, so nothing person-level is persisted here.
package cohorts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"supermetry/internal/api"
	"supermetry/internal/platform/audit"
	"supermetry/internal/platform/authz"
	"supermetry/internal/platform/store"
	"supermetry/internal/query"
)

// MaxPerSite bounds saved cohorts so listing and the filter picker stay small.
const MaxPerSite = 100

const (
	maxNameRunes   = 80
	nameConstraint = "cohorts_site_id_name_lower_key"
)

var (
	// ErrNotFound means no cohort has the given id.
	ErrNotFound = errors.New("cohort not found")
	// ErrNameTaken means the site already has a cohort with this name.
	ErrNameTaken = errors.New("a cohort with this name already exists")
	// ErrLimit means the site reached MaxPerSite.
	ErrLimit = fmt.Errorf("a site can keep at most %d cohorts", MaxPerSite)
	// ErrInvalid wraps problems the caller can fix.
	ErrInvalid = api.ErrCohortInput
)

// Service reads and writes the cohorts table.
type Service struct{ store *store.Store }

// New returns a service backed by data.
func New(data *store.Store) *Service { return &Service{store: data} }

const selectColumns = `id,site_id,name,definition,created_by,created_at,updated_at`

type scanner func(...any) error

func scan(fn scanner) (api.Cohort, error) {
	var cohort api.Cohort
	var definition []byte
	if err := fn(&cohort.Id, &cohort.SiteId, &cohort.Name, &definition, &cohort.CreatedBy, &cohort.CreatedAt, &cohort.UpdatedAt); err != nil {
		return api.Cohort{}, err
	}
	if err := json.Unmarshal(definition, &cohort.Definition); err != nil {
		return api.Cohort{}, fmt.Errorf("decode cohort definition: %w", err)
	}
	return cohort, nil
}

// ValidateName trims and checks a cohort name.
func ValidateName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > maxNameRunes || strings.ContainsRune(name, 0) {
		return "", fmt.Errorf("%w: name must be 1 to %d characters", ErrInvalid, maxNameRunes)
	}
	return name, nil
}

// List returns a site's cohorts ordered by name.
func (s *Service) List(ctx context.Context, siteID int32) ([]api.Cohort, error) {
	rows, err := s.store.Pool.Query(ctx, `select `+selectColumns+` from cohorts where site_id=$1 order by lower(name),id limit $2`, siteID, MaxPerSite)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]api.Cohort, 0)
	for rows.Next() {
		cohort, err := scan(rows.Scan)
		if err != nil {
			return nil, err
		}
		result = append(result, cohort)
	}
	return result, rows.Err()
}

// Get returns one cohort.
func (s *Service) Get(ctx context.Context, id int64) (api.Cohort, error) {
	cohort, err := scan(func(dest ...any) error {
		return s.store.Pool.QueryRow(ctx, `select `+selectColumns+` from cohorts where id=$1`, id).Scan(dest...)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return api.Cohort{}, ErrNotFound
	}
	return cohort, err
}

// Create saves a new cohort and records an audit entry.
func (s *Service) Create(ctx context.Context, principal authz.Principal, siteID int32, name string, definition api.CohortDefinition) (api.Cohort, error) {
	name, err := ValidateName(name)
	if err != nil {
		return api.Cohort{}, err
	}
	normalized, err := query.NormalizeCohortDefinition(definition)
	if err != nil {
		return api.Cohort{}, err
	}
	return s.insert(ctx, principal, siteID, name, normalized, "cohort.created", nil)
}

func (s *Service) insert(ctx context.Context, principal authz.Principal, siteID int32, name string, definition api.CohortDefinition, action string, extra map[string]any) (api.Cohort, error) {
	encoded, err := json.Marshal(definition)
	if err != nil {
		return api.Cohort{}, err
	}
	var created api.Cohort
	err = s.store.Tx(ctx, func(tx pgx.Tx) error {
		var count int
		if err := tx.QueryRow(ctx, `select count(*) from cohorts where site_id=$1`, siteID).Scan(&count); err != nil {
			return err
		}
		if count >= MaxPerSite {
			return ErrLimit
		}
		var scanErr error
		created, scanErr = scan(func(dest ...any) error {
			return tx.QueryRow(ctx, `insert into cohorts(site_id,name,definition,created_by) values ($1,$2,$3,nullif($4,0)) returning `+selectColumns,
				siteID, name, encoded, principal.UserID).Scan(dest...)
		})
		if scanErr != nil {
			return scanErr
		}
		metadata := map[string]any{"site_id": siteID}
		for key, value := range extra {
			metadata[key] = value
		}
		return audit.Write(ctx, tx, principal, action, "cohort", created.Id, metadata)
	})
	return created, nameError(err)
}

// Update renames and redefines a cohort.
func (s *Service) Update(ctx context.Context, principal authz.Principal, id int64, name string, definition api.CohortDefinition) (api.Cohort, error) {
	name, err := ValidateName(name)
	if err != nil {
		return api.Cohort{}, err
	}
	normalized, err := query.NormalizeCohortDefinition(definition)
	if err != nil {
		return api.Cohort{}, err
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return api.Cohort{}, err
	}
	var updated api.Cohort
	err = s.store.Tx(ctx, func(tx pgx.Tx) error {
		var scanErr error
		updated, scanErr = scan(func(dest ...any) error {
			return tx.QueryRow(ctx, `update cohorts set name=$2,definition=$3,updated_at=now() where id=$1 returning `+selectColumns, id, name, encoded).Scan(dest...)
		})
		if errors.Is(scanErr, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if scanErr != nil {
			return scanErr
		}
		return audit.Write(ctx, tx, principal, "cohort.updated", "cohort", id, map[string]any{"site_id": updated.SiteId})
	})
	return updated, nameError(err)
}

// Delete removes a cohort. Saved segments and shares do not reference cohorts,
// so nothing else changes; a dashboard URL that still carries the id reports it
// as not found.
func (s *Service) Delete(ctx context.Context, principal authz.Principal, id int64) error {
	return s.store.Tx(ctx, func(tx pgx.Tx) error {
		var siteID int32
		err := tx.QueryRow(ctx, `delete from cohorts where id=$1 returning site_id`, id).Scan(&siteID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		return audit.Write(ctx, tx, principal, "cohort.deleted", "cohort", id, map[string]any{"site_id": siteID})
	})
}

// Duplicate copies a cohort as "<name> (copy)", numbering the copy when taken.
func (s *Service) Duplicate(ctx context.Context, principal authz.Principal, id int64) (api.Cohort, error) {
	source, err := s.Get(ctx, id)
	if err != nil {
		return api.Cohort{}, err
	}
	for attempt := 1; attempt <= 20; attempt++ {
		suffix := " (copy)"
		if attempt > 1 {
			suffix = " (copy " + strconv.Itoa(attempt) + ")"
		}
		name := truncateRunes(source.Name, maxNameRunes-utf8.RuneCountInString(suffix)) + suffix
		copied, err := s.insert(ctx, principal, source.SiteId, name, source.Definition, "cohort.duplicated", map[string]any{"source_id": id})
		if errors.Is(err, ErrNameTaken) {
			continue
		}
		return copied, err
	}
	return api.Cohort{}, ErrNameTaken
}

func truncateRunes(value string, limit int) string {
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit])
}

func nameError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == nameConstraint {
		return ErrNameTaken
	}
	return err
}
