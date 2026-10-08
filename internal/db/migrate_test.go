package db_test

import (
	"context"
	"os"
	"strconv"
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
	entries, err := os.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	want := make(map[int]string)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		prefix, _, ok := strings.Cut(entry.Name(), "_")
		if !ok {
			t.Fatalf("migration name %q has no numeric prefix", entry.Name())
		}
		version, err := strconv.Atoi(prefix)
		if err != nil {
			t.Fatalf("parse migration name %q: %v", entry.Name(), err)
		}
		want[version] = entry.Name()
	}
	rows, err := pool.Query(ctx, `select version from schema_migrations order by version`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			t.Fatal(err)
		}
		if _, ok := want[version]; !ok {
			t.Fatalf("unexpected applied migration %04d", version)
		}
		delete(want, version)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(want) != 0 {
		t.Fatalf("migrations not applied exactly once: %v", want)
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
		"invitations", "sites", "goals", "funnels", "api_keys", "shared_links",
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
