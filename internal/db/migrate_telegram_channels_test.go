package db

import (
	"context"
	"testing"

	"supermetry/internal/testpg"
)

func TestTelegramChannelMigrationPreservesAndEnablesExistingChannels(t *testing.T) {
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
	for _, migration := range migrations {
		if migration.version < 31 {
			if err := applyMigration(ctx, conn, migration); err != nil {
				conn.Release()
				t.Fatal(err)
			}
		}
	}
	conn.Release()
	if _, err := pool.Exec(ctx, `insert into orgs(name,slug) values('Org','org');
		insert into notification_channels(org_id,kind,name,config) values(1,'email','Owners','{"recipients":["owner@example.test"]}')`); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var enabled bool
	if err := pool.QueryRow(ctx, `select enabled from notification_channels where name='Owners'`).Scan(&enabled); err != nil {
		t.Fatal(err)
	}
	if !enabled {
		t.Fatal("existing notification channel was disabled")
	}
	if _, err := pool.Exec(ctx, `insert into notification_channels(org_id,kind,name,config,enabled) values(1,'telegram','Telegram','{}',false)`); err != nil {
		t.Fatalf("Telegram kind rejected after migration: %v", err)
	}
}
