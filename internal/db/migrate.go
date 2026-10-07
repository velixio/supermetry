package db

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

const migrationCleanupTimeout = 5 * time.Second

type migration struct {
	version int
	name    string
	sql     string
}

// Migrate applies each embedded migration once, in numeric order.
func Migrate(ctx context.Context, pool *pgxpool.Pool) (resultErr error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration connection: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `select pg_advisory_lock(hashtext('panel:migrate:' || current_schema()))`); err != nil {
		return fmt.Errorf("lock migrations: %w", err)
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), migrationCleanupTimeout)
		defer cancel()

		var unlocked bool
		unlockErr := conn.QueryRow(cleanupCtx, `select pg_advisory_unlock(hashtext('panel:migrate:' || current_schema()))`).Scan(&unlocked)
		if unlockErr == nil && unlocked {
			return
		}
		if unlockErr == nil {
			unlockErr = errors.New("advisory lock was not held")
		}
		closeErr := conn.Conn().Close(cleanupCtx)
		resultErr = errors.Join(resultErr, fmt.Errorf("unlock migrations: %w", unlockErr), closeErr)
	}()

	migrations, err := loadMigrations()
	if err != nil {
		return err
	}
	for _, migration := range migrations {
		applied, err := migrationApplied(ctx, conn, migration.version)
		if err != nil {
			return fmt.Errorf("check migration %04d: %w", migration.version, err)
		}
		if applied {
			continue
		}
		if err := applyMigration(ctx, conn, migration); err != nil {
			return err
		}
	}
	return nil
}

func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read embedded migrations: %w", err)
	}

	migrations := make([]migration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
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
		contents, err := migrationFiles.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", entry.Name(), err)
		}
		migrations = append(migrations, migration{version: version, name: entry.Name(), sql: string(contents)})
	}

	sort.Slice(migrations, func(i, j int) bool { return migrations[i].version < migrations[j].version })
	for i := 1; i < len(migrations); i++ {
		if migrations[i-1].version == migrations[i].version {
			return nil, fmt.Errorf("duplicate migration version %04d", migrations[i].version)
		}
	}
	return migrations, nil
}

func migrationApplied(ctx context.Context, conn *pgxpool.Conn, version int) (bool, error) {
	var exists bool
	if err := conn.QueryRow(ctx, `select to_regclass('schema_migrations') is not null`).Scan(&exists); err != nil {
		return false, err
	}
	if !exists {
		return false, nil
	}
	var applied bool
	if err := conn.QueryRow(ctx, `select exists(select 1 from schema_migrations where version = $1)`, version).Scan(&applied); err != nil {
		return false, err
	}
	return applied, nil
}

func applyMigration(ctx context.Context, conn *pgxpool.Conn, migration migration) error {
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin migration %04d (%s): %w", migration.version, migration.name, err)
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), migrationCleanupTimeout)
		defer cancel()
		_ = tx.Rollback(cleanupCtx)
	}()

	if _, err := tx.Exec(ctx, migration.sql); err != nil {
		return fmt.Errorf("apply migration %04d (%s): %w", migration.version, migration.name, err)
	}
	if _, err := tx.Exec(ctx, `insert into schema_migrations(version) values ($1)`, migration.version); err != nil {
		return fmt.Errorf("record migration %04d (%s): %w", migration.version, migration.name, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit migration %04d (%s): %w", migration.version, migration.name, err)
	}
	return nil
}
