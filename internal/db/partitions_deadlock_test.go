package db

import (
	"context"
	"testing"
	"time"

	"supermetry/internal/testpg"
)

// A reader that touches two partitioned parents must never deadlock with
// partition creation. Before the fix one transaction created partitions across
// many parents and held AccessExclusiveLock on each until commit: it held
// events_r7 while waiting for events_rf, and a reader holding events_rf then
// asking for events_r7 closed the cycle (seen as SQLSTATE 40P01 when a demo
// project was seeded during a rollup backfill).
func TestEnsurePartitionsDoesNotDeadlockWithAReaderAcrossParents(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	reader, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Rollback(context.Background()) }()
	// The reader holds AccessShareLock on events_rf, the last events parent the creator reaches.
	if _, err := reader.Exec(ctx, `select 1 from events_rf limit 0`); err != nil {
		t.Fatal(err)
	}

	created := make(chan error, 1)
	go func() {
		from := time.Date(2031, 3, 1, 0, 0, 0, 0, time.UTC)
		created <- EnsurePartitions(ctx, pool, from, from.AddDate(0, 0, 2))
	}()

	// Wait until the creator is queued for AccessExclusiveLock on events_rf.
	deadline := time.Now().Add(20 * time.Second)
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `select exists (
  select 1 from pg_locks l join pg_class c on c.oid = l.relation
  where not l.granted and l.mode = 'AccessExclusiveLock'
    and c.relname = 'events_rf' and c.relnamespace = current_schema()::regnamespace)`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("partition creator never queued on events_rf")
		}
		time.Sleep(50 * time.Millisecond)
	}

	// The reader now asks for an earlier parent. It must not be blocked by a
	// lock the creator still holds on another parent.
	if _, err := reader.Exec(ctx, `select 1 from events_r7 limit 0`); err != nil {
		t.Fatalf("reader on a second parent: %v", err)
	}
	if err := reader.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-created:
		if err != nil {
			t.Fatalf("EnsurePartitions: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("EnsurePartitions did not finish")
	}
}
