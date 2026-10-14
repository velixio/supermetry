package db

import (
	"context"
	"testing"

	"supermetry/internal/testpg"
)

func TestNoUntaggedImpressionsMigrationCleansRowsAndRejectsEmptyCampaign(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()

	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	var cleanup migration
	for _, m := range migrations {
		if m.version <= 180 {
			if err := applyMigration(ctx, conn, m); err != nil {
				t.Fatal(err)
			}
		}
		if m.version == 190 {
			cleanup = m
		}
	}
	if cleanup.version == 0 {
		t.Fatal("migration 0190 is missing")
	}

	var orgID int64
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Pixels','pixels') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	var siteID int32
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class) values ($1,'migration190','Site','UTC',30) returning id`, orgID).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into tracking_pixels(site_id,name,tag) values ($1,'Kept','kept')`, siteID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into pixel_impressions_daily(site_id,day,campaign,country,impressions) values
		($1,'2026-10-14','kept','',1), ($1,'2026-10-14','orphan','',2), ($1,'2026-10-14','','',3)`, siteID); err != nil {
		t.Fatal(err)
	}
	if err := applyMigration(ctx, conn, cleanup); err != nil {
		t.Fatal(err)
	}

	var count int
	if err := pool.QueryRow(ctx, `select count(*) from pixel_impressions_daily where site_id=$1`, siteID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("remaining rows = %d, err %v; want only the registered campaign", count, err)
	}
	if _, err := pool.Exec(ctx, `insert into pixel_impressions_daily(site_id,day,campaign,country,impressions) values ($1,'2026-10-15','','',1)`, siteID); err == nil {
		t.Fatal("empty campaign passed the database constraint")
	}
	if _, err := pool.Exec(ctx, `insert into pixel_impressions_daily(site_id,day,campaign,country,impressions) values ($1,'2026-10-14','kept','',1)`, siteID); err == nil {
		t.Fatal("migration removed the pixel impressions primary key")
	}
}
