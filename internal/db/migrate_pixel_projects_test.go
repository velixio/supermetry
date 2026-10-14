package db_test

import (
	"context"
	"testing"

	"supermetry/internal/db"
	"supermetry/internal/testpg"
)

func TestPixelProjectsMigration(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var orgID int64
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Px','px') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	var siteID int32
	var kind string
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class) values ($1,'pxsite000001','S','UTC',30) returning id, kind`, orgID).Scan(&siteID, &kind); err != nil {
		t.Fatal(err)
	}
	if kind != "web" {
		t.Fatalf("default kind = %q, want web", kind)
	}
	if _, err := pool.Exec(ctx, `update sites set kind='email' where id=$1`, siteID); err == nil {
		t.Fatal("kind check accepted 'email'")
	}
	var pixelID int64
	var label string
	var total int64
	if err := pool.QueryRow(ctx, `insert into pixels(site_id,public_id,name) values ($1,'pAbCdEfGhIjK','GitHub profile') returning id, badge_label, total_views`, siteID).Scan(&pixelID, &label, &total); err != nil {
		t.Fatal(err)
	}
	if label != "views" || total != 0 {
		t.Fatalf("defaults badge_label=%q total_views=%d", label, total)
	}
	if _, err := pool.Exec(ctx, `update pixels set total_views=-1 where id=$1`, pixelID); err == nil {
		t.Fatal("negative total_views accepted")
	}
	for _, bad := range []string{
		`insert into pixels(site_id,public_id,name) values ($1,'short','x')`,
		`insert into pixels(site_id,public_id,name) values ($1,'xAbCdEfGhIjK','x')`,
		`insert into pixels(site_id,public_id,name) values ($1,'pAbCdEfGhIj2','   ')`,
	} {
		if _, err := pool.Exec(ctx, bad, siteID); err == nil {
			t.Errorf("accepted: %s", bad)
		}
	}
	if _, err := pool.Exec(ctx, `insert into pixel_views_daily(pixel_id,day,country,via,views) values ($1,'2026-10-14','','proxy',3)`, pixelID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into pixel_views_daily(pixel_id,day,country,via,views) values ($1,'2026-10-14','','bot',1)`, pixelID); err == nil {
		t.Fatal("via check accepted 'bot'")
	}
	if _, err := pool.Exec(ctx, `delete from pixels where id=$1`, pixelID); err != nil {
		t.Fatal(err)
	}
	var left int
	_ = pool.QueryRow(ctx, `select count(*) from pixel_views_daily`).Scan(&left)
	if left != 0 {
		t.Fatalf("daily rows left after cascade = %d", left)
	}
}
