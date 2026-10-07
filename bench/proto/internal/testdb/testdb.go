// Package testdb gives tests a migrated store in a throwaway schema of the
// local `panel` database. Tests skip when PG is unreachable.
package testdb

import (
	"context"
	"fmt"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"panel/bench/proto/internal/core"
	"panel/bench/proto/internal/store"
)

// Open returns a store bound to a fresh schema that is dropped on cleanup.
func Open(t testing.TB) *store.Store {
	return OpenWithOptions(t, store.Options{})
}

// OpenWithOptions returns a migrated store in a fresh schema with the given
// store options. SearchPath is always replaced with the throwaway schema.
func OpenWithOptions(t testing.TB, opts store.Options) *store.Store {
	t.Helper()
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, store.DSN())
	if err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}
	schema := fmt.Sprintf("test_%d_%d", time.Now().UnixNano(), rand.IntN(1e6))
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	opts.SearchPath = schema
	if opts.MaxConns == 0 {
		opts.MaxConns = 8
	}
	st, err := store.Open(ctx, store.DSN(), opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		st.Close()
		admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		admin.Close(context.Background())
	})
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return st
}

// Sites imports a sites file into st.
func Sites(t testing.TB, st *store.Store, path string) []core.Site {
	t.Helper()
	sites, err := core.LoadSitesFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertSites(context.Background(), sites); err != nil {
		t.Fatal(err)
	}
	return sites
}
