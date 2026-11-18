package appquery_test

import (
	"context"
	"reflect"
	"testing"

	"supermetry/internal/appfixture"
	"supermetry/internal/appquery"
	"supermetry/internal/appquery/search"
)

// Terminations are crash issues whose events carry an exit reason; the list
// exposes them as the kinds oom and watchdog and a crash filter keeps them.
func TestIssuesKindOOMAndWatchdog(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	for _, i := range []struct{ fp, title, errType string }{{"fp-oom", "OutOfMemoryError in a.go f", "OutOfMemoryError"}, {"fp-wd", "WatchdogTermination in a.go f", "WatchdogTermination"}} {
		var id int64
		if err := e.f.Pool.QueryRow(ctx, `insert into app_issues(site_id,fingerprint,kind,error_type,title,status,first_release,last_release,first_seen,last_seen,event_count)
values($1,$2,'crash',$3,$4,'open','1.10.0','1.10.0',$5,$5,1) returning id`, e.f.SiteID, i.fp, i.errType, i.title, e.f.Now.Add(-2*3600e9)).Scan(&id); err != nil {
			t.Fatal(err)
		}
		e.f.Issues[i.fp] = id
	}
	for _, c := range []struct{ id, issue, reason string }{{"o1", "fp-oom", "oom"}, {"w1", "fp-wd", "watchdog_termination"}} {
		e.f.Insert(t, appfixture.Event{ID: c.id, Kind: "crash", Level: "fatal", Release: "1.10.0", Platform: "ios", At: e.f.Now.Add(-2 * 3600e9), Install: "A", Issue: c.issue})
		if _, err := e.f.Pool.Exec(ctx, `update app_events set exit_reason=$2 where site_id=$3 and event_id=$1`, c.id, c.reason, e.f.SiteID); err != nil {
			t.Fatal(err)
		}
	}
	list := func(kind string) []string {
		out, err := e.q.Issues(ctx, e.scope, appquery.IssueQuery{Filter: full(), Kind: kind})
		if err != nil {
			t.Fatal(err)
		}
		return titles(out.Issues)
	}
	if got := list("oom"); !reflect.DeepEqual(got, []string{"OutOfMemoryError in a.go f"}) {
		t.Fatalf("oom = %v", got)
	}
	if got := list("watchdog"); !reflect.DeepEqual(got, []string{"WatchdogTermination in a.go f"}) {
		t.Fatalf("watchdog = %v", got)
	}
	if got := list("crash"); len(got) != 4 {
		t.Fatalf("crash keeps terminations: %v", got)
	}
	all, err := e.q.Issues(ctx, e.scope, appquery.IssueQuery{Filter: full()})
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, r := range all.Issues {
		kinds[r.ErrorType] = r.Kind
	}
	if kinds["OutOfMemoryError"] != "oom" || kinds["WatchdogTermination"] != "watchdog" || kinds["SegFault"] != "crash" {
		t.Fatalf("displayed kinds = %v", kinds)
	}
	for in, want := range map[string]string{"kind:oom": "OutOfMemoryError in a.go f", "kind:watchdog": "WatchdogTermination in a.go f"} {
		q, err := search.Parse(in)
		if err != nil {
			t.Fatal(err)
		}
		out, err := e.q.Issues(ctx, e.scope, appquery.IssueQuery{Filter: full(), Search: &q})
		if err != nil || !reflect.DeepEqual(titles(out.Issues), []string{want}) {
			t.Fatalf("%s = %v %v", in, titles(out.Issues), err)
		}
	}
	ids, err := e.q.MatchIssues(ctx, e.scope, appquery.IssueQuery{Filter: full(), Kind: "oom"}, 10)
	if err != nil || len(ids) != 1 || ids[0] != e.f.Issues["fp-oom"] {
		t.Fatalf("MatchIssues oom = %v %v", ids, err)
	}
}
