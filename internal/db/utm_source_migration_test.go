package db_test

import (
	"context"
	"testing"

	"panel/internal/db"
	"panel/internal/testpg"
)

func TestUTMSourceMigrationAddsDefaultedColumns(t *testing.T) {
	pool, _ := testpg.New(t)
	if err := db.Migrate(context.Background(), pool); err != nil {
		t.Fatal(err)
	}

	for _, table := range []string{"events", "sessions", "sessions_closed", "custom_closed"} {
		t.Run(table, func(t *testing.T) {
			var nullable, defaultValue string
			err := pool.QueryRow(context.Background(), `
				select is_nullable, column_default
				from information_schema.columns
				where table_schema = current_schema()
				  and table_name = $1
				  and column_name = 'utm_source_id'`, table).Scan(&nullable, &defaultValue)
			if err != nil {
				t.Fatalf("utm_source_id schema: %v", err)
			}
			if nullable != "NO" || defaultValue != "0" {
				t.Fatalf("utm_source_id nullable=%q default=%q, want NO and 0", nullable, defaultValue)
			}
		})
	}
}
