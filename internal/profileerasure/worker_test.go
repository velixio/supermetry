package profileerasure

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"supermetry/internal/api"
	"supermetry/internal/db"
	"supermetry/internal/dict"
	"supermetry/internal/privacy"
	"supermetry/internal/query"
	"supermetry/internal/replay"
	"supermetry/internal/sites"
	"supermetry/internal/testpg"
)

type fakeWorkerClock struct{ now time.Time }

func (c fakeWorkerClock) Now() time.Time { return c.now }

type workerRegistry struct{ site sites.Site }

func (r workerRegistry) ByPublicID(id string) (sites.Site, bool) {
	return r.site, id == r.site.PublicID
}
func (r workerRegistry) Site(id int32) (sites.Site, bool) { return r.site, id == r.site.ID }
func (r workerRegistry) All() []sites.Site                { return []sites.Site{r.site} }

type failingBlobStore struct {
	replay.BlobStore
	fail error
}

func (s *failingBlobStore) Delete(ctx context.Context, key string) (int64, error) {
	if s.fail != nil {
		err := s.fail
		s.fail = nil
		return 0, err
	}
	return s.BlobStore.Delete(ctx, key)
}

func TestWorkerDeletesRowsSettlesLateWritesAndFinishes(t *testing.T) {
	fixture := newWorkerFixture(t)
	ctx := context.Background()
	worker := NewWorker(fixture.pool, fixture.store, fixture.blobs, fakeWorkerClock{now: fixture.now})

	if err := worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	request, err := fixture.store.Get(ctx, fixture.request.ID)
	if err != nil {
		t.Fatal(err)
	}
	if request.Status != StatusSettling || request.SettleAfter == nil || !request.SettleAfter.Equal(fixture.now.Add(2*time.Minute)) {
		t.Fatalf("first pass request=%+v last_error=%v", request, errorText(request.LastError))
	}
	assertErasedCounts(t, fixture.pool, fixture.siteID, fixture.profileID, 0, 0, 0, 0, 0, 0)
	if reader, err := fixture.blobs.Open(ctx, fixture.blobKey); err == nil {
		_ = reader.Close()
		t.Fatal("first pass left replay blob")
	}

	// A write already accepted before the tombstone can arrive after the first
	// pass may have had its profile link stripped by the writer guard. The
	// settling pass must still remove it and its session from the durable key.
	if _, err := fixture.pool.Exec(ctx, `
		insert into events(site_id,retention_class,local_day,ts,visitor_id,session_id,kind,profile_id)
		values ($1,30,$2,$3,10,100,1,null)`, fixture.siteID, fixture.now, fixture.now); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(ctx, `
		insert into sessions(site_id,retention_class,session_id,visitor_id,start,last,local_day)
		values ($1,30,100,10,$3,$3,$2)`, fixture.siteID, fixture.now, fixture.now); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(ctx, `update profile_erasure_requests set settle_after=now()-interval '1 second' where id=$1`, fixture.request.ID); err != nil {
		t.Fatal(err)
	}
	if err := worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	request, err = fixture.store.Get(ctx, fixture.request.ID)
	if err != nil {
		t.Fatal(err)
	}
	if request.Status != StatusDone || request.CompletedAt == nil {
		t.Fatalf("settle request=%+v", request)
	}
	assertErasedCounts(t, fixture.pool, fixture.siteID, fixture.profileID, 0, 0, 0, 0, 0, 0)
	var audits int
	if err := fixture.pool.QueryRow(ctx, `select count(*) from audit_log where action='profile.erased' and target_id=$1`, fixture.profileID).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits != 1 {
		t.Fatalf("profile.erased audits=%d, want 1", audits)
	}
	if err := worker.RunOnce(ctx); err != nil {
		t.Fatalf("idempotent completed run: %v", err)
	}
}

func TestWorkerRebuildsClosedDayAndInvalidatesCachedQuery(t *testing.T) {
	fixture := newWorkerFixture(t)
	ctx := context.Background()
	day := fixture.now.UTC().Truncate(24 * time.Hour)
	for _, statement := range []string{
		`update sites set created_at=$2 where id=$1`,
		`insert into day_close(site_id,day,status,version,rows) values ($1,$2,'done',1,1)`,
		`insert into rollup_day(site_id,day,visitors,visits,pageviews,bounces,duration_ms,engagement_ms,scroll_sum,scroll_n)
		 values ($1,$2,1,1,1,0,0,0,0,0)`,
	} {
		if _, err := fixture.pool.Exec(ctx, statement, fixture.siteID, day); err != nil {
			t.Fatal(err)
		}
	}
	dictionary := dict.New(fixture.pool)
	if err := dictionary.Load(ctx); err != nil {
		t.Fatal(err)
	}
	reg := workerRegistry{site: sites.Site{ID: fixture.siteID, PublicID: "erasure-cache", TZ: time.UTC, Retention: db.Ret30, ProfilesEnabled: true}}
	engine := query.NewEngine(fixture.pool, dictionary, reg)
	engine.SetTrackingMode(privacy.Identified)
	engine.SetCache(query.NewCache(16))
	dateRange := api.DateRange{
		From: openapi_types.Date{Time: day},
		To:   openapi_types.Date{Time: day},
	}
	request := api.QueryRequest{SiteId: fixture.siteID, DateRange: &dateRange, Metrics: []api.Metric{api.Visitors, api.Visits, api.Pageviews}}
	queryTotals := func() api.QueryResponse {
		t.Helper()
		response, err := engine.Query(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	if first := queryTotals(); first.Meta.Cached || first.Totals["pageviews"] != 1 {
		t.Fatalf("first query = %+v, want uncached pageview", first)
	}
	if cached := queryTotals(); !cached.Meta.Cached || cached.Totals["pageviews"] != 1 {
		t.Fatalf("cached query = %+v, want cached pageview", cached)
	}

	worker := NewWorker(fixture.pool, fixture.store, fixture.blobs, fakeWorkerClock{now: fixture.now}, engine.InvalidateSite)
	if err := worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(ctx, `update profile_erasure_requests set settle_after=now()-interval '1 second' where id=$1`, fixture.request.ID); err != nil {
		t.Fatal(err)
	}
	if err := worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	finished, err := fixture.store.Get(ctx, fixture.request.ID)
	if err != nil {
		t.Fatal(err)
	}
	if finished.Status != StatusDone {
		t.Fatalf("finished request = %+v last_error=%v", finished, errorText(finished.LastError))
	}

	after := queryTotals()
	if after.Meta.Cached || after.Totals["pageviews"] != 0 || after.Totals["visits"] != 0 || after.Totals["visitors"] != 0 {
		t.Fatalf("query after erasure = %+v, want uncached zero totals", after)
	}
	var dirty int
	if err := fixture.pool.QueryRow(ctx, `select count(*) from dirty_day where site_id=$1 and day=$2`, fixture.siteID, day).Scan(&dirty); err != nil {
		t.Fatal(err)
	}
	if dirty != 0 {
		t.Fatalf("dirty rows after rebuild = %d, want 0", dirty)
	}
	if err := worker.RunOnce(ctx); err != nil {
		t.Fatalf("idempotent rerun: %v", err)
	}
}

func TestWorkerCommitsEachDeletionBatch(t *testing.T) {
	fixture := newWorkerFixture(t)
	ctx := context.Background()
	if _, err := fixture.pool.Exec(ctx, `
		create table profile_erasure_delete_txids (txid bigint not null);
		create function record_profile_erasure_delete_txid() returns trigger language plpgsql as $$
		begin
			insert into profile_erasure_delete_txids values (txid_current());
			return old;
		end
		$$;
		create trigger profile_erasure_delete_txid after delete on events
		for each row execute function record_profile_erasure_delete_txid()`); err != nil {
		t.Fatal(err)
	}
	worker := NewWorker(fixture.pool, fixture.store, fixture.blobs, fakeWorkerClock{now: fixture.now})
	worker.batchSize = 1

	if err := worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	var rows, transactions int
	if err := fixture.pool.QueryRow(ctx, `
		select count(*),count(distinct txid) from profile_erasure_delete_txids`).Scan(&rows, &transactions); err != nil {
		t.Fatal(err)
	}
	if rows != 2 || transactions != 2 {
		t.Fatalf("event deletion batches used rows=%d transactions=%d, want 2 rows across 2 transactions", rows, transactions)
	}
}

func TestWorkerCapturesKeysFromTheDeletedEventBatch(t *testing.T) {
	fixture := newWorkerFixture(t)
	ctx := context.Background()
	for _, table := range []string{"replay_sessions", "verified_page_sessions", "custom_closed", "sessions_closed", "sessions", "events"} {
		if _, err := fixture.pool.Exec(ctx, "delete from "+table); err != nil {
			t.Fatal(err)
		}
	}
	// Force the profile lookup to use its timestamp-ordered index while the
	// old OR-based delete uses heap order. Separate LIMIT scans then select
	// different rows; an atomic doomed CTE cannot.
	connections := make([]*pgxpool.Conn, 0, fixture.pool.Config().MaxConns)
	for range fixture.pool.Config().MaxConns {
		conn, err := fixture.pool.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		connections = append(connections, conn)
		if _, err := conn.Exec(ctx, "set enable_seqscan=off"); err != nil {
			t.Fatal(err)
		}
	}
	for _, conn := range connections {
		conn.Release()
	}
	if _, err := fixture.pool.Exec(ctx, `
		insert into events(site_id,retention_class,local_day,ts,visitor_id,session_id,kind,profile_id)
		values ($1,30,$2,$3,20,200,1,$5),
		       ($1,30,$2,$4,30,300,1,$5)`,
		fixture.siteID, fixture.now, fixture.now.Add(-time.Minute), fixture.now, fixture.profileID); err != nil {
		t.Fatal(err)
	}
	worker := NewWorker(fixture.pool, fixture.store, fixture.blobs, fakeWorkerClock{now: fixture.now})
	worker.batchSize = 1
	if err := worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(ctx, `
		insert into events(site_id,retention_class,local_day,ts,visitor_id,session_id,kind,profile_id)
		values ($1,30,$2,$3,20,200,1,null)`, fixture.siteID, fixture.now, fixture.now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(ctx, `
		update profile_erasure_requests set settle_after=now()-interval '1 second' where id=$1`, fixture.request.ID); err != nil {
		t.Fatal(err)
	}
	if err := worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	var lateEvents int
	if err := fixture.pool.QueryRow(ctx, `select count(*) from events where site_id=$1 and session_id=200`, fixture.siteID).Scan(&lateEvents); err != nil {
		t.Fatal(err)
	}
	if lateEvents != 0 {
		t.Fatalf("late anonymous events for the second-batch session = %d, want 0", lateEvents)
	}
}

func TestProfileEventBatchUsesProfileIndex(t *testing.T) {
	fixture := newWorkerFixture(t)
	ctx := context.Background()
	conn, err := fixture.pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "set enable_seqscan=off"); err != nil {
		t.Fatal(err)
	}
	rows, err := conn.Query(ctx, `
		explain (costs off)
		select tableoid,ctid,session_id,visitor_id,coalesce(local_day,(ts at time zone 'UTC')::date),ts
		from events where site_id=$1 and profile_id=$2 limit 10000`, fixture.siteID, fixture.profileID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(line)
		plan.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if got := plan.String(); !strings.Contains(got, "Index") || !strings.Contains(got, "profile") {
		t.Fatalf("profile event lookup did not use its profile index:\n%s", got)
	}
}

func TestWorkerRecordsSessionKeyWhenLocalDayIsNull(t *testing.T) {
	fixture := newWorkerFixture(t)
	ctx := context.Background()
	if _, err := fixture.pool.Exec(ctx, `delete from events where site_id=$1`, fixture.siteID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(ctx, `
		insert into events(site_id,retention_class,local_day,ts,visitor_id,session_id,kind,profile_id)
		values ($1,30,null,$2,40,400,1,$3)`, fixture.siteID, fixture.now, fixture.profileID); err != nil {
		t.Fatal(err)
	}
	worker := NewWorker(fixture.pool, fixture.store, fixture.blobs, fakeWorkerClock{now: fixture.now})
	if err := worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	var localDay time.Time
	if err := fixture.pool.QueryRow(ctx, `
		select local_day from profile_erasure_sessions where request_id=$1 and session_id=400`, fixture.request.ID).Scan(&localDay); err != nil {
		t.Fatal(err)
	}
	if want := fixture.now.UTC().Truncate(24 * time.Hour); !localDay.Equal(want) {
		t.Fatalf("fallback local day = %s, want %s", localDay, want)
	}
}

func TestWorkerRetriesBlobDeletionWithoutLosingWork(t *testing.T) {
	fixture := newWorkerFixture(t)
	ctx := context.Background()
	fault := &failingBlobStore{BlobStore: fixture.blobs, fail: errors.New("blob storage unavailable")}
	worker := NewWorker(fixture.pool, fixture.store, fault, fakeWorkerClock{now: fixture.now})

	if err := worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	request, err := fixture.store.Get(ctx, fixture.request.ID)
	if err != nil {
		t.Fatal(err)
	}
	if request.Status != StatusPending || request.Attempts != 1 {
		t.Fatalf("failed request=%+v last_error=%v", request, errorText(request.LastError))
	}
	pending, err := fixture.store.PendingBlobs(ctx, request.ID, 10)
	if err != nil || len(pending) != 1 || pending[0].BlobKey != fixture.blobKey {
		t.Fatalf("pending blobs=%+v err=%v", pending, err)
	}
	reader, err := fixture.blobs.Open(ctx, fixture.blobKey)
	if err != nil {
		t.Fatalf("failed delete lost blob: %v", err)
	}
	_ = reader.Close()

	if _, err := fixture.pool.Exec(ctx, `update profile_erasure_requests set settle_after=now()-interval '1 second' where id=$1`, request.ID); err != nil {
		t.Fatal(err)
	}
	if err := worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	request, err = fixture.store.Get(ctx, request.ID)
	if err != nil {
		t.Fatal(err)
	}
	if request.Status != StatusSettling {
		t.Fatalf("retried request=%+v", request)
	}
	pending, err = fixture.store.PendingBlobs(ctx, request.ID, 10)
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending after retry=%+v err=%v", pending, err)
	}
}

func TestWorkerRecoversStaleRunningClaim(t *testing.T) {
	fixture := newWorkerFixture(t)
	ctx := context.Background()
	claimed, err := fixture.store.Claim(ctx, 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("initial claim=%+v err=%v", claimed, err)
	}
	if _, err := fixture.pool.Exec(ctx, `update profile_erasure_requests set updated_at=now()-interval '11 minutes' where id=$1`, fixture.request.ID); err != nil {
		t.Fatal(err)
	}
	restarted := NewWorker(fixture.pool, fixture.store, fixture.blobs, fakeWorkerClock{now: fixture.now})
	if err := restarted.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	request, err := fixture.store.Get(ctx, fixture.request.ID)
	if err != nil || request.Status != StatusSettling || request.Attempts != 1 {
		t.Fatalf("recovered request=%+v last_error=%v err=%v", request, errorText(request.LastError), err)
	}
}

func TestWorkerContinuesWhenRecordingFailureFails(t *testing.T) {
	fixture := newWorkerFixture(t)
	ctx := context.Background()
	var secondProfileID int64
	if err := fixture.pool.QueryRow(ctx, `
		insert into profiles(site_id,external_id,first_seen_at,last_seen_at)
		values ($1,'customer-2',$2,$2) returning id`, fixture.siteID, fixture.now).Scan(&secondProfileID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(ctx, `
		insert into events(site_id,retention_class,local_day,ts,visitor_id,session_id,kind,profile_id)
		values ($1,30,$2,$3,20,200,1,$4)`, fixture.siteID, fixture.now, fixture.now, secondProfileID); err != nil {
		t.Fatal(err)
	}
	var second Request
	if err := pgx.BeginFunc(ctx, fixture.pool, func(tx pgx.Tx) error {
		var err error
		second, err = fixture.store.CreateOrGetTx(ctx, tx, fixture.siteID, secondProfileID, "customer-2", fixture.request.RequestedBy)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(ctx, fmt.Sprintf(`
		create function reject_first_erasure_failure() returns trigger language plpgsql as $$
		begin
			if old.id = %d and new.attempts > old.attempts then
				raise exception 'cannot record failure';
			end if;
			return new;
		end
		$$;
		create trigger reject_first_erasure_failure before update on profile_erasure_requests
		for each row execute function reject_first_erasure_failure()`, fixture.request.ID)); err != nil {
		t.Fatal(err)
	}
	fault := &failingBlobStore{BlobStore: fixture.blobs, fail: errors.New("blob storage unavailable")}
	worker := NewWorker(fixture.pool, fixture.store, fault, fakeWorkerClock{now: fixture.now})

	if err := worker.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce abandoned later claims: %v", err)
	}
	got, err := fixture.store.Get(ctx, second.ID)
	if err != nil || got.Status != StatusSettling {
		t.Fatalf("second request = %+v, err=%v; want settling", got, err)
	}
}

func TestWorkerContinuesWhenReloadingFailureFails(t *testing.T) {
	fixture := newWorkerFixture(t)
	ctx := context.Background()
	var secondProfileID int64
	if err := fixture.pool.QueryRow(ctx, `
		insert into profiles(site_id,external_id,first_seen_at,last_seen_at)
		values ($1,'customer-reload-2',$2,$2) returning id`, fixture.siteID, fixture.now).Scan(&secondProfileID); err != nil {
		t.Fatal(err)
	}
	var second Request
	if err := pgx.BeginFunc(ctx, fixture.pool, func(tx pgx.Tx) error {
		var err error
		second, err = fixture.store.CreateOrGetTx(ctx, tx, fixture.siteID, secondProfileID, "customer-reload-2", fixture.request.RequestedBy)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(ctx, fmt.Sprintf(`
		create function remove_first_erasure_after_failure() returns trigger language plpgsql as $$
		begin
			if new.id = %d and new.attempts > old.attempts then
				delete from profile_erasure_requests where id=new.id;
			end if;
			return null;
		end
		$$;
		create trigger remove_first_erasure_after_failure after update on profile_erasure_requests
		for each row execute function remove_first_erasure_after_failure()`, fixture.request.ID)); err != nil {
		t.Fatal(err)
	}
	fault := &failingBlobStore{BlobStore: fixture.blobs, fail: errors.New("blob storage unavailable")}
	worker := NewWorker(fixture.pool, fixture.store, fault, fakeWorkerClock{now: fixture.now})
	if err := worker.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce abandoned later claims: %v", err)
	}
	got, err := fixture.store.Get(ctx, second.ID)
	if err != nil || got.Status != StatusSettling {
		t.Fatalf("second request = %+v, err=%v; want settling", got, err)
	}
}

func TestWorkerContinuesWhenFailureAuditFails(t *testing.T) {
	fixture := newWorkerFixture(t)
	ctx := context.Background()
	var secondProfileID int64
	if err := fixture.pool.QueryRow(ctx, `
		insert into profiles(site_id,external_id,first_seen_at,last_seen_at)
		values ($1,'customer-audit-2',$2,$2) returning id`, fixture.siteID, fixture.now).Scan(&secondProfileID); err != nil {
		t.Fatal(err)
	}
	var second Request
	if err := pgx.BeginFunc(ctx, fixture.pool, func(tx pgx.Tx) error {
		var err error
		second, err = fixture.store.CreateOrGetTx(ctx, tx, fixture.siteID, secondProfileID, "customer-audit-2", fixture.request.RequestedBy)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(ctx, `update profile_erasure_requests set attempts=19 where id=$1`, fixture.request.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(ctx, fmt.Sprintf(`
		create function reject_first_erasure_audit() returns trigger language plpgsql as $$
		begin
			if new.action = 'profile.erasure_failed' and new.target_id = %d then
				raise exception 'cannot record erasure audit';
			end if;
			return new;
		end
		$$;
		create trigger reject_first_erasure_audit before insert on audit_log
		for each row execute function reject_first_erasure_audit()`, fixture.profileID)); err != nil {
		t.Fatal(err)
	}
	fault := &failingBlobStore{BlobStore: fixture.blobs, fail: errors.New("blob storage unavailable")}
	worker := NewWorker(fixture.pool, fixture.store, fault, fakeWorkerClock{now: fixture.now})
	if err := worker.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce abandoned later claims: %v", err)
	}
	got, err := fixture.store.Get(ctx, second.ID)
	if err != nil || got.Status != StatusSettling {
		t.Fatalf("second request = %+v, err=%v; want settling", got, err)
	}
}

func TestWorkerAuditsTerminalFailure(t *testing.T) {
	fixture := newWorkerFixture(t)
	ctx := context.Background()
	if _, err := fixture.pool.Exec(ctx, `update profile_erasure_requests set attempts=19 where id=$1`, fixture.request.ID); err != nil {
		t.Fatal(err)
	}
	fault := &failingBlobStore{BlobStore: fixture.blobs, fail: errors.New("blob storage unavailable")}
	worker := NewWorker(fixture.pool, fixture.store, fault, fakeWorkerClock{now: fixture.now})
	if err := worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	request, err := fixture.store.Get(ctx, fixture.request.ID)
	if err != nil || request.Status != StatusFailed || request.Attempts != 20 {
		t.Fatalf("terminal request=%+v err=%v", request, err)
	}
	var audits int
	if err := fixture.pool.QueryRow(ctx, `select count(*) from audit_log where action='profile.erasure_failed' and target_id=$1`, fixture.profileID).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits != 1 {
		t.Fatalf("failure audits=%d, want 1", audits)
	}
}

func errorText(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

type workerFixture struct {
	pool      *pgxpool.Pool
	store     *Store
	blobs     replay.BlobStore
	now       time.Time
	siteID    int32
	profileID int64
	request   Request
	blobKey   string
}

func newWorkerFixture(t *testing.T) workerFixture {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)
	if err := db.EnsurePartitions(ctx, pool, now.AddDate(0, 0, -1), now.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	var orgID, userID int64
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Worker','erasure-worker') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash) values ('worker@example.test','Worker','x') returning id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	var siteID int32
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class) values ($1,'eraseworker1','Worker','UTC',30) returning id`, orgID).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	var profileID int64
	if err := pool.QueryRow(ctx, `insert into profiles(site_id,external_id,first_seen_at,last_seen_at) values ($1,'customer-1',$2,$2) returning id`, siteID, now).Scan(&profileID); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`insert into events(site_id,retention_class,local_day,ts,visitor_id,session_id,kind,profile_id) values ($1,30,$2::date,$3::timestamptz,10,100,1,$4),($1,30,$2::date,$3::timestamptz,10,100,0,null)`, []any{siteID, now, now, profileID}},
		{`insert into sessions(site_id,retention_class,session_id,visitor_id,start,last,local_day) values ($1,30,100,10,$3,$3,$2)`, []any{siteID, now, now}},
		{`insert into sessions_closed(site_id,day,visitor_id,session_id,pageviews,custom,duration_ms,engagement_ms,vd_n,flags,seq,seq_engage,seq_scroll) values ($1,$2,10,100,1,1,0,0,1,0,'{}','{}','{}')`, []any{siteID, now}},
		{`insert into custom_closed(site_id,day,visitor_id,name_id,path_id) values ($1,$2,10,1,1)`, []any{siteID, now}},
		{`insert into verified_page_sessions(site_id,day,session_id,visitor_id,path_id,path_policy_id,path_sanitizer_version,pageviews) values ($1,$2,100,10,1,'policy',1,1)`, []any{siteID, now}},
	} {
		if _, err := pool.Exec(ctx, statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	blobs, err := replay.NewDiskStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	blobKey := replay.SessionKey(siteID, now, 100)
	if _, err := blobs.Append(ctx, blobKey, strings.NewReader("recording")); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into replay_sessions(site_id,session_id,started_at,last_seen_at,day,bytes,pages,profile_id,blob_key) values ($1,100,$2::timestamptz,$2::timestamptz,$2::date,9,1,$3,$4)`, siteID, now, profileID, blobKey); err != nil {
		t.Fatal(err)
	}
	store := New(pool)
	var request Request
	if err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		var err error
		request, err = store.CreateOrGetTx(ctx, tx, siteID, profileID, "customer-1", RequestedByUser(userID))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return workerFixture{pool: pool, store: store, blobs: blobs, now: now, siteID: siteID, profileID: profileID, request: request, blobKey: blobKey}
}

func assertErasedCounts(t *testing.T, pool *pgxpool.Pool, siteID int32, profileID int64, wantProfiles, wantEvents, wantSessions, wantClosed, wantCustom, wantVerified int) {
	t.Helper()
	ctx := context.Background()
	queries := []struct {
		query string
		args  []any
		want  int
	}{
		{`select count(*) from profiles where site_id=$1 and id=$2`, []any{siteID, profileID}, wantProfiles},
		{`select count(*) from events where site_id=$1 and (profile_id=$2 or session_id=100)`, []any{siteID, profileID}, wantEvents},
		{`select count(*) from sessions where site_id=$1 and session_id=100`, []any{siteID}, wantSessions},
		{`select count(*) from sessions_closed where site_id=$1 and session_id=100`, []any{siteID}, wantClosed},
		{`select count(*) from custom_closed where site_id=$1 and visitor_id=10`, []any{siteID}, wantCustom},
		{`select count(*) from verified_page_sessions where site_id=$1 and session_id=100`, []any{siteID}, wantVerified},
	}
	for _, query := range queries {
		var got int
		if err := pool.QueryRow(ctx, query.query, query.args...).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != query.want {
			t.Fatalf("%s: got %d, want %d", query.query, got, query.want)
		}
	}
}
