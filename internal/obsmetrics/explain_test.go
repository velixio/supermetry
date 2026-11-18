package obsmetrics

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// TestExplainHourlyQuery builds a 30-day hourly dataset with 1,000 series of
// one metric name and prints the EXPLAIN of the grouped hourly query. By
// default the other names are small (the query then reads most of the table
// and a sequential scan is the right plan). With OBSMETRICS_EXPLAIN_FULL=1 the
// other names also hold 30 days (4,000 series, about 3.6 M rows in all), so the
// name is a quarter of the table and the plan must use the primary key index
// of metric_hourly.
func TestExplainHourlyQuery(t *testing.T) {
	full := os.Getenv("OBSMETRICS_EXPLAIN_FULL") == "1"
	pool := rollFixture(t)
	ctx := context.Background()
	start := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 30)
	if _, err := pool.Exec(ctx, `create table if not exists metric_hourly_202610 partition of metric_hourly for values from ('2026-10-01') to ('2026-11-01')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `create table if not exists metric_hourly_202611 partition of metric_hourly for values from ('2026-11-01') to ('2026-12-01')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
insert into metric_series(site_id,name,kind,attrs,attrs_hash,first_seen,last_seen)
select 7, case when g <= 1000 then 'http.requests' else 'other.' || (g % 50) end, 'counter',
       jsonb_build_object('service', 'svc' || g), sha256(convert_to(g::text, 'UTF8')), now(), now()
from generate_series(1, $1::int) g`, map[bool]int{false: 2000, true: 5000}[full]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
insert into metric_hourly(site_id,series_id,hour,samples,value_sum,value_min,value_max,value_last)
select 7, s.id, h, 60, 1, 0, 1, 1
from metric_series s,
     generate_series($1::timestamp, $2::timestamp - interval '1 hour', interval '1 hour') h
where s.site_id = 7 and (s.name = 'http.requests' or $3::boolean or h >= $2::timestamp - interval '48 hours')`, start, end, full); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `analyze metric_hourly; analyze metric_series`); err != nil {
		t.Fatal(err)
	}
	var ids []int64
	var idx []int32
	rows, err := pool.Query(ctx, `select id from metric_series where site_id = 7 and name = 'http.requests' order by id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
		idx = append(idx, int32(len(ids)-1))
	}
	rows.Close()
	if len(ids) != 1000 {
		t.Fatalf("series = %d", len(ids))
	}
	explain := `explain (analyze, buffers, costs off, timing off) ` + fmt.Sprintf(hourlyPartials, `h.hour`)
	plan, err := pool.Query(ctx, explain, ids, idx, int32(7), start, end)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for plan.Next() {
		var l string
		if err := plan.Scan(&l); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, l)
	}
	plan.Close()
	text := strings.Join(lines, "\n")
	var shown []string
	for _, l := range lines {
		if len(l) > 170 {
			l = l[:170] + " ..."
		}
		shown = append(shown, l)
	}
	t.Log("\n" + strings.Join(shown, "\n"))
	if full && !strings.Contains(text, "metric_hourly_") || full && !strings.Contains(text, "Index") {
		t.Fatalf("plan does not use an index on metric_hourly partitions:\n%s", strings.Join(shown, "\n"))
	}
}
