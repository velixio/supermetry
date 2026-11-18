package obsquota_test

import (
	"context"
	"testing"
	"time"

	"supermetry/internal/db"
	"supermetry/internal/obsquota"
	"supermetry/internal/testpg"
)

func clock(t *time.Time) func() time.Time { return func() time.Time { return *t } }

func TestTakeGrantsUpToLimit(t *testing.T) {
	now := time.Date(2026, 11, 18, 23, 0, 0, 0, time.UTC)
	tr := obsquota.New(nil, clock(&now))
	if g := tr.Take(1, obsquota.ClassLogs, 600, 1000); g != 600 {
		t.Fatalf("first grant = %d, want 600", g)
	}
	if g := tr.Take(1, obsquota.ClassLogs, 600, 1000); g != 400 {
		t.Fatalf("second grant = %d, want 400", g)
	}
	if g := tr.Take(1, obsquota.ClassLogs, 5, 1000); g != 0 {
		t.Fatalf("over quota grant = %d, want 0", g)
	}
	// Another site and another class have their own counts.
	if g := tr.Take(2, obsquota.ClassLogs, 600, 1000); g != 600 {
		t.Fatalf("other site grant = %d", g)
	}
	if g := tr.Take(1, obsquota.ClassMetrics, 600, 1000); g != 600 {
		t.Fatalf("other class grant = %d", g)
	}
	// Limit 0 is unlimited.
	if g := tr.Take(3, obsquota.ClassLogs, 1_000_000, 0); g != 1_000_000 {
		t.Fatalf("unlimited grant = %d", g)
	}
	// The next UTC day grants again.
	now = now.Add(2 * time.Hour)
	if g := tr.Take(1, obsquota.ClassLogs, 600, 1000); g != 600 {
		t.Fatalf("next day grant = %d, want 600", g)
	}
}

func TestReleaseReturnsUnits(t *testing.T) {
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	tr := obsquota.New(nil, clock(&now))
	tr.Take(1, obsquota.ClassLogs, 800, 1000)
	tr.Release(1, obsquota.ClassLogs, 800)
	if g := tr.Take(1, obsquota.ClassLogs, 1000, 1000); g != 1000 {
		t.Fatalf("grant after release = %d, want 1000", g)
	}
}

func TestFlushAndPrimeAcrossRestart(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into orgs(name,slug) values('Org','org')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into sites(id,org_id,public_id,name,timezone,retention_class,kind) overriding system value
		values(1,1,'appquota0001','App','UTC',30,'app')`); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	tr := obsquota.New(pool, clock(&now))
	if g := tr.Take(1, obsquota.ClassLogs, 1000, 1000); g != 1000 {
		t.Fatalf("grant = %d", g)
	}
	tr.Take(1, obsquota.ClassLogs, 50, 1000) // 50 rejected
	tr.AddBytes(1, obsquota.ClassLogs, 4096)
	if err := tr.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	// A second flush adds nothing.
	if err := tr.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	var acc, rej, bytes int64
	if err := pool.QueryRow(ctx, `select accepted, rejected, bytes from obs_ingest_daily where site_id=1 and day='2026-11-18' and class='logs'`).Scan(&acc, &rej, &bytes); err != nil {
		t.Fatal(err)
	}
	if acc != 1000 || rej != 50 || bytes != 4096 {
		t.Fatalf("row = %d/%d/%d, want 1000/50/4096", acc, rej, bytes)
	}

	// A restarted process primes from the row: nothing is left today.
	tr2 := obsquota.New(pool, clock(&now))
	if err := tr2.Prime(ctx); err != nil {
		t.Fatal(err)
	}
	if g := tr2.Take(1, obsquota.ClassLogs, 10, 1000); g != 0 {
		t.Fatalf("grant after restart = %d, want 0", g)
	}
	if err := tr2.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `select accepted, rejected from obs_ingest_daily where site_id=1 and class='logs'`).Scan(&acc, &rej); err != nil {
		t.Fatal(err)
	}
	if acc != 1000 || rej != 60 {
		t.Fatalf("after restart row = %d/%d, want 1000/60", acc, rej)
	}
	// A site deleted since is skipped, not an error.
	tr2.Take(99, obsquota.ClassMetrics, 5, 0)
	if err := tr2.Flush(ctx); err != nil {
		t.Fatalf("flush for a missing site: %v", err)
	}
}
