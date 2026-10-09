// Package profileerasure persists restart-safe profile deletion requests.
package profileerasure

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const maxAttempts = 20

var (
	ErrNotFound     = errors.New("profile erasure request not found")
	ErrInvalidState = errors.New("invalid profile erasure request state")
)

type Status string

const (
	StatusPending  Status = "pending"
	StatusRunning  Status = "running"
	StatusSettling Status = "settling"
	StatusDone     Status = "done"
	StatusFailed   Status = "failed"
)

type Request struct {
	ID           int64
	SiteID       int32
	ProfileID    int64
	ExternalHash [sha256.Size]byte
	Status       Status
	Attempts     int
	LastError    *string
	RequestedBy  string
	SettleAfter  *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
	CompletedAt  *time.Time
}

type BlobWork struct {
	RequestID int64
	BlobKey   string
	DeletedAt *time.Time
}

type Store struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func HashExternalID(externalID string) [sha256.Size]byte {
	return sha256.Sum256([]byte(externalID))
}

func RequestedByUser(id int64) string { return "user:" + strconv.FormatInt(id, 10) }

func RequestedByAPIKey(id int64) string { return "api_key:" + strconv.FormatInt(id, 10) }

// CreateOrGetTx creates a pending request, or returns the existing non-done
// request for the same identity. The caller owns the transaction.
func (s *Store) CreateOrGetTx(ctx context.Context, tx pgx.Tx, siteID int32, profileID int64, externalID, requestedBy string) (Request, error) {
	hash := HashExternalID(externalID)
	return scanRequest(tx.QueryRow(ctx, `
		insert into profile_erasure_requests(site_id,profile_id,external_hash,requested_by)
		values ($1,$2,$3,$4)
		on conflict (site_id,external_hash) where status <> 'done'
		do update set
			status=case when profile_erasure_requests.status='failed' then 'pending' else profile_erasure_requests.status end,
			attempts=case when profile_erasure_requests.status='failed' then 0 else profile_erasure_requests.attempts end,
			last_error=case when profile_erasure_requests.status='failed' then null else profile_erasure_requests.last_error end,
			settle_after=case when profile_erasure_requests.status='failed' then null else profile_erasure_requests.settle_after end,
			updated_at=case when profile_erasure_requests.status='failed' then now() else profile_erasure_requests.updated_at end
		returning id,site_id,profile_id,external_hash,status,attempts,last_error,
		          requested_by,settle_after,created_at,updated_at,completed_at`,
		siteID, profileID, hash[:], requestedBy))
}

func (s *Store) Get(ctx context.Context, id int64) (Request, error) {
	request, err := scanRequest(s.pool.QueryRow(ctx, `
		select id,site_id,profile_id,external_hash,status,attempts,last_error,
		       requested_by,settle_after,created_at,updated_at,completed_at
		from profile_erasure_requests where id=$1`, id))
	return request, notFound(err)
}

func (s *Store) GetForSite(ctx context.Context, id int64, siteID int32) (Request, error) {
	request, err := scanRequest(s.pool.QueryRow(ctx, `
		select id,site_id,profile_id,external_hash,status,attempts,last_error,
		       requested_by,settle_after,created_at,updated_at,completed_at
		from profile_erasure_requests where id=$1 and site_id=$2`, id, siteID))
	return request, notFound(err)
}

// Claim atomically marks pending work as running and returns claimable work.
// A running request becomes claimable after ten minutes so a crashed worker
// cannot strand it. Failed attempts use settle_after as their retry deadline.
func (s *Store) Claim(ctx context.Context, limit int) ([]Request, error) {
	if limit <= 0 {
		return []Request{}, nil
	}
	rows, err := s.pool.Query(ctx, `
		with candidates as (
			select id,(
				(status='running' and updated_at <= now()-interval '10 minutes')
				or (status='settling' and settle_after is null and updated_at <= now()-interval '10 minutes')
			) as reclaimed
			from profile_erasure_requests
			where (status='pending' and (settle_after is null or settle_after <= now()))
			   or (status='running' and updated_at <= now()-interval '10 minutes')
			   or (status='settling' and (
			       (settle_after is not null and settle_after <= now())
			       or (settle_after is null and updated_at <= now()-interval '10 minutes')
			   ))
			order by created_at,id
			for update skip locked
			limit $1
		)
		update profile_erasure_requests r
		set status=case
		        when c.reclaimed and r.attempts+1 >= $2 then 'failed'
		        when r.status='pending' then 'running'
		        else r.status
		    end,
		    attempts=r.attempts+case when c.reclaimed then 1 else 0 end,
		    settle_after=case
		        when c.reclaimed and r.attempts+1 >= $2 then null
		        when r.status in ('pending','settling') then null
		        else r.settle_after
		    end,
		    updated_at=now()
		from candidates c
		where r.id=c.id
		returning r.id,r.site_id,r.profile_id,r.external_hash,r.status,r.attempts,r.last_error,
		          r.requested_by,r.settle_after,r.created_at,r.updated_at,r.completed_at`, limit, maxAttempts)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	requests := make([]Request, 0)
	for rows.Next() {
		request, err := scanRequest(rows)
		if err != nil {
			return nil, err
		}
		requests = append(requests, request)
	}
	return requests, rows.Err()
}

// Heartbeat extends the lease for a claimed request after a committed batch.
func (s *Store) Heartbeat(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `
		update profile_erasure_requests set updated_at=now()
		where id=$1 and status in ('running','settling')`, id)
	return changed(tag.RowsAffected(), err)
}

func (s *Store) MarkSettling(ctx context.Context, id int64, settleAfter time.Time) error {
	tag, err := s.pool.Exec(ctx, `
		update profile_erasure_requests
		set status='settling',settle_after=$2,last_error=null,updated_at=now()
		where id=$1 and status in ('running','settling')`, id, settleAfter)
	return changed(tag.RowsAffected(), err)
}

func (s *Store) MarkDone(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `
		update profile_erasure_requests
		set status='done',settle_after=null,last_error=null,updated_at=now(),completed_at=now()
		where id=$1 and status <> 'done'`, id)
	return changed(tag.RowsAffected(), err)
}

// MarkFailure records a failed attempt. Non-terminal failures return to the
// pending queue after exponential backoff from five seconds to ten minutes.
func (s *Store) MarkFailure(ctx context.Context, id int64, cause error) error {
	message := "unknown error"
	if cause != nil {
		message = cause.Error()
	}
	tag, err := s.pool.Exec(ctx, `
		update profile_erasure_requests
		set attempts=attempts+1,
		    status=case when attempts+1 >= $3 then 'failed' else 'pending' end,
		    last_error=$2,
		    settle_after=case when attempts+1 >= $3 then null else
		      now() + least(600::double precision,5*power(2,attempts)) * interval '1 second' end,
		    updated_at=now()
		where id=$1 and status in ('pending','running','settling')`, id, message, maxAttempts)
	return changed(tag.RowsAffected(), err)
}

func (s *Store) Retry(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `
		update profile_erasure_requests
		set status='pending',attempts=0,last_error=null,settle_after=null,
		    completed_at=null,updated_at=now()
		where id=$1 and status='failed'`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		var exists bool
		if scanErr := s.pool.QueryRow(ctx, `select exists(select 1 from profile_erasure_requests where id=$1)`, id).Scan(&exists); scanErr != nil {
			return scanErr
		}
		if exists {
			return ErrInvalidState
		}
		return ErrNotFound
	}
	return nil
}

func (s *Store) RecordBlobsTx(ctx context.Context, tx pgx.Tx, requestID int64, blobKeys []string) error {
	for _, blobKey := range blobKeys {
		if blobKey == "" {
			return errors.New("profile erasure blob key is empty")
		}
		if _, err := tx.Exec(ctx, `
			insert into profile_erasure_blobs(request_id,blob_key)
			values ($1,$2) on conflict (request_id,blob_key) do nothing`, requestID, blobKey); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) PendingBlobs(ctx context.Context, requestID int64, limit int) ([]BlobWork, error) {
	if limit <= 0 {
		return []BlobWork{}, nil
	}
	rows, err := s.pool.Query(ctx, `
		select request_id,blob_key,deleted_at
		from profile_erasure_blobs
		where request_id=$1 and deleted_at is null
		order by blob_key limit $2`, requestID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	blobs := make([]BlobWork, 0)
	for rows.Next() {
		var blob BlobWork
		if err := rows.Scan(&blob.RequestID, &blob.BlobKey, &blob.DeletedAt); err != nil {
			return nil, err
		}
		blobs = append(blobs, blob)
	}
	return blobs, rows.Err()
}

func (s *Store) MarkBlobDeleted(ctx context.Context, requestID int64, blobKey string) error {
	tag, err := s.pool.Exec(ctx, `
		update profile_erasure_blobs set deleted_at=coalesce(deleted_at,now())
		where request_id=$1 and blob_key=$2`, requestID, blobKey)
	return changed(tag.RowsAffected(), err)
}

type scanner interface {
	Scan(...any) error
}

func scanRequest(row scanner) (Request, error) {
	var request Request
	var hash []byte
	err := row.Scan(&request.ID, &request.SiteID, &request.ProfileID, &hash, &request.Status,
		&request.Attempts, &request.LastError, &request.RequestedBy, &request.SettleAfter,
		&request.CreatedAt, &request.UpdatedAt, &request.CompletedAt)
	if err != nil {
		return Request{}, err
	}
	if len(hash) != sha256.Size {
		return Request{}, fmt.Errorf("profile erasure request %d has %d-byte external hash", request.ID, len(hash))
	}
	copy(request.ExternalHash[:], hash)
	return request, nil
}

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func changed(rows int64, err error) error {
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}
