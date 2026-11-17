package retention_test

import (
	"context"
	"testing"
	"time"

	"supermetry/internal/db"
	"supermetry/internal/retention"
	"supermetry/internal/testpg"
)

func TestRunDropsExpiredAppEventPartitions(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into orgs(name,slug) values('Org','org');
insert into sites(id,org_id,public_id,name,timezone,retention_class,kind) overriding system value values(1,1,'appret001abc','App','UTC',30,'app')`); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 7, 3, 0, 0, 0, time.UTC)
	if err := db.EnsureAppEventPartitions(ctx, pool, now.AddDate(0, -3, 0), now.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	dropped, err := retention.Run(ctx, pool, now)
	if err != nil {
		t.Fatal(err)
	}
	if !containsPrefix(dropped, "app_events_202607") || !containsPrefix(dropped, "app_events_202608") {
		t.Fatalf("expected July and August app_events partitions dropped, got %v", dropped)
	}
	if containsPrefix(dropped, "app_events_202609") || containsPrefix(dropped, "app_events_202610") {
		t.Fatalf("dropped an unexpired app_events partition: %v", dropped)
	}
}

func TestRunKeepsAppEventsWhenASiteKeepsForever(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into orgs(name,slug) values('Org','org');
insert into sites(id,org_id,public_id,name,timezone,retention_class,kind) overriding system value values(1,1,'appret002abc','App','UTC',0,'app')`); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 7, 3, 0, 0, 0, time.UTC)
	if err := db.EnsureAppEventPartitions(ctx, pool, now.AddDate(0, -3, 0), now); err != nil {
		t.Fatal(err)
	}
	dropped, err := retention.Run(ctx, pool, now)
	if err != nil {
		t.Fatal(err)
	}
	if containsPrefix(dropped, "app_events_") {
		t.Fatalf("forever retention must keep app_events: %v", dropped)
	}
}

func TestRunDeletesExpiredAppRollupsAndEraseRemovesThem(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into orgs(name,slug) values('Org','org');
insert into sites(id,org_id,public_id,name,timezone,retention_class,kind) overriding system value values
 (1,1,'appret003abc','App','UTC',30,'app'), (2,1,'appret004abc','Forever','UTC',0,'app');
insert into app_issues(id,site_id,fingerprint,kind,first_seen,last_seen) overriding system value values
 (1,1,'a','crash',now(),now()), (2,2,'b','crash',now(),now())`); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 7, 3, 0, 0, 0, time.UTC)
	for _, site := range []int{1, 2} {
		for _, day := range []string{"2026-08-01", "2026-10-06"} {
			for _, q := range []string{
				`insert into app_issue_daily(site_id,issue_id,day,events) values($1::int,$1::int,$2::date,1)`,
				`insert into app_release_daily(site_id,day,release,platform,events) values($1::int,$2::date,'1.0','linux',1)`,
				`insert into app_session_daily(site_id,day,release,environment,platform,sessions) values($1::int,$2::date,'1.0','production','linux',1)`,
				`insert into app_issue_hourly(site_id,issue_id,hour,events) values($1::int,$1::int,$2::date + time '05:00',1)`,
				`insert into app_client_reports(site_id,day,reason,category,quantity) values($1::int,$2::date,'sample_rate','error',1)`,
			} {
				if _, err := pool.Exec(ctx, q, site, day); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	if _, err := retention.Run(ctx, pool, now); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"app_issue_daily", "app_release_daily", "app_session_daily", "app_issue_hourly", "app_client_reports"} {
		var s1, s2 int
		if err := pool.QueryRow(ctx, `select count(*) filter (where site_id=1), count(*) filter (where site_id=2) from `+table).Scan(&s1, &s2); err != nil {
			t.Fatal(err)
		}
		if s1 != 1 || s2 != 2 {
			t.Fatalf("%s: site 1 has %d rows (want 1), forever site has %d (want 2)", table, s1, s2)
		}
	}
	if err := retention.Erase(ctx, pool, 1, 10, 0); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"app_issue_daily", "app_release_daily", "app_session_daily", "app_issue_hourly", "app_client_reports"} {
		var n int
		if err := pool.QueryRow(ctx, `select count(*) from `+table+` where site_id=1`).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s rows left after erase: %d %v", table, n, err)
		}
	}
}

func TestRunDropsExpiredAppEventBodyPartitions(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into orgs(name,slug) values('Org','org');
insert into sites(id,org_id,public_id,name,timezone,retention_class,kind) overriding system value values(1,1,'appbod001abc','App','UTC',365,'app')`); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 7, 3, 0, 0, 0, time.UTC)
	if err := db.EnsureAppEventPartitions(ctx, pool, now.AddDate(0, -3, 0), now.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	dropped, err := retention.Run(ctx, pool, now)
	if err != nil {
		t.Fatal(err)
	}
	// Bodies live 30 days even though the project keeps events for a year.
	if !containsPrefix(dropped, "app_event_bodies_202607") || !containsPrefix(dropped, "app_event_bodies_202608") {
		t.Fatalf("expected July and August bodies dropped, got %v", dropped)
	}
	if containsPrefix(dropped, "app_event_bodies_202609") || containsPrefix(dropped, "app_event_bodies_202610") || containsPrefix(dropped, "app_events_") {
		t.Fatalf("dropped too much: %v", dropped)
	}
}

func TestRunDropsExpiredAppSessionPartitionsAndEraseRemovesRows(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into orgs(name,slug) values('Org','org');
insert into sites(id,org_id,public_id,name,timezone,retention_class,kind) overriding system value values(1,1,'appses001abc','App','UTC',30,'app')`); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 7, 3, 0, 0, 0, time.UTC)
	if err := db.EnsureAppEventPartitions(ctx, pool, now.AddDate(0, -3, 0), now.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into app_sessions(site_id,started,session_hash,release,status,updated_at)
values (1,'2026-10-06T00:00:00Z','\x01','1.0','ok',now())`); err != nil {
		t.Fatal(err)
	}
	dropped, err := retention.Run(ctx, pool, now)
	if err != nil {
		t.Fatal(err)
	}
	if !containsPrefix(dropped, "app_sessions_202607") || !containsPrefix(dropped, "app_sessions_202608") {
		t.Fatalf("expected July and August sessions dropped, got %v", dropped)
	}
	if containsPrefix(dropped, "app_sessions_202610") {
		t.Fatalf("dropped the current month: %v", dropped)
	}
	if err := retention.Erase(ctx, pool, 1, 10, 0); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pool.QueryRow(ctx, `select count(*) from app_sessions where site_id=1`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("app_sessions rows left after erase: %d %v", n, err)
	}
}
