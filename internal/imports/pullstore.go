package imports

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// pullRow is the persisted state of one pull import.
type pullRow struct {
	ImportID int64
	SiteID   int32
	OrgID    int64
	Status   Status
	Request  PullRequest
	Sealed   []byte
	Range    DayRange
	Cursor   *time.Time
}

// CreatePull stores a pending import and its pull row in one transaction. The
// sealed credentials are the API key or token_auth, encrypted by the caller.
func (s *Store) CreatePull(ctx context.Context, siteID int32, createdBy int64, request PullRequest, sealed []byte, total int) (Import, error) {
	config, err := json.Marshal(request)
	if err != nil {
		return Import{}, err
	}
	rng, err := request.Range()
	if err != nil {
		return Import{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Import{}, err
	}
	defer tx.Rollback(context.Background())
	var id int64
	if err := tx.QueryRow(ctx, `
		insert into imports(site_id,source,file_name,created_by)
		values ($1,$2,$3,$4) returning id`, siteID, request.Source(), request.FileLabel(), createdBy).Scan(&id); err != nil {
		return Import{}, err
	}
	var connection any
	if request.ConnectionID > 0 {
		connection = request.ConnectionID
	}
	var credentials any
	if len(sealed) > 0 {
		credentials = sealed
	}
	if _, err := tx.Exec(ctx, `
		insert into import_pulls(import_id,method,config,credentials,ga4_connection_id,range_from,range_to,total_days)
		values ($1,$2,$3,$4,$5,$6,$7,$8)`, id, request.Method, config, credentials, connection, rng.From, rng.To, total); err != nil {
		return Import{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Import{}, err
	}
	return s.Get(ctx, id)
}

func (s *Store) loadPull(ctx context.Context, id int64) (pullRow, error) {
	var row pullRow
	var config []byte
	err := s.pool.QueryRow(ctx, `
		select i.id,i.site_id,st.org_id,i.status,p.config,p.credentials,p.range_from,p.range_to,p.cursor_day
		from imports i
		join import_pulls p on p.import_id=i.id
		join sites st on st.id=i.site_id
		where i.id=$1`, id).Scan(&row.ImportID, &row.SiteID, &row.OrgID, &row.Status, &config, &row.Sealed,
		&row.Range.From, &row.Range.To, &row.Cursor)
	if errors.Is(err, pgx.ErrNoRows) {
		return row, ErrNotFound
	}
	if err != nil {
		return row, err
	}
	if err := json.Unmarshal(config, &row.Request); err != nil {
		return row, fmt.Errorf("decode pull request: %w", err)
	}
	return row, nil
}

// AppendPullChunk stores one fetched chunk and advances the cursor in one
// transaction. Re-storing a chunk first removes its days, so a retry after a
// crash between fetch and store never duplicates rows. It returns ErrNotFound
// when the import was deleted (undone) or is no longer running.
func (s *Store) AppendPullChunk(ctx context.Context, id int64, chunk DayRange, aggregate Aggregate, stats ParseStats) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	var siteID int32
	err = tx.QueryRow(ctx, `select site_id from imports where id=$1 and status='running' for update`, id).Scan(&siteID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	for _, table := range []string{"imported_day", "imported_day_dim"} {
		if _, err := tx.Exec(ctx, `delete from `+table+` where import_id=$1 and day between $2 and $3`, id, chunk.From, chunk.To); err != nil {
			return err
		}
	}
	if _, err := insertAggregate(ctx, tx, siteID, id, aggregate); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		update import_pulls set cursor_day=$2,
		       done_days=least(total_days, (select count(*) from generate_series(range_from,$2::date,'1 day'))),
		       rows_done=rows_done+$3,bad_rows=bad_rows+$4,updated_at=now()
		where import_id=$1`, id, chunk.To, stats.Rows, stats.BadRows); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// FinishPull marks a fully fetched pull completed, makes its days visible to
// queries (the status flip) and invalidates caches and closed-day versions.
func (s *Store) FinishPull(ctx context.Context, id int64) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	var siteID int32
	var rows, bad int64
	err = tx.QueryRow(ctx, `
		select i.site_id,p.rows_done,p.bad_rows from imports i join import_pulls p on p.import_id=i.id
		where i.id=$1 and i.status='running' for update of i`, id).Scan(&siteID, &rows, &bad)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	days, err := importDays(ctx, tx, id)
	if err != nil {
		return err
	}
	var from, to *time.Time
	if err := tx.QueryRow(ctx, `
		select min(day),max(day) from (
			select day from imported_day where import_id=$1
			union all select day from imported_day_dim where import_id=$1) d`, id).Scan(&from, &to); err != nil {
		return err
	}
	var warning *string
	switch {
	case from == nil:
		text := "the remote service returned no data for this range"
		warning = &text
	case bad > 0:
		text := fmt.Sprintf("%d invalid or skipped rows", bad)
		warning = &text
	}
	if _, err := tx.Exec(ctx, `
		update imports set status='completed',date_from=$2,date_to=$3,rows=$4,error=$5,completed_at=now()
		where id=$1`, id, from, to, rows, warning); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `update import_pulls set credentials=null,updated_at=now() where import_id=$1`, id); err != nil {
		return err
	}
	if err := bumpCloseVersions(ctx, tx, siteID, days); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	s.invalidate(siteID)
	return nil
}

// RecoverInterruptedPulls returns pull imports a previous process left
// pending or running, reset to pending so they can be queued again. Their
// stored cursor makes the run continue after the last completed chunk.
func (s *Store) RecoverInterruptedPulls(ctx context.Context) ([]int64, error) {
	rows, err := s.pool.Query(ctx, `
		update imports set status='pending'
		where status in ('pending','running')
		  and exists (select 1 from import_pulls p where p.import_id=imports.id)
		returning id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
