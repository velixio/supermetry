package appquery_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"supermetry/internal/appquery"
)

func TestNormalizeFramePath(t *testing.T) {
	for in, want := range map[string]string{
		"app:///src/pay.ts":                  "src/pay.ts",
		"webpack://shop/./src/pay.ts?abc":    "src/pay.ts",
		"webpack:///./src/pay.ts#x":          "src/pay.ts",
		"file:///home/ci/app/src/pay.ts":     "home/ci/app/src/pay.ts",
		"/home/ci/build/acme/app/src/pay.ts": "home/ci/build/acme/app/src/pay.ts",
		`C:\b\src\Pay.cs`:                    "C:/b/src/Pay.cs",
		"~/lib/util.js":                      "lib/util.js",
		"./a.go":                             "a.go",
	} {
		if got := appquery.NormalizeFramePath(in); got != want {
			t.Errorf("NormalizeFramePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMatchSuspectsScore(t *testing.T) {
	now := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	older := now.Add(-time.Hour)
	frames := []appquery.StackFrame{
		{Filename: "app:///src/pay.ts", InApp: true},
		{Filename: "/ci/build/src/cart.ts", InApp: true},
		{Filename: "node_modules/lib/util.ts", InApp: false},
	}
	commits := []appquery.CommitRef{
		{SHA: "zzzz", Message: "readme", At: &now, Paths: []string{"README.md"}},
		{SHA: "xxxx", Message: "cart", At: &now, Paths: []string{"src/cart.ts"}},
		{SHA: "yyyy", Message: "pay", At: &older, Paths: []string{"src/repay.ts", "src/pay.ts"}},
	}
	got := appquery.MatchSuspects(frames, commits)
	if len(got) != 2 || got[0].SHA != "yyyy" || got[0].Score != 20 || got[0].Path != "src/pay.ts" || got[1].SHA != "xxxx" || got[1].Score != 19 {
		t.Fatalf("suspects = %+v", got)
	}
}

func TestMatchSuspectsSegmentBoundaryAndTies(t *testing.T) {
	now := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	older := now.Add(-time.Hour)
	frames := []appquery.StackFrame{{Filename: "pay.ts", InApp: true}}
	if got := appquery.MatchSuspects(frames, []appquery.CommitRef{{SHA: "aaaa", Paths: []string{"src/repay.ts"}}}); len(got) != 0 {
		t.Fatalf("repay.ts matched pay.ts: %+v", got)
	}
	got := appquery.MatchSuspects(frames, []appquery.CommitRef{
		{SHA: "old1", At: &older, Paths: []string{"src/pay.ts"}},
		{SHA: "new1", At: &now, Paths: []string{"lib/pay.ts"}},
	})
	if len(got) != 2 || got[0].SHA != "new1" {
		t.Fatalf("tie order = %+v", got)
	}
	win := []appquery.StackFrame{{Filename: `C:\b\src\Pay.cs`, InApp: true}}
	if got := appquery.MatchSuspects(win, []appquery.CommitRef{{SHA: "bbbb", Paths: []string{"src/Pay.cs"}}}); len(got) != 1 {
		t.Fatalf("windows path: %+v", got)
	}
}

func TestSuspectCommitsRespectReleaseSites(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := e.f.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	e.scope.OrgID = 1
	exec(`insert into sites(id,org_id,public_id,name,timezone,retention_class,kind) overriding system value values(8,1,'appfix000002','B','UTC',90,'app')`)
	id := e.f.Issues["I2"]
	exec(`update app_issues set first_release='1.0' where id=$1`, id)
	var rel, repo, c int64
	if err := e.f.Pool.QueryRow(ctx, `insert into app_releases(org_id,version,site_ids) values(1,'1.0','{8}') returning id`).Scan(&rel); err != nil {
		t.Fatal(err)
	}
	if err := e.f.Pool.QueryRow(ctx, `insert into app_repositories(org_id,name) values(1,'acme/app') returning id`).Scan(&repo); err != nil {
		t.Fatal(err)
	}
	if err := e.f.Pool.QueryRow(ctx, `insert into app_commits(org_id,repository_id,sha,message,author_name) values(1,$1,'abc1234','fix pay','Dev') returning id`, repo).Scan(&c); err != nil {
		t.Fatal(err)
	}
	exec(`insert into app_commit_files(commit_id,path) values($1,'src/pay.ts')`, c)
	exec(`insert into app_release_commits(release_id,commit_id,position) values($1,$2,0)`, rel, c)
	det, err := e.q.Issue(ctx, e.scope, id, full())
	if err != nil {
		t.Fatal(err)
	}
	ev := det.LatestEvents[0]
	exec(`insert into app_event_bodies(site_id,ts,event_id,body) values($1,$2,$3,$4)`, e.f.SiteID, ev.TS, ev.EventID,
		`{"exceptions":[{"frames":[{"filename":"app:///src/pay.ts","in_app":true}]}]}`)
	if det, err = e.q.Issue(ctx, e.scope, id, full()); err != nil || len(det.SuspectCommits) != 0 {
		t.Fatalf("release of another site leaked commits: %+v %v", det.SuspectCommits, err)
	}
	exec(`update app_releases set site_ids='{}' where id=$1`, rel)
	if det, err = e.q.Issue(ctx, e.scope, id, full()); err != nil || len(det.SuspectCommits) != 1 {
		t.Fatalf("org-wide release suspects = %+v %v", det.SuspectCommits, err)
	}
}

func TestIssueDetailIntroducedIn(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := e.f.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	e.scope.OrgID = 1
	id := e.f.Issues["I2"]
	exec(`update app_issues set first_release='1.1.0' where id=$1`, id)
	var rel int64
	if err := e.f.Pool.QueryRow(ctx, `insert into app_releases(org_id,version) values(1,'1.1.0') returning id`).Scan(&rel); err != nil {
		t.Fatal(err)
	}
	exec(`insert into app_deploys(release_id,org_id,environment,date_finished) values($1,1,'production','2026-11-15T10:00:00Z')`, rel)

	det, err := e.q.Issue(ctx, e.scope, id, full())
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	if det.IntroducedIn == nil || det.IntroducedIn.Release != "1.1.0" || det.IntroducedIn.DeployAt == nil || !det.IntroducedIn.DeployAt.Equal(want) {
		t.Fatalf("introduced_in = %+v", det.IntroducedIn)
	}
	if det.SuspectCommits == nil || len(det.SuspectCommits) != 0 {
		t.Fatalf("suspect commits without commits = %#v", det.SuspectCommits)
	}

	// Commits plus a stored body of the newest event give suspects.
	var repo, c1, c2 int64
	if err := e.f.Pool.QueryRow(ctx, `insert into app_repositories(org_id,name) values(1,'acme/app') returning id`).Scan(&repo); err != nil {
		t.Fatal(err)
	}
	if err := e.f.Pool.QueryRow(ctx, `insert into app_commits(org_id,repository_id,sha,message,author_name,author_email,committed_at) values(1,$1,'abc1234','fix pay','Dev One','dev@example.com','2026-11-14T10:00:00Z') returning id`, repo).Scan(&c1); err != nil {
		t.Fatal(err)
	}
	if err := e.f.Pool.QueryRow(ctx, `insert into app_commits(org_id,repository_id,sha,message,author_name,committed_at) values(1,$1,'def5678','docs','Dev Two','2026-11-14T11:00:00Z') returning id`, repo).Scan(&c2); err != nil {
		t.Fatal(err)
	}
	exec(`insert into app_commit_files(commit_id,path) values($1,'src/pay.ts'),($2,'README.md')`, c1, c2)
	exec(`insert into app_release_commits(release_id,commit_id,position) values($1,$2,1),($1,$3,0)`, rel, c1, c2)
	ev := det.LatestEvents[0]
	exec(`insert into app_event_bodies(site_id,ts,event_id,body) values($1,$2,$3,$4)`, e.f.SiteID, ev.TS, ev.EventID,
		`{"exceptions":[{"type":"Error","frames":[{"filename":"app:///src/main.ts","in_app":true},{"filename":"app:///src/pay.ts","in_app":true}]}]}`)
	det, err = e.q.Issue(ctx, e.scope, id, full())
	if err != nil {
		t.Fatal(err)
	}
	if len(det.SuspectCommits) != 1 || !reflect.DeepEqual([]string{det.SuspectCommits[0].SHA, det.SuspectCommits[0].AuthorName, det.SuspectCommits[0].Path}, []string{"abc1234", "Dev One", "src/pay.ts"}) || det.SuspectCommits[0].Score != 20 {
		t.Fatalf("suspects = %+v", det.SuspectCommits)
	}
}
