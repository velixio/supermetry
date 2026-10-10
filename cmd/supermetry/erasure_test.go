package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/db"
	"supermetry/internal/replay"
	"supermetry/internal/testpg"
)

type failOnceDeletePrefix struct {
	replay.BlobStore
	err    error
	failed bool
}

func (s *failOnceDeletePrefix) DeletePrefix(ctx context.Context, prefix string) (int64, error) {
	if !s.failed {
		s.failed = true
		return 0, s.err
	}
	return s.BlobStore.DeletePrefix(ctx, prefix)
}

func TestEraseDeletedSitesRetriesStorageFailureAndLaterWrites(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var orgID int64
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug,deleted_at) values ('Deleted','deleted',now()) returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	var siteID int32
	if err := pool.QueryRow(ctx, `
		insert into sites(org_id,public_id,name,timezone,retention_class,revenue_currency,revenue_currency_state,deleted_at)
		values ($1,'delete000001','Deleted','UTC',30,'USD','configured',now()) returning id`, orgID).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx, `insert into dirty_day(site_id,day,reason) values ($1,$2,'late_event')`, siteID, day); err != nil {
		t.Fatal(err)
	}
	disk, err := replay.NewDiskStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("storage unavailable")
	store := &failOnceDeletePrefix{BlobStore: disk, err: wantErr}
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

	if err := eraseDeletedSites(ctx, pool, store, now); !errors.Is(err, wantErr) {
		t.Fatalf("first erasure error=%v, want storage failure", err)
	}
	assertErasureState(t, pool, siteID, false, 1)

	if err := eraseDeletedSites(ctx, pool, store, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	assertErasureState(t, pool, siteID, true, 0)

	// A buffered flush or another process can write after the observation marker.
	// Deleted sites remain candidates, so the next run (including after restart)
	// removes that late row instead of treating erased_at as a terminal state.
	if _, err := pool.Exec(ctx, `insert into dirty_day(site_id,day,reason) values ($1,$2,'late_event')`, siteID, day); err != nil {
		t.Fatal(err)
	}
	if err := eraseDeletedSites(ctx, pool, store, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	assertErasureState(t, pool, siteID, true, 0)
}

func TestEraseDeletedDemoSiteRemovesAllDemoData(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	if err := db.EnsurePartitions(ctx, pool, day, day.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	var orgID int64
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Demo','demo-erasure') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	var siteID int32
	if err := pool.QueryRow(ctx, `
		insert into sites(org_id,public_id,name,timezone,retention_class,is_demo,deleted_at)
		values ($1,'erase-demo01','Demo','UTC',90,true,now()) returning id`, orgID).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	at := day.Add(12 * time.Hour)
	for _, query := range []string{
		`insert into profiles(site_id,external_id,first_seen_at,last_seen_at) select $1,'demo-person',$3,$3 where $2::date is not null`,
		`insert into events(site_id,retention_class,local_day,ts,visitor_id,session_id,kind,path_id) values ($1,90,$2,$3,1,1,0,1)`,
		`insert into sessions(site_id,retention_class,session_id,visitor_id,start,last,local_day,pageviews) values ($1,90,1,1,$3,$3,$2,1)`,
		`insert into heatmap_cells(site_id,day,path_id,device,gx,gy,clicks) select $1,$2,1,0,1,1,1 where $3::timestamptz is not null`,
		`insert into replay_sessions(site_id,session_id,started_at,last_seen_at,day,blob_key) values ($1,1,$3,$3,$2,'replay/missing')`,
	} {
		if _, err := pool.Exec(ctx, query, siteID, day, at); err != nil {
			t.Fatal(err)
		}
	}
	blobs, err := replay.NewDiskStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := eraseDeletedSites(ctx, pool, blobs, day.Add(13*time.Hour)); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"events", "sessions", "replay_sessions", "heatmap_cells", "profiles"} {
		var count int
		if err := pool.QueryRow(ctx, `select count(*) from `+table+` where site_id=$1`, siteID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Errorf("%s rows = %d, want 0", table, count)
		}
	}
}

func assertErasureState(t *testing.T, pool *pgxpool.Pool, siteID int32, wantMarked bool, wantDirty int) {
	t.Helper()
	var marked bool
	if err := pool.QueryRow(context.Background(), `select settings ? 'erased_at' from sites where id=$1`, siteID).Scan(&marked); err != nil {
		t.Fatal(err)
	}
	var dirty int
	if err := pool.QueryRow(context.Background(), `select count(*) from dirty_day where site_id=$1`, siteID).Scan(&dirty); err != nil {
		t.Fatal(err)
	}
	if marked != wantMarked || dirty != wantDirty {
		t.Fatalf("erasure state marked=%t dirty=%d, want marked=%t dirty=%d", marked, dirty, wantMarked, wantDirty)
	}
}
