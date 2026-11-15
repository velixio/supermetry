package appquery_test

import (
	"context"
	"strings"
	"testing"

	"supermetry/internal/appfixture"
)

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
