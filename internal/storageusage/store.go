package storageusage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrRunNotActive = errors.New("storage usage run is not active")
var ErrManifestImmutable = errors.New("storage usage run manifest is immutable")

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) BeginOrResume(ctx context.Context, version int, now time.Time) (Run, error) {
	return s.beginOrResume(ctx, version, "manual-v1", now, true)
}

func (s *Store) beginOrResume(ctx context.Context, version int, fingerprint string, now time.Time, loadSites bool) (Run, error) {
	if version < 1 {
		return Run{}, fmt.Errorf("measurement version must be positive")
	}
	if fingerprint == "" {
		return Run{}, fmt.Errorf("work fingerprint is required")
	}
	var id int64
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtext('supermetry:storage-usage-run:' || current_schema()))`); err != nil {
			return fmt.Errorf("lock reconciliation run: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			update storage_usage_runs
			set status='abandoned', updated_at=$2, last_error='measurement version or work registry changed'
			where status='active' and (measurement_version<>$1 or work_fingerprint<>$3)`, version, now, fingerprint); err != nil {
			return fmt.Errorf("abandon incompatible reconciliation run: %w", err)
		}
		err := tx.QueryRow(ctx, `select id from storage_usage_runs where status='active'`).Scan(&id)
		if err == nil {
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("load active reconciliation run: %w", err)
		}
		if err := tx.QueryRow(ctx, `
			insert into storage_usage_runs(status,measurement_version,work_fingerprint,started_at,updated_at)
			values('active',$1,$3,$2,$2) returning id`, version, now, fingerprint).Scan(&id); err != nil {
			return fmt.Errorf("create reconciliation run: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			insert into storage_usage_run_sites(run_id,site_id)
			select $1,id from sites where erased_at is null`, id); err != nil {
			return fmt.Errorf("capture reconciliation sites: %w", err)
		}
		return nil
	})
	if err != nil {
		return Run{}, err
	}
	return s.loadRun(ctx, id, loadSites)
}

func (s *Store) SaveManifest(ctx context.Context, runID int64, units []WorkUnit) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var active, ready bool
		if err := tx.QueryRow(ctx, `select status='active',manifest_ready from storage_usage_runs where id=$1 for update`, runID).Scan(&active, &ready); err != nil {
			return fmt.Errorf("lock reconciliation manifest: %w", err)
		}
		if !active {
			return ErrRunNotActive
		}
		if ready {
			existing, err := loadManifest(ctx, tx, runID)
			if err != nil {
				return err
			}
			if manifestsEqual(existing, units) {
				return nil
			}
			return ErrManifestImmutable
		}
		for i, unit := range units {
			payload, err := json.Marshal(unit)
			if err != nil {
				return fmt.Errorf("encode work unit %q: %w", unit.Key, err)
			}
			if _, err := tx.Exec(ctx, `insert into storage_usage_run_work(run_id,ordinal,source_key,work) values($1,$2,$3,$4)`, runID, i, unit.Key, payload); err != nil {
				return fmt.Errorf("save work unit %q: %w", unit.Key, err)
			}
		}
		_, err := tx.Exec(ctx, `update storage_usage_runs set manifest_ready=true where id=$1`, runID)
		return err
	})
}

func (s *Store) LoadManifest(ctx context.Context, runID int64) ([]WorkUnit, bool, error) {
	var ready bool
	if err := s.pool.QueryRow(ctx, `select manifest_ready from storage_usage_runs where id=$1`, runID).Scan(&ready); err != nil {
		return nil, false, fmt.Errorf("load manifest state: %w", err)
	}
	if !ready {
		return nil, false, nil
	}
	units, err := loadManifest(ctx, s.pool, runID)
	return units, true, err
}

type manifestQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func loadManifest(ctx context.Context, q manifestQuerier, runID int64) ([]WorkUnit, error) {
	rows, err := q.Query(ctx, `select work from storage_usage_run_work where run_id=$1 order by ordinal`, runID)
	if err != nil {
		return nil, fmt.Errorf("load work manifest: %w", err)
	}
	defer rows.Close()
	var units []WorkUnit
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var unit WorkUnit
		if err := json.Unmarshal(payload, &unit); err != nil {
			return nil, fmt.Errorf("decode work manifest: %w", err)
		}
		units = append(units, unit)
	}
	return units, rows.Err()
}

func manifestsEqual(a, b []WorkUnit) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (s *Store) LoadRun(ctx context.Context, id int64) (Run, error) {
	return s.loadRun(ctx, id, true)
}

func (s *Store) loadRun(ctx context.Context, id int64, loadSites bool) (Run, error) {
	var run Run
	err := s.pool.QueryRow(ctx, `
		select id,status,measurement_version,cursor,started_at,updated_at,completed_at,last_error
		from storage_usage_runs where id=$1`, id).Scan(
		&run.ID, &run.Status, &run.MeasurementVersion, &run.Cursor, &run.StartedAt,
		&run.UpdatedAt, &run.CompletedAt, &run.LastError,
	)
	if err != nil {
		return Run{}, fmt.Errorf("load reconciliation run: %w", err)
	}
	if !loadSites {
		return run, nil
	}
	rows, err := s.pool.Query(ctx, `select site_id from storage_usage_run_sites where run_id=$1 order by site_id`, id)
	if err != nil {
		return Run{}, fmt.Errorf("load reconciliation sites: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var siteID int32
		if err := rows.Scan(&siteID); err != nil {
			return Run{}, fmt.Errorf("scan reconciliation site: %w", err)
		}
		run.SiteIDs = append(run.SiteIDs, siteID)
	}
	if err := rows.Err(); err != nil {
		return Run{}, fmt.Errorf("iterate reconciliation sites: %w", err)
	}
	return run, nil
}

func (s *Store) ActiveRun(ctx context.Context) (*Run, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `select id from storage_usage_runs where status='active'`).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load active reconciliation run: %w", err)
	}
	run, err := s.loadRun(ctx, id, false)
	if err != nil {
		return nil, err
	}
	return &run, nil
}

func (s *Store) ReplaceSource(ctx context.Context, runID int64, sourceKey string, category Category, values []Contribution) error {
	return s.ReplaceMeasurements(ctx, runID, sourceKey, []Measurement{{Category: category, Values: values}})
}

func (s *Store) ReplaceMeasurements(ctx context.Context, runID int64, sourceKey string, measurements []Measurement) error {
	if sourceKey == "" || len(sourceKey) > 512 {
		return fmt.Errorf("invalid source key")
	}
	categories := make(map[Category]struct{}, len(measurements))
	for _, measurement := range measurements {
		if !measurement.Category.valid() {
			return fmt.Errorf("invalid category %q", measurement.Category)
		}
		if _, exists := categories[measurement.Category]; exists {
			return fmt.Errorf("duplicate category %q", measurement.Category)
		}
		categories[measurement.Category] = struct{}{}
		seen := make(map[int32]struct{}, len(measurement.Values))
		for _, value := range measurement.Values {
			if value.Bytes < 0 {
				return fmt.Errorf("negative contribution for site %d", value.SiteID)
			}
			if _, exists := seen[value.SiteID]; exists {
				return fmt.Errorf("duplicate contribution for site %d", value.SiteID)
			}
			seen[value.SiteID] = struct{}{}
		}
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var active bool
		if err := tx.QueryRow(ctx, `select status='active' from storage_usage_runs where id=$1 for update`, runID).Scan(&active); err != nil {
			return fmt.Errorf("lock reconciliation run: %w", err)
		}
		if !active {
			return ErrRunNotActive
		}
		if _, err := tx.Exec(ctx, `delete from storage_usage_stage where run_id=$1 and source_key=$2`, runID, sourceKey); err != nil {
			return fmt.Errorf("clear source contribution: %w", err)
		}
		batch := &pgx.Batch{}
		count := 0
		for _, measurement := range measurements {
			for _, value := range measurement.Values {
				batch.Queue(`insert into storage_usage_stage(run_id,source_key,site_id,category,bytes) values($1,$2,$3,$4,$5)`, runID, sourceKey, value.SiteID, measurement.Category, value.Bytes)
				count++
			}
		}
		results := tx.SendBatch(ctx, batch)
		for range count {
			if _, err := results.Exec(); err != nil {
				_ = results.Close()
				return fmt.Errorf("insert source contribution: %w", err)
			}
		}
		if err := results.Close(); err != nil {
			return fmt.Errorf("close source contribution batch: %w", err)
		}
		return nil
	})
}

func (s *Store) Status(ctx context.Context) (Status, error) {
	var status Status
	if err := s.pool.QueryRow(ctx, `select max(completed_at) from storage_usage_runs where status='complete'`).Scan(&status.LastCompletedAt); err != nil {
		return Status{}, err
	}
	var runID int64
	var startedAt time.Time
	if err := s.pool.QueryRow(ctx, `select id,started_at from storage_usage_runs where status='active'`).Scan(&runID, &startedAt); errors.Is(err, pgx.ErrNoRows) {
		return status, nil
	} else if err != nil {
		return Status{}, err
	}
	status.ActiveStartedAt = &startedAt
	if err := s.pool.QueryRow(ctx, `select count(*) from storage_usage_run_work w join storage_usage_runs r on r.id=w.run_id where w.run_id=$1 and w.source_key collate "C" > r.cursor collate "C"`, runID).Scan(&status.Pending); err != nil {
		return Status{}, err
	}
	return status, nil
}

func (s *Store) AdvanceCursor(ctx context.Context, runID int64, cursor string, now time.Time) error {
	tag, err := s.pool.Exec(ctx, `update storage_usage_runs set cursor=$2,updated_at=$3 where id=$1 and status='active'`, runID, cursor, now)
	if err != nil {
		return fmt.Errorf("advance reconciliation cursor: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrRunNotActive
	}
	return nil
}

func (s *Store) RecordError(ctx context.Context, runID int64, message string, now time.Time) error {
	if len(message) > 2000 {
		message = message[:2000]
	}
	_, err := s.pool.Exec(ctx, `update storage_usage_runs set last_error=$2,updated_at=$3 where id=$1 and status='active'`, runID, message, now)
	if err != nil {
		return fmt.Errorf("record reconciliation error: %w", err)
	}
	return nil
}

func (s *Store) PruneSources(ctx context.Context, runID int64, sourceKeys []string) error {
	var err error
	if len(sourceKeys) == 0 {
		_, err = s.pool.Exec(ctx, `delete from storage_usage_stage where run_id=$1`, runID)
	} else {
		_, err = s.pool.Exec(ctx, `delete from storage_usage_stage where run_id=$1 and not (source_key=any($2))`, runID, sourceKeys)
	}
	if err != nil {
		return fmt.Errorf("prune stale storage sources: %w", err)
	}
	return nil
}

func (s *Store) Publish(ctx context.Context, runID int64, measuredAt time.Time) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var version int
		var status RunStatus
		if err := tx.QueryRow(ctx, `select measurement_version,status from storage_usage_runs where id=$1 for update`, runID).Scan(&version, &status); err != nil {
			return fmt.Errorf("lock reconciliation run: %w", err)
		}
		if status != RunActive {
			return ErrRunNotActive
		}
		if _, err := tx.Exec(ctx, `
			insert into site_storage_usage(
				site_id,hot_bytes,retained_bytes,auxiliary_bytes,
				replay_recording_bytes,replay_css_bytes,measured_at,measurement_version)
			select rs.site_id,
				coalesce(sum(st.bytes) filter(where st.category='hot'),0)::bigint,
				coalesce(sum(st.bytes) filter(where st.category='retained'),0)::bigint,
				coalesce(sum(st.bytes) filter(where st.category='auxiliary'),0)::bigint,
				coalesce(sum(st.bytes) filter(where st.category='replay_recording'),0)::bigint,
				coalesce(sum(st.bytes) filter(where st.category='replay_css'),0)::bigint,
				$2,$3
			from storage_usage_run_sites rs
			join sites s on s.id=rs.site_id and s.erased_at is null
			left join storage_usage_stage st on st.run_id=rs.run_id and st.site_id=rs.site_id
			where rs.run_id=$1
			group by rs.site_id
			on conflict(site_id) do update set
				hot_bytes=excluded.hot_bytes,
				retained_bytes=excluded.retained_bytes,
				auxiliary_bytes=excluded.auxiliary_bytes,
				replay_recording_bytes=excluded.replay_recording_bytes,
				replay_css_bytes=excluded.replay_css_bytes,
				measured_at=excluded.measured_at,
				measurement_version=excluded.measurement_version`, runID, measuredAt, version); err != nil {
			return fmt.Errorf("publish storage usage snapshots: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			delete from site_storage_usage u
			using storage_usage_run_sites rs,sites s
			where rs.run_id=$1 and rs.site_id=s.id and s.erased_at is not null and u.site_id=rs.site_id`, runID); err != nil {
			return fmt.Errorf("remove erased site snapshots: %w", err)
		}
		if _, err := tx.Exec(ctx, `delete from storage_usage_stage where run_id=$1`, runID); err != nil {
			return fmt.Errorf("clear published staging rows: %w", err)
		}
		if _, err := tx.Exec(ctx, `update storage_usage_runs set status='complete',completed_at=$2,updated_at=$2,last_error='' where id=$1`, runID, measuredAt); err != nil {
			return fmt.Errorf("complete reconciliation run: %w", err)
		}
		if _, err := tx.Exec(ctx, `delete from storage_usage_runs where id<>$1 and status<>'active'`, runID); err != nil {
			return fmt.Errorf("prune reconciliation metadata: %w", err)
		}
		return nil
	})
}

func (s *Store) EraseSite(ctx context.Context, siteID int32) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		for _, query := range []string{
			`delete from site_storage_usage where site_id=$1`,
			`delete from storage_usage_stage where site_id=$1`,
			`delete from storage_usage_run_sites where site_id=$1`,
		} {
			if _, err := tx.Exec(ctx, query, siteID); err != nil {
				return fmt.Errorf("erase site storage usage: %w", err)
			}
		}
		return nil
	})
}

func (s *Store) LastCompletedAt(ctx context.Context) (*time.Time, error) {
	var at *time.Time
	if err := s.pool.QueryRow(ctx, `select max(completed_at) from storage_usage_runs where status='complete'`).Scan(&at); err != nil {
		return nil, fmt.Errorf("load last completed reconciliation: %w", err)
	}
	return at, nil
}

// IsUndefinedRelation reports SQLSTATE 42P01 only. Other "does not exist"
// errors (missing columns or functions) are schema bugs and must surface.
func IsUndefinedRelation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42P01"
}
