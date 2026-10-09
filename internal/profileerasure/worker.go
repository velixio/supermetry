package profileerasure

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/closer"
	"supermetry/internal/platform/audit"
	"supermetry/internal/platform/authz"
	"supermetry/internal/replay"
	"supermetry/internal/scheduler"
)

const (
	defaultWorkerBatchSize = 10_000
	workerClaimSize        = 8
	settleDelay            = 2 * time.Minute
)

type workerClock interface {
	Now() time.Time
}

type wallWorkerClock struct{}

func (wallWorkerClock) Now() time.Time { return time.Now() }

// Worker completes durable profile erasure requests. Every database phase is
// idempotent so a different process can safely resume after a crash.
type Worker struct {
	pool      *pgxpool.Pool
	store     *Store
	blobs     replay.BlobStore
	clock     workerClock
	batchSize int
}

func NewWorker(pool *pgxpool.Pool, store *Store, blobs replay.BlobStore, clock workerClock) *Worker {
	if store == nil && pool != nil {
		store = New(pool)
	}
	if clock == nil {
		clock = wallWorkerClock{}
	}
	return &Worker{pool: pool, store: store, blobs: blobs, clock: clock, batchSize: defaultWorkerBatchSize}
}

func (w *Worker) Job() scheduler.Job {
	return scheduler.Job{Name: "profile-erasure", Every: 5 * time.Second, RunOnStart: true, Run: w.RunOnce}
}

func (w *Worker) RunOnce(ctx context.Context) error {
	if w == nil || w.pool == nil || w.store == nil {
		return errors.New("profile erasure worker is not configured")
	}
	requests, err := w.store.Claim(ctx, workerClaimSize)
	if err != nil {
		return fmt.Errorf("claim profile erasures: %w", err)
	}
	for _, request := range requests {
		if request.Status == StatusFailed {
			if err := w.auditFailure(ctx, request); err != nil {
				slog.ErrorContext(ctx, "audit failed profile erasure", "erasure_id", request.ID, "error", err)
			}
			continue
		}
		if err := w.process(ctx, request); err != nil {
			if recordErr := w.store.MarkFailure(ctx, request.ID, err); recordErr != nil {
				slog.ErrorContext(ctx, "record profile erasure failure", "erasure_id", request.ID,
					"error", errors.Join(err, recordErr))
				continue
			}
			failed, loadErr := w.store.Get(ctx, request.ID)
			if loadErr != nil {
				slog.ErrorContext(ctx, "load failed profile erasure", "erasure_id", request.ID, "error", loadErr)
				continue
			}
			if failed.Status == StatusFailed {
				if auditErr := w.auditFailure(ctx, failed); auditErr != nil {
					slog.ErrorContext(ctx, "audit failed profile erasure", "erasure_id", request.ID, "error", auditErr)
				}
			}
		}
	}
	return nil
}

func (w *Worker) auditFailure(ctx context.Context, request Request) error {
	return pgx.BeginFunc(ctx, w.pool, func(tx pgx.Tx) error {
		var orgID int64
		if err := tx.QueryRow(ctx, `select org_id from sites where id=$1`, request.SiteID).Scan(&orgID); err != nil {
			return fmt.Errorf("load erasure organization: %w", err)
		}
		principal, err := requestedPrincipal(request.RequestedBy, orgID)
		if err != nil {
			return err
		}
		metadata := map[string]any{"site_id": request.SiteID, "erasure_id": request.ID, "attempts": request.Attempts}
		if request.LastError != nil {
			metadata["error"] = *request.LastError
		}
		if err := audit.Write(ctx, tx, principal, "profile.erasure_failed", "profile", request.ProfileID, metadata); err != nil {
			return fmt.Errorf("audit failed profile erasure: %w", err)
		}
		return nil
	})
}

func (w *Worker) process(ctx context.Context, request Request) error {
	settling := request.Status == StatusSettling
	if err := w.eraseDatabaseRows(ctx, request); err != nil {
		return err
	}
	if err := w.deletePendingBlobs(ctx, request.ID); err != nil {
		return err
	}
	if _, err := w.pool.Exec(ctx, `delete from profiles where site_id=$1 and id=$2`, request.SiteID, request.ProfileID); err != nil {
		return fmt.Errorf("delete profile row: %w", err)
	}
	if !settling {
		if err := w.store.MarkSettling(ctx, request.ID, w.clock.Now().Add(settleDelay)); err != nil {
			return fmt.Errorf("mark profile erasure settling: %w", err)
		}
		return nil
	}
	return w.finish(ctx, request)
}

func (w *Worker) eraseDatabaseRows(ctx context.Context, request Request) error {
	deletes := []struct {
		name      string
		sql       string
		profileID bool
	}{
		{"profile events", `with doomed as materialized (
				select e.tableoid,e.ctid,e.session_id,e.visitor_id,
				       coalesce(e.local_day,(e.ts at time zone 'UTC')::date) as local_day,e.ts
				from events e
				where e.site_id=$1 and e.profile_id=$2
				limit $4
			), ins as (
				insert into profile_erasure_sessions(request_id,session_id,visitor_id,local_day,first_ts,last_ts)
				select $3,session_id,visitor_id,local_day,min(ts),max(ts)
				from doomed group by session_id,visitor_id,local_day
				on conflict (request_id,session_id,local_day) do update
				set first_ts=least(profile_erasure_sessions.first_ts,excluded.first_ts),
				    last_ts=greatest(profile_erasure_sessions.last_ts,excluded.last_ts)
				returning 1
			) delete from events e using doomed d
			where e.tableoid=d.tableoid and e.ctid=d.ctid`, true},
		{"session events", `with target as (
				select e.tableoid,e.ctid from events e
				where e.site_id=$1 and exists (
					select 1 from profile_erasure_sessions k where k.request_id=$2 and k.session_id=e.session_id
					and e.ts between k.first_ts-interval '1 day' and k.last_ts+interval '1 day')
				limit $3
			) delete from events e using target t where e.tableoid=t.tableoid and e.ctid=t.ctid`, false},
		{"sessions", `with target as (
				select s.tableoid,s.ctid from sessions s where s.site_id=$1 and exists (
					select 1 from profile_erasure_sessions k where k.request_id=$2 and k.session_id=s.session_id
					and s.start between k.first_ts-interval '1 day' and k.last_ts)
				limit $3
			) delete from sessions s using target t where s.tableoid=t.tableoid and s.ctid=t.ctid`, false},
		{"closed sessions", `with target as (
				select s.tableoid,s.ctid from sessions_closed s where s.site_id=$1 and exists (
					select 1 from profile_erasure_sessions k where k.request_id=$2 and k.session_id=s.session_id
					and s.day between k.local_day-1 and k.local_day)
				limit $3
			) delete from sessions_closed s using target t where s.tableoid=t.tableoid and s.ctid=t.ctid`, false},
		{"verified sessions", `with target as (
				select s.tableoid,s.ctid from verified_page_sessions s where s.site_id=$1 and exists (
					select 1 from profile_erasure_sessions k where k.request_id=$2 and k.session_id=s.session_id
					and s.day between k.local_day-1 and k.local_day+1)
				limit $3
			) delete from verified_page_sessions s using target t where s.tableoid=t.tableoid and s.ctid=t.ctid`, false},
		{"closed custom events", `with target as (
				select c.tableoid,c.ctid from custom_closed c where c.site_id=$1 and exists (
					select 1 from profile_erasure_sessions k where k.request_id=$2 and k.visitor_id=c.visitor_id and k.local_day=c.day)
				limit $3
			) delete from custom_closed c using target t where c.tableoid=t.tableoid and c.ctid=t.ctid`, false},
	}
	for _, deletion := range deletes {
		for {
			var deleted int64
			err := pgx.BeginFunc(ctx, w.pool, func(tx pgx.Tx) error {
				if err := closer.LockRollupRefresh(ctx, tx); err != nil {
					return fmt.Errorf("lock rollup refresh: %w", err)
				}
				args := []any{request.SiteID}
				if deletion.profileID {
					args = append(args, request.ProfileID, request.ID, w.batchSize)
				} else {
					args = append(args, request.ID, w.batchSize)
				}
				tag, err := tx.Exec(ctx, deletion.sql, args...)
				if err != nil {
					return fmt.Errorf("delete %s: %w", deletion.name, err)
				}
				deleted = tag.RowsAffected()
				return nil
			})
			if err != nil {
				return err
			}
			if err := w.store.Heartbeat(ctx, request.ID); err != nil {
				return fmt.Errorf("heartbeat profile erasure: %w", err)
			}
			if deleted < int64(w.batchSize) {
				break
			}
		}
	}
	return w.moveReplayRows(ctx, request)
}

func (w *Worker) moveReplayRows(ctx context.Context, request Request) error {
	for {
		moved := 0
		err := pgx.BeginFunc(ctx, w.pool, func(tx pgx.Tx) error {
			if err := closer.LockRollupRefresh(ctx, tx); err != nil {
				return fmt.Errorf("lock rollup refresh: %w", err)
			}
			rows, err := tx.Query(ctx, `
				select started_at,session_id,blob_key
				from replay_sessions r
				where r.site_id=$1 and (r.profile_id=$2 or exists (
					select 1 from profile_erasure_sessions k where k.request_id=$3 and k.session_id=r.session_id))
				order by started_at,session_id limit $4`, request.SiteID, request.ProfileID, request.ID, w.batchSize)
			if err != nil {
				return fmt.Errorf("load replay rows: %w", err)
			}
			type replayRow struct {
				startedAt time.Time
				sessionID int64
				blobKey   string
			}
			batch := make([]replayRow, 0, w.batchSize)
			keys := make([]string, 0, w.batchSize)
			for rows.Next() {
				var row replayRow
				if err := rows.Scan(&row.startedAt, &row.sessionID, &row.blobKey); err != nil {
					rows.Close()
					return fmt.Errorf("scan replay row: %w", err)
				}
				batch = append(batch, row)
				keys = append(keys, row.blobKey)
			}
			rowsErr := rows.Err()
			rows.Close()
			if rowsErr != nil {
				return fmt.Errorf("iterate replay rows: %w", rowsErr)
			}
			if err := w.store.RecordBlobsTx(ctx, tx, request.ID, keys); err != nil {
				return fmt.Errorf("record replay blob work: %w", err)
			}
			for _, row := range batch {
				if _, err := tx.Exec(ctx, `delete from replay_sessions where site_id=$1 and started_at=$2 and session_id=$3`, request.SiteID, row.startedAt, row.sessionID); err != nil {
					return fmt.Errorf("delete replay index row: %w", err)
				}
			}
			moved = len(batch)
			return nil
		})
		if err != nil {
			return err
		}
		if err := w.store.Heartbeat(ctx, request.ID); err != nil {
			return fmt.Errorf("heartbeat profile erasure: %w", err)
		}
		if moved < w.batchSize {
			return nil
		}
	}
}

func (w *Worker) deletePendingBlobs(ctx context.Context, requestID int64) error {
	for {
		pending, err := w.store.PendingBlobs(ctx, requestID, w.batchSize)
		if err != nil {
			return fmt.Errorf("load replay blob work: %w", err)
		}
		if len(pending) == 0 {
			return nil
		}
		if w.blobs == nil {
			return errors.New("replay blob store is unavailable")
		}
		for _, blob := range pending {
			if _, err := w.blobs.Delete(ctx, blob.BlobKey); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("delete replay blob %q: %w", blob.BlobKey, err)
			}
			if err := w.store.MarkBlobDeleted(ctx, requestID, blob.BlobKey); err != nil {
				return fmt.Errorf("complete replay blob work %q: %w", blob.BlobKey, err)
			}
		}
	}
}

func (w *Worker) finish(ctx context.Context, request Request) error {
	return pgx.BeginFunc(ctx, w.pool, func(tx pgx.Tx) error {
		var orgID int64
		err := tx.QueryRow(ctx, `
			update profile_erasure_requests r set status='done',settle_after=null,last_error=null,
				updated_at=now(),completed_at=now()
			from sites s where r.id=$1 and r.site_id=s.id and r.status <> 'done'
			returning s.org_id`, request.ID).Scan(&orgID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("mark profile erasure done: %w", err)
		}
		principal, err := requestedPrincipal(request.RequestedBy, orgID)
		if err != nil {
			return err
		}
		return audit.Write(ctx, tx, principal, "profile.erased", "profile", request.ProfileID, map[string]any{
			"site_id": request.SiteID, "erasure_id": request.ID, "aggregates_adjusted": false,
		})
	})
}

func requestedPrincipal(requestedBy string, orgID int64) (authz.Principal, error) {
	kind, rawID, ok := strings.Cut(requestedBy, ":")
	if !ok {
		return authz.Principal{}, fmt.Errorf("invalid erasure requester %q", requestedBy)
	}
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || id <= 0 {
		return authz.Principal{}, fmt.Errorf("invalid erasure requester %q", requestedBy)
	}
	principal := authz.Principal{OrgID: orgID}
	switch kind {
	case "user":
		principal.UserID = id
	case "api_key":
		principal.KeyID = id
	default:
		return authz.Principal{}, fmt.Errorf("invalid erasure requester %q", requestedBy)
	}
	return principal, nil
}
