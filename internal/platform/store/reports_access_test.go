package store

import (
	"context"
	"testing"

	"supermetry/internal/db"
	"supermetry/internal/testpg"
)

// Report delivery must re-check access: former members and members restricted
// to other sites stop receiving a site's report.
func TestReportSubscriptionsRequireCurrentAccess(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	data := &Store{Pool: pool}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	var org, viewer, member int64
	var siteA, siteB int32
	pool.QueryRow(ctx, `insert into orgs(name, slug) values ('Acme', 'acme') returning id`).Scan(&org)
	pool.QueryRow(ctx, `insert into users(email, name, password_hash) values ('v@example.test', 'V', 'h') returning id`).Scan(&viewer)
	pool.QueryRow(ctx, `insert into users(email, name, password_hash) values ('m@example.test', 'M', 'h') returning id`).Scan(&member)
	pool.QueryRow(ctx, `insert into sites(org_id, public_id, name, timezone, retention_class) values ($1, 'aaaaaaaaaaaa', 'A', 'UTC', 30) returning id`, org).Scan(&siteA)
	pool.QueryRow(ctx, `insert into sites(org_id, public_id, name, timezone, retention_class) values ($1, 'bbbbbbbbbbbb', 'B', 'UTC', 30) returning id`, org).Scan(&siteB)
	exec(`insert into memberships(org_id, user_id, role, site_ids) values ($1, $2, 'viewer', array[$3::int])`, org, viewer, siteA)
	exec(`insert into memberships(org_id, user_id, role) values ($1, $2, 'admin')`, org, member)
	exec(`insert into report_subscriptions(site_id, user_id, cadence) values ($1, $2, 'weekly'), ($1, $3, 'weekly')`, siteA, viewer, member)

	recipients := func() map[string]bool {
		t.Helper()
		subs, err := ReportSubscriptions(ctx, data)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]bool{}
		for _, s := range subs {
			out[s.Email] = true
		}
		return out
	}
	if got := recipients(); !got["v@example.test"] || !got["m@example.test"] {
		t.Fatalf("both current members must receive: %v", got)
	}
	exec(`update memberships set site_ids = array[$3::int] where org_id = $1 and user_id = $2`, org, viewer, siteB)
	if got := recipients(); got["v@example.test"] {
		t.Fatal("viewer restricted to another site still receives site A's report")
	}
	exec(`delete from memberships where org_id = $1 and user_id = $2`, org, member)
	if got := recipients(); got["m@example.test"] {
		t.Fatal("removed member still receives the report")
	}
}
