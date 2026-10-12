package sites

import (
	"context"
	"testing"
	"time"

	"supermetry/internal/db"
	"supermetry/internal/testpg"
)

func TestLastEventAtBackfillsRetainedEventsOnlyOnce(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	if err := db.EnsurePartitions(ctx, pool, now.AddDate(0, 0, -31), now.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
insert into orgs(id,name,slug) overriding system value values(1,'Status','status');
insert into sites(id,org_id,public_id,name,timezone,retention_class) overriding system value values(1,1,'status000001','Status','UTC',30)`,
	); err != nil {
		t.Fatal(err)
	}
	retained := now.Add(-25 * time.Hour)
	if _, err := pool.Exec(ctx, `insert into events(site_id,retention_class,local_day,ts,visitor_id,session_id,kind) values(1,30,$1::timestamptz::date,$1,1,1,0)`, retained); err != nil {
		t.Fatal(err)
	}

	got, err := LastEventAt(ctx, pool, 1, db.Ret30, now)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || !got.Equal(retained) {
		t.Fatalf("backfilled last event=%v, want %s", got, retained)
	}

	newer := now.Add(-time.Hour)
	if _, err := pool.Exec(ctx, `insert into events(site_id,retention_class,local_day,ts,visitor_id,session_id,kind) values(1,30,$1::timestamptz::date,$1,2,2,0)`, newer); err != nil {
		t.Fatal(err)
	}
	got, err = LastEventAt(ctx, pool, 1, db.Ret30, now)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || !got.Equal(retained) {
		t.Fatalf("second lookup rescanned events: got %v, want stored %s", got, retained)
	}
}
