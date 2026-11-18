package profileerasure

import (
	"context"
	"testing"
	"time"

	"supermetry/internal/db"
)

func TestEraseLogsByUser(t *testing.T) {
	f := newAppFixture(t)
	ctx := context.Background()
	day := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	if err := db.EnsureDailyPartitions(ctx, f.pool, "logs", day, day.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	for i, who := range []string{"alice", "alice", "alice", "bob", "bob"} {
		if _, err := f.pool.Exec(ctx, `insert into logs(site_id,ts,rid,observed_at,severity,body,user_hash,source)
values (7,'2026-11-17T10:00:00Z',$1,'2026-11-17T10:00:00Z',9,'x',$2,'otlp')`, i, appHashOf(who)); err != nil {
			t.Fatal(err)
		}
	}
	// Another site's row with the same hash stays.
	if _, err := f.pool.Exec(ctx, `insert into logs(site_id,ts,rid,observed_at,severity,body,user_hash,source)
values (8,'2026-11-17T10:00:00Z',9,'2026-11-17T10:00:00Z',9,'x',$1,'otlp')`, appHashOf("alice")); err != nil {
		t.Fatal(err)
	}
	f.create(t, 7, "alice")
	w := NewAppWorker(f.pool, f.store)
	w.batch = 2 // exercise the batch loop
	if err := w.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	var site7, site8 int
	if err := f.pool.QueryRow(ctx, `select count(*) filter (where site_id=7), count(*) filter (where site_id=8) from logs`).Scan(&site7, &site8); err != nil {
		t.Fatal(err)
	}
	if site7 != 2 || site8 != 1 {
		t.Fatalf("logs left: site7=%d site8=%d, want 2 and 1", site7, site8)
	}
}
