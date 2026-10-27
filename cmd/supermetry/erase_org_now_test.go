package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"supermetry/internal/db"
	platformhttp "supermetry/internal/platform/http"
	"supermetry/internal/platform/orgs"
	"supermetry/internal/platform/store"
	"supermetry/internal/replay"
	"supermetry/internal/testpg"
)

func TestAdminEraseOrgNowErasesDataAndKeepsOthers(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	day := time.Now().UTC().Truncate(24 * time.Hour)
	if err := db.EnsurePartitions(ctx, pool, day, day.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	var adminID, userID, doomedOrg, keptOrg int64
	if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash,is_admin) values ('root@example.test','R','h',true) returning id`).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash) values ('both@example.test','B','h') returning id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Doomed','doomed-now') returning id`).Scan(&doomedOrg); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Kept','kept-now') returning id`).Scan(&keptOrg); err != nil {
		t.Fatal(err)
	}
	for _, org := range []int64{doomedOrg, keptOrg} {
		if _, err := pool.Exec(ctx, `insert into memberships(org_id,user_id,role) values ($1,$2,'owner')`, org, userID); err != nil {
			t.Fatal(err)
		}
	}
	siteIDs := map[int64]int32{}
	for org, publicID := range map[int64]string{doomedOrg: "erasenow0001", keptOrg: "erasenow0002"} {
		var siteID int32
		if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class) values ($1,$2,'S','UTC',90) returning id`, org, publicID).Scan(&siteID); err != nil {
			t.Fatal(err)
		}
		siteIDs[org] = siteID
		at := day.Add(time.Hour)
		for _, query := range []string{
			`insert into profiles(site_id,external_id,first_seen_at,last_seen_at) select $1,'p',$3,$3 where $2::date is not null`,
			`insert into events(site_id,retention_class,local_day,ts,visitor_id,session_id,kind,path_id) values ($1,90,$2,$3,1,1,0,1)`,
			`insert into sessions(site_id,retention_class,session_id,visitor_id,start,last,local_day,pageviews) values ($1,90,1,1,$3,$3,$2,1)`,
			`insert into replay_sessions(site_id,session_id,started_at,last_seen_at,day,blob_key) values ($1,1,$3,$3,$2,'replay/missing')`,
		} {
			if _, err := pool.Exec(ctx, query, siteID, day, at); err != nil {
				t.Fatal(err)
			}
		}
	}
	services := platformhttp.Services{Store: &store.Store{Pool: pool}}
	if _, err := services.AdminEraseOrg(ctx, adminID, doomedOrg, "wrong"); err == nil {
		t.Fatal("wrong slug accepted")
	}
	status, err := services.AdminEraseOrg(ctx, adminID, doomedOrg, "doomed-now")
	if err != nil || status != platformhttp.OrgEraseStatusErasing {
		t.Fatalf("erase status=%q err=%v", status, err)
	}

	// A crash in the middle: the first job run fails on storage, the next resumes.
	disk, err := replay.NewDiskStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("storage unavailable")
	flaky := &failOnceDeletePrefix{BlobStore: disk, err: wantErr}
	now := time.Now()
	if err := eraseDeletedSites(ctx, pool, flaky, now); !errors.Is(err, wantErr) {
		t.Fatalf("first run err=%v, want storage failure", err)
	}
	var erased bool
	if err := pool.QueryRow(ctx, `select erased_at is not null from orgs where id=$1`, doomedOrg).Scan(&erased); err != nil || erased {
		t.Fatalf("org erased=%v err=%v after failed run", erased, err)
	}
	if err := eraseDeletedSites(ctx, pool, flaky, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	for _, table := range []string{"events", "sessions", "replay_sessions", "profiles"} {
		var gone, kept int
		if err := pool.QueryRow(ctx, `select count(*) from `+table+` where site_id=$1`, siteIDs[doomedOrg]).Scan(&gone); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `select count(*) from `+table+` where site_id=$1`, siteIDs[keptOrg]).Scan(&kept); err != nil {
			t.Fatal(err)
		}
		if gone != 0 || kept != 1 {
			t.Errorf("%s: doomed rows=%d (want 0), kept rows=%d (want 1)", table, gone, kept)
		}
	}
	var orgErased, siteErased, keptDeleted, userExists bool
	if err := pool.QueryRow(ctx, `select
		(select erased_at is not null from orgs where id=$1),
		(select erased_at is not null from sites where id=$2),
		(select deleted_at is not null from orgs where id=$3),
		exists(select 1 from users where id=$4)`, doomedOrg, siteIDs[doomedOrg], keptOrg, userID).Scan(&orgErased, &siteErased, &keptDeleted, &userExists); err != nil {
		t.Fatal(err)
	}
	if !orgErased || !siteErased || keptDeleted || !userExists {
		t.Fatalf("orgErased=%v siteErased=%v keptDeleted=%v userExists=%v", orgErased, siteErased, keptDeleted, userExists)
	}
	if status, err := services.AdminEraseOrg(ctx, adminID, doomedOrg, "doomed-now"); err != nil || status != platformhttp.OrgEraseStatusErased {
		t.Fatalf("repeat status=%q err=%v, want erased", status, err)
	}
	if err := orgs.New(&store.Store{Pool: pool}).AdminRestore(ctx, adminID, doomedOrg, now); !errors.Is(err, orgs.ErrNotRestorable) {
		t.Fatalf("restore err=%v, want not restorable", err)
	}
}
