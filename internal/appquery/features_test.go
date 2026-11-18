package appquery_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/appquery"
	"supermetry/internal/db"
	"supermetry/internal/testpg"
)

// Installs A..D are visitor ids 1001..1004; the hand-computed dataset of the
// phase 3 plan: purchases by D (10-06), A and C (10-07).
func featureEnv(t *testing.T) (*pgxpool.Pool, *appquery.Querier, appquery.Scope) {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsurePartitions(ctx, pool, d("2026-10-05"), d("2026-10-10")); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
insert into dict(kind,id,value) values (0,1,'/home'),(0,2,'/paywall'),(7,20,'purchase'),(7,21,'$app_open');
insert into orgs(id,name,slug) overriding system value values (1,'Apps','apps');
insert into sites(id,org_id,public_id,name,timezone,retention_class) overriding system value
values (1,1,'featureset01','Features','UTC',30),(2,1,'featureset02','Other','UTC',30);
insert into events (site_id,retention_class,local_day,ts,visitor_id,session_id,kind,name_id,path_id) values
  (1,30,'2026-10-06','2026-10-06 10:00Z',1001,1,0,0,1),
  (1,30,'2026-10-06','2026-10-06 11:00Z',1002,2,0,0,1),
  (1,30,'2026-10-06','2026-10-06 18:00Z',1004,3,0,0,1),
  (1,30,'2026-10-06','2026-10-06 18:02Z',1004,3,1,20,0),
  (1,30,'2026-10-07','2026-10-07 10:00Z',1001,4,0,0,1),
  (1,30,'2026-10-07','2026-10-07 10:02Z',1001,4,1,20,0),
  (1,30,'2026-10-07','2026-10-07 12:00Z',1003,5,0,0,1),
  (1,30,'2026-10-07','2026-10-07 12:02Z',1003,5,1,20,0),
  (1,30,'2026-10-08','2026-10-08 10:00Z',1001,6,0,0,1),
  (1,30,'2026-10-08','2026-10-08 11:00Z',1002,7,0,0,1),
  (1,30,'2026-10-09','2026-10-09 09:00Z',1001,8,0,0,1),
  (2,30,'2026-10-08','2026-10-08 09:00Z',2001,9,1,20,0)`); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	return pool, appquery.New(pool, func() time.Time { return now }), appquery.Scope{SiteID: 1, TZ: time.UTC, RetentionDays: 90}
}

func TestFeatureAdoptionHandComputed(t *testing.T) {
	pool, q, scope := featureEnv(t)
	ctx := context.Background()
	f := appquery.Filter{From: d("2026-10-06"), To: d("2026-10-09")}
	set, err := q.CreateFeatureSet(ctx, scope, "Purchase", []string{"purchase"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := q.FeatureAdoption(ctx, scope, f, set.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Users != 3 || got.ActiveUsers != 4 {
		t.Fatalf("users=%d active=%d, want 3 of 4", got.Users, got.ActiveUsers)
	}
	near(t, "share", got.Share, 0.75)
	var series []int64
	for _, p := range got.Series {
		series = append(series, p.Count)
	}
	if len(series) != 4 || series[0] != 1 || series[1] != 2 || series[2] != 0 || series[3] != 0 {
		t.Fatalf("series = %v, want [1 2 0 0]", series)
	}

	// B opens the app on 10-08: the second set counts all four installs.
	if _, err := pool.Exec(ctx, `insert into events (site_id,retention_class,local_day,ts,visitor_id,session_id,kind,name_id,path_id)
values (1,30,'2026-10-08','2026-10-08 11:01Z',1002,7,1,21,0)`); err != nil {
		t.Fatal(err)
	}
	both, err := q.CreateFeatureSet(ctx, scope, "Engaged", []string{"purchase", "$app_open"})
	if err != nil {
		t.Fatal(err)
	}
	got, err = q.FeatureAdoption(ctx, scope, f, both.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Users != 4 || got.ActiveUsers != 4 {
		t.Fatalf("users=%d active=%d, want 4 of 4", got.Users, got.ActiveUsers)
	}
	near(t, "share", got.Share, 1.0)
}

func TestFeatureSetValidationAndIsolation(t *testing.T) {
	_, q, scope := featureEnv(t)
	ctx := context.Background()
	if _, err := q.CreateFeatureSet(ctx, scope, strings.Repeat("n", 81), []string{"purchase"}); !errors.Is(err, appquery.ErrInvalid) {
		t.Fatalf("81-char name err = %v", err)
	}
	names := make([]string, 21)
	for i := range names {
		names[i] = "e" + strings.Repeat("x", i)
	}
	if _, err := q.CreateFeatureSet(ctx, scope, "too many", names); !errors.Is(err, appquery.ErrInvalid) {
		t.Fatalf("21 names err = %v", err)
	}
	if _, err := q.CreateFeatureSet(ctx, scope, "empty", nil); !errors.Is(err, appquery.ErrInvalid) {
		t.Fatalf("no names err = %v", err)
	}
	other := appquery.Scope{SiteID: 2, TZ: time.UTC}
	foreign, err := q.CreateFeatureSet(ctx, other, "Theirs", []string{"purchase"})
	if err != nil {
		t.Fatal(err)
	}
	f := appquery.Filter{From: d("2026-10-06"), To: d("2026-10-09")}
	if _, err := q.FeatureAdoption(ctx, scope, f, foreign.ID); !errors.Is(err, appquery.ErrNotFound) {
		t.Fatalf("foreign adoption err = %v", err)
	}
	if err := q.DeleteFeatureSet(ctx, scope, foreign.ID); !errors.Is(err, appquery.ErrNotFound) {
		t.Fatalf("foreign delete err = %v", err)
	}
	if _, err := q.CreateFeatureSet(ctx, scope, "Mine", []string{"purchase"}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.CreateFeatureSet(ctx, scope, "Mine", []string{"purchase"}); !errors.Is(err, appquery.ErrDuplicate) {
		t.Fatalf("duplicate err = %v", err)
	}
	list, err := q.ListFeatureSets(ctx, scope)
	if err != nil || len(list) != 1 || list[0].Name != "Mine" {
		t.Fatalf("list = %+v err=%v", list, err)
	}
	if err := q.DeleteFeatureSet(ctx, scope, list[0].ID); err != nil {
		t.Fatal(err)
	}
}
