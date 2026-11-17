package appissues_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"
	"time"

	"supermetry/internal/appissues"
	"supermetry/internal/approllup"
	"supermetry/internal/db"
)

var day1 = time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC)

// mergeFixture adds, on site 7, issues a, b and c with events on two days.
type mergeFixture struct {
	fixture
	a, b, c int64
}

func hashOf(fp string) []byte {
	s := sha256.Sum256([]byte(fp))
	return s[:]
}

func (f fixture) addIssue(t *testing.T, fp string) int64 {
	t.Helper()
	var id int64
	if err := f.pool.QueryRow(context.Background(), `insert into app_issues(site_id,fingerprint,kind,title,first_seen,last_seen,event_count)
values (7,$1,'crash','T '||$1,'2026-11-10','2026-11-11',0) returning id`, fp).Scan(&id); err != nil {
		t.Fatal(err)
	}
	h := hashOf(fp)
	if _, err := f.pool.Exec(context.Background(), `insert into app_issue_hashes(site_id,hash,grouping_version,issue_id) values (7,$1,1,$2)`, h, id); err != nil {
		t.Fatal(err)
	}
	return id
}

// addEvent stores an event and keeps the issue's event_count in step.
func (f fixture) addEvent(t *testing.T, issue int64, fp string, ts time.Time, occ int, install string) {
	t.Helper()
	ctx := context.Background()
	if _, err := f.pool.Exec(ctx, `insert into app_events(site_id,ts,received_at,event_id,issue_id,kind,fingerprint,occurrences,install_hash)
values (7,$1,$1,$2,$3,'crash',$4,$5,$6)`, ts, fmt.Sprintf("e-%d-%s", ts.UnixNano(), install), issue, fp, occ, []byte(install)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `update app_issues set event_count = event_count + $2 where id = $1`, issue, occ); err != nil {
		t.Fatal(err)
	}
}

func newMergeFixture(t *testing.T) mergeFixture {
	t.Helper()
	f := mergeFixture{fixture: newFixture(t)}
	if err := db.EnsureAppEventPartitions(context.Background(), f.pool, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	f.a, f.b, f.c = f.addIssue(t, "fp-a"), f.addIssue(t, "fp-b"), f.addIssue(t, "fp-c")
	for _, e := range []struct {
		id  int64
		fp  string
		ts  time.Time
		occ int
		ins string
	}{
		{f.a, "fp-a", day1, 2, "i1"}, {f.a, "fp-a", day1.Add(26 * time.Hour), 3, "i2"},
		{f.b, "fp-b", day1.Add(time.Hour), 4, "i1"}, {f.b, "fp-b", day1.Add(48 * time.Hour), 1, "i3"},
		{f.c, "fp-c", day1.Add(2 * time.Hour), 5, "i4"},
	} {
		f.addEvent(t, e.id, e.fp, e.ts, e.occ, e.ins)
	}
	f.rollup(t)
	return f
}

// rollup rebuilds the site's rollups the way the scheduler job does.
func (f fixture) rollup(t *testing.T) {
	t.Helper()
	for d := range 5 {
		if err := approllup.RecomputeDay(context.Background(), f.pool, 7, day1.AddDate(0, 0, d)); err != nil {
			t.Fatal(err)
		}
	}
}

type snapshot struct {
	count  int64
	first  time.Time
	last   time.Time
	daily  map[string][3]int64
	hourly map[string]int64
	merged *int64
}

func (f fixture) snap(t *testing.T, issue int64) snapshot {
	t.Helper()
	ctx := context.Background()
	s := snapshot{daily: map[string][3]int64{}, hourly: map[string]int64{}}
	if err := f.pool.QueryRow(ctx, `select event_count, first_seen, last_seen, merged_into from app_issues where id = $1`, issue).Scan(&s.count, &s.first, &s.last, &s.merged); err != nil {
		t.Fatal(err)
	}
	rows, err := f.pool.Query(ctx, `select day::text, events, installs, crashes from app_issue_daily where issue_id = $1`, issue)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var d string
		var v [3]int64
		if err := rows.Scan(&d, &v[0], &v[1], &v[2]); err != nil {
			t.Fatal(err)
		}
		s.daily[d] = v
	}
	rows.Close()
	rows, err = f.pool.Query(ctx, `select hour::text, events from app_issue_hourly where issue_id = $1`, issue)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var h string
		var n int64
		if err := rows.Scan(&h, &n); err != nil {
			t.Fatal(err)
		}
		s.hourly[h] = n
	}
	rows.Close()
	return s
}

func eventsOf(t *testing.T, f fixture, issue int64) int64 {
	t.Helper()
	var n int64
	if err := f.pool.QueryRow(context.Background(), `select coalesce(sum(occurrences),0) from app_events where issue_id=$1`, issue).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func sumDaily(parts ...snapshot) map[string]int64 {
	out := map[string]int64{}
	for _, p := range parts {
		for d, v := range p.daily {
			out[d] += v[0]
		}
	}
	return out
}

func TestMergeSumsCountsAndRollupsAndHidesSources(t *testing.T) {
	f := newMergeFixture(t)
	ctx := context.Background()
	a0, b0 := f.snap(t, f.a), f.snap(t, f.b)
	if err := f.svc.Merge(ctx, 7, f.b, []int64{f.a}, f.actor); err != nil {
		t.Fatal(err)
	}
	a1, b1 := f.snap(t, f.a), f.snap(t, f.b)
	if b1.count != a0.count+b0.count || b1.count != eventsOf(t, f.fixture, f.b) {
		t.Fatalf("target count %d, want %d (events %d)", b1.count, a0.count+b0.count, eventsOf(t, f.fixture, f.b))
	}
	want := sumDaily(a0, b0)
	for d, n := range sumDaily(b1) {
		if want[d] != n {
			t.Errorf("day %s = %d, want %d", d, n, want[d])
		}
	}
	if len(b1.daily) != len(want) {
		t.Errorf("days = %v, want %v", b1.daily, want)
	}
	var hourly int64
	for _, n := range b1.hourly {
		hourly += n
	}
	if hourly != b1.count {
		t.Errorf("hourly sum %d, want %d", hourly, b1.count)
	}
	// Installs are distinct per day, not summed: i1 hit both issues on day one.
	if got := b1.daily[day1.Format("2006-01-02")][1]; got != 1 {
		t.Errorf("installs on day one = %d, want 1 distinct", got)
	}
	if a1.merged == nil || *a1.merged != f.b || len(a1.daily)+len(a1.hourly) != 0 {
		t.Errorf("source not folded: %+v", a1)
	}
	var hashTarget int64
	if err := f.pool.QueryRow(ctx, `select issue_id from app_issue_hashes where hash = $1`, hashOf("fp-a")).Scan(&hashTarget); err != nil || hashTarget != f.b {
		t.Errorf("hash points at %d (%v), want %d", hashTarget, err, f.b)
	}
	if !b1.first.Equal(minTime(a0.first, b0.first)) {
		t.Errorf("first_seen %v", b1.first)
	}
	if k := f.activity(t, f.b); k[len(k)-1] != appissues.KindMerged {
		t.Errorf("target activity = %v", k)
	}
	if k := f.activity(t, f.a); len(k) == 0 || k[len(k)-1] != appissues.KindMerged {
		t.Errorf("source activity = %v", k)
	}
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func TestMergeChainLeavesEverySourcePointingAtTheFinalTarget(t *testing.T) {
	f := newMergeFixture(t)
	ctx := context.Background()
	if err := f.svc.Merge(ctx, 7, f.b, []int64{f.a}, f.actor); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Merge(ctx, 7, f.c, []int64{f.b}, f.actor); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{f.a, f.b} {
		if s := f.snap(t, id); s.merged == nil || *s.merged != f.c {
			t.Errorf("issue %d merged_into = %v, want %d", id, s.merged, f.c)
		}
	}
	if got := f.snap(t, f.c).count; got != 2+3+4+1+5 {
		t.Errorf("target count = %d", got)
	}
	var n int
	if err := f.pool.QueryRow(ctx, `select count(*) from app_issue_hashes where issue_id = $1`, f.c).Scan(&n); err != nil || n != 3 {
		t.Errorf("hashes on target = %d %v", n, err)
	}
	// A merged issue cannot be merged again or be a target.
	if err := f.svc.Merge(ctx, 7, f.c, []int64{f.a}, f.actor); !errors.Is(err, appissues.ErrInvalid) {
		t.Errorf("re-merge err = %v", err)
	}
	if err := f.svc.Merge(ctx, 7, f.a, []int64{f.c}, f.actor); !errors.Is(err, appissues.ErrInvalid) {
		t.Errorf("merged target err = %v", err)
	}
}

func TestMergeRejectsSelfCrossSiteAndEmpty(t *testing.T) {
	f := newMergeFixture(t)
	ctx := context.Background()
	if err := f.svc.Merge(ctx, 7, f.a, []int64{f.a}, f.actor); !errors.Is(err, appissues.ErrInvalid) {
		t.Errorf("self = %v", err)
	}
	if err := f.svc.Merge(ctx, 7, f.a, nil, f.actor); !errors.Is(err, appissues.ErrInvalid) {
		t.Errorf("empty = %v", err)
	}
	if err := f.svc.Merge(ctx, 7, f.a, []int64{f.b, f.foreign}, f.actor); !errors.Is(err, appissues.ErrNotFound) {
		t.Errorf("cross site = %v", err)
	}
	if err := f.svc.Merge(ctx, 7, f.foreign, []int64{f.a}, f.actor); !errors.Is(err, appissues.ErrNotFound) {
		t.Errorf("foreign target = %v", err)
	}
	if s := f.snap(t, f.b); s.merged != nil {
		t.Error("a rejected merge changed an issue")
	}
}

func TestUnmergeAfterTargetReceivedEventsSplitsExactlyByHash(t *testing.T) {
	f := newMergeFixture(t)
	ctx := context.Background()
	a0, b0 := f.snap(t, f.a), f.snap(t, f.b)
	if err := f.svc.Merge(ctx, 7, f.b, []int64{f.a}, f.actor); err != nil {
		t.Fatal(err)
	}
	// The target keeps receiving events: its own fingerprint and the merged one.
	f.addEvent(t, f.b, "fp-b", day1.Add(3*time.Hour), 7, "i5")
	f.addEvent(t, f.b, "fp-a", day1.Add(4*time.Hour), 6, "i6")
	f.addEvent(t, f.b, "fp-a", day1.Add(50*time.Hour), 2, "i7")
	f.rollup(t)
	before := f.snap(t, f.b)

	merged, err := f.svc.Merged(ctx, 7, f.b)
	if err != nil || len(merged) != 1 || merged[0].IssueID != f.a || merged[0].Events != 2+3+6+2 {
		t.Fatalf("merged = %+v %v", merged, err)
	}
	id, err := f.svc.Unmerge(ctx, 7, f.b, [][]byte{hashOf("fp-a")}, f.actor)
	if err != nil || id != f.a {
		t.Fatalf("unmerge = %d %v", id, err)
	}
	a1, b1 := f.snap(t, f.a), f.snap(t, f.b)
	if a1.merged != nil {
		t.Error("source still merged")
	}
	if a1.count != 2+3+6+2 || a1.count != eventsOf(t, f.fixture, f.a) {
		t.Errorf("source count %d", a1.count)
	}
	if b1.count != 4+1+7 || b1.count != eventsOf(t, f.fixture, f.b) { // b's own fp-b events only
		t.Errorf("target count %d", b1.count)
	}
	// Daily and hourly rollups of both sum to the totals before the unmerge.
	total := sumDaily(before)
	for d, n := range sumDaily(a1, b1) {
		if total[d] != n {
			t.Errorf("day %s: split %d, before %d", d, n, total[d])
		}
	}
	var hourly int64
	for _, s := range []snapshot{a1, b1} {
		for _, n := range s.hourly {
			hourly += n
		}
	}
	var beforeHourly int64
	for _, n := range before.hourly {
		beforeHourly += n
	}
	if hourly != beforeHourly {
		t.Errorf("hourly %d, want %d", hourly, beforeHourly)
	}
	// Days the merge never touched match the pre-merge state.
	day3 := day1.AddDate(0, 0, 2).Format("2006-01-02")
	if got, want := b1.daily[day3][0], b0.daily[day3][0]; got != want {
		t.Errorf("untouched day on target %d, want %d", got, want)
	}
	day2 := day1.AddDate(0, 0, 1).Format("2006-01-02")
	if a1.daily[day2] != a0.daily[day2] {
		t.Errorf("source day2 %v, want %v", a1.daily[day2], a0.daily[day2])
	}
	var ph int64
	if err := f.pool.QueryRow(ctx, `select issue_id from app_issue_hashes where hash = $1`, hashOf("fp-a")).Scan(&ph); err != nil || ph != f.a {
		t.Errorf("hash row = %d %v", ph, err)
	}
	if k := f.activity(t, f.a); k[len(k)-1] != appissues.KindUnmerged {
		t.Errorf("activity = %v", k)
	}
	// A hash that was never merged into the target is rejected.
	if _, err := f.svc.Unmerge(ctx, 7, f.b, [][]byte{hashOf("fp-c")}, f.actor); !errors.Is(err, appissues.ErrInvalid) {
		t.Errorf("unknown hash = %v", err)
	}
	if _, err := f.svc.Unmerge(ctx, 7, f.b, [][]byte{{1, 2}}, f.actor); !errors.Is(err, appissues.ErrInvalid) {
		t.Errorf("short hash = %v", err)
	}
	if _, err := f.svc.Unmerge(ctx, 7, f.foreign, [][]byte{hashOf("fp-a")}, f.actor); !errors.Is(err, appissues.ErrNotFound) {
		t.Errorf("foreign = %v", err)
	}
}

func TestMergeKeepsHistoryOlderThanStoredEvents(t *testing.T) {
	f := newMergeFixture(t)
	ctx := context.Background()
	// 100 events of b happened before retention dropped its partitions, and
	// a rollup row outlives them.
	if _, err := f.pool.Exec(ctx, `update app_issues set event_count = event_count + 100 where id = $1`, f.b); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `insert into app_issue_daily(site_id,issue_id,day,events,installs,crashes) values (7,$1,'2026-01-05',100,10,100)`, f.b); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Merge(ctx, 7, f.c, []int64{f.b}, f.actor); err != nil {
		t.Fatal(err)
	}
	s := f.snap(t, f.c)
	if s.count != 5+4+1+100 || s.daily["2026-01-05"][0] != 100 {
		t.Fatalf("history lost: count %d, old day %v", s.count, s.daily["2026-01-05"])
	}
	if _, err := f.svc.Unmerge(ctx, 7, f.c, [][]byte{hashOf("fp-b")}, f.actor); err != nil {
		t.Fatal(err)
	}
	if got := f.snap(t, f.c); got.count != 5+100 || got.daily["2026-01-05"][0] != 100 {
		t.Errorf("target after unmerge: %+v", got)
	}
}

func TestMergeGuardsMissingTagTable(t *testing.T) {
	f := newMergeFixture(t)
	var exists bool
	if err := f.pool.QueryRow(context.Background(), `select to_regclass('app_issue_tags') is not null`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Skip("app_issue_tags exists; its merge re-sum belongs to the tag rollup change")
	}
	if err := f.svc.Merge(context.Background(), 7, f.b, []int64{f.a}, f.actor); err != nil {
		t.Fatal(err)
	}
}
