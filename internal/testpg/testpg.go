// Package testpg creates isolated PostgreSQL schemas for database tests.
package testpg

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type reusableSchema struct {
	admin      *pgx.Conn
	pool       *pgxpool.Pool
	schema     string
	identifier string
}

var reusable struct {
	sync.Mutex
	enabled  bool
	parallel bool
	ready    chan *reusableSchema
	all      []*reusableSchema
}

// EnableParallelReuse allows tests checking out reusable schemas to run in
// parallel. Call it only for packages whose fixtures share no process globals.
func EnableParallelReuse() {
	reusable.Lock()
	reusable.parallel = true
	reusable.Unlock()
}

// EnableTemplate prepares two migrated schemas that New reuses for this test
// process. It is intended for package TestMain functions with many fixtures.
func EnableTemplate(migrate func(context.Context, *pgxpool.Pool) error) error {
	dsn := os.Getenv("SUPERMETRY_TEST_DSN")
	if dsn == "" {
		return nil
	}
	reusable.Lock()
	defer reusable.Unlock()
	if reusable.enabled {
		return nil
	}
	reusable.ready = make(chan *reusableSchema, 2)
	for range 2 {
		slot, err := newMigratedSchema(dsn, migrate)
		if err != nil {
			for _, created := range reusable.all {
				created.pool.Close()
				dropSchema(created.admin, created.identifier)
			}
			reusable.all = nil
			return err
		}
		reusable.all = append(reusable.all, slot)
		reusable.ready <- slot
	}
	reusable.enabled = true
	return nil
}

// CloseTemplate removes the schemas created by EnableTemplate.
func CloseTemplate() {
	reusable.Lock()
	defer reusable.Unlock()
	for _, slot := range reusable.all {
		slot.pool.Close()
		dropSchema(slot.admin, slot.identifier)
	}
	reusable.enabled = false
	reusable.parallel = false
	reusable.ready = nil
	reusable.all = nil
}

// New returns a pool bound to a fresh schema and drops the schema at cleanup.
// When EnableTemplate is active, it instead checks out a migrated schema and
// restores it to its empty post-migration state at cleanup.
func New(t testing.TB) (*pgxpool.Pool, string) {
	t.Helper()

	dsn := os.Getenv("SUPERMETRY_TEST_DSN")
	if dsn == "" {
		t.Skip("SUPERMETRY_TEST_DSN is not set")
	}
	reusable.Lock()
	enabled, parallel, ready := reusable.enabled, reusable.parallel, reusable.ready
	reusable.Unlock()
	if enabled {
		if parallel {
			if test, ok := t.(interface{ Parallel() }); ok {
				test.Parallel()
			}
		}
		slot := <-ready
		t.Cleanup(func() {
			if err := resetSchema(slot); err != nil {
				t.Errorf("reset reusable test schema: %v", err)
			}
			ready <- slot
		})
		return slot.pool, slot.schema
	}

	ctx := context.Background()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to test postgres: %v", err)
	}

	schema := randomSchema(t)
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "create schema "+identifier); err != nil {
		_ = admin.Close(ctx)
		t.Fatalf("create test schema: %v", err)
	}

	pool, err := schemaPool(ctx, dsn, identifier)
	if err != nil {
		dropSchema(admin, identifier)
		t.Fatalf("open test pool: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		dropSchema(admin, identifier)
		t.Fatalf("ping test postgres: %v", err)
	}

	t.Cleanup(func() {
		pool.Close()
		dropSchema(admin, identifier)
	})
	return pool, schema
}

func newMigratedSchema(dsn string, migrate func(context.Context, *pgxpool.Pool) error) (*reusableSchema, error) {
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("connect to test postgres: %w", err)
	}
	suffix, err := randomName()
	if err != nil {
		_ = admin.Close(ctx)
		return nil, err
	}
	schema := "t_" + suffix
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "create schema "+identifier); err != nil {
		_ = admin.Close(ctx)
		return nil, fmt.Errorf("create reusable test schema: %w", err)
	}
	pool, err := schemaPool(ctx, dsn, identifier)
	if err != nil {
		dropSchema(admin, identifier)
		return nil, err
	}
	if err := migrate(ctx, pool); err != nil {
		pool.Close()
		dropSchema(admin, identifier)
		return nil, fmt.Errorf("migrate reusable test schema: %w", err)
	}
	return &reusableSchema{admin: admin, pool: pool, schema: schema, identifier: identifier}, nil
}

func schemaPool(ctx context.Context, dsn, identifier string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse test database URL: %w", err)
	}
	config.MaxConns = 8
	config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "set search_path to "+identifier)
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("open test pool: %w", err)
	}
	return pool, nil
}

func resetSchema(slot *reusableSchema) error {
	ctx := context.Background()
	var tables string
	if err := slot.pool.QueryRow(ctx, `
select coalesce(string_agg(format('%I.%I', n.nspname, c.relname), ','), '')
from pg_class c
join pg_namespace n on n.oid=c.relnamespace
where n.nspname=$1 and c.relkind in ('r','p')
  and c.relname not in ('schema_migrations','instance_settings')
  and not exists (select 1 from pg_inherits i where i.inhrelid=c.oid)`, slot.schema).Scan(&tables); err != nil {
		return err
	}
	if tables != "" {
		if _, err := slot.pool.Exec(ctx, "truncate "+tables+" restart identity cascade"); err != nil {
			return err
		}
	}
	_, err := slot.pool.Exec(ctx, "truncate instance_settings; insert into instance_settings default values")
	return err
}

func randomSchema(t testing.TB) string {
	t.Helper()
	name, err := randomName()
	if err != nil {
		t.Fatal(err)
	}
	return "t_" + name
}

func randomName() (string, error) {
	var suffix [12]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", fmt.Errorf("generate test schema name: %w", err)
	}
	return hex.EncodeToString(suffix[:]), nil
}

func dropSchema(admin *pgx.Conn, identifier string) {
	ctx := context.Background()
	if _, err := admin.Exec(ctx, "drop schema "+identifier+" cascade"); err != nil {
		_ = admin.Close(ctx)
		return
	}
	_ = admin.Close(ctx)
}
