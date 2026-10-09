package profiletombstone

import (
	"context"
	"testing"
	"time"

	"supermetry/internal/db"
	"supermetry/internal/testpg"
)

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

func TestStoreLoadsTombstonesAfterRestart(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var orgID int64
	var siteID int32
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Tombstones','tombstones') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class) values ($1,'tombstones01','Tombstones','UTC',30) returning id`, orgID).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	clock := &fakeClock{now: time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)}
	first := New(pool, clock)
	if err := first.Remember(ctx, siteID, "customer-1"); err != nil {
		t.Fatal(err)
	}

	restarted := New(pool, clock)
	if err := restarted.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if !restarted.Contains(siteID, "customer-1") {
		t.Fatal("persisted tombstone was not loaded")
	}
	if restarted.Contains(siteID, "customer-2") {
		t.Fatal("unrelated identity is tombstoned")
	}
}

func TestStoreExpiresTombstonesAfterSaltWindow(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	clock := &fakeClock{now: time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)}
	store := New(pool, clock)
	store.RememberMemory(7, "customer-1")
	clock.now = clock.now.Add(48*time.Hour + time.Nanosecond)
	if store.Contains(7, "customer-1") {
		t.Fatal("expired tombstone still suppresses identity")
	}
}
