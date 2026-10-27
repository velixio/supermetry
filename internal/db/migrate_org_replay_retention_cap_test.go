package db

import (
	"context"
	"testing"

	"supermetry/internal/testpg"
)

func TestOrgReplayRetentionCapMigration(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range migrations {
		if m.version >= 250 {
			break
		}
		if err := applyMigration(ctx, conn, m); err != nil {
			conn.Release()
			t.Fatal(err)
		}
	}
	conn.Release()
	var existing int64
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Before','before') returning id`).Scan(&existing); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	// Organizations that existed before have no cap.
	var capDays *int
	if err := pool.QueryRow(ctx, `select replay_retention_cap_days from orgs where id=$1`, existing).Scan(&capDays); err != nil || capDays != nil {
		t.Fatalf("existing org cap = %v, %v; want null", capDays, err)
	}
	for _, value := range []int{1, 30, 365} {
		if _, err := pool.Exec(ctx, `update orgs set replay_retention_cap_days=$2 where id=$1`, existing, value); err != nil {
			t.Fatalf("cap %d rejected: %v", value, err)
		}
	}
	for _, value := range []int{0, -1, 366} {
		if _, err := pool.Exec(ctx, `update orgs set replay_retention_cap_days=$2 where id=$1`, existing, value); err == nil {
			t.Fatalf("cap %d accepted, want check violation", value)
		}
	}
}
