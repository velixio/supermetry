package approllup_test

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"

	"supermetry/internal/appfixture"
	"supermetry/internal/approllup"
)

func day(s string) time.Time {
	d, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return d
}

func recomputeAll(t *testing.T, f *appfixture.Fixture) {
	t.Helper()
	for _, d := range []string{"2026-11-12", "2026-11-13", "2026-11-14", "2026-11-15"} {
		if err := approllup.RecomputeDay(context.Background(), f.Pool, f.SiteID, day(d)); err != nil {
			t.Fatal(err)
		}
	}
}

// dump returns "key=values" lines for a query, sorted.
func dump(t *testing.T, f *appfixture.Fixture, query string) []string {
	t.Helper()
	rows, err := f.Pool.Query(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			t.Fatal(err)
		}
		line := ""
		for i, v := range vals {
			if i > 0 {
				line += " "
			}
			switch x := v.(type) {
			case time.Time:
				line += x.UTC().Format("2006-01-02T15:04")
			default:
				line += fmt.Sprint(x)
			}
		}
		out = append(out, line)
	}
	sort.Strings(out)
	return out
}

const releaseDaily = `select day, release, platform, events, crashes, installs, crashed_installs, sessions, crashed_sessions from app_release_daily order by 1,2,3`

func TestRecomputeDayHandComputedRows(t *testing.T) {
	f := appfixture.New(t)
	recomputeAll(t, f)

	// day release platform events crashes installs crashedInstalls sessions crashedSessions
	wantRelease := []string{
		"2026-11-12T00:00 1.9.0 linux 1 1 1 1 0 0",
		"2026-11-14T00:00 1.10.0 macos 1 1 1 1 1 1", // e7 at 23:55 local; e6 ping
		"2026-11-14T00:00 1.9.0 linux 3 2 2 1 2 1",
		"2026-11-15T00:00 1.10.0 linux 4 1 2 1 1 0", // e11 crashed session a2 has no ping
		"2026-11-15T00:00 1.10.0 macos 1 0 1 0 1 0", // e8 at 00:05 local is already the 15th
	}
	got := dump(t, f, releaseDaily)
	sort.Strings(wantRelease)
	if !reflect.DeepEqual(got, wantRelease) {
		t.Fatalf("app_release_daily\n got %q\nwant %q", got, wantRelease)
	}

	i1, i2, i3 := f.Issues["I1"], f.Issues["I2"], f.Issues["I3"]
	wantIssue := []string{
		fmt.Sprintf("%d 2026-11-12 1 1 1", i1), fmt.Sprintf("%d 2026-11-14 2 1 2", i1), fmt.Sprintf("%d 2026-11-15 1 1 1", i1),
		fmt.Sprintf("%d 2026-11-14 1 1 0", i2), fmt.Sprintf("%d 2026-11-15 4 2 0", i2),
		fmt.Sprintf("%d 2026-11-14 1 1 1", i3),
	}
	sort.Strings(wantIssue)
	gotIssue := dump(t, f, `select issue_id, to_char(day,'YYYY-MM-DD'), events, installs, crashes from app_issue_daily`)
	if !reflect.DeepEqual(gotIssue, wantIssue) {
		t.Fatalf("app_issue_daily\n got %q\nwant %q", gotIssue, wantIssue)
	}

	wantHourly := []string{
		fmt.Sprintf("%d 2026-11-12T06:00 1", i1), fmt.Sprintf("%d 2026-11-14T02:00 2", i1), fmt.Sprintf("%d 2026-11-15T09:00 1", i1),
		fmt.Sprintf("%d 2026-11-14T03:00 1", i2), fmt.Sprintf("%d 2026-11-15T08:00 4", i2),
		fmt.Sprintf("%d 2026-11-14T18:00 1", i3),
	}
	sort.Strings(wantHourly)
	if gotHourly := dump(t, f, `select issue_id, hour, events from app_issue_hourly`); !reflect.DeepEqual(gotHourly, wantHourly) {
		t.Fatalf("app_issue_hourly\n got %q\nwant %q", gotHourly, wantHourly)
	}
}

func TestRecomputeDayIsIdempotentAndLateEventTouchesOnlyItsDay(t *testing.T) {
	f := appfixture.New(t)
	recomputeAll(t, f)
	before := dump(t, f, releaseDaily)
	beforeIssue := dump(t, f, `select * from app_issue_daily`)
	recomputeAll(t, f)
	if !reflect.DeepEqual(before, dump(t, f, releaseDaily)) || !reflect.DeepEqual(beforeIssue, dump(t, f, `select * from app_issue_daily`)) {
		t.Fatal("a second run changed the rows")
	}

	// A late event for local 2026-11-13 (a day with no rows yet).
	f.Insert(t, appfixture.Event{ID: "late", Kind: "crash", Level: "fatal", Release: "1.9.0", Platform: "linux", At: time.Date(2026, 11, 13, 5, 0, 0, 0, time.UTC), Install: "B", Session: "x", Issue: "I2"})
	if err := approllup.RecomputeDay(context.Background(), f.Pool, f.SiteID, day("2026-11-13")); err != nil {
		t.Fatal(err)
	}
	after := dump(t, f, releaseDaily)
	var added []string
	seen := map[string]bool{}
	for _, l := range before {
		seen[l] = true
	}
	for _, l := range after {
		if !seen[l] {
			added = append(added, l)
		}
	}
	want := []string{"2026-11-13T00:00 1.9.0 linux 1 1 1 1 0 0"}
	if len(after) != len(before)+1 || !reflect.DeepEqual(added, want) {
		t.Fatalf("late event changed %q (rows %d -> %d), want only %q", added, len(before), len(after), want)
	}
	// Recomputing the day again after deleting the event removes its rows.
	if _, err := f.Pool.Exec(context.Background(), `delete from app_events where event_id='late'`); err != nil {
		t.Fatal(err)
	}
	if err := approllup.RecomputeDay(context.Background(), f.Pool, f.SiteID, day("2026-11-13")); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, dump(t, f, releaseDaily)) {
		t.Fatal("recompute did not replace the day's rows")
	}
}

// A +05:45 site has no whole-hour day boundary: the UTC hour 18:00 straddles
// local midnight, so both neighbouring days must write the same full-hour count.
func TestHourlyRowsAreConsistentAcrossAHalfHourDayBoundary(t *testing.T) {
	f := appfixture.New(t)
	// Local 11-14 ends 18:15 UTC. e7 (18:10) is before, add one at 18:20 for the same issue.
	f.Insert(t, appfixture.Event{ID: "h", Kind: "crash", Level: "fatal", Release: "1.10.0", Platform: "macos", At: time.Date(2026, 11, 14, 18, 20, 0, 0, time.UTC), Install: "C", Issue: "I3"})
	for _, d := range []string{"2026-11-14", "2026-11-15", "2026-11-14"} {
		if err := approllup.RecomputeDay(context.Background(), f.Pool, f.SiteID, day(d)); err != nil {
			t.Fatal(err)
		}
	}
	got := dump(t, f, fmt.Sprintf(`select events from app_issue_hourly where issue_id=%d and hour='2026-11-14 18:00+00'`, f.Issues["I3"]))
	if !reflect.DeepEqual(got, []string{"2"}) {
		t.Fatalf("straddling hour = %q, want one row of 2", got)
	}
	days := dump(t, f, fmt.Sprintf(`select to_char(day,'YYYY-MM-DD'), events from app_issue_daily where issue_id=%d`, f.Issues["I3"]))
	if !reflect.DeepEqual(days, []string{"2026-11-14 1", "2026-11-15 1"}) {
		t.Fatalf("daily split = %q", days)
	}
}

func TestRecomputeRecentCoversTodayAndLookback(t *testing.T) {
	f := appfixture.New(t)
	// MaxReportAgeDays 3 reaches local 11-12 from local today 11-15.
	n, err := approllup.RecomputeRecent(context.Background(), f.Pool, []approllup.Target{{SiteID: f.SiteID, LookbackDays: 3}}, f.Now)
	if err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Fatalf("days recomputed = %d, want 4", n)
	}
	if got := dump(t, f, `select count(*) from app_release_daily`); got[0] != "5" {
		t.Fatalf("rows = %v", got)
	}
}

func TestRecomputeAllRebuildsEveryAppDay(t *testing.T) {
	f := appfixture.New(t)
	days, err := approllup.RecomputeAll(context.Background(), f.Pool, f.Now)
	if err != nil {
		t.Fatal(err)
	}
	if days != 4 { // 11-12 .. 11-15
		t.Fatalf("days = %d", days)
	}
	if got := dump(t, f, `select count(*) from app_issue_daily`); got[0] != "6" {
		t.Fatalf("issue daily rows = %v", got)
	}
}
