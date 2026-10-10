// Package datapurge deletes stored traffic that matches a date range and a
// source and/or country, then rebuilds the affected closed days. It is the
// tool for removing referrer spam that already got through.
package datapurge

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/closer"
	"supermetry/internal/dict"
	"supermetry/internal/platform/audit"
	"supermetry/internal/platform/authz"
	"supermetry/internal/scheduler"
)

const (
	batchSize     = 10_000
	settleDelay   = 2 * time.Minute
	staleAfter    = 10 * time.Minute
	maxRangeDays  = 366
	maxAttempts   = 5
	sourceMaxLen  = 253
	statusPending = "pending"
	statusRunning = "running"
	statusSettle  = "settling"
	statusDone    = "done"
	statusFailed  = "failed"
)

var (
	// ErrInvalid means the request cannot be accepted as given.
	ErrInvalid = errors.New("invalid data purge request")
	// ErrBusy means the site already has an unfinished purge.
	ErrBusy = errors.New("a data purge is already running for this site")
)

// Request is one purge request as stored.
type Request struct {
	ID              int64      `json:"id"`
	SiteID          int32      `json:"site_id"`
	From            string     `json:"from"`
	To              string     `json:"to"`
	Source          string     `json:"source"`
	Country         string     `json:"country"`
	Status          string     `json:"status"`
	SessionsDeleted int64      `json:"sessions_deleted"`
	Error           *string    `json:"error,omitempty"`
	RequestedBy     string     `json:"requested_by"`
	CreatedAt       time.Time  `json:"created_at"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
}

// Input describes what to delete. From and To are site-local calendar days,
// inclusive. At least one of Source and Country is required: a purge of a whole
// range is deliberately not possible here.
type Input struct {
	SiteID      int32
	From, To    time.Time
	Source      string
	Country     string
	RequestedBy string
}

func (in Input) normalized() (Input, error) {
	in.Source = strings.TrimSpace(in.Source)
	in.Country = strings.ToUpper(strings.TrimSpace(in.Country))
	if in.From.IsZero() || in.To.IsZero() || in.From.After(in.To) {
		return in, fmt.Errorf("%w: from must be on or before to", ErrInvalid)
	}
	if in.To.Sub(in.From) > maxRangeDays*24*time.Hour {
		return in, fmt.Errorf("%w: the range may cover at most %d days", ErrInvalid, maxRangeDays)
	}
	if in.Source == "" && in.Country == "" {
		return in, fmt.Errorf("%w: give a source, a country, or both", ErrInvalid)
	}
	if len(in.Source) > sourceMaxLen {
		return in, fmt.Errorf("%w: source is too long", ErrInvalid)
	}
	if in.Country != "" && (len(in.Country) != 2 || in.Country[0] < 'A' || in.Country[0] > 'Z' || in.Country[1] < 'A' || in.Country[1] > 'Z') {
		return in, fmt.Errorf("%w: country must be a two-letter code", ErrInvalid)
	}
	return in, nil
}

// Create records a request for the worker to carry out.
func Create(ctx context.Context, pool *pgxpool.Pool, in Input) (Request, error) {
	in, err := in.normalized()
	if err != nil {
		return Request{}, err
	}
	var request Request
	err = pool.QueryRow(ctx, `
		insert into data_purge_requests(site_id,from_day,to_day,source,country,requested_by)
		values ($1,$2,$3,$4,$5,$6)
		returning id,site_id,to_char(from_day,'YYYY-MM-DD'),to_char(to_day,'YYYY-MM-DD'),source,country,status,sessions_deleted,last_error,requested_by,created_at,completed_at`,
		in.SiteID, in.From, in.To, in.Source, in.Country, in.RequestedBy).
		Scan(&request.ID, &request.SiteID, &request.From, &request.To, &request.Source, &request.Country, &request.Status,
			&request.SessionsDeleted, &request.Error, &request.RequestedBy, &request.CreatedAt, &request.CompletedAt)
	if err != nil {
		var pgErr interface{ SQLState() string }
		if errors.As(err, &pgErr) && pgErr.SQLState() == "23505" {
			return Request{}, ErrBusy
		}
		return Request{}, fmt.Errorf("record data purge: %w", err)
	}
	return request, nil
}

// List returns the most recent requests for a site, newest first.
func List(ctx context.Context, pool *pgxpool.Pool, siteID int32, limit int) ([]Request, error) {
	rows, err := pool.Query(ctx, `
		select id,site_id,to_char(from_day,'YYYY-MM-DD'),to_char(to_day,'YYYY-MM-DD'),source,country,status,sessions_deleted,last_error,requested_by,created_at,completed_at
		from data_purge_requests where site_id=$1 order by id desc limit $2`, siteID, limit)
	if err != nil {
		return nil, fmt.Errorf("list data purges: %w", err)
	}
	defer rows.Close()
	out := []Request{}
	for rows.Next() {
		var request Request
		if err := rows.Scan(&request.ID, &request.SiteID, &request.From, &request.To, &request.Source, &request.Country, &request.Status,
			&request.SessionsDeleted, &request.Error, &request.RequestedBy, &request.CreatedAt, &request.CompletedAt); err != nil {
			return nil, fmt.Errorf("scan data purge: %w", err)
		}
		out = append(out, request)
	}
	return out, rows.Err()
}

// Worker carries out purge requests. Every phase is idempotent so another
// process can resume after a crash.
type Worker struct {
	pool           *pgxpool.Pool
	invalidateSite func(int32)
	now            func() time.Time
}

func NewWorker(pool *pgxpool.Pool, invalidateSite func(int32)) *Worker {
	return &Worker{pool: pool, invalidateSite: invalidateSite, now: time.Now}
}

func (w *Worker) Job() scheduler.Job {
	return scheduler.Job{Name: "data-purge", Every: 5 * time.Second, RunOnStart: true, Run: w.RunOnce}
}

type claimed struct {
	id          int64
	siteID      int32
	from, to    time.Time
	source      string
	country     string
	status      string
	attempts    int
	requestedBy string
}

// RunOnce claims and processes at most one request.
func (w *Worker) RunOnce(ctx context.Context) error {
	request, ok, err := w.claim(ctx)
	if err != nil || !ok {
		return err
	}
	if err := w.process(ctx, request); err != nil {
		slog.ErrorContext(ctx, "data purge failed", "purge_id", request.id, "site_id", request.siteID, "error", err)
		return w.fail(ctx, request, err)
	}
	return nil
}

func (w *Worker) claim(ctx context.Context) (claimed, bool, error) {
	var c claimed
	err := w.pool.QueryRow(ctx, `
		update data_purge_requests r set status=case when r.status='settling' then 'settling' else 'running' end,
			attempts=r.attempts+1, updated_at=now()
		where r.id=(
			select id from data_purge_requests
			where (status='pending')
			   or (status='running' and updated_at < now() - make_interval(secs => $1))
			   or (status='settling' and settle_after <= now())
			order by id for update skip locked limit 1)
		returning r.id,r.site_id,r.from_day,r.to_day,r.source,r.country,r.status,r.attempts,r.requested_by`,
		staleAfter.Seconds()).Scan(&c.id, &c.siteID, &c.from, &c.to, &c.source, &c.country, &c.status, &c.attempts, &c.requestedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return claimed{}, false, nil
	}
	if err != nil {
		return claimed{}, false, fmt.Errorf("claim data purge: %w", err)
	}
	return c, true, nil
}

func (w *Worker) fail(ctx context.Context, request claimed, cause error) error {
	status := statusPending
	if request.attempts >= maxAttempts {
		status = statusFailed
	}
	if request.status == statusSettle && status == statusPending {
		status = statusSettle
	}
	_, err := w.pool.Exec(ctx, `
		update data_purge_requests set status=$2,last_error=$3,updated_at=now(),
			settle_after=case when $2='settling' then now() else settle_after end
		where id=$1`, request.id, status, cause.Error())
	return err
}

func (w *Worker) process(ctx context.Context, request claimed) error {
	settling := request.status == statusSettle
	if err := w.collect(ctx, request); err != nil {
		return err
	}
	if err := w.deleteRows(ctx, request); err != nil {
		return err
	}
	if !settling {
		// Events that were still in flight when the purge started can land
		// after the first pass. A second pass after a short wait removes them.
		_, err := w.pool.Exec(ctx, `
			update data_purge_requests set status='settling',settle_after=$2,last_error=null,updated_at=now() where id=$1`,
			request.id, w.now().Add(settleDelay))
		return err
	}
	if err := w.rebuildDays(ctx, request); err != nil {
		return err
	}
	if err := w.finish(ctx, request); err != nil {
		return err
	}
	if w.invalidateSite != nil {
		w.invalidateSite(request.siteID)
	}
	return nil
}

// collect records the sessions that match the request. It is repeatable: later
// passes add sessions that arrived since the first one.
func (w *Worker) collect(ctx context.Context, request claimed) error {
	var sourceIDs []int32
	if request.source != "" {
		rows, err := w.pool.Query(ctx, `select id from dict where kind=$1 and lower(value)=lower($2)`, int16(dict.Source), request.source)
		if err != nil {
			return fmt.Errorf("look up source: %w", err)
		}
		for rows.Next() {
			var id int32
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			sourceIDs = append(sourceIDs, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if len(sourceIDs) == 0 {
			return nil
		}
	}
	filterSource := request.source != ""
	for _, query := range []string{`
		insert into data_purge_sessions(request_id,session_id,visitor_id,local_day,first_ts,last_ts)
		select $1,s.session_id,s.visitor_id,coalesce(s.local_day,(s.start at time zone 'UTC')::date),s.start,s.last
		from sessions s
		where s.site_id=$2 and coalesce(s.local_day,(s.start at time zone 'UTC')::date) between $3 and $4
		  and (not $5 or s.source_id = any($6::int[])) and ($7='' or s.country=$7)
		on conflict do nothing`, `
		insert into data_purge_sessions(request_id,session_id,visitor_id,local_day,first_ts,last_ts)
		select $1,s.session_id,s.visitor_id,s.day,coalesce(s.started_at,s.day::timestamptz),
		       coalesce(s.started_at,s.day::timestamptz)+make_interval(secs => coalesce(s.duration_ms,0)/1000.0)
		from sessions_closed s
		where s.site_id=$2 and s.day between $3 and $4
		  and (not $5 or s.source_id = any($6::int[])) and ($7='' or s.country=$7)
		on conflict do nothing`} {
		if _, err := w.pool.Exec(ctx, query, request.id, request.siteID, request.from, request.to, filterSource, sourceIDs, request.country); err != nil {
			return fmt.Errorf("collect sessions: %w", err)
		}
	}
	return nil
}

func (w *Worker) deleteRows(ctx context.Context, request claimed) error {
	deletes := []struct{ name, sql string }{
		{"events", `with target as (
				select e.tableoid,e.ctid from events e
				where e.site_id=$1 and exists (
					select 1 from data_purge_sessions k where k.request_id=$2 and k.session_id=e.session_id
					and e.ts between k.first_ts-interval '1 day' and k.last_ts+interval '1 day')
				limit $3
			) delete from events e using target t where e.tableoid=t.tableoid and e.ctid=t.ctid`},
		{"sessions", `with target as (
				select s.tableoid,s.ctid from sessions s where s.site_id=$1 and exists (
					select 1 from data_purge_sessions k where k.request_id=$2 and k.session_id=s.session_id
					and s.start between k.first_ts-interval '1 day' and k.last_ts)
				limit $3
			) delete from sessions s using target t where s.tableoid=t.tableoid and s.ctid=t.ctid`},
		{"closed sessions", `with target as (
				select s.tableoid,s.ctid from sessions_closed s where s.site_id=$1 and exists (
					select 1 from data_purge_sessions k where k.request_id=$2 and k.session_id=s.session_id
					and s.day between k.local_day-1 and k.local_day+1)
				limit $3
			) delete from sessions_closed s using target t where s.tableoid=t.tableoid and s.ctid=t.ctid`},
		{"verified sessions", `with target as (
				select s.tableoid,s.ctid from verified_page_sessions s where s.site_id=$1 and exists (
					select 1 from data_purge_sessions k where k.request_id=$2 and k.session_id=s.session_id
					and s.day between k.local_day-1 and k.local_day+1)
				limit $3
			) delete from verified_page_sessions s using target t where s.tableoid=t.tableoid and s.ctid=t.ctid`},
		{"closed custom events", `with target as (
				select c.tableoid,c.ctid from custom_closed c where c.site_id=$1 and exists (
					select 1 from data_purge_sessions k where k.request_id=$2 and k.session_id=c.session_id
					and c.day between k.local_day-1 and k.local_day+1)
				limit $3
			) delete from custom_closed c using target t where c.tableoid=t.tableoid and c.ctid=t.ctid`},
	}
	for _, deletion := range deletes {
		for {
			var deleted int64
			err := pgx.BeginFunc(ctx, w.pool, func(tx pgx.Tx) error {
				if err := closer.LockRollupRefresh(ctx, tx); err != nil {
					return fmt.Errorf("lock rollup refresh: %w", err)
				}
				tag, err := tx.Exec(ctx, deletion.sql, request.siteID, request.id, batchSize)
				if err != nil {
					return fmt.Errorf("delete %s: %w", deletion.name, err)
				}
				deleted = tag.RowsAffected()
				return nil
			})
			if err != nil {
				return err
			}
			if _, err := w.pool.Exec(ctx, `update data_purge_requests set updated_at=now() where id=$1`, request.id); err != nil {
				return err
			}
			if deleted < batchSize {
				break
			}
		}
	}
	return nil
}

func (w *Worker) rebuildDays(ctx context.Context, request claimed) error {
	rows, err := w.pool.Query(ctx, `
		insert into dirty_day(site_id,day,reason)
		select distinct $2::integer, closed.day, 'data_purge'
		from day_close closed
		where closed.site_id=$2 and closed.status='done' and exists (
			select 1 from data_purge_sessions affected
			where affected.request_id=$1
			  and closed.day between affected.local_day-1 and affected.local_day+1
		)
		on conflict (site_id,day) do update set reason=excluded.reason
		returning day`, request.id, request.siteID)
	if err != nil {
		return fmt.Errorf("mark purge days dirty: %w", err)
	}
	var days []time.Time
	for rows.Next() {
		var day time.Time
		if err := rows.Scan(&day); err != nil {
			rows.Close()
			return err
		}
		days = append(days, day)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, day := range days {
		if _, err := closer.CloseDay(ctx, w.pool, []int32{request.siteID}, day); err != nil {
			return fmt.Errorf("rebuild day %s: %w", day.Format(time.DateOnly), err)
		}
	}
	return nil
}

func (w *Worker) finish(ctx context.Context, request claimed) error {
	return pgx.BeginFunc(ctx, w.pool, func(tx pgx.Tx) error {
		var deleted int64
		if err := tx.QueryRow(ctx, `select count(distinct session_id) from data_purge_sessions where request_id=$1`, request.id).Scan(&deleted); err != nil {
			return err
		}
		var orgID int64
		err := tx.QueryRow(ctx, `
			update data_purge_requests r set status='done',settle_after=null,last_error=null,
				sessions_deleted=$2,updated_at=now(),completed_at=now()
			from sites s where r.id=$1 and r.site_id=s.id and r.status <> 'done'
			returning s.org_id`, request.id, deleted).Scan(&orgID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("mark data purge done: %w", err)
		}
		if _, err := tx.Exec(ctx, `delete from data_purge_sessions where request_id=$1`, request.id); err != nil {
			return err
		}
		principal, err := requestedPrincipal(request.requestedBy, orgID)
		if err != nil {
			return err
		}
		return audit.Write(ctx, tx, principal, "site.data_purged", "site", int64(request.siteID), map[string]any{
			"purge_id": request.id, "from": request.from.Format(time.DateOnly), "to": request.to.Format(time.DateOnly),
			"source": request.source, "country": request.country, "sessions_deleted": deleted,
		})
	})
}

func requestedPrincipal(requestedBy string, orgID int64) (authz.Principal, error) {
	kind, rawID, ok := strings.Cut(requestedBy, ":")
	id, err := strconv.ParseInt(rawID, 10, 64)
	if !ok || err != nil || id <= 0 {
		return authz.Principal{}, fmt.Errorf("invalid purge requester %q", requestedBy)
	}
	principal := authz.Principal{OrgID: orgID}
	switch kind {
	case "user":
		principal.UserID = id
	case "api_key":
		principal.KeyID = id
	default:
		return authz.Principal{}, fmt.Errorf("invalid purge requester %q", requestedBy)
	}
	return principal, nil
}
