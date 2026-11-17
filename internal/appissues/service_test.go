package appissues_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/appissues"
	"supermetry/internal/db"
	"supermetry/internal/testpg"
)

var testNow = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

type fixture struct {
	pool                  *pgxpool.Pool
	svc                   *appissues.Service
	issue, other, foreign int64
	alice, bob, outsider  int64 // alice: owner, bob: editor limited to site 8, outsider: another org
	actor                 appissues.Actor
	adminActor, bobActor  appissues.Actor
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	f := fixture{pool: pool, svc: appissues.New(pool, func() time.Time { return testNow })}
	mustExec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	mustExec(`insert into orgs(name,slug) values('Org','org'),('Foreign','foreign')`)
	mustExec(`insert into sites(id,org_id,public_id,name,timezone,retention_class,kind) overriding system value
values (7,1,'appsite00001','App','UTC',30,'app'),(8,1,'appsite00002','App2','UTC',30,'app'),(9,2,'appsite00003','Foreign','UTC',30,'app')`)
	user := func(email string) int64 {
		var id int64
		if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash) values ($1,$1,'h') returning id`, email).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	f.alice, f.bob, f.outsider = user("alice@example.test"), user("bob@example.test"), user("carol@example.test")
	mustExec(`insert into memberships(org_id,user_id,role) values (1,$1,'owner')`, f.alice)
	mustExec(`insert into memberships(org_id,user_id,role,site_ids) values (1,$1,'editor','{8}')`, f.bob)
	mustExec(`insert into memberships(org_id,user_id,role) values (2,$1,'owner')`, f.outsider)
	issue := func(site int, fp string) int64 {
		var id int64
		if err := pool.QueryRow(ctx, `insert into app_issues(site_id,fingerprint,kind,title,first_seen,last_seen,event_count)
values ($1,$2,'crash','Title','2026-11-01','2026-11-17',5) returning id`, site, fp).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	f.issue, f.other, f.foreign = issue(7, "a"), issue(7, "b"), issue(9, "z")
	f.actor = appissues.Actor{UserID: f.alice, Admin: true}
	f.bobActor = appissues.Actor{UserID: f.bob}
	return f
}

func (f fixture) activity(t *testing.T, issue int64) []string {
	t.Helper()
	rows, err := f.pool.Query(context.Background(), `select kind from app_issue_activity where issue_id=$1 order by at, id`, issue)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var kinds []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatal(err)
		}
		kinds = append(kinds, k)
	}
	return kinds
}

func s(v string) *string { return &v }
func i64(v int64) *int64 { return &v }
func b(v bool) *bool     { return &v }

func TestUpdateWritesOneActivityRowPerChangedField(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	until := testNow.Add(24 * time.Hour)
	for _, c := range []struct {
		name string
		ch   appissues.Change
		kind string
		want func(t *testing.T)
	}{
		{"status", appissues.Change{Status: s("resolved")}, "status", func(t *testing.T) { f.expectStatus(t, "resolved") }},
		{"assign", appissues.Change{Assignee: i64(f.alice)}, "assigned", nil},
		{"unassign", appissues.Change{Assignee: i64(0)}, "unassigned", nil},
		{"priority", appissues.Change{Priority: s("high")}, "priority", nil},
		{"snooze", appissues.Change{Snooze: &appissues.Snooze{Until: &until}}, "snoozed", func(t *testing.T) { f.expectStatus(t, "ignored") }},
		{"archive", appissues.Change{Archive: b(true)}, "archived", func(t *testing.T) { f.expectStatus(t, "archived") }},
		{"github", appissues.Change{GitHubURL: s("https://github.com/acme/app/issues/12")}, "github_linked", nil},
	} {
		before := len(f.activity(t, f.issue))
		res, err := f.svc.Update(ctx, 7, []int64{f.issue}, f.actor, c.ch)
		if err != nil || len(res.Updated) != 1 || len(res.Skipped) != 0 {
			t.Fatalf("%s: %+v %v", c.name, res, err)
		}
		kinds := f.activity(t, f.issue)
		if len(kinds) != before+1 || kinds[len(kinds)-1] != c.kind {
			t.Fatalf("%s: activity = %v", c.name, kinds)
		}
		if c.want != nil {
			c.want(t)
		}
	}
	// A no-op change writes nothing.
	before := len(f.activity(t, f.issue))
	if _, err := f.svc.Update(ctx, 7, []int64{f.issue}, f.actor, appissues.Change{Priority: s("high")}); err != nil || len(f.activity(t, f.issue)) != before {
		t.Fatalf("no-op wrote activity: %v", err)
	}
	// Two fields change in one call: two rows, in one transaction.
	if _, err := f.svc.Update(ctx, 7, []int64{f.other}, f.actor, appissues.Change{Priority: s("low"), Assignee: i64(f.alice)}); err != nil {
		t.Fatal(err)
	}
	if kinds := f.activity(t, f.other); len(kinds) != 2 {
		t.Fatalf("two-field activity = %v", kinds)
	}
}

func (f fixture) expectStatus(t *testing.T, want string) {
	t.Helper()
	var got string
	if err := f.pool.QueryRow(context.Background(), `select status from app_issues where id=$1`, f.issue).Scan(&got); err != nil || got != want {
		t.Fatalf("status = %q %v, want %q", got, err, want)
	}
}

func TestUpdateSnoozeAndArchiveState(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	until := testNow.Add(time.Hour)
	if _, err := f.svc.Update(ctx, 7, []int64{f.issue}, f.actor, appissues.Change{Snooze: &appissues.Snooze{Until: &until, Events: i64(100)}}); err != nil {
		t.Fatal(err)
	}
	var st string
	var su *time.Time
	var se, base *int64
	row := f.pool.QueryRow(ctx, `select status, snooze_until, snooze_events, snooze_base_events from app_issues where id=$1`, f.issue)
	if err := row.Scan(&st, &su, &se, &base); err != nil || st != "ignored" || su == nil || !su.Equal(until) || *se != 100 || *base != 5 {
		t.Fatalf("snoozed = %s %v %v %v %v", st, su, se, base, err)
	}
	// Reopening clears the snooze; archiving then sets the escalating flag.
	if _, err := f.svc.Update(ctx, 7, []int64{f.issue}, f.actor, appissues.Change{Status: s("open")}); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `select snooze_until, snooze_events from app_issues where id=$1`, f.issue).Scan(&su, &se); err != nil || su != nil || se != nil {
		t.Fatalf("snooze not cleared: %v %v %v", su, se, err)
	}
	if _, err := f.svc.Update(ctx, 7, []int64{f.issue}, f.actor, appissues.Change{Archive: b(true)}); err != nil {
		t.Fatal(err)
	}
	var flag bool
	if err := f.pool.QueryRow(ctx, `select archived_until_escalating from app_issues where id=$1`, f.issue).Scan(&flag); err != nil || !flag {
		t.Fatalf("archive flag = %v %v", flag, err)
	}
	if _, err := f.svc.Update(ctx, 7, []int64{f.issue}, f.actor, appissues.Change{Archive: b(false)}); err != nil {
		t.Fatal(err)
	}
	f.expectStatus(t, "open")
}

func TestUpdateValidation(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	past := testNow.Add(-time.Minute)
	for name, ch := range map[string]appissues.Change{
		"empty":             {},
		"bad status":        {Status: s("regressed")},
		"bad priority":      {Priority: s("urgent")},
		"bad github":        {GitHubURL: s("https://example.com/acme/app/issues/1")},
		"github pull":       {GitHubURL: s("https://github.com/acme/app/pull/1")},
		"empty snooze":      {Snooze: &appissues.Snooze{}},
		"past snooze":       {Snooze: &appissues.Snooze{Until: &past}},
		"zero events":       {Snooze: &appissues.Snooze{Events: i64(0)}},
		"status+archive":    {Status: s("open"), Archive: b(true)},
		"release w/o fix":   {Status: s("open"), ResolvedInRelease: s("1.0")},
		"negative assignee": {Assignee: i64(-1)},
	} {
		if _, err := f.svc.Update(ctx, 7, []int64{f.issue}, f.actor, ch); !errors.Is(err, appissues.ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	if kinds := f.activity(t, f.issue); len(kinds) != 0 {
		t.Fatalf("a refused change left activity: %v", kinds)
	}
}

func TestUpdateAssigneeMustBeAnOrgMemberWithSiteAccess(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	for name, id := range map[string]int64{"other org": f.outsider, "unknown": 99999, "no access to site 7": f.bob} {
		if _, err := f.svc.Update(ctx, 7, []int64{f.issue}, f.actor, appissues.Change{Assignee: i64(id)}); !errors.Is(err, appissues.ErrInvalidAssignee) {
			t.Errorf("%s: err = %v, want ErrInvalidAssignee", name, err)
		}
	}
	// Bob can be assigned on site 8, where his access lists it.
	var other8 int64
	if err := f.pool.QueryRow(ctx, `insert into app_issues(site_id,fingerprint,kind,first_seen,last_seen) values (8,'q','crash',now(),now()) returning id`).Scan(&other8); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Update(ctx, 8, []int64{other8}, f.actor, appissues.Change{Assignee: i64(f.bob)}); err != nil {
		t.Fatalf("assign bob on site 8: %v", err)
	}
}

func TestUpdateSkipsIssuesOfOtherSites(t *testing.T) {
	f := newFixture(t)
	res, err := f.svc.Update(context.Background(), 7, []int64{f.issue, f.foreign, 424242}, f.actor, appissues.Change{Priority: s("low")})
	if err != nil || len(res.Updated) != 1 || res.Updated[0] != f.issue || len(res.Skipped) != 2 {
		t.Fatalf("res = %+v err = %v", res, err)
	}
	var prio string
	if err := f.pool.QueryRow(context.Background(), `select priority from app_issues where id=$1`, f.foreign).Scan(&prio); err != nil || prio != "medium" {
		t.Fatalf("foreign issue changed: %q %v", prio, err)
	}
}

func TestUpdateResolveNextRelease(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.Update(context.Background(), 7, []int64{f.issue}, f.actor, appissues.Change{Status: s("resolved"), ResolvedInRelease: s("2.0.0")}); err != nil {
		t.Fatal(err)
	}
	var rel *string
	if err := f.pool.QueryRow(context.Background(), `select resolved_in_release from app_issues where id=$1`, f.issue).Scan(&rel); err != nil || rel == nil || *rel != "2.0.0" {
		t.Fatalf("release = %v %v", rel, err)
	}
}

func TestCommentsLifecycleAndBounds(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	for _, body := range []string{"", "   ", strings.Repeat("x", 10001)} {
		if _, err := f.svc.Comment(ctx, 7, f.issue, f.actor, body); !errors.Is(err, appissues.ErrInvalid) {
			t.Errorf("body len %d: err = %v", len(body), err)
		}
	}
	c, err := f.svc.Comment(ctx, 7, f.issue, f.bobActor, strings.Repeat("é", 10000))
	if err != nil || c.ID == 0 || c.AuthorUserID == nil || *c.AuthorUserID != f.bob {
		t.Fatalf("comment = %+v %v", c, err)
	}
	if kinds := f.activity(t, f.issue); len(kinds) != 1 || kinds[0] != "commented" {
		t.Fatalf("activity = %v", kinds)
	}
	if _, err := f.svc.Comment(ctx, 7, f.foreign, f.actor, "hi"); !errors.Is(err, appissues.ErrNotFound) {
		t.Fatalf("comment on another site's issue: %v", err)
	}
	// Another editor who is not the author may not edit or delete; an admin may.
	third := appissues.Actor{UserID: f.outsider}
	if _, err := f.svc.EditComment(ctx, 7, c.ID, third, "x"); !errors.Is(err, appissues.ErrForbidden) {
		t.Fatalf("edit by non-author: %v", err)
	}
	if err := f.svc.DeleteComment(ctx, 7, c.ID, third); !errors.Is(err, appissues.ErrForbidden) {
		t.Fatalf("delete by non-author: %v", err)
	}
	edited, err := f.svc.EditComment(ctx, 7, c.ID, f.bobActor, "second")
	if err != nil || edited.Body != "second" || edited.EditedAt == nil {
		t.Fatalf("edit = %+v %v", edited, err)
	}
	if _, err := f.svc.EditComment(ctx, 7, c.ID, f.actor, "by admin"); err != nil {
		t.Fatalf("admin edit: %v", err)
	}
	if _, err := f.svc.EditComment(ctx, 8, c.ID, f.actor, "wrong site"); !errors.Is(err, appissues.ErrNotFound) {
		t.Fatalf("edit through another site: %v", err)
	}
	if err := f.svc.DeleteComment(ctx, 7, c.ID, f.actor); err != nil {
		t.Fatal(err)
	}
	list, err := f.svc.Comments(ctx, 7, f.issue)
	if err != nil || len(list) != 0 {
		t.Fatalf("deleted comment still listed: %v %v", list, err)
	}
	if err := f.svc.DeleteComment(ctx, 7, c.ID, f.actor); !errors.Is(err, appissues.ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
}

func TestTimelineOrderingAndPaging(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	clock := testNow
	svc := appissues.New(f.pool, func() time.Time { clock = clock.Add(time.Minute); return clock })
	for _, p := range []string{"high", "low", "medium"} {
		if _, err := svc.Update(ctx, 7, []int64{f.issue}, f.actor, appissues.Change{Priority: s(p)}); err != nil {
			t.Fatal(err)
		}
	}
	// A two-field change shares a minute; paging must not drop or repeat rows.
	if _, err := svc.Update(ctx, 7, []int64{f.issue}, f.actor, appissues.Change{Priority: s("high"), Assignee: i64(f.alice)}); err != nil {
		t.Fatal(err)
	}
	all, err := svc.Timeline(ctx, 7, f.issue, 50, time.Time{})
	if err != nil || len(all) != 5 {
		t.Fatalf("timeline = %d %v", len(all), err)
	}
	for i := 1; i < len(all); i++ {
		if all[i].At.After(all[i-1].At) {
			t.Fatalf("not newest first: %v", all)
		}
	}
	if all[0].ActorName != "alice@example.test" && all[0].ActorUserID == nil {
		t.Fatalf("actor = %+v", all[0])
	}
	var seen []int64
	var before time.Time
	for range 5 {
		page, err := svc.Timeline(ctx, 7, f.issue, 2, before)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		for _, a := range page {
			seen = append(seen, a.ID)
		}
		before = page[len(page)-1].At
	}
	if len(seen) != 5 {
		t.Fatalf("paged %d rows, want 5: %v", len(seen), seen)
	}
	if _, err := svc.Timeline(ctx, 7, f.foreign, 10, time.Time{}); !errors.Is(err, appissues.ErrNotFound) {
		t.Fatalf("timeline of another site's issue: %v", err)
	}
}

func TestRecordSystem(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := appissues.RecordSystem(ctx, tx, 7, f.issue, "regressed", map[string]any{"release": "1.2.0"}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var actor *int64
	var data string
	if err := f.pool.QueryRow(ctx, `select actor_user_id, data::text from app_issue_activity where issue_id=$1 and kind='regressed'`, f.issue).Scan(&actor, &data); err != nil || actor != nil || !strings.Contains(data, "1.2.0") {
		t.Fatalf("row = %v %s %v", actor, data, err)
	}
}

func TestUpdateGitHubURLAcceptsOnlyGitHubIssueLinks(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	for _, bad := range []string{
		"javascript:alert(1)", "http://github.com/o/r/issues/1", "https://github.com.evil.com/o/r/issues/1",
		"https://github.com/o/r/pull/1", "https://github.com/o/r/issues/1?x=1", "https://github.com/o/r/issues/12345678901",
		"https://github.com/-o_/r/issues/1", "https://github.com/" + strings.Repeat("a", 40) + "/r/issues/1", " https://github.com/o/r/issues/1",
	} {
		if _, err := f.svc.Update(ctx, 7, []int64{f.issue}, f.actor, appissues.Change{GitHubURL: s(bad)}); !errors.Is(err, appissues.ErrInvalidGitHubURL) {
			t.Errorf("%q: err = %v, want ErrInvalidGitHubURL", bad, err)
		}
	}
	if _, err := f.svc.Update(ctx, 7, []int64{f.issue}, f.actor, appissues.Change{GitHubURL: s("https://github.com/o-1/my.repo_x/issues/12")}); err != nil {
		t.Fatalf("valid url: %v", err)
	}
	if _, err := f.svc.Update(ctx, 7, []int64{f.issue}, f.actor, appissues.Change{GitHubURL: s("")}); err != nil {
		t.Fatalf("clear: %v", err)
	}
	var u *string
	if err := f.pool.QueryRow(ctx, `select github_url from app_issues where id=$1`, f.issue).Scan(&u); err != nil || u != nil {
		t.Fatalf("not cleared: %v %v", u, err)
	}
}
