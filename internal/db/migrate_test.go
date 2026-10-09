package db_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/db"
	"supermetry/internal/testpg"
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

func TestLatestMigrationVersionMatchesEmbeddedMigrations(t *testing.T) {
	entries, err := os.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	want := 0
	for _, entry := range entries {
		prefix, _, ok := strings.Cut(entry.Name(), "_")
		if entry.IsDir() || !ok || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		version, err := strconv.Atoi(prefix)
		if err != nil {
			t.Fatalf("parse migration name %q: %v", entry.Name(), err)
		}
		if version > want {
			want = version
		}
	}

	got, err := db.LatestMigrationVersion()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("LatestMigrationVersion() = %d, want %d", got, want)
	}
}

func TestCustomClosedSessionMigrationMatchesEventSessionType(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var dataType string
	var nullable string
	if err := pool.QueryRow(ctx, `
select data_type, is_nullable
from information_schema.columns
where table_schema=current_schema() and table_name='custom_closed' and column_name='session_id'`).Scan(&dataType, &nullable); err != nil {
		t.Fatal(err)
	}
	if dataType != "bigint" || nullable != "YES" {
		t.Fatalf("custom_closed.session_id = %s nullable=%s, want bigint nullable", dataType, nullable)
	}
	if err := pool.QueryRow(ctx, `
select data_type, is_nullable
from information_schema.columns
where table_schema=current_schema() and table_name='sessions_closed' and column_name='seq_at'`).Scan(&dataType, &nullable); err != nil {
		t.Fatal(err)
	}
	if dataType != "ARRAY" || nullable != "YES" {
		t.Fatalf("sessions_closed.seq_at = %s nullable=%s, want timestamp array nullable", dataType, nullable)
	}
}

func TestRollupStateMigrationSchema(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	var columns int
	if err := pool.QueryRow(ctx, `
		select count(*)
		from information_schema.columns
		where table_schema = current_schema()
		  and table_name = 'rollup_state'
		  and column_name = any($1::text[])`, []string{
		"layout_version", "dimension_set", "counter_set", "pending_layout_version",
		"pending_dimension_set", "pending_counter_set", "backfill_mode",
		"last_processed_site_id", "last_processed_day", "through_site_id", "through_day",
		"days_done", "total_days", "started_at", "finished_at",
	}).Scan(&columns); err != nil {
		t.Fatal(err)
	}
	if columns != 15 {
		t.Fatalf("rollup_state progress columns = %d, want 15", columns)
	}

	if _, err := pool.Exec(ctx, `insert into rollup_state(singleton) values (true)`); err != nil {
		t.Fatalf("insert singleton state: %v", err)
	}
	if _, err := pool.Exec(ctx, `insert into rollup_state(singleton) values (true)`); err == nil {
		t.Fatal("rollup_state accepted a second singleton row")
	}
	if _, err := pool.Exec(ctx, `insert into rollup_state(singleton) values (false)`); err == nil {
		t.Fatal("rollup_state accepted a non-singleton key")
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

func TestIntegrityMigrationSchema(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	for _, check := range []struct {
		table  string
		column string
	}{
		{table: "profile_tombstones", column: "external_hash"},
		{table: "imports", column: "file_hash"},
	} {
		var exists bool
		if err := pool.QueryRow(ctx, `
			select exists (
				select 1 from information_schema.columns
				where table_schema=current_schema() and table_name=$1 and column_name=$2
			)`, check.table, check.column).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Errorf("missing %s.%s", check.table, check.column)
		}
	}
}

func TestDimensionsMigrationDefaultsClosedSessionColumns(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	if err := db.EnsurePartitions(ctx, pool, day, day.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		insert into sessions_closed (
			site_id, day, visitor_id, session_id, pageviews, custom, duration_ms,
			engagement_ms, vd_n, flags, seq, seq_engage, seq_scroll
		) values (1, $1, 2, 3, 1, 0, 0, 0, 1, 0, '{}', '{}', '{}')`, day); err != nil {
		t.Fatal(err)
	}
	var browserVersion, osVersion, language, hostname int32
	var screenBucket int16
	if err := pool.QueryRow(ctx, `
		select browser_ver_id, os_ver_id, language_id, hostname_id, screen_bucket
		from sessions_closed where site_id=1 and day=$1 and session_id=3`, day,
	).Scan(&browserVersion, &osVersion, &language, &hostname, &screenBucket); err != nil {
		t.Fatal(err)
	}
	if browserVersion != 0 || osVersion != 0 || language != 0 || hostname != 0 || screenBucket != 0 {
		t.Fatalf("defaults=%d/%d/%d/%d/%d, want all zero", browserVersion, osVersion, language, hostname, screenBucket)
	}
}

func TestScrollGoalsMigration(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()

	entries, err := os.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		prefix, _, ok := strings.Cut(entry.Name(), "_")
		if entry.IsDir() || !ok || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		version, err := strconv.Atoi(prefix)
		if err != nil {
			t.Fatal(err)
		}
		if version >= 19 {
			continue
		}
		contents, err := os.ReadFile(filepath.Join("migrations", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, string(contents)); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("apply prerequisite migration %s: %v", entry.Name(), err)
		}
		if _, err := tx.Exec(ctx, `insert into schema_migrations(version) values ($1)`, version); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}

	var constraintName string
	if err := pool.QueryRow(ctx, `
		select conname from pg_constraint
		where conrelid='goals'::regclass and contype='c'
		  and pg_get_constraintdef(oid) like '%kind%page%event%'`).Scan(&constraintName); err != nil {
		t.Fatalf("find pre-migration goals kind constraint: %v", err)
	}
	if constraintName != "goals_kind_check" {
		t.Fatalf("pre-migration goals kind constraint = %q, want goals_kind_check", constraintName)
	}

	var orgID int64
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Scroll goals','scroll-goals') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	var siteID int32
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class) values ($1,'scrollgoal01','Scroll goals','UTC',30) returning id`, orgID).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	var existingID int64
	if err := pool.QueryRow(ctx, `insert into goals(site_id,kind,match,value,name) values ($1,'page','exact','/existing','Existing') returning id`, siteID).Scan(&existingID); err != nil {
		t.Fatal(err)
	}

	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	var kind string
	var threshold *int16
	if err := pool.QueryRow(ctx, `select kind,scroll_threshold from goals where id=$1`, existingID).Scan(&kind, &threshold); err != nil {
		t.Fatal(err)
	}
	if kind != "page" || threshold != nil {
		t.Fatalf("existing goal after migration = kind %q threshold %v", kind, threshold)
	}

	tests := []struct {
		name      string
		kind      string
		threshold *int16
		wantOK    bool
	}{
		{name: "scroll with threshold", kind: "scroll", threshold: int16Ptr(50), wantOK: true},
		{name: "scroll without threshold", kind: "scroll"},
		{name: "page with threshold", kind: "page", threshold: int16Ptr(50)},
		{name: "zero threshold", kind: "scroll", threshold: int16Ptr(0)},
		{name: "threshold above maximum", kind: "scroll", threshold: int16Ptr(101)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := pool.Exec(ctx, `insert into goals(site_id,kind,match,value,name,scroll_threshold) values ($1,$2,'exact','/pricing',$3,$4)`, siteID, tt.kind, tt.name, tt.threshold)
			if (err == nil) != tt.wantOK {
				t.Fatalf("insert error = %v, want success %v", err, tt.wantOK)
			}
		})
	}
}

func int16Ptr(value int16) *int16 { return &value }

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

	config, err := pgxpool.ParseConfig(os.Getenv("SUPERMETRY_TEST_DSN"))
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
	if err := probe.QueryRow(ctx, `select pg_try_advisory_lock(hashtext('supermetry:migrate'))`).Scan(&acquired); err != nil {
		t.Fatal(err)
	}
	if !acquired {
		t.Fatal("migration connection still holds advisory lock")
	}
	if _, err := probe.Exec(ctx, `select pg_advisory_unlock(hashtext('supermetry:migrate'))`); err != nil {
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
