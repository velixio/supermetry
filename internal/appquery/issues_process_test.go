package appquery_test

import (
	"context"
	"reflect"
	"sort"
	"testing"
	"time"

	"supermetry/internal/appfixture"
	"supermetry/internal/appquery"
	"supermetry/internal/appquery/search"
	"supermetry/internal/approllup"
)

// The process tag of an Electron app (main, renderer, ...) is a search key
// backed by the issue tag rollup. I1's events are main-process, I2's are
// renderer-process, I3 has no process tag.
func TestIssueSearchProcessKey(t *testing.T) {
	f := appfixture.New(t)
	ctx := context.Background()
	for issue, process := range map[string]string{"I1": "main", "I2": "renderer"} {
		if _, err := f.Pool.Exec(ctx, `update app_events set tags = coalesce(tags, '{}'::jsonb) || jsonb_build_object('process', $1::text) where issue_id = $2`, process, f.Issues[issue]); err != nil {
			t.Fatal(err)
		}
	}
	for _, day := range []string{"2026-11-12", "2026-11-13", "2026-11-14", "2026-11-15"} {
		if err := approllup.RecomputeDay(ctx, f.Pool, f.SiteID, d(day)); err != nil {
			t.Fatal(err)
		}
	}
	q := appquery.New(f.Pool, func() time.Time { return f.Now })
	scope := appquery.Scope{SiteID: f.SiteID, TZ: f.Loc, RetentionDays: 90}
	for _, c := range []struct {
		in   string
		want []string
	}{
		{"process:main", []string{"I1"}},
		{"process:renderer", []string{"I2"}},
		{"!process:main", []string{"I2", "I3"}},
		{"process:gpu", nil},
	} {
		parsed, err := search.Parse(c.in)
		if err != nil {
			t.Fatalf("parse %q: %v", c.in, err)
		}
		out, err := q.Issues(ctx, scope, appquery.IssueQuery{Filter: full(), Search: &parsed})
		if err != nil {
			t.Fatalf("%q: %v", c.in, err)
		}
		var got []string
		for _, r := range out.Issues {
			for k, id := range f.Issues {
				if id == r.ID {
					got = append(got, k)
				}
			}
		}
		sort.Strings(got)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q = %v, want %v", c.in, got, c.want)
		}
	}
}
