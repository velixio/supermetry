// Package testpg creates isolated PostgreSQL databases for database tests.
package testpg

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	templatePrefix      = "supermetry_tpl_"
	buildPrefix         = "supermetry_build_"
	testDatabasePrefix  = "supermetry_tdb_"
	templateSchema      = "supermetry_template"
	templateLockName    = "supermetry:testpg:template"
	staleDatabaseAge    = time.Hour
	cleanupTimeout      = time.Minute
	migrationHashLength = 20
	maxTestPoolConns    = 8
)

var (
	databaseCounter        atomic.Uint64
	sweepOnce              sync.Once
	sweepResult            error
	fallbackLogOnce        sync.Once
	errTemplateUnsupported = errors.New("template databases are not permitted")
)

// New returns a pool bound to a fresh database and drops the database at cleanup.
//
// Old migration templates are intentionally retained so binaries with different
// migration sets can run concurrently. Remove them manually when no tests run:
// SELECT format('DROP DATABASE %I;', datname) FROM pg_database WHERE datname LIKE 'supermetry_tpl_%';
func New(t testing.TB) (*pgxpool.Pool, string) {
	t.Helper()

	dsn := os.Getenv("SUPERMETRY_TEST_DSN")
	if dsn == "" {
		t.Skip("SUPERMETRY_TEST_DSN is not set")
	}
	if needsSharedDSNCompatibility() {
		return newSchema(t, dsn)
	}
	pool, schema, err := newFromTemplate(t, dsn)
	if errors.Is(err, errTemplateUnsupported) {
		fallbackLogOnce.Do(func() {
			t.Log("PostgreSQL role cannot create template databases; using schema-based test setup")
		})
		return newSchema(t, dsn)
	}
	if err != nil {
		t.Fatalf("create test database: %v", err)
	}
	return pool, schema
}

// These tests pass the returned schema to a subprocess that reconnects using
// SUPERMETRY_TEST_DSN. A database name cannot be represented through their
// established search_path-only contract, so retain schema isolation for them.
func needsSharedDSNCompatibility() bool {
	callers := make([]uintptr, 16)
	count := runtime.Callers(2, callers)
	frames := runtime.CallersFrames(callers[:count])
	for {
		frame, more := frames.Next()
		file := filepath.ToSlash(frame.File)
		if strings.HasSuffix(file, "/cmd/supermetry/backup_test.go") ||
			strings.HasSuffix(file, "/test/golden/golden_test.go") ||
			strings.HasSuffix(file, "/test/platform/flow_test.go") {
			return true
		}
		if !more {
			return false
		}
	}
}

func newFromTemplate(t testing.TB, dsn string) (*pgxpool.Pool, string, error) {
	t.Helper()
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return nil, "", fmt.Errorf("connect to test postgres: %w", err)
	}

	sweepOnce.Do(func() { sweepResult = sweepStaleDatabases(ctx, admin, time.Now()) })
	if sweepResult != nil {
		_ = admin.Close(ctx)
		return nil, "", sweepResult
	}

	templateName := "template0"
	migrated := !needsUnmigratedDatabase()
	if migrated {
		hash, err := migrationHash()
		if err != nil {
			_ = admin.Close(ctx)
			return nil, "", err
		}
		templateName = templatePrefix + hash[:migrationHashLength]
		if err := ensureTemplate(ctx, admin, dsn, templateName); err != nil {
			_ = admin.Close(ctx)
			return nil, "", err
		}
	}

	databaseName := uniqueDatabaseName(testDatabasePrefix, time.Now())
	if _, err := admin.Exec(ctx, "create database "+identifier(databaseName)+" template "+identifier(templateName)); err != nil {
		_ = admin.Close(ctx)
		if isInsufficientPrivilege(err) {
			return nil, "", errTemplateUnsupported
		}
		return nil, "", fmt.Errorf("clone migration template: %w", err)
	}

	cleanupDatabase := func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()
		_ = dropDatabase(cleanupCtx, admin, databaseName)
	}
	clone, err := connectDatabase(ctx, dsn, databaseName)
	if err != nil {
		cleanupDatabase()
		_ = admin.Close(ctx)
		return nil, "", err
	}
	schemaSQL := "create schema " + identifier(databaseName)
	if migrated {
		schemaSQL = "alter schema " + identifier(templateSchema) + " rename to " + identifier(databaseName)
	}
	if _, err := clone.Exec(ctx, schemaSQL); err != nil {
		_ = clone.Close(ctx)
		cleanupDatabase()
		_ = admin.Close(ctx)
		return nil, "", fmt.Errorf("prepare cloned schema: %w", err)
	}
	if err := clone.Close(ctx); err != nil {
		cleanupDatabase()
		_ = admin.Close(ctx)
		return nil, "", fmt.Errorf("close cloned database setup connection: %w", err)
	}

	// Preserve the historical second return value for the one caller that opens
	// an extra pool against the shared DSN and uses it as a search_path.
	if _, err := admin.Exec(ctx, "create schema "+identifier(databaseName)); err != nil {
		cleanupDatabase()
		_ = admin.Close(ctx)
		return nil, "", fmt.Errorf("create compatibility schema: %w", err)
	}

	pool, err := openPool(ctx, dsn, databaseName, databaseName)
	if err != nil {
		_, _ = admin.Exec(ctx, "drop schema "+identifier(databaseName)+" cascade")
		cleanupDatabase()
		_ = admin.Close(ctx)
		return nil, "", err
	}

	t.Cleanup(func() {
		pool.Close()
		cleanupCtx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()
		if _, err := admin.Exec(cleanupCtx, "drop schema "+identifier(databaseName)+" cascade"); err != nil {
			t.Errorf("drop test compatibility schema: %v", err)
		}
		if err := dropDatabase(cleanupCtx, admin, databaseName); err != nil {
			t.Errorf("drop test database: %v", err)
		}
		if err := admin.Close(cleanupCtx); err != nil {
			t.Errorf("close test postgres admin connection: %v", err)
		}
	})
	return pool, databaseName, nil
}

// The db package's migration tests deliberately build historical schemas by
// applying individual migrations. They still get a unique database, but it
// must start empty for that existing contract to remain meaningful.
func needsUnmigratedDatabase() bool {
	callers := make([]uintptr, 16)
	count := runtime.Callers(2, callers)
	frames := runtime.CallersFrames(callers[:count])
	for {
		frame, more := frames.Next()
		file := filepath.ToSlash(frame.File)
		if strings.HasSuffix(file, "_test.go") && (strings.Contains(file, "/internal/db/") ||
			strings.HasSuffix(file, "/internal/replay/migrate_test.go")) {
			return true
		}
		if !more {
			return false
		}
	}
}

func ensureTemplate(ctx context.Context, admin *pgx.Conn, dsn, templateName string) (resultErr error) {
	if _, err := admin.Exec(ctx, `select pg_advisory_lock(hashtext($1))`, templateLockName); err != nil {
		return fmt.Errorf("lock migration template creation: %w", err)
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()
		var unlocked bool
		err := admin.QueryRow(cleanupCtx, `select pg_advisory_unlock(hashtext($1))`, templateLockName).Scan(&unlocked)
		if err != nil || !unlocked {
			if err == nil {
				err = errors.New("advisory lock was not held")
			}
			resultErr = errors.Join(resultErr, fmt.Errorf("unlock migration template creation: %w", err))
		}
	}()

	var exists bool
	if err := admin.QueryRow(ctx, `select exists(select 1 from pg_database where datname=$1)`, templateName).Scan(&exists); err != nil {
		return fmt.Errorf("check migration template: %w", err)
	}
	if exists {
		return nil
	}

	buildName := uniqueDatabaseName(buildPrefix, time.Now())
	if _, err := admin.Exec(ctx, "create database "+identifier(buildName)+" template template0"); err != nil {
		if isInsufficientPrivilege(err) {
			return errTemplateUnsupported
		}
		return fmt.Errorf("create migration template build database: %w", err)
	}
	defer func() {
		if resultErr != nil {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
			defer cancel()
			resultErr = errors.Join(resultErr, dropDatabase(cleanupCtx, admin, buildName))
		}
	}()

	setup, err := connectDatabase(ctx, dsn, buildName)
	if err != nil {
		return err
	}
	if _, err := setup.Exec(ctx, "create schema "+identifier(templateSchema)); err != nil {
		_ = setup.Close(ctx)
		return fmt.Errorf("create migration template schema: %w", err)
	}
	if err := setup.Close(ctx); err != nil {
		return fmt.Errorf("close migration template setup connection: %w", err)
	}

	pool, err := openPool(ctx, dsn, buildName, templateSchema)
	if err != nil {
		return err
	}
	if err := migrateTemplate(ctx, pool); err != nil {
		pool.Close()
		return fmt.Errorf("migrate template database: %w", err)
	}
	pool.Close()

	if _, err := admin.Exec(ctx, "alter database "+identifier(buildName)+" rename to "+identifier(templateName)); err != nil {
		return fmt.Errorf("publish migration template: %w", err)
	}
	return nil
}

func migrationHash() (string, error) {
	migrations, err := loadTemplateMigrations()
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	for _, migration := range migrations {
		_, _ = hash.Write([]byte(migration.name))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write(migration.contents)
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

type templateMigration struct {
	version  int
	name     string
	contents []byte
}

func loadTemplateMigrations() ([]templateMigration, error) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		return nil, errors.New("locate testpg source")
	}
	directory := filepath.Join(filepath.Dir(filename), "..", "db", "migrations")
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("read migrations for template: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	migrations := make([]templateMigration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sql" {
			continue
		}
		prefix, _, ok := strings.Cut(entry.Name(), "_")
		if !ok {
			return nil, fmt.Errorf("migration %q has no numeric prefix", entry.Name())
		}
		version, err := strconv.Atoi(prefix)
		if err != nil || version < 1 {
			return nil, fmt.Errorf("migration %q has invalid numeric prefix", entry.Name())
		}
		contents, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", entry.Name(), err)
		}
		migrations = append(migrations, templateMigration{version: version, name: entry.Name(), contents: contents})
	}
	return migrations, nil
}

func migrateTemplate(ctx context.Context, pool *pgxpool.Pool) error {
	migrations, err := loadTemplateMigrations()
	if err != nil {
		return err
	}
	for _, migration := range migrations {
		tx, err := pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin migration %04d (%s): %w", migration.version, migration.name, err)
		}
		if _, err := tx.Exec(ctx, string(migration.contents)); err != nil {
			_ = tx.Rollback(context.Background())
			return fmt.Errorf("apply migration %04d (%s): %w", migration.version, migration.name, err)
		}
		if _, err := tx.Exec(ctx, `insert into schema_migrations(version) values ($1)`, migration.version); err != nil {
			_ = tx.Rollback(context.Background())
			return fmt.Errorf("record migration %04d (%s): %w", migration.version, migration.name, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit migration %04d (%s): %w", migration.version, migration.name, err)
		}
	}
	return nil
}

func sweepStaleDatabases(ctx context.Context, admin *pgx.Conn, now time.Time) error {
	rows, err := admin.Query(ctx, `select datname from pg_database where datname like $1 or datname like $2`, testDatabasePrefix+"%", buildPrefix+"%")
	if err != nil {
		return fmt.Errorf("list stale test databases: %w", err)
	}
	var stale []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return fmt.Errorf("scan stale test database: %w", err)
		}
		createdAt, ok := databaseTimestamp(name)
		if ok && now.Sub(createdAt) > staleDatabaseAge {
			stale = append(stale, name)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("list stale test databases: %w", err)
	}
	rows.Close()
	for _, name := range stale {
		if err := dropDatabase(ctx, admin, name); err != nil && !isInvalidCatalogName(err) {
			return fmt.Errorf("drop stale test database %q: %w", name, err)
		}
		if strings.HasPrefix(name, testDatabasePrefix) {
			if _, err := admin.Exec(ctx, "drop schema if exists "+identifier(name)+" cascade"); err != nil {
				return fmt.Errorf("drop stale compatibility schema %q: %w", name, err)
			}
		}
	}
	return nil
}

func databaseTimestamp(name string) (time.Time, bool) {
	parts := strings.Split(name, "_")
	if len(parts) < 3 {
		return time.Time{}, false
	}
	seconds, err := strconv.ParseInt(parts[len(parts)-3], 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(seconds, 0), true
}

func uniqueDatabaseName(prefix string, now time.Time) string {
	return fmt.Sprintf("%s%d_%d_%d", prefix, now.Unix(), os.Getpid(), databaseCounter.Add(1))
}

func openPool(ctx context.Context, dsn, databaseName, schema string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse test database URL: %w", err)
	}
	config.ConnConfig.Database = databaseName
	config.MaxConns = maxTestPoolConns
	config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "set search_path to "+identifier(schema))
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("open test pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping test postgres: %w", err)
	}
	return pool, nil
}

func connectDatabase(ctx context.Context, dsn, databaseName string) (*pgx.Conn, error) {
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse test database URL: %w", err)
	}
	config.Database = databaseName
	conn, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("connect to database %q: %w", databaseName, err)
	}
	return conn, nil
}

func dropDatabase(ctx context.Context, admin *pgx.Conn, name string) error {
	_, err := admin.Exec(ctx, "drop database if exists "+identifier(name)+" with (force)")
	return err
}

func identifier(name string) string { return pgx.Identifier{name}.Sanitize() }

func isInsufficientPrivilege(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42501"
}

func isInvalidCatalogName(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "3D000"
}

func newSchema(t testing.TB, dsn string) (*pgxpool.Pool, string) {
	t.Helper()
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to test postgres: %v", err)
	}

	schema := randomSchema(t)
	if _, err := admin.Exec(ctx, "create schema "+identifier(schema)); err != nil {
		_ = admin.Close(ctx)
		t.Fatalf("create test schema: %v", err)
	}
	pool, err := openPool(ctx, dsn, admin.Config().Database, schema)
	if err != nil {
		dropSchema(admin, schema)
		t.Fatal(err)
	}

	t.Cleanup(func() {
		pool.Close()
		dropSchema(admin, schema)
	})
	return pool, schema
}

func randomSchema(t testing.TB) string {
	t.Helper()
	var suffix [12]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatalf("generate test schema name: %v", err)
	}
	return "t_" + hex.EncodeToString(suffix[:])
}

func dropSchema(admin *pgx.Conn, schema string) {
	ctx := context.Background()
	_, _ = admin.Exec(ctx, "drop schema "+identifier(schema)+" cascade")
	_ = admin.Close(ctx)
}
