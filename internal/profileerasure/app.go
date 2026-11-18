package profileerasure

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/appproduct"
	"supermetry/internal/platform/audit"
	"supermetry/internal/scheduler"
)

const (
	appBatchSize        = 1000
	appClaimSize        = 4
	appLease            = 10 * time.Minute
	appDoneRetention    = 30 * 24 * time.Hour
	appMaxAttempts      = 20
	appErasureJobPeriod = 5 * time.Second
)

// AppUserHash returns the keyed hash app_events.user_hash carries for userID,
// or ok=false when the project never hashed a user (no secret exists), in
// which case there is nothing to erase.
func AppUserHash(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, siteID int32, userID string) (hash []byte, ok bool, err error) {
	if userID == "" {
		return nil, false, errors.New("empty user id")
	}
	var secret []byte
	err = q.QueryRow(ctx, `select secret from app_hash_secrets where site_id = $1`, siteID).Scan(&secret)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read app hash secret: %w", err)
	}
	h := hmac.New(sha256.New, secret)
	h.Write([]byte(userID))
	return h.Sum(nil), true, nil
}

// AppRequest is a queued erasure of one end user's data in an app project.
// It carries the keyed hash only, never the user id.
type AppRequest struct {
	ID               int64
	SiteID           int32
	UserHash         []byte
	Status           Status
	BodiesDeleted    int64
	EventsAnonymized int64
	Attempts         int
	LastError        *string
	RequestedBy      string
	CreatedAt        time.Time
	UpdatedAt        time.Time
	CompletedAt      *time.Time
}

const appColumns = `id,site_id,user_hash,status,bodies_deleted,events_anonymized,attempts,last_error,requested_by,created_at,updated_at,completed_at`

func scanAppRequest(row scanner) (AppRequest, error) {
	var r AppRequest
	err := row.Scan(&r.ID, &r.SiteID, &r.UserHash, &r.Status, &r.BodiesDeleted, &r.EventsAnonymized,
		&r.Attempts, &r.LastError, &r.RequestedBy, &r.CreatedAt, &r.UpdatedAt, &r.CompletedAt)
	return r, err
}

// AppStore persists app user erasure requests.
type AppStore struct{ pool *pgxpool.Pool }

func NewAppStore(pool *pgxpool.Pool) *AppStore { return &AppStore{pool: pool} }

// CreateOrGetTx queues an erasure for userID, or returns the live request for
// the same user. The caller owns the transaction. When the project never
// hashed a user the request is stored already done with zero counts.
func (s *AppStore) CreateOrGetTx(ctx context.Context, tx pgx.Tx, siteID int32, userID, requestedBy string) (AppRequest, error) {
	hash, ok, err := AppUserHash(ctx, tx, siteID, userID)
	if err != nil {
		return AppRequest{}, err
	}
	if !ok {
		return scanAppRequest(tx.QueryRow(ctx, `
			insert into app_erasure_requests(site_id,status,requested_by,completed_at)
			values ($1,'done',$2,now()) returning `+appColumns, siteID, requestedBy))
	}
	return scanAppRequest(tx.QueryRow(ctx, `
		insert into app_erasure_requests(site_id,user_hash,requested_by)
		values ($1,$2,$3)
		on conflict (site_id,user_hash) where status <> 'done'
		do update set
			status=case when app_erasure_requests.status='failed' then 'pending' else app_erasure_requests.status end,
			attempts=case when app_erasure_requests.status='failed' then 0 else app_erasure_requests.attempts end,
			last_error=case when app_erasure_requests.status='failed' then null else app_erasure_requests.last_error end,
			retry_after=case when app_erasure_requests.status='failed' then null else app_erasure_requests.retry_after end,
			updated_at=case when app_erasure_requests.status='failed' then now() else app_erasure_requests.updated_at end
		returning `+appColumns, siteID, hash, requestedBy))
}

// GetForSite returns a request of the site, or ErrNotFound.
func (s *AppStore) GetForSite(ctx context.Context, id int64, siteID int32) (AppRequest, error) {
	r, err := scanAppRequest(s.pool.QueryRow(ctx, `select `+appColumns+` from app_erasure_requests where id=$1 and site_id=$2`, id, siteID))
	return r, notFound(err)
}

// Claim marks pending requests running and returns them. A running request
// whose lease lapsed (a crashed worker) is claimable again and counts as an
// attempt; after appMaxAttempts it fails.
func (s *AppStore) Claim(ctx context.Context, limit int) ([]AppRequest, error) {
	rows, err := s.pool.Query(ctx, `
		with candidates as (
			select id,(status='running') as reclaimed
			from app_erasure_requests
			where (status='pending' and (retry_after is null or retry_after <= now()))
			   or (status='running' and updated_at <= now()-$3::double precision*interval '1 second')
			order by created_at,id
			for update skip locked
			limit $1
		)
		update app_erasure_requests r
		set status=case when c.reclaimed and r.attempts+1 >= $2 then 'failed' else 'running' end,
		    attempts=r.attempts+case when c.reclaimed then 1 else 0 end,
		    last_error=case when c.reclaimed and r.attempts+1 >= $2 then 'worker lease expired too many times' else r.last_error end,
		    retry_after=null,
		    updated_at=now()
		from candidates c
		where r.id=c.id
		returning `+prefixed("r", appColumns), limit, appMaxAttempts, appLease.Seconds())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AppRequest{}
	for rows.Next() {
		r, err := scanAppRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func prefixed(alias, columns string) string {
	parts := strings.Split(columns, ",")
	for i, p := range parts {
		parts[i] = alias + "." + p
	}
	return strings.Join(parts, ",")
}

// markFailure returns the request to the queue after exponential backoff
// (5 s up to 10 min), or fails it for good at the attempt limit.
func (s *AppStore) markFailure(ctx context.Context, id int64, cause error) error {
	_, err := s.pool.Exec(ctx, `
		update app_erasure_requests
		set attempts=attempts+1,
		    status=case when attempts+1 >= $3 then 'failed' else 'pending' end,
		    last_error=$2,
		    retry_after=case when attempts+1 >= $3 then null else
		      now() + least(600::double precision,5*power(2,attempts)) * interval '1 second' end,
		    updated_at=now()
		where id=$1 and status='running'`, id, cause.Error(), appMaxAttempts)
	return err
}

// AppWorker runs queued app user erasures in batches. Each batch is one
// statement that also adds to the request's counters and renews its lease, so
// a restart resumes where the last committed batch stopped and the counts stay
// exact.
type AppWorker struct {
	pool  *pgxpool.Pool
	store *AppStore
	batch int
}

func NewAppWorker(pool *pgxpool.Pool, store *AppStore) *AppWorker {
	return &AppWorker{pool: pool, store: store, batch: appBatchSize}
}

func (w *AppWorker) Job() scheduler.Job {
	return scheduler.Job{Name: "app-user-erasure", Every: appErasureJobPeriod, RunOnStart: true, Run: w.RunOnce}
}

func (w *AppWorker) RunOnce(ctx context.Context) error {
	requests, err := w.store.Claim(ctx, appClaimSize)
	if err != nil {
		return fmt.Errorf("claim app erasures: %w", err)
	}
	for _, r := range requests {
		if r.Status == StatusFailed {
			if err := w.audit(ctx, r, "app.user_erase_failed"); err != nil {
				slog.ErrorContext(ctx, "audit failed app erasure", "erasure_id", r.ID, "error", err)
			}
			continue
		}
		if err := w.process(ctx, r); err != nil {
			slog.WarnContext(ctx, "app user erasure attempt failed", "erasure_id", r.ID, "error", err)
			if mErr := w.store.markFailure(ctx, r.ID, err); mErr != nil {
				slog.ErrorContext(ctx, "record app erasure failure", "erasure_id", r.ID, "error", mErr)
			}
		}
	}
	return w.prune(ctx)
}

func (w *AppWorker) process(ctx context.Context, r AppRequest) error {
	// Bodies first: they are found through the event row's user hash, which the
	// second phase clears. Both phases are idempotent, so a resumed run
	// re-checks the first and carries on with the second.
	if err := w.drain(ctx, r.ID, `
with target as (
  select b.tableoid, b.ctid from app_event_bodies b
  join app_events e on e.site_id = b.site_id and e.event_id = b.event_id and e.ts = b.ts
  where e.site_id = $1 and e.user_hash = $2
  limit $3
), del as (
  delete from app_event_bodies b using target t where b.tableoid = t.tableoid and b.ctid = t.ctid returning 1
) update app_erasure_requests set bodies_deleted = bodies_deleted + (select count(*) from del), updated_at = now()
where id = $4 returning (select count(*) from del)`, r); err != nil {
		return fmt.Errorf("delete app event bodies: %w", err)
	}
	if err := w.drain(ctx, r.ID, `
with target as (
  select tableoid, ctid from app_events where site_id = $1 and user_hash = $2 limit $3
), upd as (
  update app_events e set user_hash = null from target t where e.tableoid = t.tableoid and e.ctid = t.ctid returning 1
) update app_erasure_requests set events_anonymized = events_anonymized + (select count(*) from upd), updated_at = now()
where id = $4 returning (select count(*) from upd)`, r); err != nil {
		return fmt.Errorf("clear app event user hash: %w", err)
	}
	return w.finish(ctx, r.ID)
}

// drain repeats a batch statement until a batch comes back short.
func (w *AppWorker) drain(ctx context.Context, id int64, sql string, r AppRequest) error {
	for {
		var n int64
		err := w.pool.QueryRow(ctx, sql, r.SiteID, r.UserHash, w.batch, id).Scan(&n)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("request %d is no longer running", id)
		}
		if err != nil {
			return err
		}
		if n < int64(w.batch) {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
}

// finish marks the request done and writes the audit row (counts only) in one
// transaction, so the audit is written exactly once.
func (w *AppWorker) finish(ctx context.Context, id int64) error {
	return pgx.BeginFunc(ctx, w.pool, func(tx pgx.Tx) error {
		r, err := scanAppRequest(tx.QueryRow(ctx, `
			update app_erasure_requests
			set status='done',last_error=null,retry_after=null,updated_at=now(),completed_at=now()
			where id=$1 and status='running' returning `+appColumns, id))
		if err != nil {
			return fmt.Errorf("mark app erasure done: %w", err)
		}
		return w.auditTx(ctx, tx, r, "app.user_erased")
	})
}

func (w *AppWorker) audit(ctx context.Context, r AppRequest, action string) error {
	return pgx.BeginFunc(ctx, w.pool, func(tx pgx.Tx) error { return w.auditTx(ctx, tx, r, action) })
}

func (w *AppWorker) auditTx(ctx context.Context, tx pgx.Tx, r AppRequest, action string) error {
	var orgID int64
	if err := tx.QueryRow(ctx, `select org_id from sites where id=$1`, r.SiteID).Scan(&orgID); err != nil {
		return fmt.Errorf("load erasure organization: %w", err)
	}
	principal, err := requestedPrincipal(r.RequestedBy, orgID)
	if err != nil {
		return err
	}
	return audit.Write(ctx, tx, principal, action, "site", int64(r.SiteID), map[string]any{
		"bodies": r.BodiesDeleted, "events": r.EventsAnonymized,
	})
}

// AppHashSecret returns the project's hash secret, or ok=false when it has
// none (nothing was ever hashed, so there is nothing to erase).
func AppHashSecret(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, siteID int32) (secret []byte, ok bool, err error) {
	err = q.QueryRow(ctx, `select secret from app_hash_secrets where site_id = $1`, siteID).Scan(&secret)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read app hash secret: %w", err)
	}
	return secret, true, nil
}

// EraseAppInstall removes what an app project keeps about one install beyond
// raw events: its app_active_day rows (found by the keyed visitor id) and, when
// the install has a profile (external id "i:" + keyed hash), a queued profile
// erasure that the profile worker carries out. It returns the number of
// active-day rows deleted. The caller owns the transaction. The raw install id
// is only hashed here, never stored.
func EraseAppInstall(ctx context.Context, tx pgx.Tx, profiles *Store, siteID int32, installID string, secret []byte, requestedBy string) (int64, error) {
	if installID == "" || len(secret) == 0 {
		return 0, errors.New("install id and hash secret are required")
	}
	tag, err := tx.Exec(ctx, `delete from app_active_day where site_id = $1 and visitor_id = $2`,
		siteID, appproduct.VisitorID(secret, installID))
	if err != nil {
		return 0, fmt.Errorf("delete app active days: %w", err)
	}
	external := appproduct.ProfileExternalID(secret, installID)
	var profileID int64
	err = tx.QueryRow(ctx, `select id from profiles where site_id = $1 and external_id = $2`, siteID, external).Scan(&profileID)
	if errors.Is(err, pgx.ErrNoRows) {
		return tag.RowsAffected(), nil
	}
	if err != nil {
		return 0, fmt.Errorf("find install profile: %w", err)
	}
	if _, err := profiles.CreateOrGetTx(ctx, tx, siteID, profileID, external, requestedBy); err != nil {
		return 0, fmt.Errorf("queue install profile erasure: %w", err)
	}
	return tag.RowsAffected(), nil
}

// prune drops finished requests after a month.
func (w *AppWorker) prune(ctx context.Context) error {
	_, err := w.pool.Exec(ctx, `delete from app_erasure_requests where status='done' and completed_at < now() - $1::double precision * interval '1 second'`,
		appDoneRetention.Seconds())
	return err
}
