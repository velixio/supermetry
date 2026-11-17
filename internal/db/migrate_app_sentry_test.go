package db

import (
	"context"
	"testing"
	"time"

	"supermetry/internal/testpg"
)

// TestAppSentryMigrationsKeepV1DataAndEnforceConstraints applies the
// migrations before 0274 with v1 app data in place, then the rest.
func TestAppSentryMigrationsKeepV1DataAndEnforceConstraints(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, migration := range migrations {
		if migration.version < 274 {
			if err := applyMigration(ctx, conn, migration); err != nil {
				conn.Release()
				t.Fatal(err)
			}
		}
	}
	conn.Release()
	day := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	if err := EnsurePartitions(ctx, pool, day, day.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into orgs(name,slug) values('Org','org');
insert into sites(id,org_id,public_id,name,timezone,retention_class,kind) overriding system value values(1,1,'appsen001abc','App','UTC',30,'app');
insert into app_ingest_keys(site_id,prefix,hash) values(1,'abcdefgh',decode(repeat('ab',32),'hex'));
insert into app_issues(site_id,fingerprint,kind,first_seen,last_seen,event_count) values(1,'ab','crash',now(),now(),1);
insert into app_events(site_id,ts,received_at,event_id,kind) values(1,'2026-10-08T00:00:00Z','2026-10-08T00:00:00Z','e1','crash')`); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var kind, level, culprit, sdk, sym string
	var gv, issueGV int16
	var pub *string
	if err := pool.QueryRow(ctx, `select k.kind, k.public_key, e.culprit, e.sdk_name, e.symbolication, e.grouping_version, i.level, i.grouping_version
		from app_ingest_keys k, app_events e, app_issues i`).Scan(&kind, &pub, &culprit, &sdk, &sym, &gv, &level, &issueGV); err != nil {
		t.Fatal(err)
	}
	if kind != "smk" || pub != nil || culprit != "" || sdk != "" || sym != "" || gv != 1 || level != "" || issueGV != 1 {
		t.Fatalf("v1 rows after migrate: %s %v %q %q %q %d %q %d", kind, pub, culprit, sdk, sym, gv, level, issueGV)
	}

	insertKey := func(kind string, public any) error {
		_, err := pool.Exec(ctx, `insert into app_ingest_keys(site_id,prefix,hash,kind,public_key) values(1,'zzzzzzzz',decode(md5(random()::text) || md5(random()::text),'hex'),$1,$2)`, kind, public)
		return err
	}
	good := "0123456789abcdef0123456789abcdef"
	if err := insertKey("dsn", good); err != nil {
		t.Fatalf("valid dsn key: %v", err)
	}
	if err := insertKey("dsn", good); err == nil {
		t.Error("duplicate public key accepted")
	}
	for name, c := range map[string]struct {
		kind   string
		public any
	}{
		"dsn without key": {"dsn", nil},
		"smk with key":    {"smk", "fedcba9876543210fedcba9876543210"},
		"uppercase key":   {"dsn", "0123456789ABCDEF0123456789ABCDEF"},
		"short key":       {"dsn", "0123"},
		"unknown kind":    {"x", nil},
	} {
		if err := insertKey(c.kind, c.public); err == nil {
			t.Errorf("%s accepted", name)
		}
	}

	if err := EnsurePartitions(ctx, pool, day, day.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into app_event_bodies(site_id,ts,event_id,body,size) values(1,'2026-10-08T00:00:00Z','e1','{"a":1}',7)`); err != nil {
		t.Fatalf("body insert: %v", err)
	}
	if _, err := pool.Exec(ctx, `insert into app_event_bodies(site_id,ts,event_id,body,size) values(1,'2026-10-08T00:00:00Z','e1','{}',2)`); err == nil {
		t.Error("duplicate body accepted")
	}
	if _, err := pool.Exec(ctx, `delete from sites where id=1`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pool.QueryRow(ctx, `select count(*) from app_event_bodies`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("bodies after site delete = %d err=%v", n, err)
	}
}
