// Package testpg creates isolated PostgreSQL schemas for database tests.
package testpg

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// New returns a pool bound to a fresh schema and drops the schema at cleanup.
func New(t testing.TB) (*pgxpool.Pool, string) {
	t.Helper()

	dsn := os.Getenv("PANEL_TEST_DSN")
	if dsn == "" {
		t.Skip("PANEL_TEST_DSN is not set")
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

	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		dropSchema(admin, identifier)
		t.Fatalf("parse test database URL: %v", err)
	}
	config.MaxConns = 8
	config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "set search_path to "+identifier)
		return err
	}

	pool, err := pgxpool.NewWithConfig(ctx, config)
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

func randomSchema(t testing.TB) string {
	t.Helper()
	var suffix [12]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatalf("generate test schema name: %v", err)
	}
	return "t_" + hex.EncodeToString(suffix[:])
}

func dropSchema(admin *pgx.Conn, identifier string) {
	ctx := context.Background()
	if _, err := admin.Exec(ctx, "drop schema "+identifier+" cascade"); err != nil {
		_ = admin.Close(ctx)
		return
	}
	_ = admin.Close(ctx)
}
