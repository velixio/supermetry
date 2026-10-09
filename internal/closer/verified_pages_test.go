package closer_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"supermetry/internal/closer"
	"supermetry/internal/db"
	"supermetry/internal/retention"
	"supermetry/internal/testpg"
)

func TestVerifiedPagesClosePreservesPolicyCohortsAndRebuildsAfterRawExpiry(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	day := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsurePartitions(ctx, pool, day.AddDate(0, 0, -1), day.AddDate(0, 0, 2)); err != nil {
		t.Fatal(err)
	}
	_, err := pool.Exec(ctx, `insert into events(site_id,retention_class,local_day,ts,visitor_id,session_id,kind,path_id,path_sanitizer_version,path_policy_id)
 select 1,30,'2026-10-08','2026-10-08 12:00Z',visitor,session,kind,path,version,policy
 from (values
 (1::bigint,1::bigint,0,1,0,''),
 (1,1,0,1,1,'policy-a'),(1,1,0,1,1,'policy-a'),(1,1,0,2,1,'policy-a'),
 (1,2,0,1,1,'policy-a'),(2,3,0,1,1,'policy-a'),
 (1,1,0,1,1,'policy-b'),(1,1,1,1,1,'policy-a'),(1,1,0,1,2,'future')
 ) v(visitor,session,kind,path,version,policy)`)
	if err != nil {
		t.Fatal(err)
	}
	closeDay := func() {
		t.Helper()
		if _, err := closer.CloseDay(ctx, pool, []int32{1}, day); err != nil {
			t.Fatal(err)
		}
	}
	assertCounts := func(policy string, path int, wantV, wantS, wantP int64) {
		t.Helper()
		var v, s, p int64
		err := pool.QueryRow(ctx, `select visitors,visits,pageviews from verified_page_day where site_id=1 and day=$1 and path_sanitizer_version=1 and path_policy_id=$2 and path_id=$3`, day, policy, path).Scan(&v, &s, &p)
		if err != nil || v != wantV || s != wantS || p != wantP {
			t.Fatalf("%s/%d got=%d,%d,%d want=%d,%d,%d err=%v", policy, path, v, s, p, wantV, wantS, wantP, err)
		}
	}
	closeDay()
	assertCounts("policy-a", 0, 2, 3, 5)
	assertCounts("policy-a", 1, 2, 3, 4)
	assertCounts("policy-a", 2, 1, 1, 1)
	assertCounts("policy-b", 0, 1, 1, 1)
	var groups int
	if err := pool.QueryRow(ctx, `select count(*) from verified_page_sessions`).Scan(&groups); err != nil || groups != 5 {
		t.Fatalf("groups=%d err=%v", groups, err)
	}
	closeDay()
	assertCounts("policy-a", 0, 2, 3, 5)
	if _, err := pool.Exec(ctx, `insert into events(site_id,retention_class,local_day,ts,visitor_id,session_id,kind,path_id,path_sanitizer_version,path_policy_id) values(1,30,'2026-10-08','2026-10-08 13:00Z',1,1,0,1,1,'policy-a')`); err != nil {
		t.Fatal(err)
	}
	closeDay()
	assertCounts("policy-a", 0, 2, 3, 6)
	if _, err := pool.Exec(ctx, `delete from events where path_policy_id='policy-b'`); err != nil {
		t.Fatal(err)
	}
	if _, err := closer.CloseDay(ctx, pool, []int32{1}, day); !errors.Is(err, closer.ErrVerifiedPageSourceIncomplete) {
		t.Fatalf("partial expiry close err=%v", err)
	}
	assertCounts("policy-b", 0, 1, 1, 1)
	if _, err := retention.Run(ctx, pool, day.AddDate(0, 0, 40)); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `select count(*) from events`).Scan(&groups); err != nil || groups != 0 {
		t.Fatalf("raw expiry rows=%d err=%v", groups, err)
	}
	if _, err := pool.Exec(ctx, `update verified_page_day set pageviews=999`); err != nil {
		t.Fatal(err)
	}
	if days, err := closer.RecomputeRollups(ctx, pool); err != nil || days != 1 {
		t.Fatalf("recompute days=%d err=%v", days, err)
	}
	assertCounts("policy-a", 0, 2, 3, 6)
	assertCounts("policy-b", 0, 1, 1, 1)
	if _, err := closer.CloseDay(ctx, pool, []int32{1}, day); err == nil {
		t.Fatal("expired raw reclose must fail before deleting closed data")
	}
	assertCounts("policy-a", 0, 2, 3, 6)
	if err := retention.Erase(ctx, pool, 1, 2, 0); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"verified_page_sessions", "verified_page_day"} {
		if err := pool.QueryRow(ctx, "select count(*) from "+table+" where site_id=1").Scan(&groups); err != nil || groups != 0 {
			t.Fatalf("erasure %s rows=%d err=%v", table, groups, err)
		}
	}
}
