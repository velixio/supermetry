package retention_test

import (
	"context"
	"testing"
	"time"

	"supermetry/internal/db"
	"supermetry/internal/retention"
	"supermetry/internal/testpg"
)

func TestLogRetention(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into orgs(name,slug) values('Org','org')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into sites(id,org_id,public_id,name,timezone,retention_class,kind,settings) overriding system value
		values(1,1,'applogs00001','Seven','UTC',30,'app','{"app":{"log_retention_days":7}}'),
		      (2,1,'applogs00002','Three','UTC',30,'app','{"app":{"log_retention_days":3}}')`); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	if err := db.EnsureDailyPartitions(ctx, pool, "logs", now.AddDate(0, 0, -12), now.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	dropped, err := retention.Run(ctx, pool, now)
	if err != nil {
		t.Fatal(err)
	}
	// Cutoff = now - 7 - 1 days = 2026-11-10T00:00Z: partitions whose upper
	// bound is at or before it go, the one starting at it stays.
	if !containsPrefix(dropped, "logs_20261109") || !containsPrefix(dropped, "logs_20261106") {
		t.Fatalf("dropped = %v", dropped)
	}
	if containsPrefix(dropped, "logs_20261110") || containsPrefix(dropped, "logs_20261118") {
		t.Fatalf("dropped too much: %v", dropped)
	}
}

func TestEraseRemovesLogs(t *testing.T) {
	pool, now := spansFixture(t, `{}`)
	ctx := context.Background()
	if err := db.EnsureDailyPartitions(ctx, pool, "logs", now.AddDate(0, 0, -1), now.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into sites(id,org_id,public_id,name,timezone,retention_class,kind) overriding system value
		values(2,1,'appspan002ab','Other','UTC',30,'app')`); err != nil {
		t.Fatal(err)
	}
	for _, site := range []int{1, 2} {
		if _, err := pool.Exec(ctx, `insert into logs(site_id,ts,rid,observed_at,severity,body,source) values($1,$2,1,$2,9,'x','otlp')`, site, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := retention.Erase(ctx, pool, 1, 10, 0); err != nil {
		t.Fatal(err)
	}
	var one, two int
	if err := pool.QueryRow(ctx, `select count(*) filter (where site_id=1), count(*) filter (where site_id=2) from logs`).Scan(&one, &two); err != nil || one != 0 || two != 1 {
		t.Fatalf("after erase: site1=%d site2=%d %v", one, two, err)
	}
}
