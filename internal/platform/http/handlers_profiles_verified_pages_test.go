package platformhttp_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"supermetry/internal/closer"
	"supermetry/internal/db"
	"supermetry/internal/privacy"
)

func TestDeleteProfileErasesVerifiedPageSessionsPreservingUnrelatedRows(t *testing.T) {
	f := newProfileFixture(t, privacy.Anonymous)
	ctx := context.Background()
	// The fixture identifies profile sessions 100 and 101 on October 9.
	// Include a preceding-day row (midnight session), an unrelated session,
	// an older reuse of the number, and a different site's row.
	if _, err := f.pool.Exec(ctx, `insert into verified_page_sessions
 (site_id,day,session_id,visitor_id,path_id,path_policy_id,path_sanitizer_version,pageviews)
 values ($1,'2026-10-09',100,10,1,'policy',1,1),
 ($1,'2026-10-09',101,10,1,'policy',1,1),
 ($1,'2026-10-08',100,10,1,'policy',1,1),
 ($1,'2026-10-09',200,20,1,'policy',1,1),
 ($1,'2026-10-07',100,30,1,'policy',1,1),
 ($2,'2026-10-09',100,10,1,'policy',1,1)`, f.siteID, f.foreignSite); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `insert into verified_page_day
 (site_id,day,path_sanitizer_version,path_policy_id,path_id,visitors,visits,pageviews)
 values ($1,'2026-10-09',1,'policy',0,2,3,3)`, f.siteID); err != nil {
		t.Fatal(err)
	}
	// Anonymous pageviews share the known profile's session. They must
	// disappear with it, while the unrelated anonymous session remains rebuildable.
	if _, err := f.pool.Exec(ctx, `insert into events
 (site_id,retention_class,local_day,ts,visitor_id,session_id,kind,path_id,path_policy_id,path_sanitizer_version)
 values ($1,30,'2026-10-09','2026-10-09 09:59Z',10,100,0,1,'policy',1),
 ($1,30,'2026-10-09','2026-10-09 09:59Z',20,200,0,1,'policy',1)`, f.siteID); err != nil {
		t.Fatal(err)
	}
	response := f.request(t, "owner", http.MethodDelete, fmt.Sprintf("/api/private/profiles/%d", f.profileID))
	if response.Code != http.StatusNoContent {
		t.Fatalf("delete=%d %s", response.Code, response.Body.String())
	}
	var remaining string
	if err := f.pool.QueryRow(ctx, `select string_agg(day::text||':'||session_id::text,',' order by day,session_id) from verified_page_sessions where site_id=$1`, f.siteID).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != "2026-10-07:100,2026-10-09:200" {
		t.Fatalf("remaining=%s", remaining)
	}
	var foreign int
	if err := f.pool.QueryRow(ctx, `select count(*) from verified_page_sessions where site_id=$1`, f.foreignSite).Scan(&foreign); err != nil || foreign != 1 {
		t.Fatalf("foreign=%d err=%v", foreign, err)
	}
	// Preserve the established erasure contract: count-only aggregates are not
	// synchronously adjusted, and the audit record must disclose that fact.
	var pageviews int64
	if err := f.pool.QueryRow(ctx, `select pageviews from verified_page_day where site_id=$1`, f.siteID).Scan(&pageviews); err != nil || pageviews != 3 {
		t.Fatalf("aggregate=%d err=%v", pageviews, err)
	}

	var adjusted bool
	if err := f.pool.QueryRow(ctx, `select (metadata->>'aggregates_adjusted')::boolean from audit_log where action='profile.erased' and target_id=$1`, f.profileID).Scan(&adjusted); err != nil || adjusted {
		t.Fatalf("audit aggregate disclosure=%v err=%v", adjusted, err)
	}
	if _, err := closer.CloseDay(ctx, f.pool, []int32{f.siteID}, time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	var sessions string
	if err := f.pool.QueryRow(ctx, `select string_agg(session_id::text,',' order by session_id) from verified_page_sessions where site_id=$1 and day='2026-10-09'`, f.siteID).Scan(&sessions); err != nil || sessions != "200" {
		t.Fatalf("reclosed sessions=%s err=%v", sessions, err)
	}
}

func TestDeleteProfileErasesFollowingDayVerifiedPageInSameSession(t *testing.T) {
	f := newProfileFixture(t, privacy.Anonymous)
	ctx := context.Background()
	if err := db.EnsurePartitions(ctx, f.pool, time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `insert into events
 (site_id,retention_class,local_day,ts,visitor_id,session_id,kind,profile_id,path_id,path_policy_id,path_sanitizer_version)
 values ($1,30,'2026-10-09','2026-10-09 23:59Z',30,300,1,$2,1,'',0),
 ($1,30,'2026-10-10','2026-10-10 00:05Z',30,300,0,null,1,'policy',1)`, f.siteID, f.profileID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `insert into verified_page_sessions
 (site_id,day,session_id,visitor_id,path_id,path_policy_id,path_sanitizer_version,pageviews)
 values ($1,'2026-10-10',300,30,1,'policy',1,1),
 ($1,'2026-10-10',400,40,1,'policy',1,1)`, f.siteID); err != nil {
		t.Fatal(err)
	}
	response := f.request(t, "owner", http.MethodDelete, fmt.Sprintf("/api/private/profiles/%d", f.profileID))
	if response.Code != http.StatusNoContent {
		t.Fatalf("delete=%d %s", response.Code, response.Body.String())
	}
	var raw int
	if err := f.pool.QueryRow(ctx, `select count(*) from events where site_id=$1 and session_id=300`, f.siteID).Scan(&raw); err != nil || raw != 0 {
		t.Fatalf("erased raw rows=%d err=%v", raw, err)
	}
	var remaining string
	if err := f.pool.QueryRow(ctx, `select string_agg(session_id::text,',' order by session_id) from verified_page_sessions where site_id=$1 and day='2026-10-10'`, f.siteID).Scan(&remaining); err != nil || remaining != "400" {
		t.Fatalf("following-day sessions=%s want=400 err=%v", remaining, err)
	}
}
