package appquery_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"supermetry/internal/spanrollup"
)

// TestPerformanceReadsUseIndexes seeds 300,000 spans over three days (rolled
// up hour by hour), then checks the transaction list reads span_hourly by its
// primary key range and the trace lookup uses spans_trace_idx. Run with -v to
// see the plans. The statements mirror the ones in performance.go and trace.go.
func TestPerformanceReadsUseIndexes(t *testing.T) {
	e := perfSetup(t)
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `
insert into spans(site_id, start_ts, duration_us, bucket, trace_id, span_id, segment_id, is_segment, name, op, group_hash, release, environment, platform)
select 7 + (g % 2) * 2, timestamptz '2026-11-15 00:00+00' + g * interval '0.864 seconds',
       1000 + (g % 997) * 100, least(230, floor(ln(1000 + (g % 997) * 100) / ln(1.1)))::smallint,
       decode(lpad(to_hex(g / 3), 32, '0'), 'hex'), decode(lpad(to_hex(g), 16, '0'), 'hex'), decode(lpad(to_hex(g), 16, '0'), 'hex'),
       g % 3 = 0, case when g % 3 = 0 then 'GET /route/' || (g % 20) else '' end, 'http.server', 0, '1.0', 'prod', 'go'
from generate_series(1, 300000) g`); err != nil {
		t.Fatal(err)
	}
	for h := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC); h.Before(time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)); h = h.Add(time.Hour) {
		if err := spanrollup.RecomputeHour(ctx, e.pool, 7, time.UTC, h); err != nil {
			t.Fatal(err)
		}
	}
	// Other projects' rollups give span_hourly a realistic size: 200 sites'
	// worth of rows would be far more, but 400,000 is enough for the planner
	// to prefer the primary key over a sequential scan.
	if _, err := e.pool.Exec(ctx, `
insert into span_hourly(site_id, local_hour, is_segment, name, op, group_hash, release, environment, platform, count, errors, sum_us, buckets, bucket_counts)
select 9, timestamp '2026-10-01' + h * interval '1 hour', true, 'GET /r/' || n, 'http.server', 0, '1.0', 'prod', 'go', 10, 0, 1000, '{10,20}', '{5,5}'
from generate_series(0, 1000) h, generate_series(1, 400) n`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `analyze span_hourly`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `analyze spans`); err != nil {
		t.Fatal(err)
	}
	explain := func(name, query string, args ...any) string {
		rows, err := e.pool.Query(ctx, `explain (analyze, buffers, costs off) `+query, args...)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var plan []string
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, line)
		}
		text := strings.Join(plan, "\n")
		t.Logf("%s:\n%s", name, text)
		return text
	}
	lo, hi := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	var done []time.Time
	for h := lo; h.Before(hi); h = h.Add(time.Hour) {
		done = append(done, h)
	}
	plan := explain("transaction list (rollup)", `
select h.name, h.op, h.group_hash, '', sum(h.count), sum(h.errors), sum(h.sum_us)
from span_hourly h where h.site_id = $1 and h.local_hour >= $2 and h.local_hour < $3 and h.local_hour = any($4::timestamp[]) and h.is_segment
group by 1, 2, 3, 4`, int32(7), lo, hi, done)
	if !strings.Contains(plan, "span_hourly_pkey") {
		t.Errorf("transaction list does not use the primary key:\n%s", plan)
	}
	var tid []byte
	if err := e.pool.QueryRow(ctx, `select trace_id from spans where site_id = 7 and start_ts >= '2026-11-16' limit 1 offset 1000`).Scan(&tid); err != nil {
		t.Fatal(err)
	}
	plan = explain("trace lookup", `select span_id, parent_span_id, is_segment, name, op, description, status, http_status, start_ts, duration_us, release
from spans where site_id = $1 and trace_id = $2 order by start_ts, duration_us desc limit 10001`, int32(7), tid)
	if !strings.Contains(plan, "_site_id_trace_id_idx") {
		t.Errorf("trace lookup does not use the trace index:\n%s", plan)
	}
}
