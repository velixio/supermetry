package appquery_test

import (
	"context"
	"testing"
	"time"

	"supermetry/internal/appfixture"
	"supermetry/internal/appquery"
)

func TestDeploysWindowSitesAndOrgs(t *testing.T) {
	f := appfixture.New(t)
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := f.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`insert into orgs(name,slug) values('Other','other')`)
	exec(`insert into sites(id,org_id,public_id,name,timezone,retention_class,kind) overriding system value
values(8,1,'appfix000002','App2','UTC',90,'app'),(9,2,'appfix000003','App3','UTC',90,'app')`)
	rel := func(org int64, version string, sites []int32) int64 {
		var id int64
		if err := f.Pool.QueryRow(ctx, `insert into app_releases(org_id,version,site_ids) values($1,$2,$3) returning id`, org, version, sites).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	wide := rel(1, "1.0.0", []int32{})
	onSeven := rel(1, "2.0.0", []int32{7})
	onEight := rel(1, "3.0.0", []int32{8})
	foreign := rel(2, "1.0.0", []int32{})
	dep := func(release, org int64, env string, sites []int32, at string) {
		exec(`insert into app_deploys(release_id,org_id,site_ids,environment,date_finished) values($1,$2,$3,$4,$5)`, release, org, sites, env, at)
	}
	dep(wide, 1, "in-window-wide", []int32{}, "2026-11-15T20:00:00Z") // 01:30 IST on the 16th
	dep(wide, 1, "outside-late", []int32{}, "2026-11-16T19:00:00Z")   // 00:30 IST on the 17th
	dep(wide, 1, "before", []int32{}, "2026-11-09T18:00:00Z")         // 23:30 IST on the 9th
	dep(onSeven, 1, "own-release", []int32{}, "2026-11-12T10:00:00Z") // falls back to the release's sites
	dep(onEight, 1, "other-site-release", []int32{}, "2026-11-12T10:00:00Z")
	dep(wide, 1, "explicit-eight", []int32{8}, "2026-11-12T11:00:00Z") // names site 8 only
	dep(wide, 1, "explicit-seven", []int32{7}, "2026-11-12T12:00:00Z")
	dep(foreign, 2, "foreign-org", []int32{}, "2026-11-12T10:00:00Z")

	ist, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		t.Fatal(err)
	}
	q := appquery.New(f.Pool, func() time.Time { return f.Now })
	scope := appquery.Scope{SiteID: 7, OrgID: 1, TZ: ist, RetentionDays: 90}
	fl := appquery.Filter{From: d("2026-11-10"), To: d("2026-11-16")}

	envs := func(rows []appquery.Deploy) []string {
		var out []string
		for _, r := range rows {
			out = append(out, r.Environment)
		}
		return out
	}
	rows, err := q.Deploys(ctx, scope, fl, "")
	if err != nil {
		t.Fatal(err)
	}
	// Newest first.
	want := []string{"in-window-wide", "explicit-seven", "own-release"}
	if got := envs(rows); len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("site 7 deploys = %v, want %v", got, want)
	}
	if rows[0].Release != "1.0.0" || !rows[0].At.Equal(time.Date(2026, 11, 15, 20, 0, 0, 0, time.UTC)) {
		t.Fatalf("first row = %+v", rows[0])
	}

	rows, _ = q.Deploys(ctx, appquery.Scope{SiteID: 8, OrgID: 1, TZ: ist, RetentionDays: 90}, fl, "")
	if got := envs(rows); len(got) != 3 || got[0] != "in-window-wide" || got[1] != "explicit-eight" || got[2] != "other-site-release" {
		t.Fatalf("site 8 deploys = %v", got)
	}

	rows, _ = q.Deploys(ctx, scope, fl, "2.0.0")
	if got := envs(rows); len(got) != 1 || got[0] != "own-release" {
		t.Fatalf("release filter = %v", got)
	}
	rows, _ = q.Deploys(ctx, scope, appquery.Filter{From: fl.From, To: fl.To, Environment: "explicit-seven"}, "")
	if got := envs(rows); len(got) != 1 {
		t.Fatalf("environment filter = %v", got)
	}

	// The other org's deploy never shows, even for a site id it does not own.
	rows, _ = q.Deploys(ctx, appquery.Scope{SiteID: 7, OrgID: 2, TZ: ist, RetentionDays: 90}, fl, "")
	if got := envs(rows); len(got) != 1 || got[0] != "foreign-org" {
		t.Fatalf("org 2 = %v", got)
	}
}

func TestDeploysEmptyIsNotNil(t *testing.T) {
	f := appfixture.New(t)
	q := appquery.New(f.Pool, func() time.Time { return f.Now })
	rows, err := q.Deploys(context.Background(), appquery.Scope{SiteID: f.SiteID, OrgID: f.OrgID, TZ: f.Loc}, appquery.Filter{From: d("2026-11-10"), To: d("2026-11-16")}, "")
	if err != nil || rows == nil || len(rows) != 0 {
		t.Fatalf("rows = %v err = %v", rows, err)
	}
	if _, err := q.Deploys(context.Background(), appquery.Scope{SiteID: f.SiteID, OrgID: f.OrgID}, appquery.Filter{}, ""); err == nil {
		t.Fatal("zero range accepted")
	}
}
