package alerts

import (
	"context"
	"math"
	"testing"
	"time"

	"supermetry/internal/appfixture"
	"supermetry/internal/approllup"
)

func fixtureDay(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse(time.DateOnly, s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestAppStoreReadsCandidatesFromTheFixture(t *testing.T) {
	f := appfixture.New(t)
	ctx := context.Background()
	for _, day := range []string{"2026-11-14", "2026-11-15"} {
		if err := approllup.RecomputeDay(ctx, f.Pool, f.SiteID, fixtureDay(t, day)); err != nil {
			t.Fatal(err)
		}
	}
	store := NewAppStore(f.Pool)
	at := func(day, clock string) time.Time {
		v, err := time.Parse(time.RFC3339, day+"T"+clock+"Z")
		if err != nil {
			t.Fatal(err)
		}
		return v
	}

	site, err := store.Site(ctx, f.SiteID)
	if err != nil || site.Name != "App" || site.OrgSlug != "org" || site.SiteSlug == "" {
		t.Fatalf("Site() = %+v, %v", site, err)
	}

	issues, err := store.NewIssues(ctx, f.SiteID, at("2026-11-14", "00:00:00"), nil, 10)
	if err != nil || len(issues) != 2 {
		t.Fatalf("NewIssues() = %+v, %v", issues, err)
	}
	crashes, err := store.NewIssues(ctx, f.SiteID, at("2026-11-14", "00:00:00"), []string{"crash"}, 10)
	if err != nil || len(crashes) != 1 || crashes[0].ID != f.Issues["I3"] || crashes[0].Release != "1.10.0" || crashes[0].Events != 1 {
		t.Fatalf("NewIssues(crash) = %+v, %v", crashes, err)
	}

	if _, err := f.Pool.Exec(ctx, `update app_issues set regressed_at=$2 where id=$1`, f.Issues["I1"], at("2026-11-15", "09:30:00")); err != nil {
		t.Fatal(err)
	}
	regressed, err := store.RegressedIssues(ctx, f.SiteID, at("2026-11-15", "00:00:00"), 10)
	if err != nil || len(regressed) != 1 || regressed[0].ID != f.Issues["I1"] || !regressed[0].RegressedAt.Equal(at("2026-11-15", "09:30:00")) {
		t.Fatalf("RegressedIssues() = %+v, %v", regressed, err)
	}

	// Last 24 h before 11-15 10:00 UTC: release 1.10.0 only, installs A B C, crashed A and C;
	// sessions c2 and b2 (their pings are inside), the crash sessions have no ping inside.
	release, err := store.LatestRelease(ctx, f.SiteID, at("2026-11-14", "10:00:00"), 3)
	if err != nil || release == nil || release.Release != "1.10.0" || release.Installs != 3 {
		t.Fatalf("LatestRelease() = %+v, %v", release, err)
	}
	if release.CrashFreeUsers == nil || math.Abs(*release.CrashFreeUsers-1.0/3) > 1e-9 || release.CrashFreeSessions == nil || *release.CrashFreeSessions != 1 {
		t.Fatalf("crash-free = %v %v", release.CrashFreeUsers, release.CrashFreeSessions)
	}
	if none, err := store.LatestRelease(ctx, f.SiteID, at("2026-11-14", "10:00:00"), 4); err != nil || none != nil {
		t.Fatalf("LatestRelease(min 4) = %+v, %v", none, err)
	}
	// The wider window sees 1.9.0 as well, and 1.10.0 is still the newest.
	wide, err := store.LatestRelease(ctx, f.SiteID, at("2026-11-13", "00:00:00"), 2)
	if err != nil || wide == nil || wide.Release != "1.10.0" {
		t.Fatalf("LatestRelease(wide) = %+v, %v", wide, err)
	}

	// I2 had 3+1 events in the 08:00 hour on 11-15; add history: 2 yesterday, 3 four days ago, 100 eight days ago (outside).
	for hour, events := range map[string]int{"2026-11-14T08:00:00Z": 2, "2026-11-11T08:00:00Z": 3, "2026-11-07T08:00:00Z": 100} {
		h, _ := time.Parse(time.RFC3339, hour)
		if _, err := f.Pool.Exec(ctx, `insert into app_issue_hourly(site_id,issue_id,hour,events) values ($1,$2,$3,$4)`, f.SiteID, f.Issues["I2"], h, events); err != nil {
			t.Fatal(err)
		}
	}
	hours, err := store.IssueHours(ctx, f.SiteID, at("2026-11-15", "08:00:00"), 4, 7)
	if err != nil || len(hours) != 1 {
		t.Fatalf("IssueHours() = %+v, %v", hours, err)
	}
	if h := hours[0]; h.ID != f.Issues["I2"] || h.Events != 4 || h.BaselineSum != 5 || h.Title == "" || h.Kind != "error" {
		t.Fatalf("IssueHours()[0] = %+v", h)
	}
}

func TestAppStoreDedupKeys(t *testing.T) {
	f := appfixture.New(t)
	ctx := context.Background()
	store := NewAppStore(f.Pool)
	var alertID int64
	var channelID int64
	if err := f.Pool.QueryRow(ctx, `insert into notification_channels(org_id,kind,name,config) values (1,'webhook','c','{}') returning id`).Scan(&channelID); err != nil {
		t.Fatal(err)
	}
	if err := f.Pool.QueryRow(ctx, `insert into alerts(site_id,kind,threshold,channel_ids) values ($1,'app_new_issue',1,$2) returning id`, f.SiteID, []int64{channelID}).Scan(&alertID); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	if err := store.RecordAppFired(ctx, alertID, "issue:1", now); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordAppFired(ctx, alertID, "issue:1", now.Add(time.Hour)); err != nil {
		t.Fatalf("recording twice must be idempotent: %v", err)
	}
	got, err := store.AppFired(ctx, alertID, []string{"issue:1", "issue:2"})
	if err != nil || len(got) != 1 || !got["issue:1"].Equal(now.Add(time.Hour)) {
		t.Fatalf("AppFired() = %v, %v", got, err)
	}
	if err := store.ForgetAppFired(ctx, alertID, "issue:1"); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.AppFired(ctx, alertID, []string{"issue:1"}); len(got) != 0 {
		t.Fatalf("after forget = %v", got)
	}
}
