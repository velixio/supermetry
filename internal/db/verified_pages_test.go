package db_test

import (
	"context"
	"testing"
	"time"

	"supermetry/internal/db"
	"supermetry/internal/testpg"
)

func TestVerifiedPageSchemaDefaultsFailClosed(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	day := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsurePartitions(ctx, pool, day, day.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into events(site_id,retention_class,local_day,ts,visitor_id,session_id,kind,path_id) values(1,30,'2026-10-08','2026-10-08 12:00Z',1,1,0,1)`); err != nil {
		t.Fatal(err)
	}
	var version int16
	var policy string
	if err := pool.QueryRow(ctx, `select path_sanitizer_version,path_policy_id from events`).Scan(&version, &policy); err != nil || version != 0 || policy != "" {
		t.Fatalf("legacy default version=%d policy=%q err=%v", version, policy, err)
	}
	for _, table := range []string{"verified_page_sessions", "verified_page_day"} {
		parts, err := db.ListMonthlyPartitions(ctx, pool, table)
		if err != nil || len(parts) != 1 || parts[0].Name != table+"_202610" {
			t.Fatalf("partitions %s=%+v err=%v", table, parts, err)
		}
	}
	for _, tc := range []struct {
		name          string
		version, path int
		policy        string
	}{
		{"unverified", 0, 1, "policy"}, {"future version", 2, 1, "policy"}, {"missing policy", 1, 1, ""}, {"reserved total path", 1, 0, "policy"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := pool.Exec(ctx, `insert into verified_page_sessions(site_id,day,session_id,visitor_id,path_id,path_policy_id,path_sanitizer_version,pageviews) values(1,$1,1,1,$2,$3,$4,1)`, day, tc.path, tc.policy, tc.version)
			if err == nil {
				t.Fatal("invalid closed provenance accepted")
			}
		})
	}
}
