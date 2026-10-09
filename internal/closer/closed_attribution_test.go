package closer_test

import (
	"context"
	"reflect"
	"testing"

	"supermetry/internal/closer"
	"supermetry/internal/db"
	"supermetry/internal/testpg"
)

func TestCloseCustomPreservesEventAttributionIncludingAbsentValues(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	day := utc(2026, 10, 8, 0, 0)
	if err := db.EnsurePartitions(ctx, pool, day, day.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into sessions(site_id,retention_class,session_id,visitor_id,start,last,local_day,entry_path,exit_path,source_id,utm_source_id,medium_id,campaign_id,pageviews,events,bounce)
 values(1,30,1,1,'2026-10-08 12:00Z','2026-10-08 12:03Z','2026-10-08',1,1,10,11,12,13,1,3,false);
 insert into events(site_id,retention_class,local_day,ts,visitor_id,session_id,kind,name_id,path_id,source_id,utm_source_id,medium_id,campaign_id,utm_content_id,utm_term_id,revenue_micros,revenue_currency)
 values
 (1,30,'2026-10-08','2026-10-08 12:00Z',1,1,0,0,1,10,11,12,13,14,15,0,null),
 (1,30,'2026-10-08','2026-10-08 12:01Z',1,1,1,101,1,0,0,0,0,0,0,1000000,'USD'),
 (1,30,'2026-10-08','2026-10-08 12:02Z',1,1,1,102,1,20,21,22,23,24,25,2000000,'USD'),
 (1,30,'2026-10-08','2026-10-08 12:03Z',1,1,1,103,1,30,0,32,0,0,35,3000000,'USD')`); err != nil {
		t.Fatal(err)
	}
	for _, pass := range []string{"first close", "repeated close"} {
		t.Run(pass, func(t *testing.T) {
			if _, err := closer.CloseDay(ctx, pool, []int32{1}, day); err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct {
				name string
				id   int
				want []int32
			}{
				{"absent after tagged entry", 101, []int32{0, 0, 0, 0, 0, 0}},
				{"tagged differently from entry", 102, []int32{20, 21, 22, 23, 24, 25}},
				{"partial tags preserve individual zeroes", 103, []int32{30, 0, 32, 0, 0, 35}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					var eventValues, sessionValues []int32
					if err := pool.QueryRow(ctx, `select array[event_source_id,event_utm_source_id,event_utm_medium_id,event_utm_campaign_id,event_utm_content_id,event_utm_term_id],array[source_id,utm_source_id,medium_id,campaign_id,utm_content_id,utm_term_id] from custom_closed where name_id=$1`, tc.id).Scan(&eventValues, &sessionValues); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(eventValues, tc.want) {
						t.Fatalf("event attribution=%v want=%v", eventValues, tc.want)
					}
					if !reflect.DeepEqual(sessionValues, []int32{10, 11, 12, 13, 14, 15}) {
						t.Fatalf("session attribution changed=%v", sessionValues)
					}
				})
			}
			var count int
			var revenue int64
			if err := pool.QueryRow(ctx, `select count(*),sum(revenue_micros) from custom_closed`).Scan(&count, &revenue); err != nil {
				t.Fatal(err)
			}
			if count != 3 || revenue != 6000000 {
				t.Fatalf("custom rows=%d revenue=%d", count, revenue)
			}
			var source int32
			var visits int
			if err := pool.QueryRow(ctx, `select source_id,visits from cube_day where site_id=1 and day=$1`, day).Scan(&source, &visits); err != nil {
				t.Fatal(err)
			}
			if source != 10 || visits != 1 {
				t.Fatalf("session cube source=%d visits=%d", source, visits)
			}
		})
	}
}
