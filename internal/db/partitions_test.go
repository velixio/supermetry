package db

import (
	"context"
	"testing"
	"time"

	"supermetry/internal/testpg"
)

func TestEnsurePartitionsCreatesPerClassAndDay(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 3)
	if err := EnsurePartitions(ctx, pool, from, to); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePartitions(ctx, pool, from, to); err != nil {
		t.Fatal("idempotent:", err)
	}
	ps, err := ListPartitions(ctx, pool, "events")
	if err != nil {
		t.Fatal(err)
	}
	daily := 0
	monthly := 0
	for _, p := range ps {
		if p.Class == RetForever {
			monthly++
		} else {
			daily++
		}
	}
	if daily != 4*3 || monthly != 1 {
		t.Fatalf("daily=%d monthly=%d want 12/1", daily, monthly)
	}

	// A row routes by retention class and timestamp.
	_, err = pool.Exec(ctx, `insert into events(site_id, retention_class, ts, visitor_id, session_id, kind) values (1, 30, '2026-10-02T05:00:00Z', 1, 1, 0)`)
	if err != nil {
		t.Fatal(err)
	}
}

func TestEnsurePartitionsCreatesReplayMonthlyPartitions(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	if err := EnsurePartitions(ctx, pool, from, to); err != nil {
		t.Fatal(err)
	}

	partitions, err := ListMonthlyPartitions(ctx, pool, "replay_sessions")
	if err != nil {
		t.Fatal(err)
	}
	if len(partitions) != 3 {
		t.Fatalf("replay partitions = %d, want 3", len(partitions))
	}
	if _, err := pool.Exec(ctx, `insert into replay_sessions
(site_id, session_id, started_at, last_seen_at, day, blob_key)
values (1, 7, '2026-10-08T12:00:00Z', '2026-10-08T12:00:00Z', '2026-10-08', 'replay/1/2026-10-08/7.gz')`); err != nil {
		t.Fatal(err)
	}
}

func TestParsePartitionTimeAcceptsMinuteOffsets(t *testing.T) {
	for _, value := range []string{
		"2026-10-01 05:30:00+05:30",
		"2026-10-01 05:45:00+05:45",
	} {
		got, err := parsePartitionTime(value)
		if err != nil {
			t.Fatalf("parsePartitionTime(%q): %v", value, err)
		}
		if want := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC); !got.Equal(want) {
			t.Fatalf("parsePartitionTime(%q) = %s, want %s", value, got, want)
		}
	}
}
