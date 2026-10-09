package query

import (
	"context"
	"testing"
	"time"

	"supermetry/internal/api"
	"supermetry/internal/db"
	"supermetry/internal/testpg"
)

func TestFunnelMatchedEventPlansCompleteWithinBound(t *testing.T) {
	if testing.Short() {
		t.Skip("performance regression test")
	}
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 9)
	if err := db.EnsurePartitions(ctx, pool, from, to.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
insert into sessions_closed(
  site_id,day,visitor_id,session_id,started_at,pageviews,custom,duration_ms,
  engagement_ms,vd_n,flags,seq,seq_engage,seq_scroll,seq_at
)
select 1,day_value,session_id,session_id,started_at,1,1,0,0,1,0,
       array[4]::int[] || array_fill(-999,array[198]) || array[-2]::int[],
       array_fill(0,array[200]),array_fill(0::smallint,array[200]),
       array(select started_at + n * interval '1 second' from generate_series(0,199) n)
from (
  select session_id,
         $1::date + ((session_id-1) % 10)::int as day_value,
         $1::timestamptz + ((session_id-1) % 10) * interval '1 day'
           + session_id * interval '1 minute' as started_at
  from generate_series(1,1000) session_id
) fixture`, from); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
insert into sessions(
  site_id,retention_class,session_id,visitor_id,start,last,local_day,
  entry_path,exit_path,pageviews,events,engagement_ms,bounce
)
select 2,30,session_id,session_id,started_at,started_at + interval '99 seconds',day_value,
       1,1,1,99,0,false
from (
  select session_id,
         $1::date + ((session_id-1) % 10)::int as day_value,
         $1::timestamptz + ((session_id-1) % 10) * interval '1 day'
           + session_id * interval '1 minute' as started_at
  from generate_series(1,1000) session_id
) fixture`, from); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
insert into events(
  site_id,retention_class,local_day,ts,visitor_id,session_id,kind,name_id,path_id
)
select 2,30,s.local_day,s.start + n * interval '1 second',s.visitor_id,s.session_id,
       case when n=0 then 0 else 1 end,
       case when n=99 then 2 else 999 end,
       case when n=0 then 1 else 0 end
from sessions s cross join generate_series(0,99) n
where s.site_id=2`); err != nil {
		t.Fatal(err)
	}

	matchers := []funnelMatcher{{page: true, ids: []int32{1}}, {ids: []int32{2}}}
	tests := []struct {
		name          string
		siteID        int32
		closedThrough time.Time
		window        api.FunnelRequestWindow
	}{
		{name: "closed/session", siteID: 1, closedThrough: to, window: api.FunnelWindowSession},
		{name: "closed/1h", siteID: 1, closedThrough: to, window: api.FunnelWindow1h},
		{name: "closed/24h", siteID: 1, closedThrough: to, window: api.FunnelWindow24h},
		{name: "closed/7d", siteID: 1, closedThrough: to, window: api.FunnelWindow7d},
		{name: "hot/session", siteID: 2, closedThrough: from.AddDate(0, 0, -1), window: api.FunnelWindowSession},
		{name: "hot/1h", siteID: 2, closedThrough: from.AddDate(0, 0, -1), window: api.FunnelWindow1h},
		{name: "hot/24h", siteID: 2, closedThrough: from.AddDate(0, 0, -1), window: api.FunnelWindow24h},
		{name: "hot/7d", siteID: 2, closedThrough: from.AddDate(0, 0, -1), window: api.FunnelWindow7d},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			querySQL, args := buildFunnelSQL(tt.siteID, from, to, tt.closedThrough, matchers, api.FunnelCountBySessions, tt.window)
			queryCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			started := time.Now()
			var first, second int64
			if err := pool.QueryRow(queryCtx, querySQL, args...).Scan(&first, &second); err != nil {
				t.Fatalf("window %s exceeded 5s bound or failed after %s: %v", tt.window, time.Since(started), err)
			}
			if elapsed := time.Since(started); elapsed > 5*time.Second {
				t.Fatalf("window %s took %s, want at most 5s", tt.window, elapsed)
			}
			if first != 1000 || second != 1000 {
				t.Fatalf("window %s counts=(%d,%d), want (1000,1000)", tt.window, first, second)
			}
		})
	}
}
