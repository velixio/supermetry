package imports

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("import not found")

type AlreadyImportedError struct {
	ImportID   int64
	ImportedAt time.Time
}

func (e *AlreadyImportedError) Error() string { return "file was already imported" }

type Source string

const (
	SourcePlausible Source = "plausible"
	SourceUmami     Source = "umami"
	SourceGA4       Source = "ga4"
	SourceMixpanel  Source = "mixpanel"
	SourceAmplitude Source = "amplitude"
	SourceMatomo    Source = "matomo"
)

type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
)

type Import struct {
	ID          int64      `json:"id"`
	SiteID      int32      `json:"site_id"`
	Source      Source     `json:"source"`
	Status      Status     `json:"status"`
	FileName    string     `json:"file_name"`
	DateFrom    *time.Time `json:"date_from"`
	DateTo      *time.Time `json:"date_to"`
	Rows        int64      `json:"rows"`
	Error       *string    `json:"error"`
	CreatedBy   *int64     `json:"created_by"`
	CreatedAt   time.Time  `json:"created_at"`
	CompletedAt *time.Time `json:"completed_at"`
	// Method and Progress are set only for pull imports (data fetched from
	// another service's API); file imports leave them empty.
	Method   string    `json:"method,omitempty"`
	Progress *Progress `json:"progress,omitempty"`
}

type CreateInput struct {
	SiteID    int32
	Source    Source
	FileName  string
	CreatedBy int64
	FileHash  [32]byte
	Replace   bool
}

type Store struct {
	pool           *pgxpool.Pool
	invalidateSite func(int32)
}

func NewStore(pool *pgxpool.Pool, invalidators ...func(int32)) *Store {
	store := &Store{pool: pool}
	if len(invalidators) > 0 {
		store.invalidateSite = invalidators[0]
	}
	return store
}

func (s *Store) Create(ctx context.Context, input CreateInput) (Import, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Import{}, err
	}
	defer tx.Rollback(context.Background())
	var hash any
	if input.FileHash != ([32]byte{}) {
		hash = input.FileHash[:]
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock($1,hashtext(encode($2::bytea,'hex')))`, input.SiteID, hash); err != nil {
			return Import{}, err
		}
		var priorID int64
		var importedAt time.Time
		err := tx.QueryRow(ctx, `
			select id,created_at from imports
			where site_id=$1 and file_hash=$2 and status <> 'failed'
			order by created_at desc,id desc limit 1`, input.SiteID, hash).Scan(&priorID, &importedAt)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return Import{}, err
		}
		if err == nil && !input.Replace {
			return Import{}, &AlreadyImportedError{ImportID: priorID, ImportedAt: importedAt}
		}
	}
	row := tx.QueryRow(ctx, `
		insert into imports(site_id,source,file_name,created_by,file_hash,replace_existing)
		values ($1,$2,$3,$4,$5,$6)
		returning id,site_id,source,status,file_name,date_from,date_to,rows,error,
		          created_by,created_at,completed_at,
		          null::text,null::int,null::int,null::bigint,null::bigint,null::date,null::timestamptz`,
		input.SiteID, input.Source, input.FileName, input.CreatedBy, hash, input.Replace)
	created, err := scanImport(row)
	if err != nil {
		return Import{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Import{}, err
	}
	return created, nil
}

func (s *Store) Get(ctx context.Context, id int64) (Import, error) {
	value, err := scanImport(s.pool.QueryRow(ctx, `
		select `+importColumns+`
		from imports i left join import_pulls p on p.import_id=i.id where i.id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Import{}, ErrNotFound
	}
	return value, err
}

func (s *Store) List(ctx context.Context, siteID int32) ([]Import, error) {
	rows, err := s.pool.Query(ctx, `
		select `+importColumns+`
		from imports i left join import_pulls p on p.import_id=i.id
		where i.site_id=$1 order by i.created_at desc,i.id desc`, siteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Import, 0)
	for rows.Next() {
		value, err := scanImport(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (s *Store) Delete(ctx context.Context, id int64) (bool, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, err
	}
	defer tx.Rollback(context.Background())
	var siteID int32
	if err := tx.QueryRow(ctx, `select site_id from imports where id=$1 for update`, id).Scan(&siteID); errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	days, err := importDays(ctx, tx, id)
	if err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `delete from imports where id=$1`, id); err != nil {
		return false, err
	}
	if err := bumpCloseVersions(ctx, tx, siteID, days); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	s.invalidate(siteID)
	return true, nil
}

func (s *Store) SetRunning(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `
		update imports set status='running',error=null,completed_at=null
		where id=$1 and status='pending'`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SetFailed(ctx context.Context, id int64, cause error) error {
	// A failed pull never resumes, so its stored credentials are dropped
	// first: once the status reads failed the secret is already gone.
	if _, err := s.pool.Exec(ctx, `update import_pulls set credentials=null where import_id=$1`, id); err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `
		update imports set status='failed',error=$2,completed_at=now()
		where id=$1`, id, cause.Error())
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ImportLocation(ctx context.Context, id int64) (*time.Location, error) {
	var timezone string
	err := s.pool.QueryRow(ctx, `
		select s.timezone from imports i join sites s on s.id=i.site_id where i.id=$1`, id).Scan(&timezone)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, fmt.Errorf("load site timezone %q: %w", timezone, err)
	}
	return location, nil
}

// FailRunningAtStartup marks file imports a previous process left pending or
// running as failed: their jobs lived only in that process's in-memory queue.
// Pull imports are skipped because they resume from their stored cursor (see
// RecoverInterruptedPulls).
// It assumes a single application instance; a second instance starting while
// another is importing would mark that live import failed.
func (s *Store) FailRunningAtStartup(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `
		update imports set status='failed',error='import interrupted by server restart',completed_at=now()
		where status in ('pending','running')
		  and not exists (select 1 from import_pulls p where p.import_id=imports.id)`)
	return err
}

func (s *Store) Complete(ctx context.Context, id int64, aggregate Aggregate, stats ParseStats) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())

	var siteID int32
	var fileHash []byte
	var replaceExisting bool
	if err := tx.QueryRow(ctx, `select site_id,file_hash,replace_existing from imports where id=$1 for update`, id).Scan(&siteID, &fileHash, &replaceExisting); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	affectedDays, err := importDays(ctx, tx, id)
	if err != nil {
		return err
	}
	if replaceExisting && len(fileHash) != 0 {
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock($1,hashtext(encode($2::bytea,'hex')))`, siteID, fileHash); err != nil {
			return err
		}
		replacedDays, err := importDaysForFile(ctx, tx, siteID, fileHash, id)
		if err != nil {
			return err
		}
		affectedDays = append(affectedDays, replacedDays...)
		if _, err := tx.Exec(ctx, `
			delete from imports
			where site_id=$1 and file_hash=$2 and id<>$3 and status='completed'`, siteID, fileHash, id); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `delete from imported_day_dim where import_id=$1`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `delete from imported_day where import_id=$1`, id); err != nil {
		return err
	}

	inserted, err := insertAggregate(ctx, tx, siteID, id, aggregate)
	if err != nil {
		return err
	}
	affectedDays = append(affectedDays, inserted...)
	from, to := aggregateRange(aggregate)
	var warning *string
	if stats.BadRows > 0 {
		value := fmt.Sprintf("%d invalid rows skipped", stats.BadRows)
		if len(stats.Errors) > 0 {
			value += fmt.Sprintf(" (first: row %d: %s)", stats.Errors[0].Row, stats.Errors[0].Message)
		}
		warning = &value
	}
	if _, err := tx.Exec(ctx, `
		update imports set status='completed',date_from=$2,date_to=$3,rows=$4,
		                   error=$5,completed_at=now()
		where id=$1`, id, from, to, stats.Rows, warning); err != nil {
		return err
	}
	if err := bumpCloseVersions(ctx, tx, siteID, affectedDays); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	s.invalidate(siteID)
	return nil
}

func importDays(ctx context.Context, tx pgx.Tx, importID int64) ([]time.Time, error) {
	rows, err := tx.Query(ctx, `
		select day from imported_day where import_id=$1
		union
		select day from imported_day_dim where import_id=$1`, importID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var days []time.Time
	for rows.Next() {
		var day time.Time
		if err := rows.Scan(&day); err != nil {
			return nil, err
		}
		days = append(days, day)
	}
	return days, rows.Err()
}

func importDaysForFile(ctx context.Context, tx pgx.Tx, siteID int32, fileHash []byte, exceptID int64) ([]time.Time, error) {
	rows, err := tx.Query(ctx, `
		select d.day from (
			select import_id,day from imported_day
			union
			select import_id,day from imported_day_dim
		) d join imports i on i.id=d.import_id
		where i.site_id=$1 and i.file_hash=$2 and i.id<>$3 and i.status='completed'
		group by d.day`, siteID, fileHash, exceptID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var days []time.Time
	for rows.Next() {
		var day time.Time
		if err := rows.Scan(&day); err != nil {
			return nil, err
		}
		days = append(days, day)
	}
	return days, rows.Err()
}

func bumpCloseVersions(ctx context.Context, tx pgx.Tx, siteID int32, days []time.Time) error {
	if len(days) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx, `update day_close set version=version+1,closed_at=clock_timestamp()
		where site_id=$1 and day=any($2) and status='done'`, siteID, days)
	return err
}

func (s *Store) invalidate(siteID int32) {
	if s.invalidateSite != nil {
		s.invalidateSite(siteID)
	}
}

// importColumns lists the imports columns plus the pull progress columns of
// the left-joined import_pulls row (aliases i and p).
const importColumns = `i.id,i.site_id,i.source,i.status,i.file_name,i.date_from,i.date_to,i.rows,i.error,
	i.created_by,i.created_at,i.completed_at,
	p.method,p.total_days,p.done_days,p.rows_done,p.bad_rows,p.cursor_day,p.updated_at`

func scanImport(row pgx.Row) (Import, error) {
	var value Import
	var method *string
	var total, done *int32
	var rows, bad *int64
	var cursor, updated *time.Time
	err := row.Scan(&value.ID, &value.SiteID, &value.Source, &value.Status, &value.FileName,
		&value.DateFrom, &value.DateTo, &value.Rows, &value.Error, &value.CreatedBy,
		&value.CreatedAt, &value.CompletedAt, &method, &total, &done, &rows, &bad, &cursor, &updated)
	if err != nil {
		return value, err
	}
	if method != nil {
		value.Method = *method
		progress := Progress{TotalDays: int(*total), DoneDays: int(*done), Rows: *rows, BadRows: *bad}
		if cursor != nil {
			text := cursor.Format(time.DateOnly)
			progress.CursorDay = &text
		}
		if updated != nil {
			progress.UpdatedAt = *updated
		}
		value.Progress = &progress
	}
	return value, nil
}

func sortedDays(values map[time.Time]Metrics) []time.Time {
	result := make([]time.Time, 0, len(values))
	for day := range values {
		result = append(result, day)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Before(result[j]) })
	return result
}

func sortedDimensions(values map[DimensionKey]Metrics) []DimensionKey {
	result := make([]DimensionKey, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	sort.Slice(result, func(i, j int) bool {
		if !result[i].Day.Equal(result[j].Day) {
			return result[i].Day.Before(result[j].Day)
		}
		if result[i].Dim != result[j].Dim {
			return result[i].Dim < result[j].Dim
		}
		return result[i].Value < result[j].Value
	})
	return result
}

func aggregateRange(aggregate Aggregate) (*time.Time, *time.Time) {
	var from, to time.Time
	visit := func(day time.Time) {
		if from.IsZero() || day.Before(from) {
			from = day
		}
		if to.IsZero() || day.After(to) {
			to = day
		}
	}
	for day := range aggregate.Days {
		visit(day)
	}
	for key := range aggregate.Dimensions {
		visit(key.Day)
	}
	if from.IsZero() {
		return nil, nil
	}
	return &from, &to
}

// insertAggregate copies an aggregate into the import tables inside tx and
// returns every day it touched. It is the single place that maps the logical
// event dimension onto the stored layout, shared by file and pull imports.
func insertAggregate(ctx context.Context, tx pgx.Tx, siteID int32, id int64, aggregate Aggregate) ([]time.Time, error) {
	var affected []time.Time
	days := sortedDays(aggregate.Days)
	affected = append(affected, days...)
	if len(days) > 0 {
		_, err := tx.CopyFrom(ctx, pgx.Identifier{"imported_day"},
			[]string{"site_id", "import_id", "day", "visitors", "visits", "pageviews", "bounces", "visit_duration_s"},
			pgx.CopyFromSlice(len(days), func(index int) ([]any, error) {
				day := days[index]
				metrics := aggregate.Days[day]
				return []any{siteID, id, day, metrics.Visitors, metrics.Visits, metrics.Pageviews, metrics.Bounces, metrics.VisitDurationS}, nil
			}))
		if err != nil {
			return nil, fmt.Errorf("copy imported days: %w", err)
		}
	}
	dimensions := sortedDimensions(aggregate.Dimensions)
	for _, key := range dimensions {
		affected = append(affected, key.Day)
	}
	if len(dimensions) > 0 {
		_, err := tx.CopyFrom(ctx, pgx.Identifier{"imported_day_dim"},
			[]string{"site_id", "import_id", "day", "dim", "value", "visitors", "visits", "pageviews", "bounces", "visit_duration_s", "events"},
			pgx.CopyFromSlice(len(dimensions), func(index int) ([]any, error) {
				key := dimensions[index]
				metrics := aggregate.Dimensions[key]
				dim, value := key.Dim, key.Value
				if dim == DimEvent {
					dim, value = DimPage, EventValuePrefix+value
				}
				return []any{siteID, id, key.Day, dim, value, metrics.Visitors, metrics.Visits, metrics.Pageviews, metrics.Bounces, metrics.VisitDurationS, metrics.Events}, nil
			}))
		if err != nil {
			return nil, fmt.Errorf("copy imported dimensions: %w", err)
		}
	}
	return affected, nil
}
