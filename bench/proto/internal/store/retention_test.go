package store_test

import (
	"context"
	"testing"
	"time"

	"supermetry/bench/proto/internal/core"
	"supermetry/bench/proto/internal/store"
	"supermetry/bench/proto/internal/testdb"
)

func TestDropRawEventPartitionsKeepsCutoffAndClosedTier(t *testing.T) {
	st := testdb.OpenWithOptions(t, store.Options{RawPartition: store.RawPartitionDaily})
	ctx := context.Background()
	if err := st.UpsertSites(ctx, []core.Site{{ID: 1, Host: "a.test", TZ: "UTC", Loc: time.UTC}}); err != nil {
		t.Fatal(err)
	}

	for day := 1; day <= 10; day++ {
		ts := time.Date(2026, 7, day, 12, 0, 0, 0, time.UTC)
		if err := st.EnsureEventPartitions(ctx, ts); err != nil {
			t.Fatal(err)
		}
		if _, err := st.Pool.Exec(ctx, `INSERT INTO events
			(site_id, local_date, ts, visitor_id, session_id, name, path, source, country, browser, os, device)
			VALUES (1, $1, $2, $3, $3, 'pageview', '/', '', '', '', '', '')`, ts, ts, day); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.Pool.Exec(ctx, `INSERT INTO rollup_day VALUES (1, '2026-07-01', 1, 1, 1, 1, 0)`); err != nil {
		t.Fatal(err)
	}

	dropped, err := st.DropRawEventPartitions(ctx, time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(dropped) != 7 || dropped[0] != "events_d20260701" || dropped[6] != "events_d20260707" {
		t.Fatalf("dropped = %v, want daily partitions 01..07", dropped)
	}
	var events, rollups int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM events`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM rollup_day`).Scan(&rollups); err != nil {
		t.Fatal(err)
	}
	if events != 3 || rollups != 1 {
		t.Fatalf("events=%d rollups=%d, want 3 and 1", events, rollups)
	}
}

func TestDropRawEventPartitionsOnlyDropsWholeMonthlyPartitions(t *testing.T) {
	st := testdb.Open(t)
	ctx := context.Background()
	for _, ts := range []time.Time{
		time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC),
	} {
		if err := st.EnsureEventPartitions(ctx, ts); err != nil {
			t.Fatal(err)
		}
	}
	dropped, err := st.DropRawEventPartitions(ctx, time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(dropped) != 1 || dropped[0] != "events_p2026_06" {
		t.Fatalf("dropped = %v, want only June", dropped)
	}
}
