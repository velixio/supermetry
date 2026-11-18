package appquery_test

import (
	"context"
	"strings"
	"testing"

	"supermetry/internal/appfixture"
)

// TestActiveDayReadsUseIndexes seeds 20,000 installs over 30 days and
// checks the MAU window read is an index scan (the new-install anti-join may hash).
// Run with -v to see the plans.
func TestActiveDayReadsUseIndexes(t *testing.T) {
	p := appfixture.NewProduct(t, "UTC")
	ctx := context.Background()
	if _, err := p.Pool.Exec(ctx, `
insert into app_active_day(site_id,day,visitor_id,events,screens,sessions,engagement_ms)
select 7, date '2026-09-10' + d, v, 0, 1, 1, 0
from generate_series(0, 29) d, generate_series(1, 20000) v
where (v + d) % 3 <> 0`); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Pool.Exec(ctx, `analyze app_active_day`); err != nil {
		t.Fatal(err)
	}
	for name, query := range map[string]string{
		"mau window": `select count(distinct visitor_id) from app_active_day where site_id = 7 and day >= '2026-09-10' and day <= '2026-10-09'`,
		"new install lookup": `select count(*) from app_active_day a where a.site_id = 7 and a.day = '2026-10-01'
and not exists (select 1 from app_active_day p where p.site_id = 7 and p.visitor_id = a.visitor_id and p.day < a.day)`,
	} {
		rows, err := p.Pool.Query(ctx, `explain (analyze, buffers, costs off) `+query)
		if err != nil {
			t.Fatal(err)
		}
		var plan []string
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, line)
		}
		rows.Close()
		text := strings.Join(plan, "\n")
		t.Logf("%s:\n%s", name, text)
		if name == "mau window" && !strings.Contains(text, "Index") {
			t.Fatalf("%s uses no index:\n%s", name, text)
		}
	}
}

// TestOverviewReadUsesTheSiteTimeIndex seeds a second app and a few tens of
// thousands of rows, then checks the overview's raw scan is served by
// the site/time index on the one pruned partition. Run with -v to see the plan.
func TestOverviewReadUsesTheSiteTimeIndex(t *testing.T) {
	f := appfixture.New(t)
	ctx := context.Background()
	if _, err := f.Pool.Exec(ctx, `insert into sites(id,org_id,public_id,name,timezone,retention_class,kind) overriding system value
values(8,1,'appfix000002','App 2','UTC',90,'app')`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Pool.Exec(ctx, `
insert into app_events(site_id,ts,received_at,event_id,kind,level,release,platform,occurrences,install_hash)
select 7 + (g % 2), timestamptz '2026-10-01 00:00+00' + (g * interval '100 seconds'), now(), 'g' || g,
       case when g % 50 = 0 then 'crash' else 'session' end, 'error', '1.' || (g % 3), 'linux', 1, sha256((g % 500)::text::bytea)
from generate_series(1, 30000) g`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Pool.Exec(ctx, `analyze app_events`); err != nil {
		t.Fatal(err)
	}
	rows, err := f.Pool.Query(ctx, `explain (costs off) select coalesce(sum(occurrences),0), count(distinct install_hash) from app_events
where site_id = 7 and ts >= '2026-11-10 00:00+00' and ts < '2026-11-16 00:00+00' and ('' = '' or release = '') and ('' = '' or environment = '') and ('' = '' or platform = '')`)
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
	t.Log("\n" + text)
	if !strings.Contains(text, "Index Scan on app_events_202611_site_id_ts_idx") { // the partition's child of app_events_site_ts_idx
		t.Fatalf("unexpected plan:\n%s", text)
	}
	if strings.Contains(text, "Seq Scan") {
		t.Fatalf("plan uses a sequential scan:\n%s", text)
	}
}

// TestSessionRollupReadUsesThePrimaryKey seeds a year of daily session rows for
// several sites and checks the release list's rollup read is a range scan on
// the primary key. Run with -v to see the plan.
func TestSessionRollupReadUsesThePrimaryKey(t *testing.T) {
	f := appfixture.New(t)
	ctx := context.Background()
	if _, err := f.Pool.Exec(ctx, `
insert into app_session_daily(site_id, day, release, environment, platform, sessions, errored, crashed, abnormal, users)
select 7, d::date, 'r' || r, e, p, 100, 5, 1, 1, 10
from generate_series(date '2026-01-01', date '2026-11-15', interval '1 day') d, generate_series(1, 30) r,
     unnest(array['production','beta']) e, unnest(array['linux','macos','windows']) p`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Pool.Exec(ctx, `analyze app_session_daily`); err != nil {
		t.Fatal(err)
	}
	rows, err := f.Pool.Query(ctx, `explain (costs off) select release, day, sum(sessions), sum(errored), sum(crashed), sum(abnormal)
from app_session_daily
where site_id = 7 and day >= '2026-10-17' and day <= '2026-11-15' and ('' = '' or release = '') and ('' = '' or environment = '') and ('' = '' or platform = '') and release <> ''
group by 1, day`)
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
	t.Log("\n" + text)
	if !strings.Contains(text, "app_session_daily_pkey") || strings.Contains(text, "Seq Scan") {
		t.Fatalf("unexpected plan:\n%s", text)
	}
}
