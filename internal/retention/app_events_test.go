package retention_test

import (
	"context"
	"testing"
	"time"

	"supermetry/internal/db"
	"supermetry/internal/retention"
	"supermetry/internal/testpg"
)

func TestRunDropsExpiredAppEventPartitions(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into orgs(name,slug) values('Org','org');
insert into sites(id,org_id,public_id,name,timezone,retention_class,kind) overriding system value values(1,1,'appret001abc','App','UTC',30,'app')`); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 7, 3, 0, 0, 0, time.UTC)
	if err := db.EnsureAppEventPartitions(ctx, pool, now.AddDate(0, -3, 0), now.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	dropped, err := retention.Run(ctx, pool, now)
	if err != nil {
		t.Fatal(err)
	}
	if !containsPrefix(dropped, "app_events_202607") || !containsPrefix(dropped, "app_events_202608") {
		t.Fatalf("expected July and August app_events partitions dropped, got %v", dropped)
	}
	if containsPrefix(dropped, "app_events_202609") || containsPrefix(dropped, "app_events_202610") {
		t.Fatalf("dropped an unexpired app_events partition: %v", dropped)
	}
}

func TestRunKeepsAppEventsWhenASiteKeepsForever(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into orgs(name,slug) values('Org','org');
insert into sites(id,org_id,public_id,name,timezone,retention_class,kind) overriding system value values(1,1,'appret002abc','App','UTC',0,'app')`); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 7, 3, 0, 0, 0, time.UTC)
	if err := db.EnsureAppEventPartitions(ctx, pool, now.AddDate(0, -3, 0), now); err != nil {
		t.Fatal(err)
	}
	dropped, err := retention.Run(ctx, pool, now)
	if err != nil {
		t.Fatal(err)
	}
	if containsPrefix(dropped, "app_events_") {
		t.Fatalf("forever retention must keep app_events: %v", dropped)
	}
}
