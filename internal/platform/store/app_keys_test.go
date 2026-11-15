package store_test

import (
	"context"
	"testing"
	"time"

	"supermetry/internal/db"
	"supermetry/internal/platform/store"
	"supermetry/internal/testpg"
)

func TestRotateAppKeyGrace(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var orgID int64
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('K','keys-org') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	var siteID int32
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,kind,timezone,allowed_hosts,retention_class) values ($1,'keyssite0001','K','app','UTC','{}',90) returning id`, orgID).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	s := &store.Store{Pool: pool}
	h := func(b byte) [32]byte { var x [32]byte; x[0] = b; return x }
	expiry := func(b byte) *time.Time {
		var e *time.Time
		hb := h(b)
		if err := pool.QueryRow(ctx, `select expires_at from app_ingest_keys where hash=$1`, hb[:]).Scan(&e); err != nil {
			t.Fatalf("key %d: %v", b, err)
		}
		return e
	}
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	week := 7 * 24 * time.Hour

	prev, err := s.RotateAppKey(ctx, siteID, nil, "aaaaaaaa", h(1), t0)
	if err != nil || prev != nil {
		t.Fatalf("first rotate prev=%v err=%v", prev, err)
	}
	t1 := t0.Add(time.Hour)
	prev, err = s.RotateAppKey(ctx, siteID, nil, "bbbbbbbb", h(2), t1)
	if err != nil || prev == nil || !prev.Equal(t1.Add(week)) {
		t.Fatalf("second rotate prev=%v err=%v", prev, err)
	}
	if e := expiry(1); e == nil || !e.Equal(t1.Add(week)) {
		t.Fatalf("first expiry=%v", e)
	}
	t2 := t0.Add(2 * time.Hour)
	if _, err = s.RotateAppKey(ctx, siteID, nil, "cccccccc", h(3), t2); err != nil {
		t.Fatal(err)
	}
	if e := expiry(1); e == nil || !e.Equal(t1.Add(week)) {
		t.Fatalf("first expiry changed: %v", e)
	}
	if e := expiry(2); e == nil || !e.Equal(t2.Add(week)) {
		t.Fatalf("second expiry=%v", e)
	}
	if e := expiry(3); e != nil {
		t.Fatalf("current key expiry=%v", e)
	}
	if _, err = s.RotateAppKey(ctx, siteID, nil, "dddddddd", h(4), t1.Add(week+time.Second)); err != nil {
		t.Fatal(err)
	}
	var n int
	hOne := h(1)
	if err := pool.QueryRow(ctx, `select count(*) from app_ingest_keys where hash=$1`, hOne[:]).Scan(&n); err != nil || n != 0 {
		t.Fatalf("expired key rows=%d err=%v", n, err)
	}
	// ListAppKeys/ActiveAppKeys use now(); all remaining test keys expire in 2026-01, so only the current key is listed.
	list, err := s.ListAppKeys(ctx, siteID)
	if err != nil || len(list) != 1 || list[0].Prefix != "dddddddd" {
		t.Fatalf("list=%+v err=%v", list, err)
	}
	active, err := s.ActiveAppKeys(ctx)
	if err != nil || len(active) != 1 || active[0].SiteID != siteID || active[0].Hash != h(4) {
		t.Fatalf("active=%+v err=%v", active, err)
	}
}
