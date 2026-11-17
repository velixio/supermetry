package profileerasure

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/appproduct"
	"supermetry/internal/db"
	"supermetry/internal/testpg"
)

var appTestSecret = []byte("0123456789abcdef0123456789abcdef")

func appHashOf(id string) []byte {
	h := hmac.New(sha256.New, appTestSecret)
	h.Write([]byte(id))
	return h.Sum(nil)
}

type appFixture struct {
	pool  *pgxpool.Pool
	store *AppStore
}

func newAppFixture(t *testing.T) appFixture {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into orgs(name,slug) values('Org','org')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into users(id,email,name,password_hash) overriding system value values (5,'owner@example.test','Owner','x')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into sites(id,org_id,public_id,name,timezone,retention_class,kind) overriding system value
values(7,1,'appsite00001','App','UTC',30,'app'),(8,1,'appsite00002','Other','UTC',30,'app')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into app_hash_secrets(site_id,secret) values (7,$1)`, appTestSecret); err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	if err := db.EnsureAppEventPartitions(ctx, pool, day, day.AddDate(0, 1, 0)); err != nil {
		t.Fatal(err)
	}
	for _, e := range []struct {
		site int
		id   string
		user string
	}{{7, "a", "alice"}, {7, "b", "alice"}, {7, "c", "bob"}, {7, "d", "alice"}, {8, "e", "alice"}} {
		if _, err := pool.Exec(ctx, `insert into app_events(site_id,ts,received_at,event_id,kind,release,user_hash)
values ($1,'2026-11-17T10:00:00Z','2026-11-17T10:00:00Z',$2,'error','1.0',$3)`, e.site, e.id, appHashOf(e.user)); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `insert into app_event_bodies(site_id,ts,event_id,body,size) values ($1,'2026-11-17T10:00:00Z',$2,'{}',2)`, e.site, e.id); err != nil {
			t.Fatal(err)
		}
	}
	return appFixture{pool: pool, store: NewAppStore(pool)}
}

func (f appFixture) create(t *testing.T, site int32, user string) AppRequest {
	t.Helper()
	var r AppRequest
	err := pgx.BeginFunc(context.Background(), f.pool, func(tx pgx.Tx) error {
		var err error
		r, err = f.store.CreateOrGetTx(context.Background(), tx, site, user, RequestedByUser(5))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func (f appFixture) counts(t *testing.T) (bodies, hashed int) {
	t.Helper()
	if err := f.pool.QueryRow(context.Background(), `select (select count(*) from app_event_bodies where site_id=7),
 (select count(*) from app_events where site_id=7 and user_hash is not null)`).Scan(&bodies, &hashed); err != nil {
		t.Fatal(err)
	}
	return
}

func TestEraseAppInstallRemovesExactlyThatInstall(t *testing.T) {
	f := newAppFixture(t)
	ctx := context.Background()
	if err := db.EnsureAppActiveDayPartitions(ctx, f.pool, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	mine, other := appproduct.VisitorID(appTestSecret, "install-1"), appproduct.VisitorID(appTestSecret, "install-2")
	for _, row := range []struct {
		site    int
		day     string
		visitor int64
	}{{7, "2026-11-16", mine}, {7, "2026-11-17", mine}, {7, "2026-11-17", other}, {8, "2026-11-17", mine}} {
		if _, err := f.pool.Exec(ctx, `insert into app_active_day(site_id,day,visitor_id,events,screens,sessions,engagement_ms) values($1,$2,$3,0,1,1,0)`,
			row.site, row.day, row.visitor); err != nil {
			t.Fatal(err)
		}
	}
	external := appproduct.ProfileExternalID(appTestSecret, "install-1")
	if _, err := f.pool.Exec(ctx, `insert into profiles(site_id,external_id,first_seen_at,last_seen_at) values (7,$1,now(),now())`, external); err != nil {
		t.Fatal(err)
	}
	var rows int64
	err := pgx.BeginFunc(ctx, f.pool, func(tx pgx.Tx) error {
		var err error
		rows, err = EraseAppInstall(ctx, tx, New(f.pool), 7, "install-1", appTestSecret, RequestedByUser(5))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if rows != 2 {
		t.Fatalf("rows deleted = %d, want 2", rows)
	}
	var left, requests int
	if err := f.pool.QueryRow(ctx, `select count(*), (select count(*) from profile_erasure_requests where site_id = 7) from app_active_day`).Scan(&left, &requests); err != nil {
		t.Fatal(err)
	}
	if left != 2 || requests != 1 {
		t.Fatalf("rows left = %d (want 2: other install, other site), profile erasures = %d (want 1)", left, requests)
	}
	// An install without a profile only loses its active days.
	err = pgx.BeginFunc(ctx, f.pool, func(tx pgx.Tx) error {
		_, err := EraseAppInstall(ctx, tx, New(f.pool), 7, "install-2", appTestSecret, RequestedByUser(5))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `select count(*) from profile_erasure_requests`).Scan(&requests); err != nil || requests != 1 {
		t.Fatalf("profile erasures = %d %v", requests, err)
	}
}

func TestAppWorkerErasesBodiesAndHashAndAuditsCountsOnly(t *testing.T) {
	f := newAppFixture(t)
	ctx := context.Background()
	r := f.create(t, 7, "alice")
	if r.Status != StatusPending || !bytes.Equal(r.UserHash, appHashOf("alice")) {
		t.Fatalf("request = %+v", r)
	}
	w := NewAppWorker(f.pool, f.store)
	w.batch = 1 // exercise the batch loops
	if err := w.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := f.store.GetForSite(ctx, r.ID, 7)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusDone || got.BodiesDeleted != 3 || got.EventsAnonymized != 3 || got.CompletedAt == nil {
		t.Fatalf("done request = %+v", got)
	}
	if bodies, hashed := f.counts(t); bodies != 1 || hashed != 1 {
		t.Fatalf("bodies=%d hashed=%d, want only bob's left", bodies, hashed)
	}
	var other, total int
	if err := f.pool.QueryRow(ctx, `select (select count(*) from app_event_bodies where site_id=8),(select count(*) from app_events)`).Scan(&other, &total); err != nil || other != 1 || total != 5 {
		t.Fatalf("other site bodies=%d events=%d err=%v", other, total, err)
	}
	var n int
	var meta string
	if err := f.pool.QueryRow(ctx, `select count(*), coalesce(max(metadata::text),'') from audit_log where action='app.user_erased'`).Scan(&n, &meta); err != nil {
		t.Fatal(err)
	}
	if n != 1 || strings.Contains(meta, "alice") || !strings.Contains(strings.ReplaceAll(meta, " ", ""), `"bodies":3`) {
		t.Fatalf("audit n=%d meta=%s", n, meta)
	}
	// A second pass finds nothing to do and writes no second audit row.
	if err := w.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `select count(*) from audit_log where action='app.user_erased'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("audits after rerun = %d %v", n, err)
	}
}

func TestAppRequestStoresOnlyTheKeyedHash(t *testing.T) {
	f := newAppFixture(t)
	r := f.create(t, 7, "alice-secret-id")
	var dump string
	if err := f.pool.QueryRow(context.Background(), `select r::text from app_erasure_requests r where id=$1`, r.ID).Scan(&dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump, "alice-secret-id") {
		t.Fatalf("raw user id stored: %s", dump)
	}
	var hash []byte
	if err := f.pool.QueryRow(context.Background(), `select user_hash from app_erasure_requests where id=$1`, r.ID).Scan(&hash); err != nil || !bytes.Equal(hash, appHashOf("alice-secret-id")) {
		t.Fatalf("hash = %x err=%v", hash, err)
	}
}

func TestAppRequestRepeatIsIdempotent(t *testing.T) {
	f := newAppFixture(t)
	first := f.create(t, 7, "alice")
	second := f.create(t, 7, "alice")
	if first.ID != second.ID {
		t.Fatalf("repeat created a new request: %d vs %d", first.ID, second.ID)
	}
	other := f.create(t, 7, "bob")
	if other.ID == first.ID {
		t.Fatal("different users share a request")
	}
	w := NewAppWorker(f.pool, f.store)
	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	// After completion the same user can be erased again and reports zeros.
	again := f.create(t, 7, "alice")
	if again.ID == first.ID {
		t.Fatal("a finished request must not be reused")
	}
	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := f.store.GetForSite(context.Background(), again.ID, 7)
	if err != nil || got.Status != StatusDone || got.BodiesDeleted != 0 || got.EventsAnonymized != 0 {
		t.Fatalf("repeat = %+v err=%v", got, err)
	}
}

func TestAppRequestWithoutHashSecretIsBornDone(t *testing.T) {
	f := newAppFixture(t)
	r := f.create(t, 8, "alice") // site 8 never hashed a user
	if r.Status != StatusDone || r.UserHash != nil || r.CompletedAt == nil || r.BodiesDeleted != 0 {
		t.Fatalf("request = %+v", r)
	}
}

func TestAppRequestIsScopedToItsSite(t *testing.T) {
	f := newAppFixture(t)
	r := f.create(t, 7, "alice")
	if _, err := f.store.GetForSite(context.Background(), r.ID, 8); err != ErrNotFound {
		t.Fatalf("other site err = %v, want ErrNotFound", err)
	}
}

func TestAppWorkerResumesAfterRestart(t *testing.T) {
	f := newAppFixture(t)
	ctx := context.Background()
	r := f.create(t, 7, "alice")
	// A first worker got through the bodies of two events (counts committed with
	// each batch) and the process died; its lease has lapsed.
	if _, err := f.pool.Exec(ctx, `delete from app_event_bodies where site_id=7 and event_id in ('a','b')`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `update app_erasure_requests set status='running', bodies_deleted=2, updated_at=now()-interval '1 hour' where id=$1`, r.ID); err != nil {
		t.Fatal(err)
	}
	// A request still inside its lease must not be taken over.
	fresh := f.create(t, 7, "bob")
	if _, err := f.pool.Exec(ctx, `update app_erasure_requests set status='running' where id=$1`, fresh.ID); err != nil {
		t.Fatal(err)
	}
	if err := NewAppWorker(f.pool, f.store).RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := f.store.GetForSite(ctx, r.ID, 7)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusDone || got.BodiesDeleted != 3 || got.EventsAnonymized != 3 || got.Attempts != 1 {
		t.Fatalf("resumed request = %+v", got)
	}
	if bodies, hashed := f.counts(t); bodies != 1 || hashed != 1 {
		t.Fatalf("bodies=%d hashed=%d, want only bob's left", bodies, hashed)
	}
	if f2, err := f.store.GetForSite(ctx, fresh.ID, 7); err != nil || f2.Status != StatusRunning {
		t.Fatalf("leased request was taken over: %+v %v", f2, err)
	}
}

func TestAppWorkerRetriesWithBackoffThenFails(t *testing.T) {
	f := newAppFixture(t)
	ctx := context.Background()
	r := f.create(t, 7, "alice")
	// Make the batch statement fail: the request row exists but the events table is gone.
	if _, err := f.pool.Exec(ctx, `alter table app_event_bodies rename to app_event_bodies_x`); err != nil {
		t.Fatal(err)
	}
	w := NewAppWorker(f.pool, f.store)
	if err := w.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := f.store.GetForSite(ctx, r.ID, 7)
	if got.Status != StatusPending || got.Attempts != 1 || got.LastError == nil {
		t.Fatalf("after failure = %+v", got)
	}
	// Backoff keeps it out of the queue; once due and at the limit it fails and is audited.
	if err := w.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ = f.store.GetForSite(ctx, r.ID, 7); got.Attempts != 1 {
		t.Fatalf("claimed during backoff: %+v", got)
	}
	if _, err := f.pool.Exec(ctx, `update app_erasure_requests set attempts=$2, retry_after=null where id=$1`, r.ID, appMaxAttempts-1); err != nil {
		t.Fatal(err)
	}
	if err := w.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ = f.store.GetForSite(ctx, r.ID, 7); got.Status != StatusFailed {
		t.Fatalf("want failed, got %+v", got)
	}
	// A repeat request revives the failed one rather than queueing a second.
	if again := f.create(t, 7, "alice"); again.ID != r.ID || again.Status != StatusPending || again.Attempts != 0 {
		t.Fatalf("revived = %+v", again)
	}
}

func TestAppWorkerPrunesOldFinishedRequests(t *testing.T) {
	f := newAppFixture(t)
	ctx := context.Background()
	r := f.create(t, 8, "alice")
	if _, err := f.pool.Exec(ctx, `update app_erasure_requests set completed_at=now()-interval '31 days' where id=$1`, r.ID); err != nil {
		t.Fatal(err)
	}
	if err := NewAppWorker(f.pool, f.store).RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.GetForSite(ctx, r.ID, 8); err != ErrNotFound {
		t.Fatalf("old finished request kept: %v", err)
	}
}
