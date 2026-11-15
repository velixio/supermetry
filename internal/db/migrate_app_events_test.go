package db_test

import (
	"context"
	"testing"
	"time"

	"supermetry/internal/db"
	"supermetry/internal/testpg"
)

func TestAppEventsMigrationPartitionsChecksAndCascade(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	if err := db.EnsurePartitions(ctx, pool, from, to); err != nil {
		t.Fatal(err)
	}
	var partitions int
	if err := pool.QueryRow(ctx, `select count(*) from pg_inherits where inhparent='app_events'::regclass`).Scan(&partitions); err != nil {
		t.Fatal(err)
	}
	if partitions != 3 {
		t.Fatalf("app_events partitions = %d, want 3", partitions)
	}
	if _, err := pool.Exec(ctx, `insert into orgs(name,slug) values('Org','org');
insert into sites(id,org_id,public_id,name,timezone,retention_class,kind) overriding system value values(1,1,'appmig001abc','App','UTC',30,'app');
insert into app_issues(site_id,fingerprint,kind,first_seen,last_seen,event_count) values(1,'ab','crash',now(),now(),1);
insert into app_hash_secrets(site_id,secret) values(1,decode(repeat('ab',32),'hex'));
insert into app_events(site_id,ts,received_at,event_id,kind) values(1,'2026-10-08T00:00:00Z','2026-10-08T00:00:00Z','e1','crash')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into app_events(site_id,ts,received_at,event_id,kind) values(1,'2026-10-08T00:00:00Z','2026-10-08T00:00:00Z','e2','bogus')`); err == nil {
		t.Fatal("unknown kind must be rejected")
	}
	if _, err := pool.Exec(ctx, `update app_issues set status='weird'`); err == nil {
		t.Fatal("unknown status must be rejected")
	}
	if _, err := pool.Exec(ctx, `insert into app_hash_secrets(site_id,secret) values(1,'\x00')`); err == nil {
		t.Fatal("short secret must be rejected")
	}
	if _, err := pool.Exec(ctx, `insert into site_drop_daily(site_id,day,reason,count) values(1,'2026-10-08','app_invalid',1),(1,'2026-10-08','app_duplicate',1),(1,'2026-10-08','app_rate_limited',1)`); err != nil {
		t.Fatalf("app drop reasons: %v", err)
	}
	if _, err := pool.Exec(ctx, `delete from sites where id=1`); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"app_events", "app_issues", "app_hash_secrets"} {
		var n int
		if err := pool.QueryRow(ctx, `select count(*) from `+table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("%s kept %d rows after site delete", table, n)
		}
	}
}
