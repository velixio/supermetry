package appquery_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"supermetry/internal/appfixture"
	"supermetry/internal/appquery"
)

// seedDaily inserts one app_session_daily row (production, linux).
func seedDaily(t *testing.T, f *appfixture.Fixture, day, release string, sessions, crashed, abnormal, users, crashedUsers int) {
	t.Helper()
	if _, err := f.Pool.Exec(context.Background(), `insert into app_session_daily(site_id,day,release,environment,platform,sessions,crashed,abnormal,users,crashed_users)
values($1,$2,$3,'production','linux',$4,$5,$6,$7,$8)`, f.SiteID, day, release, sessions, crashed, abnormal, users, crashedUsers); err != nil {
		t.Fatal(err)
	}
}

func detailQuerier(f *appfixture.Fixture) (*appquery.Querier, appquery.Scope) {
	return appquery.New(f.Pool, func() time.Time { return f.Now }), appquery.Scope{SiteID: f.SiteID, OrgID: f.OrgID, TZ: f.Loc, RetentionDays: 90}
}

// Site sessions per day: day 1 1.0=90 1.1=10; day 2 1.0=40 1.1=60 (3 crashed,
// 1 abnormal, 20 users, 2 crashed users); day 3 1.1=100 (0 crashed).
func TestRolloutHandComputed(t *testing.T) {
	f := appfixture.New(t)
	seedDaily(t, f, "2026-11-10", "1.0", 90, 0, 0, 0, 0)
	seedDaily(t, f, "2026-11-10", "1.1", 10, 0, 0, 0, 0)
	seedDaily(t, f, "2026-11-11", "1.0", 40, 0, 0, 0, 0)
	seedDaily(t, f, "2026-11-11", "1.1", 60, 3, 1, 20, 2)
	seedDaily(t, f, "2026-11-12", "1.1", 100, 0, 0, 0, 0)
	q, scope := detailQuerier(f)
	got, err := q.ReleaseDetail(context.Background(), scope, appquery.Filter{From: d("2026-11-10"), To: d("2026-11-12")}, "1.1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rollout) != 3 || got.HealthSource != appquery.HealthSessions {
		t.Fatalf("rollout = %+v source %q", got.Rollout, got.HealthSource)
	}
	for i, want := range []float64{0.10, 0.60, 1.00} {
		near(t, "adoption", got.Rollout[i].Adoption, want)
	}
	if got.Rollout[0].Day != "2026-11-10" || got.Rollout[1].Sessions != 60 {
		t.Fatalf("points = %+v", got.Rollout)
	}
	// Abnormal sessions are not crashes (2a's definition): 1 - 3/60.
	near(t, "cfs day 2", got.Rollout[1].CrashFreeSessions, 0.95)
	near(t, "cfs day 3", got.Rollout[2].CrashFreeSessions, 1)
	near(t, "cfu day 2", got.Rollout[1].CrashFreeUsers, 0.9)
	if got.Rollout[0].CrashFreeUsers != nil {
		t.Fatalf("no users: crash-free users must be null, got %v", *got.Rollout[0].CrashFreeUsers)
	}
	if got.Health.Sessions != 170 {
		t.Fatalf("health sessions = %d", got.Health.Sessions)
	}
}

func TestRolloutV1Fallback(t *testing.T) {
	f := appfixture.New(t)
	for _, r := range []struct {
		rel      string
		sessions int
	}{{"1.0", 60}, {"1.1", 20}} {
		if _, err := f.Pool.Exec(context.Background(), `insert into app_release_daily(site_id,day,release,platform,sessions,crashed_sessions) values($1,'2026-11-11',$2,'linux',$3,0)`, f.SiteID, r.rel, r.sessions); err != nil {
			t.Fatal(err)
		}
	}
	q, scope := detailQuerier(f)
	got, err := q.ReleaseDetail(context.Background(), scope, appquery.Filter{From: d("2026-11-10"), To: d("2026-11-12")}, "1.1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rollout) != 1 || got.HealthSource != appquery.HealthEvents {
		t.Fatalf("rollout = %+v source %q", got.Rollout, got.HealthSource)
	}
	near(t, "adoption", got.Rollout[0].Adoption, 0.25)
	near(t, "cfs", got.Rollout[0].CrashFreeSessions, 1)
}

// Review focus 3: a release only present in events opens without 404.
func TestReleaseDetailEventOnly(t *testing.T) {
	f := appfixture.New(t)
	q, scope := detailQuerier(f)
	got, err := q.ReleaseDetail(context.Background(), scope, appquery.Filter{From: d("2026-11-10"), To: d("2026-11-15")}, "1.9.0")
	if err != nil {
		t.Fatal(err)
	}
	if got.Created != nil || got.Status != "open" || len(got.Deploys) != 0 || got.CommitCount != 0 || got.FirstSeen == nil {
		t.Fatalf("detail = %+v", got)
	}
	if got.Rollout == nil || got.Deploys == nil || got.NewIssues == nil || got.RegressedIssues == nil {
		t.Fatal("lists must be empty arrays, not null")
	}
	// I1 and I2 first appeared in 1.9.0.
	if len(got.NewIssues) != 2 || got.NewIssues[0].Events < got.NewIssues[1].Events || got.Health.NewIssues != 2 {
		t.Fatalf("new issues = %+v", got.NewIssues)
	}
	if _, err := q.ReleaseDetail(context.Background(), scope, appquery.Filter{From: d("2026-11-10"), To: d("2026-11-15")}, "9.9.9"); !errors.Is(err, appquery.ErrNotFound) {
		t.Fatalf("unknown release err = %v", err)
	}
}

// An API row with no events yet is a real release.
func TestReleaseDetailAPIRowOnly(t *testing.T) {
	f := appfixture.New(t)
	ctx := context.Background()
	var id int64
	if err := f.Pool.QueryRow(ctx, `insert into app_releases(org_id,version,ref,status,site_ids) values(1,'3.0.0','abc123','archived','{7}') returning id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Pool.Exec(ctx, `insert into app_deploys(release_id,org_id,site_ids,environment,date_finished) values($1,1,'{}','production','2026-11-14T10:00:00Z')`, id); err != nil {
		t.Fatal(err)
	}
	q, scope := detailQuerier(f)
	got, err := q.ReleaseDetail(ctx, scope, appquery.Filter{From: d("2026-11-10"), To: d("2026-11-15")}, "3.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if got.Created == nil || got.Status != "archived" || got.Ref != "abc123" || len(got.Deploys) != 1 {
		t.Fatalf("detail = %+v", got)
	}
}

// Review focus 5: another org's row with the same version never leaks.
func TestReleaseDetailForeignOrg(t *testing.T) {
	f := appfixture.New(t)
	ctx := context.Background()
	if _, err := f.Pool.Exec(ctx, `insert into orgs(name,slug) values('Other','other')`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Pool.Exec(ctx, `insert into app_releases(org_id,version,ref,status) values(2,'1.9.0','leak','archived')`); err != nil {
		t.Fatal(err)
	}
	q, scope := detailQuerier(f)
	got, err := q.ReleaseDetail(ctx, scope, appquery.Filter{From: d("2026-11-10"), To: d("2026-11-15")}, "1.9.0")
	if err != nil {
		t.Fatal(err)
	}
	if got.Ref != "" || got.Created != nil || got.Status != "open" {
		t.Fatalf("leaked foreign row: %+v", got)
	}
}
