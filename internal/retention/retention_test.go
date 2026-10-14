package retention_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/closer"
	"supermetry/internal/db"
	"supermetry/internal/retention"
	"supermetry/internal/testpg"
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

func TestRunDeletesDropStatsOlderThanNinetyDays(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into orgs(name,slug) values('Org','org');
insert into sites(id,org_id,public_id,name,timezone,retention_class) overriding system value values(1,1,'dropstats001','Site','UTC',30);
insert into site_drop_daily(site_id,day,reason,count) values(1,'2026-07-09','privacy_signal',1),(1,'2026-07-10','privacy_signal',2);
insert into site_bot_rule_daily(site_id,day,rule,count) values(1,'2026-07-09','headless',1),(1,'2026-07-10','headless',2)`); err != nil {
		t.Fatal(err)
	}
	if _, err := retention.Run(ctx, pool, time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"site_drop_daily", "site_bot_rule_daily"} {
		var days []time.Time
		rows, err := pool.Query(ctx, "select day from "+table+" order by day")
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var day time.Time
			if err := rows.Scan(&day); err != nil {
				t.Fatal(err)
			}
			days = append(days, day)
		}
		rows.Close()
		if len(days) != 1 || days[0].Format(time.DateOnly) != "2026-07-10" {
			t.Fatalf("%s days = %v, want only cutoff day", table, days)
		}
	}
}

func TestRunDeletesExpiredPixelViews(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var orgID int64
	_ = pool.QueryRow(ctx, `insert into orgs(name,slug) values ('R','r-px') returning id`).Scan(&orgID)
	var siteID int32
	_ = pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class,kind) values ($1,'retpx0000001','R','UTC',30,'pixel') returning id`, orgID).Scan(&siteID)
	var pixelID int64
	_ = pool.QueryRow(ctx, `insert into pixels(site_id,public_id,name) values ($1,'pRETENTION00','r') returning id`, siteID).Scan(&pixelID)
	if _, err := pool.Exec(ctx, `insert into pixel_views_daily values ($1,'2026-08-01','','proxy',1),($1,'2026-10-10','','proxy',2)`, pixelID); err != nil {
		t.Fatal(err)
	}
	if _, err := retention.Run(ctx, pool, time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	var left int64
	_ = pool.QueryRow(ctx, `select coalesce(sum(views),0) from pixel_views_daily`).Scan(&left)
	if left != 2 {
		t.Fatalf("left = %d, want only the recent row (2)", left)
	}
}

func TestRunKeepsPixelViewsForForeverRetention(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var orgID int64
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Forever','forever-px') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	var siteID int32
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class,kind) values ($1,'foreverpx001','Forever','UTC',0,'pixel') returning id`, orgID).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	var pixelID int64
	if err := pool.QueryRow(ctx, `insert into pixels(site_id,public_id,name) values ($1,'pFOREVER0001','forever') returning id`, siteID).Scan(&pixelID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into pixel_views_daily values ($1,'2020-01-01','','proxy',7)`, pixelID); err != nil {
		t.Fatal(err)
	}
	if _, err := retention.Run(ctx, pool, time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	var views int64
	if err := pool.QueryRow(ctx, `select coalesce(sum(views),0) from pixel_views_daily where pixel_id=$1`, pixelID).Scan(&views); err != nil || views != 7 {
		t.Fatalf("forever-retention views = %d, err %v", views, err)
	}
}

func TestRunUsesEachSitesLocalDayForDropStatsRetention(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into orgs(name,slug) values('Org','org');
insert into sites(id,org_id,public_id,name,timezone,retention_class) overriding system value values(1,1,'dropstats001','Site','America/Los_Angeles',30);
insert into site_drop_daily(site_id,day,reason,count) values(1,'2026-07-08','privacy_signal',1),(1,'2026-07-09','privacy_signal',2)`); err != nil {
		t.Fatal(err)
	}
	if _, err := retention.Run(ctx, pool, time.Date(2026, 10, 8, 1, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	var days []time.Time
	rows, err := pool.Query(ctx, `select day from site_drop_daily order by day`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var day time.Time
		if err := rows.Scan(&day); err != nil {
			t.Fatal(err)
		}
		days = append(days, day)
	}
	if len(days) != 1 || days[0].Format(time.DateOnly) != "2026-07-09" {
		t.Fatalf("days = %v, want local cutoff day", days)
	}
}

func TestRunDropsEmptyExpiredReplayPartitions(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`create table replay_sessions_202608 partition of replay_sessions for values from ('2026-08-01') to ('2026-09-01')`,
		`create table replay_sessions_202610 partition of replay_sessions for values from ('2026-10-01') to ('2026-11-01')`,
	} {
		if _, err := pool.Exec(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}

	dropped, err := retention.Run(ctx, pool, time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if !containsPrefix(dropped, "replay_sessions_202608") {
		t.Fatalf("dropped partitions = %v, want expired replay partition", dropped)
	}
	partitions, err := db.ListMonthlyPartitions(ctx, pool, "replay_sessions")
	if err != nil {
		t.Fatal(err)
	}
	if len(partitions) != 1 || partitions[0].Name != "replay_sessions_202610" {
		t.Fatalf("remaining replay partitions = %+v, want current month", partitions)
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
	if _, err := pool.Exec(ctx, `insert into orgs(id,name,slug) overriding system value values (1,'One','one'),(2,'Two','two');
insert into sites(id,org_id,public_id,name,timezone,retention_class) overriding system value values
(1,1,'erase-site01','One','UTC',30),(2,2,'erase-site02','Two','UTC',30);
insert into profiles(site_id,external_id,first_seen_at,last_seen_at) values
(1,'person-one','2026-10-07','2026-10-07'),(2,'person-two','2026-10-07','2026-10-07');
insert into profile_tombstones(site_id,external_hash,erased_at) values
(1,decode(repeat('01',32),'hex'),'2026-10-07'),(2,decode(repeat('02',32),'hex'),'2026-10-07')`); err != nil {
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
		"profiles",
		"profile_tombstones",
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
