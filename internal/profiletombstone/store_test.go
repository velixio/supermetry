package profiletombstone

import (
	"context"
	"crypto/sha256"
	"sync"
	"testing"
	"time"

	"supermetry/internal/db"
	"supermetry/internal/session"
	"supermetry/internal/testpg"
)

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

type countingProfileSessions struct {
	mu             sync.Mutex
	tombstoneCalls int
}

func (s *countingProfileSessions) TombstoneProfile(_ int32, _ [sha256.Size]byte) {
	s.mu.Lock()
	s.tombstoneCalls++
	s.mu.Unlock()
}

func (s *countingProfileSessions) ExpireProfileTombstone(_ int32, _ [sha256.Size]byte) {}

func (s *countingProfileSessions) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tombstoneCalls
}

func TestStoreLoadsTombstonesAfterRestart(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var orgID int64
	var siteID int32
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Tombstones','tombstones') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class) values ($1,'tombstones01','Tombstones','UTC',30) returning id`, orgID).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	clock := &fakeClock{now: time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)}
	first := New(pool, clock)
	if err := first.Remember(ctx, siteID, "customer-1"); err != nil {
		t.Fatal(err)
	}

	restarted := New(pool, clock)
	if err := restarted.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if !restarted.Contains(siteID, "customer-1") {
		t.Fatal("persisted tombstone was not loaded")
	}
	if restarted.Contains(siteID, "customer-2") {
		t.Fatal("unrelated identity is tombstoned")
	}
}

func TestStorePollPropagatesToAnotherServer(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	clock := &fakeClock{now: time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)}
	var orgID int64
	var siteID int32
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Poll','poll') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class) values ($1,'tombpoll0001','Poll','UTC',30) returning id`, orgID).Scan(&siteID); err != nil {
		t.Fatal(err)
	}

	serverA := New(pool, clock)
	serverB := New(pool, clock)
	sessionsB := session.NewMemory(1, clock)
	serverB.SetSessions(sessionsB)
	live := sessionsB.Touch(session.Hit{SiteID: siteID, TS: clock.now, LocalDay: clock.now, VisitorID: 1})
	if !sessionsB.LinkProfile(siteID, live.SessionID, "customer-1") {
		t.Fatal("link profile")
	}
	if err := serverB.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if err := serverA.Remember(ctx, siteID, "customer-1"); err != nil {
		t.Fatal(err)
	}
	if serverB.Contains(siteID, "customer-1") {
		t.Fatal("second server observed tombstone before polling")
	}
	if err := serverB.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	if !serverB.Contains(siteID, "customer-1") || !sessionsB.ProfileBlocked(siteID, live.SessionID) {
		t.Fatal("second server did not enforce polled tombstone")
	}
	// The overlap cursor is intentional: distinct tombstones can receive the
	// same application timestamp and both must propagate.
	if err := serverA.Remember(ctx, siteID, "customer-2"); err != nil {
		t.Fatal(err)
	}
	if err := serverB.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	if !serverB.Contains(siteID, "customer-2") {
		t.Fatal("second tombstone sharing the cursor timestamp was skipped")
	}
	if interval := serverB.PollInterval(); interval > 2*time.Second {
		t.Fatalf("poll interval = %s, want at most 2s for propagation within 5s", interval)
	}
}

func TestStorePollOverlapsWatermarkForLateCommit(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	clockA := &fakeClock{now: time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)}
	clockB := &fakeClock{now: clockA.now}
	var orgID int64
	var siteID int32
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Overlap','overlap') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class) values ($1,'tomboverlap1','Overlap','UTC',30) returning id`, orgID).Scan(&siteID); err != nil {
		t.Fatal(err)
	}

	serverA := New(pool, clockA)
	serverB := New(pool, clockB)
	if err := serverA.Remember(ctx, siteID, "watermark"); err != nil {
		t.Fatal(err)
	}
	if err := serverB.Load(ctx); err != nil {
		t.Fatal(err)
	}

	clockA.now = clockA.now.Add(-30 * time.Second)
	if err := serverA.Remember(ctx, siteID, "late-commit"); err != nil {
		t.Fatal(err)
	}
	if err := serverB.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	if !serverB.Contains(siteID, "late-commit") {
		t.Fatal("poll skipped late-committing tombstone older than its watermark")
	}
}

func TestStoreRepeatPollDoesNotRetombstoneKnownProfile(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	clock := &fakeClock{now: time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)}
	var orgID int64
	var siteID int32
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Dedupe','dedupe') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class) values ($1,'tombdedupe01','Dedupe','UTC',30) returning id`, orgID).Scan(&siteID); err != nil {
		t.Fatal(err)
	}

	serverA := New(pool, clock)
	if err := serverA.Remember(ctx, siteID, "customer-1"); err != nil {
		t.Fatal(err)
	}
	serverB := New(pool, clock)
	sessions := &countingProfileSessions{}
	serverB.SetSessions(sessions)
	if err := serverB.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if got := sessions.calls(); got != 1 {
		t.Fatalf("TombstoneProfile calls after load = %d, want 1", got)
	}
	if err := serverB.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	if got := sessions.calls(); got != 1 {
		t.Fatalf("TombstoneProfile calls after repeat poll = %d, want 1", got)
	}
	firstWrites, ok := any(serverB).(interface{ rememberWriteLockCount() uint64 })
	if !ok {
		t.Fatal("store does not expose its internal remember write-lock count")
	}
	writes := firstWrites.rememberWriteLockCount()
	if err := serverB.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	if got := firstWrites.rememberWriteLockCount(); got != writes {
		t.Fatalf("repeat overlap poll remember write locks = %d, want %d", got, writes)
	}
}

func TestStoreCompletionPollOverlapsWatermarkForLateCommit(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	clock := &fakeClock{now: time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)}
	var orgID int64
	var siteID int32
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Completion Overlap','completion-overlap') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class) values ($1,'tombcomplete','Completion Overlap','UTC',30) returning id`, orgID).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	serverA := New(pool, clock)
	serverB := New(pool, clock)
	base := clock.now
	for index, identity := range []string{"earlier", "later"} {
		if index == 0 {
			clock.now = base.Add(-10 * time.Minute)
		} else {
			clock.now = base
		}
		if err := serverA.Remember(ctx, siteID, identity); err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256([]byte(identity))
		if _, err := pool.Exec(ctx, `
			insert into profile_erasure_requests(site_id,profile_id,external_hash,requested_by)
			values ($1,42,$2,'user:1')`, siteID, hash[:]); err != nil {
			t.Fatal(err)
		}
	}
	if err := serverB.Load(ctx); err != nil {
		t.Fatal(err)
	}
	t2 := base
	laterHash := sha256.Sum256([]byte("later"))
	if _, err := pool.Exec(ctx, `
		update profile_erasure_requests set status='done',completed_at=$3,updated_at=$3
		where site_id=$1 and external_hash=$2`, siteID, laterHash[:], t2); err != nil {
		t.Fatal(err)
	}
	if err := serverB.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	t1 := t2.Add(-30 * time.Second)
	earlierHash := sha256.Sum256([]byte("earlier"))
	if _, err := pool.Exec(ctx, `
		update profile_erasure_requests set status='done',completed_at=$3,updated_at=$3
		where site_id=$1 and external_hash=$2`, siteID, earlierHash[:], t1); err != nil {
		t.Fatal(err)
	}
	if err := serverB.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	serverB.mu.RLock()
	completedAt := serverB.items[key{siteID: siteID, hash: earlierHash}].completedAt
	serverB.mu.RUnlock()
	if !completedAt.Equal(t1) {
		t.Fatalf("late completion = %s, want %s", completedAt, t1)
	}
}

func TestStoreExpiryStartsWhenErasureRequestCompletes(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	clock := &fakeClock{now: time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)}
	var orgID int64
	var siteID int32
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Expiry','expiry') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class) values ($1,'tombexpiry01','Expiry','UTC',30) returning id`, orgID).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	store := New(pool, clock)
	sessions := session.NewMemory(1, clock)
	store.SetSessions(sessions)
	live := sessions.Touch(session.Hit{SiteID: siteID, TS: clock.now, LocalDay: clock.now, VisitorID: 1})
	if !sessions.LinkProfile(siteID, live.SessionID, "customer-1") {
		t.Fatal("link profile")
	}
	if err := store.Remember(ctx, siteID, "customer-1"); err != nil {
		t.Fatal(err)
	}
	if !sessions.ProfileBlocked(siteID, live.SessionID) {
		t.Fatal("remember did not block linked session")
	}
	hash := sha256.Sum256([]byte("customer-1"))
	var requestID int64
	if err := pool.QueryRow(ctx, `
		insert into profile_erasure_requests(site_id,profile_id,external_hash,requested_by)
		values ($1,42,$2,'user:1') returning id`, siteID, hash[:]).Scan(&requestID); err != nil {
		t.Fatal(err)
	}
	clock.now = clock.now.Add(7 * 24 * time.Hour)
	if err := store.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	if !store.Contains(siteID, "customer-1") {
		t.Fatal("pending erasure tombstone expired")
	}

	completedAt := clock.now
	if _, err := pool.Exec(ctx, `update profile_erasure_requests set status='done',completed_at=$2,updated_at=$2 where id=$1`, requestID, completedAt); err != nil {
		t.Fatal(err)
	}
	clock.now = completedAt.Add(Window)
	if err := store.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	if !store.Contains(siteID, "customer-1") {
		t.Fatal("tombstone expired at the exact 48h boundary")
	}
	clock.now = clock.now.Add(time.Nanosecond)
	if err := store.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	if store.Contains(siteID, "customer-1") {
		t.Fatal("tombstone did not expire after completion plus 48h")
	}
	if sessions.ProfileBlocked(siteID, live.SessionID) {
		t.Fatal("per-server session block survived tombstone expiry")
	}
}

func TestStoreExpiryFallsBackToErasedAtWithoutRequest(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	clock := &fakeClock{now: time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)}
	var orgID int64
	var siteID int32
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Legacy Expiry','legacy-expiry') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class) values ($1,'tomblegacy01','Legacy Expiry','UTC',30) returning id`, orgID).Scan(&siteID); err != nil {
		t.Fatal(err)
	}

	store := New(pool, clock)
	if err := store.Remember(ctx, siteID, "pre-request-tombstone"); err != nil {
		t.Fatal(err)
	}
	clock.now = clock.now.Add(Window + time.Millisecond)
	if err := store.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	if store.Contains(siteID, "pre-request-tombstone") {
		t.Fatal("tombstone without an erasure request survived erased_at plus 48h")
	}
	var count int
	if err := pool.QueryRow(ctx, `select count(*) from profile_tombstones where site_id=$1`, siteID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("persisted tombstones after expiry = %d, want 0", count)
	}
}

func TestRememberTxRollbackDoesNotPublishOrForgetMemory(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	clock := &fakeClock{now: time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)}
	var orgID int64
	var siteID int32
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Rollback','rollback') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class) values ($1,'tombrollbk01','Rollback','UTC',30) returning id`, orgID).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	store := New(pool, clock)
	if err := store.Remember(ctx, siteID, "valid-profile"); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RememberTx(ctx, tx, siteID, "new-profile"); err != nil {
		t.Fatal(err)
	}
	if err := store.RememberTx(ctx, tx, siteID, "valid-profile"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if store.Contains(siteID, "new-profile") {
		t.Fatal("rolled-back tombstone was published to memory")
	}
	if !store.Contains(siteID, "valid-profile") {
		t.Fatal("rollback removed an earlier valid tombstone")
	}
}

func TestStoreRunHonorsConfiguredIntervalAndCancellation(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := NewWithPollInterval(pool, nil, 10*time.Millisecond)
	if got := store.PollInterval(); got != 10*time.Millisecond {
		t.Fatalf("poll interval = %s, want 10ms", got)
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- store.Run(runCtx) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("poll loop did not stop after cancellation")
	}
}
