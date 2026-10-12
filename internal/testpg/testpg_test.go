package testpg

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestNewCreatesIsolatedDatabasesForConcurrentCallers(t *testing.T) {
	requireTestDSN(t)
	const callers = 2
	ready := make(chan struct{})
	var readyOnce sync.Once
	var readyCount int
	var mu sync.Mutex
	databaseNames := make(chan string, callers)

	tests := []struct {
		name   string
		marker int
	}{
		{name: "first", marker: 1},
		{name: "second", marker: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			pool, _ := New(t)
			ctx := context.Background()

			if _, err := pool.Exec(ctx, `create table testpg_isolation (marker integer not null)`); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `insert into testpg_isolation(marker) values ($1)`, tt.marker); err != nil {
				t.Fatal(err)
			}

			mu.Lock()
			readyCount++
			if readyCount == callers {
				readyOnce.Do(func() { close(ready) })
			}
			mu.Unlock()
			select {
			case <-ready:
			case <-time.After(30 * time.Second):
				t.Fatal("timed out waiting for concurrent database setup")
			}

			var databaseName string
			var markers int
			if err := pool.QueryRow(ctx, `select current_database()`).Scan(&databaseName); err != nil {
				t.Fatal(err)
			}
			if err := pool.QueryRow(ctx, `select count(*) from testpg_isolation`).Scan(&markers); err != nil {
				t.Fatal(err)
			}
			if markers != 1 {
				t.Fatalf("marker count = %d, want 1", markers)
			}
			databaseNames <- databaseName
		})
	}
	t.Cleanup(func() {
		seen := make(map[string]bool, callers)
		for i := 0; i < callers; i++ {
			name := <-databaseNames
			if seen[name] {
				t.Errorf("database %q was shared by concurrent callers", name)
			}
			seen[name] = true
		}
		if len(seen) != callers {
			t.Errorf("got %d database names, want %d", len(seen), callers)
		}
	})
}

func TestNewDropsDatabaseAtCleanup(t *testing.T) {
	dsn := requireTestDSN(t)
	var databaseName string
	t.Run("database owner", func(t *testing.T) {
		pool, _ := New(t)
		if err := pool.QueryRow(context.Background(), `select current_database()`).Scan(&databaseName); err != nil {
			t.Fatal(err)
		}
	})

	admin, err := pgx.Connect(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = admin.Close(context.Background()) }()
	var exists bool
	if err := admin.QueryRow(context.Background(), `select exists(select 1 from pg_database where datname=$1)`, databaseName).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatalf("database %q still exists after cleanup", databaseName)
	}
}

func TestNewReusesMigratedTemplate(t *testing.T) {
	dsn := requireTestDSN(t)
	admin, err := pgx.Connect(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = admin.Close(context.Background()) }()

	hash, err := migrationHash()
	if err != nil {
		t.Fatal(err)
	}
	templateName := templatePrefix + hash[:migrationHashLength]
	templateExists := func() bool {
		t.Helper()
		var exists bool
		if err := admin.QueryRow(context.Background(), `select exists(select 1 from pg_database where datname=$1)`, templateName).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		return exists
	}

	alreadyReusable := templateExists()
	started := time.Now()
	first, _ := New(t)
	firstDuration := time.Since(started)
	if err := first.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	started = time.Now()
	second, _ := New(t)
	secondDuration := time.Since(started)
	if err := second.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}

	if alreadyReusable {
		t.Skip("migration template was already reusable")
	}
	if secondDuration*5 > firstDuration {
		t.Fatalf("template reuse took %s after initial migration took %s; want at least 5x faster", secondDuration, firstDuration)
	}
}

func requireTestDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("SUPERMETRY_TEST_DSN")
	if dsn == "" {
		t.Skip("SUPERMETRY_TEST_DSN is not set")
	}
	return dsn
}
