package retention_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/db"
	"supermetry/internal/retention"
	"supermetry/internal/testpg"
)

func spansFixture(t *testing.T, settings string) (*pgxpool.Pool, time.Time) {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into orgs(name,slug) values('Org','org')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into sites(id,org_id,public_id,name,timezone,retention_class,kind,settings) overriding system value
		values(1,1,'appspan001ab','App','UTC',30,'app',$1::jsonb)`, settings); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 18, 3, 0, 0, 0, time.UTC)
	if err := db.EnsureDailyPartitions(ctx, pool, "spans", now.AddDate(0, 0, -20), now.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	return pool, now
}

func TestRunDropsExpiredSpanPartitions(t *testing.T) {
	pool, now := spansFixture(t, `{}`)
	dropped, err := retention.Run(context.Background(), pool, now)
	if err != nil {
		t.Fatal(err)
	}
	// 14 days plus one day of slack: the cutoff is 2026-11-03 03:00, so
	// 2026-11-02 is the newest dropped day and 2026-11-03 stays.
	if !containsPrefix(dropped, "spans_20261102") || !containsPrefix(dropped, "spans_20261029") {
		t.Fatalf("expected old span days dropped, got %v", dropped)
	}
	if containsPrefix(dropped, "spans_20261103") || containsPrefix(dropped, "spans_20261118") {
		t.Fatalf("dropped an unexpired span day: %v", dropped)
	}
}

func TestRunShortensSpanRetentionWhenEveryProjectAsks(t *testing.T) {
	pool, now := spansFixture(t, `{"app":{"span_retention_days":3}}`)
	dropped, err := retention.Run(context.Background(), pool, now)
	if err != nil {
		t.Fatal(err)
	}
	if !containsPrefix(dropped, "spans_20261113") || containsPrefix(dropped, "spans_20261114") {
		t.Fatalf("3 + 1 days: want 11-13 dropped and 11-14 kept, got %v", dropped)
	}
}

func TestEraseRemovesSpans(t *testing.T) {
	pool, now := spansFixture(t, `{}`)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `insert into sites(id,org_id,public_id,name,timezone,retention_class,kind) overriding system value
		values(2,1,'appspan002ab','Other','UTC',30,'app')`); err != nil {
		t.Fatal(err)
	}
	for _, site := range []int{1, 2} {
		if _, err := pool.Exec(ctx, `insert into spans(site_id,start_ts,duration_us,bucket,trace_id,span_id,segment_id,is_segment,name)
			values($1,$2,10,1,decode(repeat('01',16),'hex'),decode(repeat('02',8),'hex'),decode(repeat('02',8),'hex'),true,'t')`, site, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := retention.Erase(ctx, pool, 1, 10, 0); err != nil {
		t.Fatal(err)
	}
	var one, two int
	if err := pool.QueryRow(ctx, `select count(*) filter (where site_id=1), count(*) filter (where site_id=2) from spans`).Scan(&one, &two); err != nil || one != 0 || two != 1 {
		t.Fatalf("after erase: site1=%d site2=%d %v", one, two, err)
	}
}
