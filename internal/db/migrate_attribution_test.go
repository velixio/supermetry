package db

import (
	"context"
	"reflect"
	"testing"
	"time"

	"panel/internal/testpg"
)

func TestCustomEventAttributionMigrationPreservesUnknownLegacyValues(t *testing.T) {
	for _, tc := range []struct {
		name    string
		through int
	}{
		{name: "fresh"},
		{name: "upgrade from 27", through: 27},
		{name: "upgrade from 28", through: 28},
	} {
		name := tc.name
		t.Run(name, func(t *testing.T) {
			pool, _ := testpg.New(t)
			ctx := context.Background()
			if tc.through > 0 {
				migrations, err := loadMigrations()
				if err != nil {
					t.Fatal(err)
				}
				conn, err := pool.Acquire(ctx)
				if err != nil {
					t.Fatal(err)
				}
				for _, migration := range migrations {
					if migration.version <= tc.through {
						if err := applyMigration(ctx, conn, migration); err != nil {
							conn.Release()
							t.Fatal(err)
						}
					}
				}
				conn.Release()
			} else if err := Migrate(ctx, pool); err != nil {
				t.Fatal(err)
			}
			day := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
			if err := EnsurePartitions(ctx, pool, day, day.AddDate(0, 0, 1)); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `insert into custom_closed(site_id,day,visitor_id,name_id,path_id,source_id,utm_source_id,medium_id,campaign_id,utm_content_id,utm_term_id)
    values(1,$1,1,1,1,10,11,12,13,14,15)`, day); err != nil {
				t.Fatal(err)
			}
			if err := Migrate(ctx, pool); err != nil {
				t.Fatal(err)
			}
			if err := Migrate(ctx, pool); err != nil {
				t.Fatal(err)
			}
			columns := []string{"event_source_id", "event_utm_source_id", "event_utm_medium_id", "event_utm_campaign_id", "event_utm_content_id", "event_utm_term_id"}
			for _, column := range columns {
				var nullable, dataType string
				var defaultValue *string
				if err := pool.QueryRow(ctx, `select is_nullable,data_type,column_default from information_schema.columns where table_schema=current_schema() and table_name='custom_closed' and column_name=$1`, column).Scan(&nullable, &dataType, &defaultValue); err != nil {
					t.Fatalf("%s: %v", column, err)
				}
				if nullable != "YES" || dataType != "integer" || defaultValue != nil {
					t.Fatalf("%s nullable=%s type=%s default=%v", column, nullable, dataType, defaultValue)
				}
			}
			var unknown int
			var sessionValues []int32
			if err := pool.QueryRow(ctx, `select num_nulls(event_source_id,event_utm_source_id,event_utm_medium_id,event_utm_campaign_id,event_utm_content_id,event_utm_term_id),array[source_id,utm_source_id,medium_id,campaign_id,utm_content_id,utm_term_id] from custom_closed`).Scan(&unknown, &sessionValues); err != nil {
				t.Fatal(err)
			}
			if unknown != 6 || !reflect.DeepEqual(sessionValues, []int32{10, 11, 12, 13, 14, 15}) {
				t.Fatalf("unknown=%d original attribution=%v", unknown, sessionValues)
			}
		})
	}
}
