package db_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"panel/internal/db"
	"panel/internal/testpg"
)

func TestMigrateIsIdempotentAndOrdered(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal("second run:", err)
	}
	rows, err := pool.Query(ctx, `select version from schema_migrations order by version`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	previous := 0
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			t.Fatal(err)
		}
		reservedGap := (previous == 6 && version == 8) || (previous == 8 && version == 11)
		if version != previous+1 && !reservedGap {
			t.Fatalf("migration version %d follows %d", version, previous)
		}
		previous = version
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestPlatformMigrationTables(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{
		"orgs", "users", "memberships", "user_sessions", "password_resets",
		"invitations", "sites", "goals", "funnels", "segments", "api_keys", "shared_links",
		"report_subscriptions", "audit_log", "setup_state",
	} {
		var exists bool
		if err := pool.QueryRow(ctx, `select to_regclass($1) is not null`, table).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Errorf("missing %s", table)
		}
	}
	if _, err := pool.Exec(ctx, `
		insert into users(email, name, password_hash)
		values ('A@x.io', 'a', 'h'), ('a@X.io', 'b', 'h')`); err == nil {
		t.Fatal("email must be case-insensitive unique")
	}
}

func TestSegmentsMigrationSchemaConstraints(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	var userID, orgID int64
	var siteID int32
	if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash) values ('segments@example.test','Segments','hash') returning id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Segments','segments') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class) values ($1,'segmentssite','Segments','UTC',30) returning id`, orgID).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into segments(site_id,name,filters,created_by) values ($1,'Paid traffic','[{"dimension":"source","operator":"contains","values":["ads"]}]',$2)`, siteID, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into segments(site_id,name,filters) values ($1,'paid TRAFFIC','[]')`, siteID); err == nil {
		t.Fatal("segment names must be case-insensitively unique per site")
	}
	if _, err := pool.Exec(ctx, `insert into segments(site_id,name,filters) values ($1,'Not an array','{}')`, siteID); err == nil {
		t.Fatal("segment filters must be a JSON array")
	}
	if _, err := pool.Exec(ctx, `delete from users where id=$1`, userID); err != nil {
		t.Fatal(err)
	}
	var createdBy *int64
	if err := pool.QueryRow(ctx, `select created_by from segments where site_id=$1`, siteID).Scan(&createdBy); err != nil {
		t.Fatal(err)
	}
	if createdBy != nil {
		t.Fatalf("created_by=%v, want null after user deletion", *createdBy)
	}
	if _, err := pool.Exec(ctx, `delete from sites where id=$1`, siteID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(ctx, `select count(*) from segments where site_id=$1`, siteID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("segments after site deletion=%d, want 0", count)
	}
}

func TestMigrateConcurrentRunnersSerialize(t *testing.T) {
	pool, _ := testpg.New(t)
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		go func() { errs <- db.Migrate(context.Background(), pool) }()
	}
	for i := 0; i < 4; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
}

func TestMigrateDiscardsConnectionWhenUnlockFails(t *testing.T) {
	probe, schema := testpg.New(t)
	ctx := context.Background()

	config, err := pgxpool.ParseConfig(os.Getenv("PANEL_TEST_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	config.MaxConns = 1
	config.ConnConfig.Tracer = cancelUnlockTracer{}
	identifier := pgx.Identifier{schema}.Sanitize()
	config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "set search_path to "+identifier)
		return err
	}

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	err = db.Migrate(ctx, pool)
	if err == nil || !strings.Contains(err.Error(), "unlock migrations") {
		t.Fatalf("want unlock error, got %v", err)
	}

	var acquired bool
	if err := probe.QueryRow(ctx, `select pg_try_advisory_lock(hashtext('panel:migrate'))`).Scan(&acquired); err != nil {
		t.Fatal(err)
	}
	if !acquired {
		t.Fatal("migration connection still holds advisory lock")
	}
	if _, err := probe.Exec(ctx, `select pg_advisory_unlock(hashtext('panel:migrate'))`); err != nil {
		t.Fatal(err)
	}
}

type cancelUnlockTracer struct{}

func (cancelUnlockTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.Contains(data.SQL, "pg_advisory_unlock") {
		ctx, cancel := context.WithCancel(ctx)
		cancel()
		return ctx
	}
	return ctx
}

func (cancelUnlockTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}
