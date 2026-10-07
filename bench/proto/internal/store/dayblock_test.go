package store_test

import (
	"context"
	"testing"
	"time"

	"panel/bench/proto/internal/core"
	"panel/bench/proto/internal/query"
	"panel/bench/proto/internal/store"
	"panel/bench/proto/internal/testdb"
)

func TestDayBlocksEqualDailyAndRefreshAncestors(t *testing.T) {
	ctx := context.Background()
	st := testdb.Open(t)
	if _, err := st.Pool.Exec(ctx, `INSERT INTO sites (id, host, tz) VALUES (1, 'blocks.test', 'America/New_York')`); err != nil {
		t.Fatal(err)
	}
	first := core.ContainingDayBlock(core.CivilDay(2026, 1, 1), 4).Start
	for i := range 16 {
		d := first + core.Day(i)
		if _, err := st.Pool.Exec(ctx, `INSERT INTO rollup_day
  (site_id, day, visitors, visits, pageviews, bounces, duration_ms)
VALUES (1, $1, $2, $3, $4, $5, $6)`, d.Time(), i+1, i+2, i+3, i%3, i*100+7); err != nil {
			t.Fatal(err)
		}
		// Day 3 is a real completed day with no row for this dimension value.
		if i == 3 {
			continue
		}
		if _, err := st.Pool.Exec(ctx, `INSERT INTO rollup_day_dim
  (site_id, dim, day, value, visitors, visits, pageviews, bounces, duration_ms)
VALUES (1, 1, $1, 'x', $2, $3, $4, $5, $6)`, d.Time(), i+1, i+2, i+3, i%3, i*100+7); err != nil {
			t.Fatal(err)
		}
	}

	if err := st.RebuildDayBlocks(ctx, first, first+15, []int32{1}); err != nil {
		t.Fatal(err)
	}
	assertStoredBlocksEqualDaily(t, st, first, first+15)
	var before int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM rollup_block_dim`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := st.RebuildDayBlocks(ctx, first, first+15, []int32{1}); err != nil {
		t.Fatal(err)
	}
	var after int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM rollup_block_dim`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("idempotent rebuild rows = %d, want %d", after, before)
	}

	changed := first + 5
	if _, err := st.Pool.Exec(ctx, `UPDATE rollup_day_dim SET visitors = visitors + 1000, pageviews = pageviews + 2000
WHERE site_id = 1 AND dim = 1 AND day = $1 AND value = 'x'`, changed.Time()); err != nil {
		t.Fatal(err)
	}
	if err := st.RefreshDayBlocks(ctx, []core.SaltKey{{Site: 1, Day: changed}}); err != nil {
		t.Fatal(err)
	}
	assertStoredBlocksEqualDaily(t, st, first, first+15)
}

func assertStoredBlocksEqualDaily(t *testing.T, st *store.Store, first, last core.Day) {
	t.Helper()
	ctx := context.Background()
	var mismatches int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM (
  SELECT b.level, b.block_start, b.value
  FROM rollup_block_dim b
  LEFT JOIN rollup_day_dim d ON d.site_id = b.site_id AND d.dim = b.dim AND d.value = b.value
    AND d.day >= b.block_start AND d.day < b.block_start + (1::int << b.level)
  WHERE b.site_id = 1 AND b.block_start BETWEEN $1 AND $2
  GROUP BY b.level, b.block_start, b.value, b.visitors, b.visits, b.pageviews, b.bounces, b.duration_ms
  HAVING b.visitors <> coalesce(sum(d.visitors), 0)
      OR b.visits <> coalesce(sum(d.visits), 0)
      OR b.pageviews <> coalesce(sum(d.pageviews), 0)
      OR b.bounces <> coalesce(sum(d.bounces), 0)
      OR b.duration_ms <> coalesce(sum(d.duration_ms), 0)
) bad`, first.Time(), last.Time()).Scan(&mismatches); err != nil {
		t.Fatal(err)
	}
	if mismatches != 0 {
		t.Fatalf("stored blocks have %d mismatches against daily sums", mismatches)
	}
	var want core.Metrics
	if err := st.Pool.QueryRow(ctx, `SELECT coalesce(sum(visitors),0), coalesce(sum(visits),0), coalesce(sum(pageviews),0),
coalesce(sum(bounces),0), coalesce(sum(duration_ms),0) FROM rollup_day_dim
WHERE site_id = 1 AND dim = 1 AND day BETWEEN $1 AND $2`, first.Time(), last.Time()).Scan(
		&want.Visitors, &want.Visits, &want.Pageviews, &want.Bounces, &want.DurationMs); err != nil {
		t.Fatal(err)
	}
	for _, noMonthly := range []bool{false, true} {
		eng := &query.Engine{Pool: st.Pool, NoMonthly: noMonthly, Site: func(id int32) *core.Site {
			if id != 1 {
				return nil
			}
			return &core.Site{ID: 1, Host: "blocks.test", TZ: "UTC", Loc: time.UTC}
		}}
		rows, err := eng.Breakdown(ctx, core.DimPath, query.Params{Site: 1, From: first, To: last, Mode: query.ModeRollup, Limit: -1})
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 || rows[0].Value != "x" || rows[0].Metrics != want {
			t.Fatalf("noMonthly=%v block query = %#v, want x: %#v", noMonthly, rows, want)
		}
	}
	var done, rows, maxLevel int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*), max(level) FROM rollup_block_done WHERE site_id = 1`).Scan(&done, &maxLevel); err != nil {
		t.Fatal(err)
	}
	if done != 31 || maxLevel != 4 {
		t.Fatalf("done blocks = %d through level %d, want 31 through level 4", done, maxLevel)
	}
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM rollup_block_dim WHERE site_id = 1`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 15 {
		t.Fatalf("stored dimension blocks = %d, want 15", rows)
	}
}
