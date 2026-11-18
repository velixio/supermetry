package retention_test

import (
	"context"
	"testing"
	"time"

	"supermetry/internal/db"
	"supermetry/internal/retention"
	"supermetry/internal/testpg"
)

func TestRunDropsStaleMissingDebugFilesAndEraseClearsThem(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 7, 3, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx, `insert into orgs(name,slug) values('Org','org');
insert into sites(id,org_id,public_id,name,timezone,retention_class,kind) overriding system value values(1,1,'appmis001abc','App','UTC',0,'app')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into app_missing_debug_files(site_id,kind,debug_id,first_seen,last_seen,events) values
 (1,'elf','7a3c9e10-55b2-4b8e-8a53-2f0c8d4e9a01',$1,$2,5),
 (1,'macho','de7fada3-56f7-3ce7-b81e-0b6e964459c1',$1,$3,1)`,
		now.AddDate(0, 0, -60), now.AddDate(0, 0, -31), now.AddDate(0, 0, -2)); err != nil {
		t.Fatal(err)
	}
	if _, err := retention.Run(ctx, pool, now); err != nil {
		t.Fatal(err)
	}
	var kinds string
	if err := pool.QueryRow(ctx, `select string_agg(kind, ',') from app_missing_debug_files`).Scan(&kinds); err != nil || kinds != "macho" {
		t.Fatalf("kinds = %q err = %v (a row idle for 31 days goes, a recent one stays, whatever the retention class)", kinds, err)
	}
	if err := retention.Erase(ctx, pool, 1, 10, 0); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pool.QueryRow(ctx, `select count(*) from app_missing_debug_files`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rows after erase = %d err = %v", n, err)
	}
}
