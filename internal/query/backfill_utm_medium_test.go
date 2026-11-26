package query_test

import (
	"context"
	"testing"
	"time"

	"supermetry/internal/closer"
	"supermetry/internal/db"
	"supermetry/internal/dict"
	"supermetry/internal/query"
	"supermetry/internal/sites"
	"supermetry/internal/testpg"
)

func TestBackfillUTMMediumRestoresPreMigrationClosedRows(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsurePartitions(ctx, pool, day("2026-10-07"), day("2026-10-09")); err != nil {
		t.Fatal(err)
	}
	// Rows as written before migration 0329: utm_medium_id is 0 everywhere.
	// Session 107 was tagged (utm_source set, derived medium cpc); session 108
	// was untagged organic traffic (derived medium "email" stands in for any
	// referrer-derived value) and must stay out of the UTM medium breakdown.
	if _, err := pool.Exec(ctx, `
insert into dict(kind,id,value) values
 (0,1,'/landing'),(2,10,'Google'),(2,20,'Bing'),(3,11,'cpc'),(3,21,'organic'),(4,12,'spring'),(7,30,'signup');
insert into sessions
 (site_id,retention_class,session_id,visitor_id,start,last,local_day,entry_path,exit_path,
  source_id,utm_source_id,medium_id,campaign_id,pageviews,events,bounce)
values
 (1,30,107,1007,'2026-10-07 12:00Z','2026-10-07 12:03Z','2026-10-07',1,1,10,10,11,12,1,1,false),
 (1,30,108,1008,'2026-10-07 13:00Z','2026-10-07 13:03Z','2026-10-07',1,1,20,0,21,0,1,0,true);
insert into events
 (site_id,retention_class,local_day,ts,visitor_id,session_id,kind,name_id,path_id,
  source_id,utm_source_id,medium_id,campaign_id)
values
 (1,30,'2026-10-07','2026-10-07 12:00Z',1007,107,0,0,1,10,10,11,12),
 (1,30,'2026-10-07','2026-10-07 12:01Z',1007,107,1,30,1,10,10,11,12),
 (1,30,'2026-10-07','2026-10-07 13:00Z',1008,108,0,0,1,20,0,21,0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := closer.CloseDay(ctx, pool, []int32{1}, day("2026-10-07")); err != nil {
		t.Fatal(err)
	}
	// Simulate an event compacted before event_raw_utm_medium_id existed.
	if _, err := pool.Exec(ctx, `update custom_closed set event_raw_utm_medium_id=null`); err != nil {
		t.Fatal(err)
	}

	dictionary := dict.New(pool)
	if err := dictionary.Load(ctx); err != nil {
		t.Fatal(err)
	}
	engine := identifiedEngine(pool, dictionary, registry{site: sites.Site{ID: 1, PublicID: "backfill", TZ: time.UTC, Retention: db.Ret30, ProfilesEnabled: true}})
	mediumRows := func(route query.Route) []map[string]any {
		return mustQuery(t, engine.WithForcedRoute(route), queryRequestRange("2026-10-07", "2026-10-07", []string{"utm_medium"}, nil)).Rows
	}
	for _, route := range []query.Route{query.RouteClosed, query.RouteRollup} {
		if rows := mediumRows(route); len(rows) != 0 {
			t.Fatalf("route %s before backfill rows=%+v, want none", routeName(route), rows)
		}
	}

	counts := func() (tagged, untagged, customRaw int) {
		if err := pool.QueryRow(ctx, `select
 count(*) filter (where utm_source_id<>0 and utm_medium_id=11),
 count(*) filter (where utm_source_id=0 and utm_medium_id=0)
 from sessions_closed`).Scan(&tagged, &untagged); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `select count(*) from custom_closed where event_raw_utm_medium_id is not null`).Scan(&customRaw); err != nil {
			t.Fatal(err)
		}
		return
	}

	first, err := closer.BackfillUTMMedium(ctx, pool, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.Sessions != 1 || first.Custom != 1 || first.CustomEvent != 1 || first.RollupDays != 1 {
		t.Fatalf("first run = %+v, want 1 session, 1 custom, 1 custom event, 1 rollup day", first)
	}
	tagged, untagged, customRaw := counts()
	if tagged != 1 || untagged != 1 || customRaw != 1 {
		t.Fatalf("after first run tagged=%d untagged=%d customRaw=%d, want 1 1 1", tagged, untagged, customRaw)
	}

	second, err := closer.BackfillUTMMedium(ctx, pool, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if second.Sessions != 0 || second.Custom != 0 || second.CustomEvent != 0 {
		t.Fatalf("second run changed rows: %+v", second)
	}
	if t2, u2, c2 := counts(); t2 != tagged || u2 != untagged || c2 != customRaw {
		t.Fatalf("second run altered data: %d %d %d", t2, u2, c2)
	}

	for _, route := range []query.Route{query.RouteClosed, query.RouteRollup} {
		rows := mediumRows(route)
		if len(rows) != 1 || rows[0]["utm_medium"] != "cpc" || rows[0]["visitors"] != float64(1) {
			t.Fatalf("route %s after backfill rows=%+v, want only cpc with 1 visitor", routeName(route), rows)
		}
	}
}
