package db_test

import (
	"context"
	"testing"
	"time"

	"supermetry/internal/db"
	"supermetry/internal/testpg"
)

func TestAccountSessionMigrationPreservesLegacyTimestamps(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	// Reconstruct the pre-0028 table shape in this isolated test schema.
	if _, err := pool.Exec(ctx, `alter table user_sessions drop column ip_address, drop column country_code, drop column browser; delete from schema_migrations where version=28`); err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 9, 1, 10, 11, 12, 0, time.UTC)
	active := created.Add(2 * time.Hour)
	if _, err := pool.Exec(ctx, `insert into users(email,name,password_hash) values ('legacy@example.test','Legacy','test');
 insert into user_sessions(id_hash,user_id,device_label,created_at,last_seen_at,expires_at)
 select decode(repeat('ab',32),'hex'),id,'Old device','2026-09-01T10:11:12Z','2026-09-01T12:11:12Z','2026-12-01T10:11:12Z' from users`); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var gotCreated, gotActive time.Time
	var unknown bool
	if err := pool.QueryRow(ctx, `select created_at,last_seen_at,ip_address is null and country_code is null and browser is null from user_sessions`).Scan(&gotCreated, &gotActive, &unknown); err != nil {
		t.Fatal(err)
	}
	if !gotCreated.Equal(created) || !gotActive.Equal(active) || !unknown {
		t.Fatalf("migration invented metadata: created=%v active=%v unknown=%v", gotCreated, gotActive, unknown)
	}
}
