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

func TestProfilesMigrationSchema(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	var profileColumn bool
	if err := pool.QueryRow(ctx, `
		select exists (
			select 1 from information_schema.columns
			where table_schema = current_schema()
			  and table_name = 'events'
			  and column_name = 'profile_id'
			  and data_type = 'bigint'
			  and is_nullable = 'YES'
		)`).Scan(&profileColumn); err != nil {
		t.Fatal(err)
	}
	if !profileColumn {
		t.Fatal("events.profile_id nullable bigint column is missing")
	}
	var eventProfileFKs int
	if err := pool.QueryRow(ctx, `
		select count(*)
		from pg_constraint constraint_row
		join pg_attribute attribute_row
		  on attribute_row.attrelid = constraint_row.conrelid
		 and attribute_row.attnum = any(constraint_row.conkey)
		where constraint_row.conrelid = 'events'::regclass
		  and constraint_row.contype = 'f'
		  and attribute_row.attname = 'profile_id'`).Scan(&eventProfileFKs); err != nil {
		t.Fatal(err)
	}
	if eventProfileFKs != 0 {
		t.Fatalf("events.profile_id foreign keys = %d, want 0", eventProfileFKs)
	}

	var definition string
	if err := pool.QueryRow(ctx, `select pg_get_indexdef(indexrelid)
		from pg_index
		where indrelid = 'events'::regclass
		  and indexprs is null
		  and pg_get_expr(indpred, indrelid) = '(profile_id IS NOT NULL)'`).Scan(&definition); err != nil {
		t.Fatalf("profile timeline partial index: %v", err)
	}
	for _, fragment := range []string{"site_id", "profile_id", "ts DESC", "WHERE (profile_id IS NOT NULL)"} {
		if !strings.Contains(definition, fragment) {
			t.Fatalf("profile timeline index = %q, missing %q", definition, fragment)
		}
	}

	var orgID int64
	if err := pool.QueryRow(ctx, `insert into orgs(name, slug) values ('Profiles', 'profiles') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	var siteID int32
	if err := pool.QueryRow(ctx, `
		insert into sites(org_id, public_id, name, allowed_hosts, timezone, retention_class)
		values ($1, 'profiles0001', 'Profiles', '{}', 'UTC', 30)
		returning id`, orgID).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	var profilesEnabled bool
	if err := pool.QueryRow(ctx, `
		select (settings->>'profiles_enabled')::boolean
		from sites where id = $1`, siteID).Scan(&profilesEnabled); err != nil {
		t.Fatalf("profiles_enabled default: %v", err)
	}
	if profilesEnabled {
		t.Fatal("profiles_enabled must default false")
	}
	var profileID int64
	if err := pool.QueryRow(ctx, `
		insert into profiles(site_id, external_id, traits, first_seen_at, last_seen_at)
		values ($1, 'customer-1', '{"plan":"pro"}', '2026-10-01', '2026-10-02')
		returning id`, siteID).Scan(&profileID); err != nil {
		t.Fatal(err)
	}
	if profileID == 0 {
		t.Fatal("profile identity was not generated")
	}
	if _, err := pool.Exec(ctx, `
		insert into profiles(site_id, external_id, traits, first_seen_at, last_seen_at)
		values ($1, 'customer-1', '{}', '2026-10-01', '2026-10-02')`, siteID); err == nil {
		t.Fatal("duplicate site/external profile was accepted")
	}
	if _, err := pool.Exec(ctx, `
		insert into profiles(site_id, external_id, traits, first_seen_at, last_seen_at)
		values ($1, 'bad-traits', '[]', '2026-10-01', '2026-10-02')`, siteID); err == nil {
		t.Fatal("non-object profile traits were accepted")
	}
	if _, err := pool.Exec(ctx, `delete from sites where id = $1`, siteID); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := pool.QueryRow(ctx, `select count(*) from profiles where site_id = $1`, siteID).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("profiles after site delete = %d, want 0", remaining)
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
