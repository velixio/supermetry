package db_test

import (
	"context"
	"testing"
	"time"

	"panel/internal/db"
	"panel/internal/testpg"
)

func TestClosedSchemaHasExpectedColumns(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"events":           {"region_id", "city_id"},
		"sessions":         {"region_id", "city_id"},
		"sessions_closed":  {"site_id", "day", "visitor_id", "session_id", "region_id", "city_id", "flags", "vd_n", "seq", "seq_engage", "seq_scroll", "engagement_ms"},
		"custom_closed":    {"region_id", "city_id"},
		"visitor_day":      {"site_id", "day", "country", "browser_id", "os_id", "device", "visitors", "visits", "pageviews", "bounces", "duration_ms", "engagement_ms"},
		"rollup_block_dim": {"site_id", "k", "start_day", "dim", "value_id", "visitors"},
	}
	for table, columns := range want {
		for _, column := range columns {
			var ok bool
			if err := pool.QueryRow(ctx, `
select exists(
    select 1
    from information_schema.columns
    where table_schema = current_schema()
      and table_name = $1
      and column_name = $2
)`, table, column).Scan(&ok); err != nil {
				t.Fatal(err)
			}
			if !ok {
				t.Errorf("%s.%s missing", table, column)
			}
		}
	}
}

func TestClosedSchemaPartitionsCoverMonths(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 1, 31, 12, 0, 0, 0, time.UTC)
	to := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	if err := db.EnsurePartitions(ctx, pool, from, to); err != nil {
		t.Fatal(err)
	}

	for _, table := range []string{
		"sessions_closed",
		"custom_closed",
		"cube_day",
		"visitor_day",
		"rollup_day",
		"rollup_day_dim",
	} {
		var count int
		if err := pool.QueryRow(ctx, `
select count(*)
from pg_inherits
where inhparent = to_regclass($1)`, table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 2 {
			t.Errorf("%s has %d monthly partitions, want 2", table, count)
		}
	}
}
