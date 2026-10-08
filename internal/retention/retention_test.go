package retention_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"panel/internal/closer"
	"panel/internal/db"
	"panel/internal/retention"
	"panel/internal/testpg"
)

func TestRunDropsOnlyExpiredPerClass(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 10, 7, 3, 0, 0, 0, time.UTC)
	if err := db.EnsurePartitions(ctx, pool, now.AddDate(0, 0, -40), now.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}

	dropped, err := retention.Run(ctx, pool, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, partition := range dropped {
		if strings.HasPrefix(partition, "events_rf_") ||
			strings.HasPrefix(partition, "events_r90_") ||
			strings.HasPrefix(partition, "events_r365_") ||
			strings.HasPrefix(partition, "sessions_rf_") ||
			strings.HasPrefix(partition, "sessions_r90_") ||
			strings.HasPrefix(partition, "sessions_r365_") {
			t.Errorf("dropped unexpired partition %s", partition)
		}
	}
	for _, table := range []string{"events", "sessions"} {
		partitions, err := db.ListPartitions(ctx, pool, table)
		if err != nil {
			t.Fatal(err)
		}
		for _, partition := range partitions {
			cutoff := now.AddDate(0, 0, -int(partition.Class)-1)
			if partition.Class != db.RetForever && partition.To.Before(cutoff) {
				t.Errorf("kept expired partition %s", partition.Name)
			}
		}
	}
	for _, prefix := range []string{"events_r7_", "events_r30_", "sessions_r7_", "sessions_r30_"} {
		if !containsPrefix(dropped, prefix) {
			t.Errorf("expected a %s partition drop: %v", prefix, dropped)
		}
	}
}

func TestEraseRemovesSiteRowsEverywhere(t *testing.T) {
	pool, _ := testpg.New(t)
	loadFixture(t, pool)

	if err := retention.Erase(context.Background(), pool, 1, 1, 0); err != nil {
		t.Fatal(err)
	}

	for _, table := range siteTables() {
		var erased, preserved int
		if err := pool.QueryRow(context.Background(), "select count(*) from "+table+" where site_id=1").Scan(&erased); err != nil {
			t.Fatalf("count erased rows in %s: %v", table, err)
		}
		if err := pool.QueryRow(context.Background(), "select count(*) from "+table+" where site_id=2").Scan(&preserved); err != nil {
			t.Fatalf("count preserved rows in %s: %v", table, err)
		}
		if erased != 0 {
			t.Errorf("%s still has %d erased-site rows", table, erased)
		}
		if preserved == 0 {
			t.Errorf("%s lost the other site's rows", table)
		}
	}
}

func containsPrefix(values []string, prefix string) bool {
	for _, value := range values {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func loadFixture(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)
	if err := db.EnsurePartitions(ctx, pool, day, day.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}

	if _, err := pool.Exec(ctx, `
insert into sessions
  (site_id, retention_class, session_id, visitor_id, start, last, local_day,
   entry_path, exit_path, source_id, country, browser_id, os_id, device,
   pageviews, events, engagement_ms, bounce)
values
  (1, 30, 10, 100, '2026-10-07 10:00Z', '2026-10-07 10:01Z', '2026-10-07', 1, 1, 10, 'IN', 20, 30, 1, 1, 1, 1000, false),
  (2, 30, 20, 200, '2026-10-07 11:00Z', '2026-10-07 11:01Z', '2026-10-07', 2, 2, 11, 'NP', 21, 31, 2, 1, 1, 2000, false);

insert into events
  (site_id, retention_class, local_day, ts, visitor_id, session_id, kind,
   name_id, path_id, source_id, country, browser_id, os_id, device, props)
values
  (1, 30, '2026-10-07', '2026-10-07 10:00Z', 100, 10, 0, 0, 1, 10, 'IN', 20, 30, 1, null),
  (1, 30, '2026-10-07', '2026-10-07 10:01Z', 100, 10, 1, 101, 1, 10, 'IN', 20, 30, 1, '{"plan":"pro"}'),
  (2, 30, '2026-10-07', '2026-10-07 11:00Z', 200, 20, 0, 0, 2, 11, 'NP', 21, 31, 2, null),
  (2, 30, '2026-10-07', '2026-10-07 11:01Z', 200, 20, 1, 102, 2, 11, 'NP', 21, 31, 2, '{"plan":"free"}');

insert into dirty_day (site_id, day, reason) values
  (1, '2026-10-07', 'test'), (2, '2026-10-07', 'test');
insert into heatmap_cells(site_id,day,path_id,device,gx,gy,clicks) values
  (1,'2026-10-07',1,0,1,1,2), (2,'2026-10-07',2,0,2,2,3)`); err != nil {
		t.Fatal(err)
	}

	if _, err := closer.CloseDay(ctx, pool, []int32{1, 2}, day); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
insert into rollup_month_dim
  (site_id, month, dim, value_id, visitors, visits, pageviews, bounces,
   duration_ms, engagement_ms, scroll_sum, scroll_n)
select site_id, '2026-10-01', dim, value_id, visitors, visits, pageviews, bounces,
       duration_ms, engagement_ms, scroll_sum, scroll_n
from rollup_day_dim where day = '2026-10-07';
insert into rollup_month_done (site_id, month) values
  (1, '2026-10-01'), (2, '2026-10-01')
on conflict do nothing;
insert into rollup_block_dim
  (site_id, k, start_day, dim, value_id, visitors, visits, pageviews, bounces,
   duration_ms, engagement_ms, scroll_sum, scroll_n)
select site_id, 1, '2026-10-06', dim, value_id, visitors, visits, pageviews, bounces,
       duration_ms, engagement_ms, scroll_sum, scroll_n
from rollup_day_dim where day = '2026-10-07';
insert into rollup_block_done (site_id, k, start_day) values
  (1, 1, '2026-10-06'), (2, 1, '2026-10-06')
on conflict do nothing;
insert into dirty_day (site_id, day, reason) values
  (1, '2026-10-07', 'test'), (2, '2026-10-07', 'test')
on conflict do nothing`); err != nil {
		t.Fatal(err)
	}
}

func siteTables() []string {
	return []string{
		"heatmap_cells",
		"events",
		"sessions",
		"sessions_closed",
		"custom_closed",
		"cube_day",
		"visitor_day",
		"rollup_day",
		"rollup_day_dim",
		"rollup_month_dim",
		"rollup_month_done",
		"rollup_block_dim",
		"rollup_block_done",
		"day_close",
		"dirty_day",
	}
}
