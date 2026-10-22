package ghstars

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/db"
	"supermetry/internal/testpg"
)

const testRepo = "velixio/supermetry"

func newTestStore(t *testing.T) (*Store, *pgxpool.Pool) {
	t.Helper()
	pool, _ := testpg.New(t)
	if err := db.Migrate(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	loc, err := time.LoadLocation("Asia/Kathmandu")
	if err != nil {
		t.Fatal(err)
	}
	return NewStore(pool, testRepo, loc), pool
}

func at(s string) time.Time {
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return v
}

// seed holds the hand-computed fixture. Kathmandu is UTC+5:45:
//
//	u0 starred   2026-09-30T00:00Z  (before the range)
//	u1 starred   2026-10-01T18:00Z  = Oct 1 23:45 local
//	u2 starred   2026-10-01T18:30Z  = Oct 2 00:15 local
//	u1 unstarred 2026-10-02T03:00Z  = Oct 2 08:45 local
//	u3 starred   2026-10-02T20:00Z  = Oct 3 01:45 local
func seed(t *testing.T, s *Store) {
	t.Helper()
	events := []Event{
		{UserID: 10, Login: "u0", Action: ActionStarred, OccurredAt: at("2026-09-30T00:00:00Z"), Source: SourceBackfill},
		{UserID: 11, Login: "u1", Action: ActionStarred, OccurredAt: at("2026-10-01T18:00:00Z"), Source: SourceWebhook},
		{UserID: 12, Login: "u2", Action: ActionStarred, OccurredAt: at("2026-10-01T18:30:00Z"), Source: SourceWebhook},
		{UserID: 11, Login: "u1", Action: ActionUnstarred, OccurredAt: at("2026-10-02T03:00:00Z"), Source: SourceWebhook},
		{UserID: 13, Login: "u3", Action: ActionStarred, OccurredAt: at("2026-10-02T20:00:00Z"), Source: SourceReconcile},
	}
	if err := s.InsertEvents(context.Background(), events); err != nil {
		t.Fatal(err)
	}
}

func TestSeriesDayInSiteZone(t *testing.T) {
	s, _ := newTestStore(t)
	seed(t, s)
	got, err := s.Series(context.Background(), "day", at("2026-09-30T18:15:00Z"), at("2026-10-03T18:15:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	want := []struct{ gained, lost, net, cumulative int }{
		{1, 0, 1, 2}, // Oct 1: u1; baseline 1 from u0
		{1, 1, 0, 2}, // Oct 2: u2 gained, u1 lost
		{1, 0, 1, 3}, // Oct 3: u3
	}
	if len(got) != len(want) {
		t.Fatalf("points = %+v", got)
	}
	for i, w := range want {
		p := got[i]
		if p.Gained != w.gained || p.Lost != w.lost || p.Net != w.net || p.Cumulative != w.cumulative {
			t.Errorf("day %d = %+v, want %+v", i, p, w)
		}
	}
	if first := got[0].Bucket; first.Day() != 1 || first.Hour() != 0 || first.Location().String() != "Asia/Kathmandu" {
		t.Errorf("first bucket = %v", first)
	}
}

func TestSeriesHourFillsEmptyBuckets(t *testing.T) {
	s, _ := newTestStore(t)
	seed(t, s)
	got, err := s.Series(context.Background(), "hour", at("2026-10-01T18:00:00Z"), at("2026-10-01T21:00:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	// Local buckets 23:00, 00:00, 01:00, 02:00.
	wantGained := []int{1, 1, 0, 0}
	wantCumulative := []int{2, 3, 3, 3}
	if len(got) != 4 {
		t.Fatalf("points = %+v", got)
	}
	for i := range got {
		if got[i].Gained != wantGained[i] || got[i].Cumulative != wantCumulative[i] {
			t.Errorf("hour %d = %+v", i, got[i])
		}
	}
}

func TestSeriesRejectsBadArguments(t *testing.T) {
	s, _ := newTestStore(t)
	from := at("2026-10-01T00:00:00Z")
	for name, call := range map[string]func() error{
		"granularity": func() error { _, err := s.Series(context.Background(), "week", from, from.Add(time.Hour)); return err },
		"reversed":    func() error { _, err := s.Series(context.Background(), "day", from, from); return err },
		"hour range": func() error {
			_, err := s.Series(context.Background(), "hour", from, from.AddDate(0, 0, 40))
			return err
		},
		"day range": func() error {
			_, err := s.Series(context.Background(), "day", from, from.AddDate(10, 0, 0))
			return err
		},
	} {
		if err := call(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestSummaryNetChange(t *testing.T) {
	s, _ := newTestStore(t)
	seed(t, s)
	ctx := context.Background()
	now := at("2026-10-03T00:00:00Z")
	if err := s.SaveState(ctx, Counts{Stars: 3, Forks: 1, Watchers: 2, OpenIssues: 4}, now); err != nil {
		t.Fatal(err)
	}
	got, err := s.Summary(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	// 24h window is (Oct 2 00:00Z, Oct 3 00:00Z]: unstar u1 (-1) and star u3 (+1) = 0.
	// 7d window is (Sep 26, Oct 3]: u0, u1, u2, u1 unstar, u3 = +4 -1 = 3.
	if got.Stars != 3 || got.Forks != 1 || got.Watchers != 2 || got.OpenIssues != 4 || got.Net24h != 0 || got.Net7d != 3 {
		t.Fatalf("summary = %+v", got)
	}
	empty := NewStore(s.pool, "other/repo", nil)
	if got, err := empty.Summary(ctx, now); err != nil || got.UpdatedAt != nil || got.Stars != 0 {
		t.Fatalf("empty summary = %+v, %v", got, err)
	}
}

func TestRecordEventIsIdempotentAndKeepsWatchers(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	if err := s.SaveState(ctx, Counts{Stars: 5, Watchers: 9}, at("2026-10-01T00:00:00Z")); err != nil {
		t.Fatal(err)
	}
	e := Event{DeliveryID: "d-1", UserID: 1, Login: "a", Action: ActionStarred, OccurredAt: at("2026-10-01T01:00:00Z"), Source: SourceWebhook}
	ok, err := s.RecordEvent(ctx, e, Counts{Stars: 6, Forks: 2, Watchers: KeepWatchers, OpenIssues: 3}, at("2026-10-01T01:00:01Z"))
	if err != nil || !ok {
		t.Fatalf("first = %v, %v", ok, err)
	}
	ok, err = s.RecordEvent(ctx, e, Counts{Stars: 99, Watchers: KeepWatchers}, at("2026-10-01T02:00:00Z"))
	if err != nil || ok {
		t.Fatalf("duplicate = %v, %v", ok, err)
	}
	sum, _ := s.Summary(ctx, at("2026-10-01T03:00:00Z"))
	if sum.Stars != 6 || sum.Forks != 2 || sum.Watchers != 9 || sum.OpenIssues != 3 {
		t.Fatalf("state = %+v", sum)
	}
	if n, _ := s.EventCount(ctx); n != 1 {
		t.Fatalf("events = %d", n)
	}
}

func TestCurrentStargazersUsesLatestEvent(t *testing.T) {
	s, _ := newTestStore(t)
	seed(t, s)
	got, err := s.CurrentStargazers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// u1 unstarred, so u0, u2 and u3 remain.
	if len(got) != 3 || got[10] != "u0" || got[12] != "u2" || got[13] != "u3" {
		t.Fatalf("current = %v", got)
	}
}

func TestEventsPagination(t *testing.T) {
	s, _ := newTestStore(t)
	seed(t, s)
	ctx := context.Background()
	page, next, err := s.Events(ctx, "", 2)
	if err != nil || len(page) != 2 || next == "" {
		t.Fatalf("page 1 = %+v, %q, %v", page, next, err)
	}
	if page[0].Login != "u3" || page[1].Login != "u1" || page[1].Action != ActionUnstarred {
		t.Fatalf("page 1 order = %+v", page)
	}
	rest, next, err := s.Events(ctx, next, 10)
	if err != nil || next != "" || len(rest) != 3 || rest[0].Login != "u2" || rest[2].Login != "u0" {
		t.Fatalf("page 2 = %+v, %q, %v", rest, next, err)
	}
	if _, _, err := s.Events(ctx, "garbage", 5); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad cursor err = %v", err)
	}
	if _, _, err := s.Events(ctx, "", 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad limit err = %v", err)
	}
}

func TestAnnotations(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	a, err := s.AddAnnotation(ctx, at("2026-10-05T00:00:00Z"), "  Launch  ", 0)
	if err != nil || a.Label != "Launch" || a.CreatedBy != nil {
		t.Fatalf("add = %+v, %v", a, err)
	}
	if _, err := s.AddAnnotation(ctx, at("2026-10-01T00:00:00Z"), "  ", 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("blank err = %v", err)
	}
	if _, err := s.AddAnnotation(ctx, at("2026-10-01T00:00:00Z"), "Earlier", 0); err != nil {
		t.Fatal(err)
	}
	list, _ := s.Annotations(ctx)
	if len(list) != 2 || list[0].Label != "Earlier" {
		t.Fatalf("list = %+v", list)
	}
	if err := s.DeleteAnnotation(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAnnotation(ctx, a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete err = %v", err)
	}
}

func TestEraseUserDeletesEventsAndAudits(t *testing.T) {
	s, pool := newTestStore(t)
	seed(t, s)
	ctx := context.Background()
	n, err := s.EraseUser(ctx, "U1", 0)
	if err != nil || n != 2 {
		t.Fatalf("erased = %d, %v", n, err)
	}
	if count, _ := s.EventCount(ctx); count != 3 {
		t.Fatalf("remaining = %d", count)
	}
	var audits int
	if err := pool.QueryRow(ctx, `select count(*) from audit_log where action='instance.github_stars.user_erased' and metadata->>'events'='2'`).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("audits = %d, %v", audits, err)
	}
}

func TestSaveSnapshotUpserts(t *testing.T) {
	s, pool := newTestStore(t)
	ctx := context.Background()
	day := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	for _, stars := range []int{10, 12} {
		if err := s.SaveSnapshot(ctx, day, Counts{Stars: stars}); err != nil {
			t.Fatal(err)
		}
	}
	var rows, stars int
	if err := pool.QueryRow(ctx, `select count(*), max(stars) from github_star_snapshots`).Scan(&rows, &stars); err != nil || rows != 1 || stars != 12 {
		t.Fatalf("rows=%d stars=%d err=%v", rows, stars, err)
	}
}

func TestEraseUserWritesTombstoneAndBlocksFutureEvents(t *testing.T) {
	s, pool := newTestStore(t)
	seed(t, s)
	ctx := context.Background()
	if _, err := s.EraseUser(ctx, "u1", 0); err != nil {
		t.Fatal(err)
	}
	var ids []int64
	rows, err := pool.Query(ctx, `select user_id from github_star_erasures where repo=$1`, testRepo)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if len(ids) != 1 || ids[0] != 11 {
		t.Fatalf("tombstones = %v", ids)
	}
	// Erasing again, or after new events are blocked, keeps a single tombstone.
	if _, err := s.EraseUser(ctx, "u1", 0); err != nil {
		t.Fatal(err)
	}
	before, _ := s.EventCount(ctx)
	err = s.InsertEvents(ctx, []Event{
		{UserID: 11, Login: "u1", Action: ActionStarred, OccurredAt: at("2026-10-05T00:00:00Z"), Source: SourceReconcile},
		{UserID: 14, Login: "u4", Action: ActionStarred, OccurredAt: at("2026-10-05T00:00:00Z"), Source: SourceReconcile},
	})
	if err != nil {
		t.Fatal(err)
	}
	if after, _ := s.EventCount(ctx); after != before+1 {
		t.Fatalf("events %d -> %d, want one new", before, after)
	}
}
