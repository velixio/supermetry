package main

import (
	"context"
	"errors"
	"fmt"
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
	calls  int
}

func (s *failOnceDeletePrefix) DeletePrefix(ctx context.Context, prefix string) (int64, error) {
	s.calls++
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
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug,deleted_at) values ('Deleted','deleted',$1) returning id`, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	var siteID int32
	if err := pool.QueryRow(ctx, `
		insert into sites(org_id,public_id,name,timezone,retention_class,revenue_currency,revenue_currency_state,deleted_at)
		values ($1,'delete000001','Deleted','UTC',30,'USD','configured',$2) returning id`, orgID, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)).Scan(&siteID); err != nil {
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
	if store.calls != 2 {
		t.Fatalf("replay erase calls = %d, want 2", store.calls)
	}

	// Once marked, a site is terminal and a later run must not erase it again.
	if _, err := pool.Exec(ctx, `insert into dirty_day(site_id,day,reason) values ($1,$2,'late_event')`, siteID, day); err != nil {
		t.Fatal(err)
	}
	if err := eraseDeletedSites(ctx, pool, store, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	assertErasureState(t, pool, siteID, true, 1)
	if store.calls != 2 {
		t.Fatalf("second run replay erase calls = %d, want 2", store.calls)
	}
}

func TestEraseDeletedSitesResumesClaimedSite(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	var orgID int64
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Resume','resume') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	var siteID int32
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class,deleted_at,erasure_started_at) values ($1,'resume-site1','Resume','UTC',30,$2,$3) returning id`, orgID, now.Add(-8*24*time.Hour), now.Add(-time.Minute)).Scan(&siteID); err != nil {
		t.Fatal(err)
	}

	if err := eraseDeletedSites(ctx, pool, nil, now); err != nil {
		t.Fatal(err)
	}
	assertErasureState(t, pool, siteID, true, 0)
}

func TestEraseDeletedSitesHonorsGraceBoundary(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	deletedAt := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	if err := db.EnsurePartitions(ctx, pool, deletedAt, deletedAt.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	var orgID int64
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Grace','grace-erasure') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	var siteID int32
	if err := pool.QueryRow(ctx, `
		insert into sites(org_id,public_id,name,timezone,retention_class,deleted_at)
		values ($1,'grace-real01','Grace','UTC',30,$2) returning id`, orgID, deletedAt).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into events(site_id,retention_class,local_day,ts,visitor_id,session_id,kind,path_id) values ($1,30,$2,$3,1,1,0,1)`, siteID, deletedAt, deletedAt.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	if err := eraseDeletedSites(ctx, pool, nil, deletedAt.Add(6*24*time.Hour+23*time.Hour)); err != nil {
		t.Fatal(err)
	}
	assertEventErasure(t, pool, siteID, 1, false)
	if err := eraseDeletedSites(ctx, pool, nil, deletedAt.Add(7*24*time.Hour+time.Minute)); err != nil {
		t.Fatal(err)
	}
	assertEventErasure(t, pool, siteID, 0, true)
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

func TestEraseDeletedSitesMarksOrganizationsErased(t *testing.T) {
	tests := []struct {
		name       string
		deletedAgo time.Duration
		demoSite   bool
		withOrg    bool
		wantErased bool
	}{
		{name: "no-site organization is immediate", deletedAgo: time.Minute, wantErased: true},
		{name: "demo-only organization is immediate", deletedAgo: time.Minute, demoSite: true, wantErased: true},
		{name: "scheduled organization waits for grace", deletedAgo: 6*24*time.Hour + 23*time.Hour, withOrg: true},
		{name: "scheduled organization after grace", deletedAgo: 7*24*time.Hour + time.Minute, withOrg: true, wantErased: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pool, _ := testpg.New(t)
			ctx := context.Background()
			if err := db.Migrate(ctx, pool); err != nil {
				t.Fatal(err)
			}
			now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
			deletedAt := now.Add(-test.deletedAgo)
			var orgID int64
			if err := pool.QueryRow(ctx, `insert into orgs(name,slug,deleted_at) values ('Deleted',$1,$2) returning id`, "erase-org-"+test.name, deletedAt).Scan(&orgID); err != nil {
				t.Fatal(err)
			}
			if test.demoSite || test.withOrg {
				if _, err := pool.Exec(ctx, `
					insert into sites(org_id,public_id,name,timezone,retention_class,is_demo,deleted_at,deleted_with_org)
					values ($1,$2,'Deleted','UTC',30,$3,$4,$5)`, orgID, fmt.Sprintf("eraseorg%04d", orgID), test.demoSite, deletedAt, test.withOrg); err != nil {
					t.Fatal(err)
				}
			}
			if err := eraseDeletedSites(ctx, pool, nil, now); err != nil {
				t.Fatal(err)
			}
			var erased bool
			if err := pool.QueryRow(ctx, `select erased_at is not null from orgs where id=$1`, orgID).Scan(&erased); err != nil {
				t.Fatal(err)
			}
			if erased != test.wantErased {
				t.Fatalf("organization erased=%t, want %t", erased, test.wantErased)
			}
		})
	}
}

func assertErasureState(t *testing.T, pool *pgxpool.Pool, siteID int32, wantMarked bool, wantDirty int) {
	t.Helper()
	var marked bool
	if err := pool.QueryRow(context.Background(), `select erased_at is not null and settings ? 'erased_at' from sites where id=$1`, siteID).Scan(&marked); err != nil {
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

func assertEventErasure(t *testing.T, pool *pgxpool.Pool, siteID int32, wantEvents int, wantErased bool) {
	t.Helper()
	var events int
	if err := pool.QueryRow(context.Background(), `select count(*) from events where site_id=$1`, siteID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	var erased bool
	if err := pool.QueryRow(context.Background(), `select erased_at is not null from sites where id=$1`, siteID).Scan(&erased); err != nil {
		t.Fatal(err)
	}
	if events != wantEvents || erased != wantErased {
		t.Fatalf("events=%d erased=%t, want events=%d erased=%t", events, erased, wantEvents, wantErased)
	}
}
