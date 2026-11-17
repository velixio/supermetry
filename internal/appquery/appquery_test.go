package appquery_test

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/appfixture"
	"supermetry/internal/appquery"
	"supermetry/internal/approllup"
)

func d(s string) time.Time {
	v, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return v
}

type env struct {
	f     *appfixture.Fixture
	q     *appquery.Querier
	scope appquery.Scope
}

func setup(t *testing.T) env {
	t.Helper()
	f := appfixture.New(t)
	for _, day := range []string{"2026-11-12", "2026-11-13", "2026-11-14", "2026-11-15"} {
		if err := approllup.RecomputeDay(context.Background(), f.Pool, f.SiteID, d(day)); err != nil {
			t.Fatal(err)
		}
	}
	return env{f: f, q: appquery.New(f.Pool, func() time.Time { return f.Now }), scope: appquery.Scope{SiteID: f.SiteID, TZ: f.Loc, RetentionDays: 90}}
}

func full() appquery.Filter { return appquery.Filter{From: d("2026-11-12"), To: d("2026-11-15")} }

func near(t *testing.T, name string, got *float64, want float64) {
	t.Helper()
	if got == nil || math.Abs(*got-want) > 1e-9 {
		t.Errorf("%s = %v, want %v", name, deref(got), want)
	}
}

func isNil(t *testing.T, name string, got *float64) {
	t.Helper()
	if got != nil {
		t.Errorf("%s = %v, want null", name, *got)
	}
}

func deref(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

func TestOverviewHandComputed(t *testing.T) {
	e := setup(t)
	ov, err := e.q.Overview(context.Background(), e.scope, full())
	if err != nil {
		t.Fatal(err)
	}
	// 10 events, 5 crashes (e1 1, e3 2, e7 1, e11 1); installs A B C, crashed A C;
	// session pings a1 b1 c1 c2 b2, crashed a1 c1.
	if ov.Events != 10 || ov.Crashes != 5 || ov.NewIssues != 3 || ov.RegressedIssues != 1 {
		t.Fatalf("totals = %+v", ov)
	}
	near(t, "crash_free_users", ov.CrashFreeUsers, 1.0/3)
	near(t, "crash_free_sessions", ov.CrashFreeSessions, 0.6)
	if len(ov.Series) != 4 {
		t.Fatalf("series = %+v", ov.Series)
	}
	type pt struct {
		day             string
		events, crashes int64
		cfu             float64
		null            bool
	}
	for i, want := range []pt{
		{"2026-11-12", 1, 1, 0, false},
		{"2026-11-13", 0, 0, 0, true},
		{"2026-11-14", 4, 3, 1.0 / 3, false}, // installs 2+1, crashed 1+1, summed over (release, platform)
		{"2026-11-15", 5, 1, 2.0 / 3, false}, // installs 1+2, crashed 0+1
	} {
		got := ov.Series[i]
		if got.Day != want.day || got.Events != want.events || got.Crashes != want.crashes {
			t.Errorf("series[%d] = %+v, want %+v", i, got, want)
		}
		if want.null {
			isNil(t, want.day+" cfu", got.CrashFreeUsers)
		} else {
			near(t, want.day+" cfu", got.CrashFreeUsers, want.cfu)
		}
	}
}

func TestOverviewNarrowRangeAndSiteLocalDayBucketing(t *testing.T) {
	e := setup(t)
	ov, err := e.q.Overview(context.Background(), e.scope, appquery.Filter{From: d("2026-11-14"), To: d("2026-11-14")})
	if err != nil {
		t.Fatal(err)
	}
	// Local 11-14 holds e2-e7 (e7 at 23:55 local); e8 (18:20 UTC) is local 11-15
	// and must not count: UTC bucketing would add session c2 and give 0.25.
	if ov.Events != 4 || ov.Crashes != 3 || ov.NewIssues != 2 || ov.RegressedIssues != 0 { // I1 is regressed but last seen on the 15th
		t.Fatalf("totals = %+v", ov)
	}
	near(t, "cfu", ov.CrashFreeUsers, 1.0/3) // installs A B C, crashed A C
	near(t, "cfs", ov.CrashFreeSessions, 1.0/3)
	if len(ov.Series) != 1 || ov.Series[0].Day != "2026-11-14" {
		t.Fatalf("series = %+v", ov.Series)
	}
}

func TestOverviewFiltersAndNullCases(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	f := full()
	f.Release = "1.9.0"
	ov, err := e.q.Overview(ctx, e.scope, f)
	if err != nil {
		t.Fatal(err)
	}
	near(t, "1.9.0 cfu", ov.CrashFreeUsers, 0.5)
	near(t, "1.9.0 cfs", ov.CrashFreeSessions, 0.5)
	if ov.Events != 4 || ov.Crashes != 3 || ov.NewIssues != 2 || ov.RegressedIssues != 0 {
		t.Fatalf("1.9.0 = %+v", ov)
	}
	f.Release = "1.10.0"
	ov, _ = e.q.Overview(ctx, e.scope, f)
	near(t, "1.10.0 cfu", ov.CrashFreeUsers, 1.0/3)
	near(t, "1.10.0 cfs", ov.CrashFreeSessions, 2.0/3)
	if ov.NewIssues != 1 || ov.RegressedIssues != 1 {
		t.Fatalf("1.10.0 = %+v", ov)
	}

	f = full()
	f.Platform = "windows"
	ov, _ = e.q.Overview(ctx, e.scope, f)
	isNil(t, "no installs cfu", ov.CrashFreeUsers)
	isNil(t, "no sessions cfs", ov.CrashFreeSessions)
	if ov.Events != 0 || len(ov.Series) != 4 {
		t.Fatalf("windows = %+v", ov)
	}

	// environment is not a rollup dimension: the series is bucketed from raw events.
	f = full()
	f.Environment = "beta"
	ov, _ = e.q.Overview(ctx, e.scope, f)
	near(t, "beta cfu", ov.CrashFreeUsers, 1)
	isNil(t, "beta has no session pings", ov.CrashFreeSessions)
	if ov.Events != 3 || ov.Crashes != 0 {
		t.Fatalf("beta = %+v", ov)
	}
	if got := ov.Series[3]; got.Day != "2026-11-15" || got.Events != 3 || got.Crashes != 0 {
		t.Fatalf("beta series = %+v", ov.Series)
	}
	near(t, "beta series cfu", ov.Series[3].CrashFreeUsers, 1)
	isNil(t, "beta quiet day", ov.Series[0].CrashFreeUsers)
}

func TestReadsNeverSeeEventsOlderThanTheSiteRetention(t *testing.T) {
	e := setup(t)
	scope := e.scope
	scope.RetentionDays = 2 // cutoff 2026-11-13 10:00 UTC: e1 (11-12 06:00) is gone
	wide := appquery.Filter{From: d("2026-10-01"), To: d("2026-11-15")}
	ov, err := e.q.Overview(context.Background(), scope, wide)
	if err != nil {
		t.Fatal(err)
	}
	if ov.Events != 9 || ov.Crashes != 4 || ov.NewIssues != 2 {
		t.Fatalf("overview = %+v", ov)
	}
	list, err := e.q.Issues(context.Background(), scope, appquery.IssueQuery{Filter: wide})
	if err != nil {
		t.Fatal(err)
	}
	if list.Issues[0].Title != "SegFault in a.go f" || list.Issues[0].Events != 3 {
		t.Fatalf("first issue = %+v", list.Issues[0])
	}
	rels, _ := e.q.Releases(context.Background(), scope, wide)
	for _, r := range rels {
		if r.Release == "1.9.0" && r.FirstSeen.Before(time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)) {
			t.Fatalf("release first seen reaches behind retention: %v", r.FirstSeen)
		}
	}
	if len(ov.Series) > 366 {
		t.Fatal("series too long")
	}
}

func TestRangeLimits(t *testing.T) {
	e := setup(t)
	_, err := e.q.Overview(context.Background(), e.scope, appquery.Filter{From: d("2025-01-01"), To: d("2026-11-15")})
	if !errors.Is(err, appquery.ErrRange) {
		t.Fatalf("err = %v, want ErrRange", err)
	}
	_, err = e.q.Overview(context.Background(), e.scope, appquery.Filter{From: d("2026-11-15"), To: d("2026-11-14")})
	if !errors.Is(err, appquery.ErrRange) {
		t.Fatalf("descending range err = %v", err)
	}
}

func titles(rows []appquery.IssueRow) []string {
	var out []string
	for _, r := range rows {
		out = append(out, r.Title)
	}
	return out
}

func TestIssuesListFiltersSortAndPaging(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	q := func(mut func(*appquery.IssueQuery)) appquery.IssueList {
		in := appquery.IssueQuery{Filter: full()}
		mut(&in)
		out, err := e.q.Issues(ctx, e.scope, in)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	const i1, i2, i3 = "SegFault in a.go f", "ValueError in a.go f", "Panic in a.go f"

	out := q(func(*appquery.IssueQuery) {})
	if !reflect.DeepEqual(titles(out.Issues), []string{i1, i2, i3}) || out.Total != 3 || out.NextOffset != nil {
		t.Fatalf("default = %v total %d", titles(out.Issues), out.Total)
	}
	row := out.Issues[1] // I2
	if row.Events != 5 || row.Users != 2 || row.Status != "open" || row.Kind != "error" || row.ErrorType != "ValueError" ||
		row.FirstRelease != "1.9.0" || row.LastRelease != "1.10.0" || !reflect.DeepEqual(row.Sparkline, []int64{0, 0, 1, 4}) {
		t.Fatalf("I2 row = %+v", row)
	}
	if got := out.Issues[0]; got.Events != 4 || got.Users != 1 || !reflect.DeepEqual(got.Sparkline, []int64{1, 0, 2, 1}) {
		t.Fatalf("I1 row = %+v", got)
	}
	if out := q(func(in *appquery.IssueQuery) { in.Sort = "events" }); !reflect.DeepEqual(titles(out.Issues), []string{i2, i1, i3}) {
		t.Fatalf("by events = %v", titles(out.Issues))
	}
	if out := q(func(in *appquery.IssueQuery) { in.Sort = "users" }); !reflect.DeepEqual(titles(out.Issues), []string{i2, i1, i3}) {
		t.Fatalf("by users = %v", titles(out.Issues))
	}
	if out := q(func(in *appquery.IssueQuery) { in.Status = "regressed" }); !reflect.DeepEqual(titles(out.Issues), []string{i1}) {
		t.Fatalf("status = %v", titles(out.Issues))
	}
	if all, none := q(func(in *appquery.IssueQuery) { in.Status = "all" }), q(func(in *appquery.IssueQuery) {}); !reflect.DeepEqual(titles(all.Issues), titles(none.Issues)) || len(all.Issues) == 0 {
		t.Fatalf("status=all = %v, want same as no filter %v", titles(all.Issues), titles(none.Issues))
	}
	if out := q(func(in *appquery.IssueQuery) { in.Kind = "error" }); !reflect.DeepEqual(titles(out.Issues), []string{i2}) {
		t.Fatalf("kind = %v", titles(out.Issues))
	}
	if out := q(func(in *appquery.IssueQuery) { in.Q = "seg" }); !reflect.DeepEqual(titles(out.Issues), []string{i1}) {
		t.Fatalf("q title prefix = %v", titles(out.Issues))
	}
	if out := q(func(in *appquery.IssueQuery) { in.Q = "ValueE" }); !reflect.DeepEqual(titles(out.Issues), []string{i2}) {
		t.Fatalf("q error_type prefix = %v", titles(out.Issues))
	}
	if out := q(func(in *appquery.IssueQuery) { in.Q = "%" }); len(out.Issues) != 0 {
		t.Fatalf("a literal percent must not match everything: %v", titles(out.Issues))
	}
	if out := q(func(in *appquery.IssueQuery) { in.Release = "1.9.0" }); !reflect.DeepEqual(titles(out.Issues), []string{i1, i2}) || out.Issues[0].Events != 3 {
		t.Fatalf("release filter = %+v", out.Issues)
	}
	page1 := q(func(in *appquery.IssueQuery) { in.Limit = 2 })
	if !reflect.DeepEqual(titles(page1.Issues), []string{i1, i2}) || page1.Total != 3 || page1.NextOffset == nil || *page1.NextOffset != 2 {
		t.Fatalf("page 1 = %v next %v", titles(page1.Issues), page1.NextOffset)
	}
	page2 := q(func(in *appquery.IssueQuery) { in.Limit = 2; in.Offset = 2 })
	if !reflect.DeepEqual(titles(page2.Issues), []string{i3}) || page2.NextOffset != nil {
		t.Fatalf("page 2 = %v", titles(page2.Issues))
	}
	if _, err := e.q.Issues(ctx, e.scope, appquery.IssueQuery{Filter: full(), Limit: 101}); !errors.Is(err, appquery.ErrInvalid) {
		t.Fatalf("limit 101 err = %v", err)
	}
	if _, err := e.q.Issues(ctx, e.scope, appquery.IssueQuery{Filter: full(), Sort: "bogus"}); !errors.Is(err, appquery.ErrInvalid) {
		t.Fatalf("bad sort err = %v", err)
	}
	if _, err := e.q.Issues(ctx, e.scope, appquery.IssueQuery{Filter: full(), Status: "bogus"}); !errors.Is(err, appquery.ErrInvalid) {
		t.Fatalf("bad status err = %v", err)
	}
}

func TestIssueDetail(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	det, err := e.q.Issue(ctx, e.scope, e.f.Issues["I2"], full())
	if err != nil {
		t.Fatal(err)
	}
	if det.ID != e.f.Issues["I2"] || det.Status != "open" || det.Title != "ValueError in a.go f" || det.EventCount != 5 {
		t.Fatalf("issue = %+v", det)
	}
	wantSeries := []appquery.IssueDay{iday("2026-11-12", 0, 0), iday("2026-11-13", 0, 0), iday("2026-11-14", 1, 1), iday("2026-11-15", 4, 2)}
	if !reflect.DeepEqual(det.Series, wantSeries) {
		t.Fatalf("series = %+v", det.Series)
	}
	bd := func(dim string) []appquery.Breakdown { return det.Breakdowns[dim] }
	want := map[string][]appquery.Breakdown{
		"release":      {b("1.10.0", 4, 2), b("1.9.0", 1, 1)},
		"platform":     {b("linux", 4, 1), b("macos", 1, 1)},
		"os_version":   {b("24.04", 4, 1), b("14.5", 1, 1)},
		"arch":         {b("x64", 4, 1), b("arm64", 1, 1)},
		"device_class": {b("cpu", 5, 2)},
		"environment":  {b("beta", 3, 1), b("production", 2, 2)},
		"tag:gpu":      {b("x", 5, 2)},
	}
	for dim, w := range want {
		if !reflect.DeepEqual(bd(dim), w) {
			t.Errorf("breakdown %s = %+v, want %+v", dim, bd(dim), w)
		}
	}
	if len(det.LatestEvents) != 3 {
		t.Fatalf("latest = %d", len(det.LatestEvents))
	}
	if first := det.LatestEvents[0]; !first.TS.Equal(time.Date(2026, 11, 15, 8, 45, 0, 0, time.UTC)) || first.Release != "1.10.0" || first.Platform != "macos" ||
		first.OSVersion != "14.5" || first.Arch != "arm64" || first.DeviceClass != "cpu" || first.Runtime != "electron" || first.Frames != "a.go:1 f" || first.Tags["gpu"] != "x" {
		t.Fatalf("latest[0] = %+v", first)
	}
	raw, _ := json.Marshal(det)
	for _, banned := range []string{"hash", "install_", "session_"} {
		if strings.Contains(strings.ToLower(string(raw)), banned) {
			t.Fatalf("issue detail JSON mentions %q: %s", banned, raw)
		}
	}
	if _, err := e.q.Issue(ctx, e.scope, 999999, full()); !errors.Is(err, appquery.ErrNotFound) {
		t.Fatalf("missing issue err = %v", err)
	}
	other := e.scope
	other.SiteID = 8
	if _, err := e.q.Issue(ctx, other, e.f.Issues["I2"], full()); !errors.Is(err, appquery.ErrNotFound) {
		t.Fatalf("issue of another site err = %v", err)
	}
}

func TestCompatibility(t *testing.T) {
	e := setup(t)
	rows, err := e.q.Compatibility(context.Background(), e.scope, full())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %+v", rows)
	}
	r1, r3, r2 := rows[0], rows[1], rows[2] // by crashes, descending: 4, 1, 0
	if r1.Platform != "linux" || r1.OSVersion != "22.04" || r1.Arch != "x64" || r1.DeviceClass != "cuda" || r1.Events != 4 || r1.Crashes != 4 || r1.Installs != 1 {
		t.Fatalf("linux 22.04 = %+v", r1)
	}
	near(t, "linux 22.04 cfu", r1.CrashFreeUsers, 0)
	if len(r1.TopIssues) != 1 || r1.TopIssues[0].Title != "SegFault in a.go f" || r1.TopIssues[0].Events != 4 || r1.TopIssues[0].ID != e.f.Issues["I1"] {
		t.Fatalf("linux 22.04 top = %+v", r1.TopIssues)
	}
	if r3.Platform != "macos" || r3.OSVersion != "14.5" || r3.Arch != "arm64" || r3.Events != 2 || r3.Crashes != 1 || r3.Installs != 1 {
		t.Fatalf("macos = %+v", r3)
	}
	near(t, "macos cfu", r3.CrashFreeUsers, 0)
	if len(r3.TopIssues) != 2 || r3.TopIssues[0].ID != e.f.Issues["I2"] || r3.TopIssues[1].ID != e.f.Issues["I3"] {
		t.Fatalf("macos top (ties by issue id) = %+v", r3.TopIssues)
	}
	if r2.OSVersion != "24.04" || r2.Events != 4 || r2.Crashes != 0 || r2.Installs != 1 {
		t.Fatalf("linux 24.04 = %+v", r2)
	}
	near(t, "linux 24.04 cfu", r2.CrashFreeUsers, 1)
}

func TestReleases(t *testing.T) {
	e := setup(t)
	rows, err := e.q.Releases(context.Background(), e.scope, full())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Release != "1.10.0" || rows[1].Release != "1.9.0" {
		t.Fatalf("order = %+v", rows)
	}
	n, o := rows[0], rows[1]
	if !n.FirstSeen.Equal(time.Date(2026, 11, 14, 4, 0, 0, 0, time.UTC)) || !n.LastSeen.Equal(time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)) || n.Installs != 3 {
		t.Fatalf("1.10.0 = %+v", n)
	}
	near(t, "1.10.0 cfu", n.CrashFreeUsers, 1.0/3)
	near(t, "1.10.0 cfs", n.CrashFreeSessions, 2.0/3)
	if n.NewIssues != 1 || n.RegressedIssues != 1 {
		t.Fatalf("1.10.0 issues = %+v", n)
	}
	if !o.FirstSeen.Equal(time.Date(2026, 11, 12, 6, 0, 0, 0, time.UTC)) || !o.LastSeen.Equal(time.Date(2026, 11, 14, 3, 5, 0, 0, time.UTC)) || o.Installs != 2 {
		t.Fatalf("1.9.0 = %+v", o)
	}
	near(t, "1.9.0 cfu", o.CrashFreeUsers, 0.5)
	near(t, "1.9.0 cfs", o.CrashFreeSessions, 0.5)
	if o.NewIssues != 2 || o.RegressedIssues != 0 {
		t.Fatalf("1.9.0 issues = %+v", o)
	}
}

func iday(day string, events, installs int64) appquery.IssueDay {
	return appquery.IssueDay{Day: day, Events: events, Installs: installs}
}

func b(value string, events, users int64) appquery.Breakdown {
	return appquery.Breakdown{Value: value, Events: events, Users: users}
}

func str(s string) *string { return &s }

func setStatus(t *testing.T, e env, issue int64, in appquery.StatusInput) (appquery.StatusResult, error) {
	t.Helper()
	var out appquery.StatusResult
	err := pgx.BeginFunc(context.Background(), e.f.Pool, func(tx pgx.Tx) error {
		var err error
		out, err = e.q.SetStatus(context.Background(), tx, e.scope, issue, in)
		return err
	})
	return out, err
}

func TestSetStatus(t *testing.T) {
	e := setup(t)
	i2 := e.f.Issues["I2"]
	got, err := setStatus(t, e, i2, appquery.StatusInput{Status: "resolved", ResolvedInRelease: str("next")})
	if err != nil {
		t.Fatal(err)
	}
	// Newest release seen is 1.10.0 by semver (a string sort would say 1.9.0).
	if got.Status != "resolved" || got.ResolvedInRelease == nil || *got.ResolvedInRelease != "1.10.0" || !got.ResolvedNext || got.Previous != "open" {
		t.Fatalf("resolve next = %+v", got)
	}
	got, err = setStatus(t, e, i2, appquery.StatusInput{Status: "resolved", ResolvedInRelease: str("2.0.0")})
	if err != nil || got.ResolvedNext || *got.ResolvedInRelease != "2.0.0" || got.Previous != "resolved" {
		t.Fatalf("resolve explicit = %+v %v", got, err)
	}
	got, err = setStatus(t, e, i2, appquery.StatusInput{Status: "ignored"})
	if err != nil || got.ResolvedInRelease != nil || got.ResolvedNext {
		t.Fatalf("ignore must clear the fix release: %+v %v", got, err)
	}
	if got, err = setStatus(t, e, i2, appquery.StatusInput{Status: "open"}); err != nil || got.Status != "open" {
		t.Fatalf("reopen = %+v %v", got, err)
	}
	var stored string
	if err := e.f.Pool.QueryRow(context.Background(), `select status from app_issues where id=$1`, i2).Scan(&stored); err != nil || stored != "open" {
		t.Fatalf("stored = %q %v", stored, err)
	}
	for _, bad := range []appquery.StatusInput{
		{Status: "regressed"}, {Status: "bogus"}, {Status: "open", ResolvedInRelease: str("1.0.0")}, {Status: "resolved", ResolvedInRelease: str(strings.Repeat("x", 65))},
	} {
		if _, err := setStatus(t, e, i2, bad); !errors.Is(err, appquery.ErrInvalid) {
			t.Errorf("%+v err = %v, want ErrInvalid", bad, err)
		}
	}
	if _, err := setStatus(t, e, 999999, appquery.StatusInput{Status: "open"}); !errors.Is(err, appquery.ErrNotFound) {
		t.Fatalf("unknown issue err = %v", err)
	}
	// An issue of another site is not found even by id.
	e.scope.SiteID = 8
	if _, err := setStatus(t, e, i2, appquery.StatusInput{Status: "open"}); !errors.Is(err, appquery.ErrNotFound) {
		t.Fatalf("foreign issue err = %v", err)
	}
}

func TestResolveNextWithoutAnyReleaseSeenStoresNone(t *testing.T) {
	e := setup(t)
	if _, err := e.f.Pool.Exec(context.Background(), `delete from app_events; update app_issues set last_release=''`); err != nil {
		t.Fatal(err)
	}
	got, err := setStatus(t, e, e.f.Issues["I2"], appquery.StatusInput{Status: "resolved", ResolvedInRelease: str("next")})
	if err != nil || got.ResolvedInRelease != nil || got.ResolvedNext || got.Status != "resolved" {
		t.Fatalf("got %+v %v", got, err)
	}
}

// insertSession writes a session row the way the Sentry front door does:
// an init row carries a session hash, an aggregate row carries none and
// stands for occ sessions.
func insertSession(t *testing.T, e env, id, release, exit string, hash []byte, occ int) {
	t.Helper()
	at := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	if _, err := e.f.Pool.Exec(context.Background(), `insert into app_events(site_id,ts,received_at,event_id,kind,level,release,environment,platform,occurrences,session_hash,exit_reason)
values($1,$2,$2,$3,'session','error',$4,'production','web',$5,$6,$7)`, e.f.SiteID, at, id, release, occ, hash, exit); err != nil {
		t.Fatal(err)
	}
}

func TestCrashFreeSessionsSumsOccurrencesOfSessionRows(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	// 9.8.0: aggregates only, exited 3, errored 1, crashed 1 -> 4 of 5 healthy.
	insertSession(t, e, "agg-exited", "9.8.0", "", nil, 3)
	insertSession(t, e, "agg-errored", "9.8.0", "", nil, 1)
	insertSession(t, e, "agg-crashed", "9.8.0", "crashed", nil, 1)
	// 9.9.0 mixes everything: v1 pings s1 (crashes) and s2, one session init
	// row s3 (also sent twice: a retry), and the same aggregates as above
	// (8 sessions, 2 crashed -> 75%).
	e.f.Insert(t, appfixture.Event{ID: "v1-s1", Kind: "session", Release: "9.9.0", Platform: "web", At: time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC), Session: "s1", Install: "i1"})
	e.f.Insert(t, appfixture.Event{ID: "v1-s2", Kind: "session", Release: "9.9.0", Platform: "web", At: time.Date(2026, 11, 14, 9, 1, 0, 0, time.UTC), Session: "s2", Install: "i2"})
	e.f.Insert(t, appfixture.Event{ID: "v1-crash", Kind: "crash", Release: "9.9.0", Platform: "web", At: time.Date(2026, 11, 14, 9, 2, 0, 0, time.UTC), Session: "s1", Install: "i1"})
	insertSession(t, e, "init-s3", "9.9.0", "", appfixture.Hash("s3"), 1)
	insertSession(t, e, "init-s3-retry", "9.9.0", "", appfixture.Hash("s3"), 1)
	insertSession(t, e, "agg9-exited", "9.9.0", "", nil, 3)
	insertSession(t, e, "agg9-errored", "9.9.0", "", nil, 1)
	insertSession(t, e, "agg9-crashed", "9.9.0", "crashed", nil, 1)

	for release, want := range map[string]float64{"9.8.0": 0.8, "9.9.0": 0.75} {
		f := full()
		f.Release = release
		ov, err := e.q.Overview(ctx, e.scope, f)
		if err != nil {
			t.Fatal(err)
		}
		near(t, release+" crash-free sessions", ov.CrashFreeSessions, want)
		rel, err := e.q.Releases(ctx, e.scope, f)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, r := range rel {
			if r.Release == release {
				near(t, release+" release row", r.CrashFreeSessions, want)
				found = true
			}
		}
		if !found {
			t.Errorf("release %s missing from %+v", release, rel)
		}
	}
	// The rollup must agree with the query.
	if err := approllup.RecomputeDay(ctx, e.f.Pool, e.f.SiteID, d("2026-11-14")); err != nil {
		t.Fatal(err)
	}
	var sessions, crashed int64
	if err := e.f.Pool.QueryRow(ctx, `select sum(sessions), sum(crashed_sessions) from app_release_daily where release = '9.9.0'`).Scan(&sessions, &crashed); err != nil {
		t.Fatal(err)
	}
	if sessions != 8 || crashed != 2 {
		t.Errorf("rollup sessions=%d crashed=%d, want 8 and 2", sessions, crashed)
	}
}
