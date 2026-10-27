package db

import (
	"context"
	"testing"

	"supermetry/internal/testpg"
)

func TestGitHubStarsPerOrgMigrationAssignsExistingRepos(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range migrations {
		if m.version >= 246 {
			break
		}
		if err := applyMigration(ctx, conn, m); err != nil {
			conn.Release()
			t.Fatal(err)
		}
	}
	conn.Release()

	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	var firstOrg, secondOrg, repoA, repoB int64
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('First','first') returning id`).Scan(&firstOrg); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Second','second') returning id`).Scan(&secondOrg); err != nil {
		t.Fatal(err)
	}
	for repo, target := range map[string]*int64{"acme/one": &repoA, "acme/two": &repoB} {
		if err := pool.QueryRow(ctx, `insert into github_star_repos(public_id,repo,webhook_secret_enc) values ($1,$2,'\x01') returning id`, repo+"-pub", repo).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	exec(`insert into github_star_events(delivery_id,repo,user_id,login,action,occurred_at,source) values
		('d1','acme/one',1,'ann','starred',now(),'webhook'),('d2','acme/one',2,'bob','starred',now(),'webhook'),('d3','acme/two',1,'ann','starred',now(),'webhook'),
		('d4','acme/gone',9,'zed','starred',now(),'webhook')`)
	exec(`insert into github_star_state(repo,stars,updated_at) values ('acme/one',2,now()),('acme/two',1,now())`)
	exec(`insert into github_star_snapshots(repo,day,stars) values ('acme/one','2026-10-01',2),('acme/two','2026-10-01',1)`)
	exec(`insert into github_star_annotations(repo,at,label) values ('acme/one',now(),'launch')`)
	exec(`insert into github_star_erasures(repo,user_id) values ('acme/two',7)`)

	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	for _, id := range []int64{repoA, repoB} {
		var org int64
		var public bool
		if err := pool.QueryRow(ctx, `select org_id,public_counter from github_star_repos where id=$1`, id).Scan(&org, &public); err != nil {
			t.Fatal(err)
		}
		if org != firstOrg || !public {
			t.Fatalf("repo %d org=%d public=%v, want org %d public true", id, org, public, firstOrg)
		}
	}
	count := func(sql string, args ...any) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(`select count(*) from github_star_events where repo_id=$1`, repoA); n != 2 {
		t.Fatalf("events of repo one = %d, want 2", n)
	}
	if n := count(`select count(*) from github_star_events where repo_id=$1`, repoB); n != 1 {
		t.Fatalf("events of repo two = %d, want 1", n)
	}
	if n := count(`select count(*) from github_star_events`); n != 3 {
		t.Fatalf("events = %d, want 3 (the orphan is dropped)", n)
	}
	if n := count(`select stars from github_star_state where repo_id=$1`, repoA); n != 2 {
		t.Fatalf("state stars = %d, want 2", n)
	}
	if n := count(`select count(*) from github_star_snapshots`); n != 2 {
		t.Fatalf("snapshots = %d, want 2", n)
	}
	if n := count(`select count(*) from github_star_annotations where repo_id=$1`, repoA); n != 1 {
		t.Fatalf("annotations = %d, want 1", n)
	}
	if n := count(`select count(*) from github_star_erasures where repo_id=$1 and user_id=7`, repoB); n != 1 {
		t.Fatalf("erasures = %d, want 1", n)
	}
	// The same GitHub repository can now be tracked by another organization.
	exec(`insert into github_star_repos(public_id,repo,org_id,webhook_secret_enc) values ('other-pub','acme/one',$1,'\x02')`, secondOrg)
	if _, err := pool.Exec(ctx, `insert into github_star_repos(public_id,repo,org_id,webhook_secret_enc) values ('dup-pub','acme/one',$1,'\x02')`, secondOrg); err == nil {
		t.Fatal("the same repository twice in one organization must be refused")
	}
	// Deleting a repository removes its data; the other repository keeps its own.
	exec(`delete from github_star_repos where id=$1`, repoA)
	if n := count(`select count(*) from github_star_events`); n != 1 {
		t.Fatalf("events after delete = %d, want 1", n)
	}
	// Deleting an organization removes its repositories.
	exec(`delete from orgs where id=$1`, firstOrg)
	if n := count(`select count(*) from github_star_repos where org_id=$1`, firstOrg); n != 0 {
		t.Fatalf("repos left after org delete = %d", n)
	}
	if n := count(`select count(*) from github_star_events`); n != 0 {
		t.Fatalf("events left after org delete = %d", n)
	}
}

func TestGitHubStarsPerOrgMigrationWithoutOrgsOrRepos(t *testing.T) {
	pool, _ := testpg.New(t)
	if err := Migrate(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
}
