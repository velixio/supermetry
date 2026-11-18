package appquery_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"supermetry/internal/appquery"
	"supermetry/internal/appquery/search"
)

// The fixture's issues (see appfixture): I1 regressed crash, fatal, releases
// 1.9.0 and 1.10.0, 4 events, 1 user; I2 open error, level error, both
// releases, environments production and beta, linux and macos, 5 events,
// 2 users; I3 resolved crash, fatal, 1.10.0, macos, 1 event, 1 user. The clock
// is 2026-11-15 10:00 UTC.
func TestIssueSearchKeys(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	ids := e.f.Issues
	var user int64
	if err := e.f.Pool.QueryRow(ctx, `insert into users(email,name,password_hash) values ('ann@example.test','Ann','h') returning id`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	if _, err := e.f.Pool.Exec(ctx, `update app_issues set assignee_user_id = $1 where id = $2`, user, ids["I1"]); err != nil {
		t.Fatal(err)
	}
	if _, err := e.f.Pool.Exec(ctx, `update app_issues set priority = 'high' where id = $1`, ids["I3"]); err != nil {
		t.Fatal(err)
	}
	lookup := func(email string) (int64, bool) { return user, email == "ann@example.test" }
	run := func(in string) []string {
		t.Helper()
		q, err := search.Parse(in)
		if err != nil {
			t.Fatalf("parse %q: %v", in, err)
		}
		out, err := e.q.Issues(ctx, e.scope, appquery.IssueQuery{Filter: full(), Search: &q, Me: user, LookupMember: lookup})
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		var got []string
		for _, r := range out.Issues {
			for k, id := range ids {
				if id == r.ID {
					got = append(got, k)
				}
			}
		}
		return got
	}
	for _, c := range []struct {
		in   string
		want []string
	}{
		{"", []string{"I1", "I2", "I3"}},
		{"is:unresolved", []string{"I1", "I2"}},
		{"is:resolved", []string{"I3"}},
		{"!is:resolved", []string{"I1", "I2"}},
		{"is:ignored", nil},
		{"is:archived", nil},
		{"is:snoozed", nil},
		{"is:assigned", []string{"I1"}},
		{"is:unassigned", []string{"I2", "I3"}},
		{"assigned:me", []string{"I1"}},
		{"!assigned:me", []string{"I2", "I3"}},
		{"assigned:none", []string{"I2", "I3"}},
		{"assigned:ann@example.test", []string{"I1"}},
		{"assigned:nobody@example.test", nil},
		{"release:1.9.0", []string{"I1", "I2"}},
		{"release:1.10.0", []string{"I1", "I2", "I3"}},
		{"!release:1.9.0", []string{"I3"}},
		{`release:"1.9.0" release:1.10.0`, []string{"I1", "I2"}},
		{"environment:beta", []string{"I2"}},
		{"environment:production", []string{"I1", "I2", "I3"}},
		{"platform:macos", []string{"I2", "I3"}},
		{"level:fatal", []string{"I1", "I3"}},
		{"level:error", []string{"I2"}},
		{"kind:crash", []string{"I1", "I3"}},
		{"kind:error", []string{"I2"}},
		{"priority:high", []string{"I3"}},
		{"priority:medium", []string{"I1", "I2"}},
		{"first-seen:-24h", []string{"I3"}},
		{"first-seen:2026-11-14", []string{"I2", "I3"}},
		{"last-seen:-1h", []string{"I1"}},
		{"last-seen:+1d", nil},
		{"last-seen:+2h", []string{"I3"}},
		{"events:>3", []string{"I1", "I2"}},
		{"events:>=5", []string{"I2"}},
		{"events:<4", []string{"I3"}},
		{"events:1", []string{"I3"}},
		{"users:>1", []string{"I2"}},
		{"users:<=1", []string{"I1", "I3"}},
		{"segfault", []string{"I1"}},
		{"!SEGFAULT", []string{"I2", "I3"}},
		{`"in a.go"`, []string{"I1", "I2", "I3"}},
		{"is:unresolved level:fatal", []string{"I1"}},
		// Metacharacters and unicode are literals that match nothing here.
		{"%", nil},
		{"_", nil},
		{`"'; drop table app_issues; --"`, nil},
		{"日本語", nil},
		{`release:"1.9.0' or '1'='1"`, nil},
	} {
		if got := run(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q = %v, want %v", c.in, got, c.want)
		}
	}
	if _, err := e.f.Pool.Exec(ctx, `update app_issues set status = 'ignored', snooze_until = '2026-12-01' where id = $1`, ids["I2"]); err != nil {
		t.Fatal(err)
	}
	if got := run("is:snoozed"); !reflect.DeepEqual(got, []string{"I2"}) {
		t.Errorf("is:snoozed = %v", got)
	}
	// The old parameters stay and are ANDed with the search.
	q, _ := search.Parse("level:fatal")
	out, err := e.q.Issues(ctx, e.scope, appquery.IssueQuery{Filter: full(), Search: &q, Kind: "crash", Status: "resolved"})
	if err != nil || len(out.Issues) != 1 || out.Issues[0].ID != ids["I3"] {
		t.Errorf("legacy filters with search = %+v, %v", out, err)
	}
	// assigned:me without a user is a client error.
	q, _ = search.Parse("assigned:me")
	if _, err := e.q.Issues(ctx, e.scope, appquery.IssueQuery{Filter: full(), Search: &q}); !errors.Is(err, appquery.ErrInvalid) {
		t.Errorf("assigned:me without user: %v", err)
	}
}

func TestMatchIssuesAndSearchEvents(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	q, _ := search.Parse("is:unresolved")
	got, err := e.q.MatchIssues(ctx, e.scope, appquery.IssueQuery{Filter: full(), Search: &q}, 10)
	want := []int64{e.f.Issues["I1"], e.f.Issues["I2"]}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("MatchIssues = %v, %v; want %v", got, err, want)
	}
	if got, _ := e.q.MatchIssues(ctx, e.scope, appquery.IssueQuery{Filter: full(), Search: &q}, 1); len(got) != 2 {
		t.Errorf("max+1 matches expected so a caller can detect overflow, got %v", got)
	}
	hits, err := e.q.SearchEvents(ctx, e.scope, appquery.IssueQuery{Filter: full(), Search: &q}, 3)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, h := range hits {
		ids = append(ids, h.EventID)
	}
	if !reflect.DeepEqual(ids, []string{"e11", "e12", "e10"}) {
		t.Errorf("SearchEvents = %v, want [e11 e12 e10]", ids)
	}
	if hits[0].IssueID != e.f.Issues["I1"] || hits[0].Release != "1.10.0" || hits[0].Platform != "linux" {
		t.Errorf("hit = %+v", hits[0])
	}
	if _, err := e.q.SearchEvents(ctx, e.scope, appquery.IssueQuery{Filter: full()}, 51); !errors.Is(err, appquery.ErrInvalid) {
		t.Errorf("limit 51: %v", err)
	}
	if strings.Contains(strings.ToLower(reflectFields(hits[0])), "hash") {
		t.Errorf("event hit carries a hash field")
	}
}

func reflectFields(v any) string {
	t := reflect.TypeOf(v)
	var names []string
	for i := 0; i < t.NumField(); i++ {
		names = append(names, t.Field(i).Name)
	}
	return strings.Join(names, ",")
}
