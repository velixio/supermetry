package db

import (
	"context"
	"testing"
	"time"

	"panel/internal/testpg"
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
