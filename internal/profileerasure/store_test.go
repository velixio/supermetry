package profileerasure

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/db"
	"supermetry/internal/testpg"
)

func TestMigrationCreatesProfileErasureSchema(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	for _, table := range []string{"profile_erasure_requests", "profile_erasure_blobs", "profile_erasure_sessions"} {
		var exists bool
		if err := pool.QueryRow(ctx, `select to_regclass($1) is not null`, table).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Fatalf("table %s was not created", table)
		}
	}

	var predicate string
	if err := pool.QueryRow(ctx, `
		select pg_get_expr(indpred, indrelid)
		from pg_index
		where indexrelid = 'profile_erasure_requests_active_identity_idx'::regclass`).Scan(&predicate); err != nil {
		t.Fatal(err)
	}
	if predicate == "" {
		t.Fatal("active identity index is not partial")
	}
}

func TestStoreCreateOrGetAndGetForSite(t *testing.T) {
	store, pool, siteID, userID := newStoreFixture(t)
	ctx := context.Background()

	var first Request
	err := withTx(ctx, pool, func(tx pgx.Tx) error {
		var err error
		first, err = store.CreateOrGetTx(ctx, tx, siteID, 41, "customer-42", RequestedByUser(userID))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	var duplicate Request
	err = withTx(ctx, pool, func(tx pgx.Tx) error {
		var err error
		duplicate, err = store.CreateOrGetTx(ctx, tx, siteID, 99, "customer-42", RequestedByUser(userID))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if duplicate.ID != first.ID {
		t.Fatalf("duplicate request ID = %d, want %d", duplicate.ID, first.ID)
	}
	if first.ExternalHash != HashExternalID("customer-42") || first.Status != StatusPending || first.RequestedBy != RequestedByUser(userID) {
		t.Fatalf("created request = %+v", first)
	}
	if first.CreatedAt.IsZero() || first.UpdatedAt.IsZero() {
		t.Fatalf("timestamps were not populated: %+v", first)
	}

	got, err := store.Get(ctx, first.ID)
	if err != nil || got.ID != first.ID {
		t.Fatalf("Get() = (%+v, %v)", got, err)
	}
	if _, err := store.GetForSite(ctx, first.ID, siteID+1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetForSite(other site) error = %v, want ErrNotFound", err)
	}

	if err := store.MarkDone(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	err = withTx(ctx, pool, func(tx pgx.Tx) error {
		var err error
		duplicate, err = store.CreateOrGetTx(ctx, tx, siteID, 99, "customer-42", RequestedByUser(userID))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if duplicate.ID == first.ID {
		t.Fatal("a completed erasure prevented a new request")
	}
}

func TestStoreCreateOrGetReactivatesFailedRequest(t *testing.T) {
	store, pool, siteID, userID := newStoreFixture(t)
	ctx := context.Background()
	request := createRequest(t, store, pool, siteID, 41, "failed-customer", userID)
	if _, err := pool.Exec(ctx, `
		update profile_erasure_requests
		set status='failed', attempts=20, last_error='worker crashed', settle_after=null
		where id=$1`, request.ID); err != nil {
		t.Fatal(err)
	}

	var retried Request
	if err := withTx(ctx, pool, func(tx pgx.Tx) error {
		var err error
		retried, err = store.CreateOrGetTx(ctx, tx, siteID, 99, "failed-customer", RequestedByUser(userID))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if retried.ID != request.ID || retried.Status != StatusPending || retried.Attempts != 0 || retried.LastError != nil {
		t.Fatalf("reactivated request = %+v, want same pending request with cleared failure", retried)
	}
}

func TestStoreClaimSkipsLockedRequest(t *testing.T) {
	store, pool, siteID, userID := newStoreFixture(t)
	ctx := context.Background()
	first := createRequest(t, store, pool, siteID, 41, "first", userID)
	second := createRequest(t, store, pool, siteID, 42, "second", userID)

	lock, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback(context.Background())
	if _, err := lock.Exec(ctx, `select id from profile_erasure_requests where id=$1 for update`, first.ID); err != nil {
		t.Fatal(err)
	}

	claimed, err := store.Claim(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 || claimed[0].ID != second.ID || claimed[0].Status != StatusRunning {
		t.Fatalf("Claim() = %+v, want running request %d", claimed, second.ID)
	}
}

func TestStoreExpiredRunningClaimsExhaustAttempts(t *testing.T) {
	store, pool, siteID, userID := newStoreFixture(t)
	ctx := context.Background()
	request := createRequest(t, store, pool, siteID, 41, "crashing", userID)

	for wantAttempts := 1; wantAttempts <= maxAttempts; wantAttempts++ {
		if _, err := pool.Exec(ctx, `
			update profile_erasure_requests
			set status='running',updated_at=now()-interval '11 minutes'
			where id=$1`, request.ID); err != nil {
			t.Fatal(err)
		}
		claimed, err := store.Claim(ctx, 1)
		if err != nil || len(claimed) != 1 {
			t.Fatalf("expired claim %d = (%+v, %v)", wantAttempts, claimed, err)
		}
		wantStatus := StatusRunning
		if wantAttempts == maxAttempts {
			wantStatus = StatusFailed
		}
		if claimed[0].Attempts != wantAttempts || claimed[0].Status != wantStatus {
			t.Fatalf("expired claim %d = %+v, want attempts=%d status=%s", wantAttempts, claimed[0], wantAttempts, wantStatus)
		}
	}
}

func TestStoreHeartbeatKeepsRunningClaimLeased(t *testing.T) {
	store, pool, siteID, userID := newStoreFixture(t)
	ctx := context.Background()
	request := createRequest(t, store, pool, siteID, 41, "heartbeat", userID)
	claimed, err := store.Claim(ctx, 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("initial claim = (%+v, %v)", claimed, err)
	}
	if _, err := pool.Exec(ctx, `update profile_erasure_requests set updated_at=now()-interval '11 minutes' where id=$1`, request.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.Heartbeat(ctx, request.ID); err != nil {
		t.Fatal(err)
	}
	if duplicate, err := New(pool).Claim(ctx, 1); err != nil || len(duplicate) != 0 {
		t.Fatalf("claim after heartbeat = (%+v, %v), want no duplicate", duplicate, err)
	}
}

func TestStoreExpiredSettlingClaimsExhaustAttempts(t *testing.T) {
	store, pool, siteID, userID := newStoreFixture(t)
	ctx := context.Background()
	request := createRequest(t, store, pool, siteID, 41, "stale-settling", userID)

	for wantAttempts := 1; wantAttempts <= maxAttempts; wantAttempts++ {
		if _, err := pool.Exec(ctx, `
			update profile_erasure_requests
			set status='settling',settle_after=null,updated_at=now()-interval '11 minutes'
			where id=$1`, request.ID); err != nil {
			t.Fatal(err)
		}
		claimed, err := store.Claim(ctx, 1)
		if err != nil || len(claimed) != 1 {
			t.Fatalf("expired settling claim %d = (%+v, %v)", wantAttempts, claimed, err)
		}
		wantStatus := StatusSettling
		if wantAttempts == maxAttempts {
			wantStatus = StatusFailed
		}
		if claimed[0].Attempts != wantAttempts || claimed[0].Status != wantStatus {
			t.Fatalf("expired settling claim %d = %+v, want attempts=%d status=%s", wantAttempts, claimed[0], wantAttempts, wantStatus)
		}
	}
}

func TestStoreTransitionsAndRetry(t *testing.T) {
	store, pool, siteID, userID := newStoreFixture(t)
	ctx := context.Background()
	request := createRequest(t, store, pool, siteID, 41, "transition", userID)

	claimed, err := store.Claim(ctx, 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("Claim() = (%+v, %v)", claimed, err)
	}
	if err := store.MarkFailure(ctx, request.ID, errors.New("blob unavailable")); err != nil {
		t.Fatal(err)
	}
	failedOnce, err := store.Get(ctx, request.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failedOnce.Status != StatusPending || failedOnce.Attempts != 1 || failedOnce.LastError == nil || *failedOnce.LastError != "blob unavailable" {
		t.Fatalf("after failure = %+v", failedOnce)
	}
	if claimed, err := store.Claim(ctx, 1); err != nil || len(claimed) != 0 {
		t.Fatalf("Claim() during backoff = (%+v, %v)", claimed, err)
	}
	if _, err := pool.Exec(ctx, `update profile_erasure_requests set settle_after=now()-interval '1 second' where id=$1`, request.ID); err != nil {
		t.Fatal(err)
	}
	if claimed, err := store.Claim(ctx, 1); err != nil || len(claimed) != 1 || claimed[0].Status != StatusRunning {
		t.Fatalf("Claim() after backoff = (%+v, %v)", claimed, err)
	}

	settleAfter := time.Now().Add(time.Hour).UTC().Truncate(time.Microsecond)
	if err := store.MarkSettling(ctx, request.ID, settleAfter); err != nil {
		t.Fatal(err)
	}
	if claimed, err := store.Claim(ctx, 1); err != nil || len(claimed) != 0 {
		t.Fatalf("Claim() before settle = (%+v, %v)", claimed, err)
	}
	if _, err := pool.Exec(ctx, `update profile_erasure_requests set settle_after=now()-interval '1 second' where id=$1`, request.ID); err != nil {
		t.Fatal(err)
	}
	if claimed, err := store.Claim(ctx, 1); err != nil || len(claimed) != 1 || claimed[0].Status != StatusSettling {
		t.Fatalf("Claim() after settle = (%+v, %v)", claimed, err)
	}
	if claimed, err := store.Claim(ctx, 1); err != nil || len(claimed) != 0 {
		t.Fatalf("concurrent settling Claim() = (%+v, %v)", claimed, err)
	}

	if _, err := pool.Exec(ctx, `update profile_erasure_requests set status='running',attempts=19 where id=$1`, request.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkFailure(ctx, request.ID, errors.New("last failure")); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, request.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusFailed || got.Attempts != 20 {
		t.Fatalf("terminal failure = %+v", got)
	}
	if err := store.Retry(ctx, request.ID); err != nil {
		t.Fatal(err)
	}
	got, err = store.Get(ctx, request.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusPending || got.Attempts != 0 || got.LastError != nil || got.SettleAfter != nil {
		t.Fatalf("retried request = %+v", got)
	}
}

func TestStoreBlobWorkListSurvivesFailures(t *testing.T) {
	store, pool, siteID, userID := newStoreFixture(t)
	ctx := context.Background()
	request := createRequest(t, store, pool, siteID, 41, "blobs", userID)

	if err := withTx(ctx, pool, func(tx pgx.Tx) error {
		return store.RecordBlobsTx(ctx, tx, request.ID, []string{"replay/a", "replay/b", "replay/a"})
	}); err != nil {
		t.Fatal(err)
	}
	blobs, err := store.PendingBlobs(ctx, request.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(blobs) != 2 || blobs[0].BlobKey != "replay/a" || blobs[1].BlobKey != "replay/b" {
		t.Fatalf("PendingBlobs() = %+v", blobs)
	}
	if err := store.MarkBlobDeleted(ctx, request.ID, "replay/a"); err != nil {
		t.Fatal(err)
	}
	blobs, err = store.PendingBlobs(ctx, request.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(blobs) != 1 || blobs[0].BlobKey != "replay/b" {
		t.Fatalf("pending after delete = %+v", blobs)
	}
}

func newStoreFixture(t *testing.T) (*Store, *pgxpool.Pool, int32, int64) {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var orgID, userID int64
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Test','erasure-test') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash) values ('owner@example.test','Owner','x') returning id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	var siteID int32
	if err := pool.QueryRow(ctx, `
		insert into sites(org_id,public_id,name,timezone,retention_class)
		values ($1,'erase0000001','Test','UTC',30) returning id`, orgID).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	return New(pool), pool, siteID, userID
}

type txPool interface {
	Begin(context.Context) (pgx.Tx, error)
}

func withTx(ctx context.Context, pool txPool, fn func(pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func createRequest(t *testing.T, store *Store, pool txPool, siteID int32, profileID int64, externalID string, userID int64) Request {
	t.Helper()
	var request Request
	if err := withTx(context.Background(), pool, func(tx pgx.Tx) error {
		var err error
		request, err = store.CreateOrGetTx(context.Background(), tx, siteID, profileID, externalID, RequestedByUser(userID))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return request
}
