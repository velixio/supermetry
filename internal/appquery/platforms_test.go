package appquery_test

import (
	"context"
	"reflect"
	"testing"
)

// Hand-computed from the fixture table in internal/appfixture: linux has the
// non-session events e1 (1 crash), e3 (2 crashes), e5, e10 (3), e11 (fatal),
// so 8 events and 4 crashes; installs A and B, A crashed; sessions a1 b1 b2
// with a1 crashed. macos has e7 (crash) and e12: 2 events, 1 crash; install C
// crashed; sessions c1 c2 with c1 crashed.
func TestPlatformBreakdownHandComputed(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	// e1 e3 resolved, e5 missing symbols, e7 partially resolved; the session
	// pings are never counted.
	for _, c := range []struct{ id, state string }{{"e1", "ok"}, {"e3", "ok"}, {"e5", "missing"}, {"e7", "partial"}, {"e4", "missing"}} {
		if _, err := e.f.Pool.Exec(ctx, `update app_events set symbolication=$2 where site_id=$3 and event_id=$1`, c.id, c.state, e.f.SiteID); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := e.q.PlatformBreakdown(ctx, e.scope, full())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Platform != "linux" || rows[1].Platform != "macos" {
		t.Fatalf("rows = %+v", rows)
	}
	l, m := rows[0], rows[1]
	if l.Events != 8 || l.Crashes != 4 || l.Symbolicated != 2 || l.Missing != 1 {
		t.Fatalf("linux = %+v", l)
	}
	near(t, "linux crash-free users", l.CrashFreeUsers, 0.5)
	near(t, "linux crash-free sessions", l.CrashFreeSessions, 2.0/3)
	if m.Events != 2 || m.Crashes != 1 || m.Symbolicated != 0 || m.Missing != 1 {
		t.Fatalf("macos = %+v", m)
	}
	near(t, "macos crash-free users", m.CrashFreeUsers, 0)
	near(t, "macos crash-free sessions", m.CrashFreeSessions, 0.5)

	f := full()
	f.Release = "1.10.0"
	rows, err = e.q.PlatformBreakdown(ctx, e.scope, f)
	if err != nil {
		t.Fatal(err)
	}
	// Release 1.10.0: linux e10 (3) + e11 (1) = 4 events, 1 crash; macos e7 + e12.
	got := [][2]int64{}
	for _, r := range rows {
		got = append(got, [2]int64{r.Events, r.Crashes})
	}
	if !reflect.DeepEqual(got, [][2]int64{{4, 1}, {2, 1}}) {
		t.Fatalf("release filter rows = %+v", rows)
	}
}
