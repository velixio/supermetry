package datapurge

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/db"
	"supermetry/internal/dict"
	"supermetry/internal/testpg"
)

func TestInputValidation(t *testing.T) {
	day := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for name, in := range map[string]Input{
		"no filter":      {From: day, To: day},
		"reversed range": {From: day.AddDate(0, 0, 1), To: day, Source: "x.test"},
		"missing range":  {Source: "x.test"},
		"too long":       {From: day, To: day.AddDate(2, 0, 0), Source: "x.test"},
		"bad country":    {From: day, To: day, Country: "USA"},
		"digit country":  {From: day, To: day, Country: "U1"},
	} {
		if _, err := in.normalized(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: error=%v, want ErrInvalid", name, err)
		}
	}
	got, err := Input{From: day, To: day, Country: " in "}.normalized()
	if err != nil || got.Country != "IN" {
		t.Fatalf("country filter = %+v, %v", got, err)
	}
}

func TestWorkerDeletesMatchingSessionsOnly(t *testing.T) {
	pool, siteID, spamSource := newFixture(t)
	ctx := context.Background()
	day := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)

	request, err := Create(ctx, pool, Input{SiteID: siteID, From: day, To: day, Source: "SPAM.test", RequestedBy: "user:1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Create(ctx, pool, Input{SiteID: siteID, From: day, To: day, Country: "US", RequestedBy: "user:1"}); !errors.Is(err, ErrBusy) {
		t.Fatalf("second active purge error=%v, want ErrBusy", err)
	}
	worker := NewWorker(pool, nil)
	if err := worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if status := statusOf(t, pool, request.ID); status != statusSettle {
		t.Fatalf("after first pass status=%q, want settling", status)
	}
	// A session that arrived after the first pass is picked up by the second.
	insertSession(t, pool, siteID, 3, 30, day.Add(time.Hour), spamSource, "BR")
	if _, err := pool.Exec(ctx, `update data_purge_requests set settle_after=now()-interval '1 second' where id=$1`, request.ID); err != nil {
		t.Fatal(err)
	}
	if err := worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if status := statusOf(t, pool, request.ID); status != statusDone {
		t.Fatalf("final status=%q, want done", status)
	}
	var kept, sessions, closed, custom, verified, deleted int
	for table, target := range map[string]*int{"events": &kept, "sessions": &sessions, "sessions_closed": &closed, "custom_closed": &custom, "verified_page_sessions": &verified} {
		if err := pool.QueryRow(ctx, `select count(*) from `+table+` where site_id=$1`, siteID).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	if kept != 1 || sessions != 1 || closed != 1 || custom != 1 || verified != 1 {
		t.Fatalf("remaining events=%d sessions=%d closed=%d custom=%d verified=%d, want one of each (the Google session)", kept, sessions, closed, custom, verified)
	}
	if err := pool.QueryRow(ctx, `select sessions_deleted from data_purge_requests where id=$1`, request.ID).Scan(&deleted); err != nil || deleted != 2 {
		t.Fatalf("sessions_deleted=%d err=%v, want 2", deleted, err)
	}
	var audits int
	if err := pool.QueryRow(ctx, `select count(*) from audit_log where action='site.data_purged'`).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("audit rows=%d err=%v, want 1", audits, err)
	}
	if err := worker.RunOnce(ctx); err != nil {
		t.Fatalf("idle run: %v", err)
	}
}

func TestWorkerCountryFilterAndUnknownSource(t *testing.T) {
	pool, siteID, _ := newFixture(t)
	ctx := context.Background()
	day := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	worker := NewWorker(pool, nil)

	// A source that was never seen deletes nothing and still completes.
	unknown, err := Create(ctx, pool, Input{SiteID: siteID, From: day, To: day, Source: "never-seen.test", RequestedBy: "user:1"})
	if err != nil {
		t.Fatal(err)
	}
	runToDone(t, pool, worker, unknown.ID)
	var sessions int
	if err := pool.QueryRow(ctx, `select count(*) from sessions where site_id=$1`, siteID).Scan(&sessions); err != nil || sessions != 2 {
		t.Fatalf("sessions after unknown source = %d err=%v, want 2", sessions, err)
	}

	byCountry, err := Create(ctx, pool, Input{SiteID: siteID, From: day, To: day, Country: "us", RequestedBy: "user:1"})
	if err != nil {
		t.Fatal(err)
	}
	runToDone(t, pool, worker, byCountry.ID)
	if err := pool.QueryRow(ctx, `select count(*) from sessions where site_id=$1 and country='US'`, siteID).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatalf("US sessions left = %d err=%v", sessions, err)
	}
	if err := pool.QueryRow(ctx, `select count(*) from sessions where site_id=$1`, siteID).Scan(&sessions); err != nil || sessions != 1 {
		t.Fatalf("sessions left = %d err=%v, want the IN one", sessions, err)
	}
}

func runToDone(t *testing.T, pool *pgxpool.Pool, worker *Worker, id int64) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < 3 && statusOf(t, pool, id) != statusDone; i++ {
		if _, err := pool.Exec(ctx, `update data_purge_requests set settle_after=now()-interval '1 second' where id=$1 and status='settling'`, id); err != nil {
			t.Fatal(err)
		}
		if err := worker.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if status := statusOf(t, pool, id); status != statusDone {
		t.Fatalf("request %d status=%q, want done", id, status)
	}
}

func statusOf(t *testing.T, pool *pgxpool.Pool, id int64) string {
	t.Helper()
	var status string
	if err := pool.QueryRow(context.Background(), `select status from data_purge_requests where id=$1`, id).Scan(&status); err != nil {
		t.Fatal(err)
	}
	return status
}

// newFixture creates a site with two sessions on 2026-10-09: one from
// spam.test in the US (session 1) and one from Google in India (session 2).
func newFixture(t *testing.T) (*pgxpool.Pool, int32, int32) {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	if err := db.EnsurePartitions(ctx, pool, day.AddDate(0, 0, -1), day.AddDate(0, 0, 2)); err != nil {
		t.Fatal(err)
	}
	var orgID int64
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Purge','purge') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into users(email,name,password_hash) values ('purge@example.test','Purge','x')`); err != nil {
		t.Fatal(err)
	}
	var siteID int32
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class) values ($1,'purgesite001','Purge','UTC',30) returning id`, orgID).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	var spam, google int32
	if err := pool.QueryRow(ctx, `insert into dict(kind,value) values ($1,'spam.test') returning id`, int16(dict.Source)).Scan(&spam); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into dict(kind,value) values ($1,'Google') returning id`, int16(dict.Source)).Scan(&google); err != nil {
		t.Fatal(err)
	}
	insertSession(t, pool, siteID, 1, 10, day.Add(10*time.Hour), spam, "US")
	insertSession(t, pool, siteID, 2, 20, day.Add(11*time.Hour), google, "IN")
	return pool, siteID, spam
}

func insertSession(t *testing.T, pool *pgxpool.Pool, siteID int32, sessionID, visitorID int64, at time.Time, source int32, country string) {
	t.Helper()
	ctx := context.Background()
	day := at.Truncate(24 * time.Hour)
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`insert into events(site_id,retention_class,local_day,ts,visitor_id,session_id,kind) values ($1,30,$2::date,$3::timestamptz,$4,$5,0)`, []any{siteID, day, at, visitorID, sessionID}},
		{`insert into sessions(site_id,retention_class,session_id,visitor_id,start,last,local_day,source_id,country) values ($1,30,$2,$3,$4,$4,$5,$6,$7)`, []any{siteID, sessionID, visitorID, at, day, source, country}},
		{`insert into sessions_closed(site_id,day,visitor_id,session_id,pageviews,custom,duration_ms,engagement_ms,vd_n,flags,seq,seq_engage,seq_scroll,source_id,country,started_at) values ($1,$2,$3,$4,1,1,0,0,1,0,'{}','{}','{}',$5,$6,$7)`, []any{siteID, day, visitorID, sessionID, source, country, at}},
		{`insert into custom_closed(site_id,day,visitor_id,name_id,path_id,session_id) values ($1,$2,$3,1,1,$4)`, []any{siteID, day, visitorID, sessionID}},
		{`insert into verified_page_sessions(site_id,day,session_id,visitor_id,path_id,path_policy_id,path_sanitizer_version,pageviews) values ($1,$2,$3,$4,1,'policy',1,1)`, []any{siteID, day, sessionID, visitorID}},
	} {
		if _, err := pool.Exec(ctx, statement.sql, statement.args...); err != nil {
			t.Fatalf("seed session %d: %v", sessionID, err)
		}
	}
}
