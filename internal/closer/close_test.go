package closer_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"panel/internal/closer"
	"panel/internal/db"
	"panel/internal/sites"
	"panel/internal/testpg"
)

type registry struct {
	sites []sites.Site
}

func (r registry) ByPublicID(id string) (sites.Site, bool) {
	for _, site := range r.sites {
		if site.PublicID == id {
			return site, true
		}
	}
	return sites.Site{}, false
}

func (r registry) Site(id int32) (sites.Site, bool) {
	for _, site := range r.sites {
		if site.ID == id {
			return site, true
		}
	}
	return sites.Site{}, false
}

func (r registry) All() []sites.Site { return append([]sites.Site(nil), r.sites...) }

func TestDueRespectsLocalMidnightPlus45m(t *testing.T) {
	reg := registry{sites: []sites.Site{
		{ID: 1, TZ: mustLocation(t, "Asia/Kathmandu")},
		{ID: 2, TZ: mustLocation(t, "America/New_York")},
		{ID: 3, TZ: time.UTC},
		{ID: 4, TZ: mustLocation(t, "Asia/Kolkata")},
	}}
	never := func(int32, time.Time) bool { return false }

	tests := []struct {
		name string
		now  time.Time
		site int32
		day  string
		want bool
	}{
		{name: "Kathmandu before quarter-hour boundary", now: utc(2026, 10, 7, 18, 59), site: 1, day: "2026-10-07", want: false},
		{name: "Kathmandu after quarter-hour boundary", now: utc(2026, 10, 7, 19, 1), site: 1, day: "2026-10-07", want: true},
		{name: "Kolkata before half-hour boundary", now: utc(2026, 10, 7, 19, 14), site: 4, day: "2026-10-07", want: false},
		{name: "Kolkata after half-hour boundary", now: utc(2026, 10, 7, 19, 16), site: 4, day: "2026-10-07", want: true},
		{name: "New York spring forward before boundary", now: utc(2026, 3, 9, 4, 44), site: 2, day: "2026-03-08", want: false},
		{name: "New York spring forward after boundary", now: utc(2026, 3, 9, 4, 46), site: 2, day: "2026-03-08", want: true},
		{name: "New York fall back before boundary", now: utc(2026, 11, 2, 5, 44), site: 2, day: "2026-11-01", want: false},
		{name: "New York fall back after boundary", now: utc(2026, 11, 2, 5, 46), site: 2, day: "2026-11-01", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := has(closer.Due(tt.now, reg, never), tt.site, tt.day)
			if got != tt.want {
				t.Fatalf("due=%v, want %v", got, tt.want)
			}
		})
	}

	closed := func(site int32, day time.Time) bool {
		return site == 3 && day.Format(time.DateOnly) == "2026-10-07"
	}
	if due := closer.Due(utc(2026, 10, 8, 0, 46), reg, closed); has(due, 3, "2026-10-07") {
		t.Fatal("closed day returned as due")
	}
}

func TestCloseDayMatchesTruthAndIsIdempotent(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	loadFixture(t, pool)

	for _, day := range fixtureDays() {
		stats, err := closer.CloseDay(ctx, pool, []int32{1}, day)
		if err != nil {
			t.Fatal(err)
		}
		if day.Equal(fixtureDays()[1]) && (stats.Sessions != 3 || stats.Custom != 1 || stats.Duration <= 0) {
			t.Fatalf("stats=%+v", stats)
		}
	}

	var visitors, visits, pageviews, bounces int
	var duration, engagement, scrollSum, scrollN int64
	if err := pool.QueryRow(ctx, `
		select visitors, visits, pageviews, bounces, duration_ms, engagement_ms, scroll_sum, scroll_n
		from rollup_day where site_id = 1 and day = '2026-10-07'`).Scan(
		&visitors, &visits, &pageviews, &bounces, &duration, &engagement, &scrollSum, &scrollN,
	); err != nil {
		t.Fatal(err)
	}
	if got, want := []any{visitors, visits, pageviews, bounces, duration, engagement, scrollSum, scrollN}, []any{2, 3, 3, 1, int64(100_000), int64(5_000), int64(105), int64(2)}; !reflect.DeepEqual(got, want) {
		t.Fatalf("rollup totals=%v, want %v", got, want)
	}

	var seq, seqEngage []int32
	var seqScroll []int16
	var utmContent, utmTerm int32
	if err := pool.QueryRow(ctx, `
		select seq, seq_engage, seq_scroll, utm_content_id, utm_term_id from sessions_closed
		where site_id = 1 and day = '2026-10-07' and session_id = 10`).Scan(
		&seq, &seqEngage, &seqScroll, &utmContent, &utmTerm,
	); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(seq, []int32{7, 11}) || !reflect.DeepEqual(seqEngage, []int32{1_000, 4_000}) || !reflect.DeepEqual(seqScroll, []int16{25, 80}) {
		t.Fatalf("seq=%v engage=%v scroll=%v", seq, seqEngage, seqScroll)
	}
	if utmContent != 41 || utmTerm != 42 {
		t.Fatalf("utm_content_id=%d utm_term_id=%d", utmContent, utmTerm)
	}
	if err := pool.QueryRow(ctx, `
		select utm_content_id, utm_term_id from custom_closed
		where site_id = 1 and day = '2026-10-07' and name_id = 100`).Scan(&utmContent, &utmTerm); err != nil {
		t.Fatal(err)
	}
	if utmContent != 51 || utmTerm != 52 {
		t.Fatalf("custom utm_content_id=%d utm_term_id=%d", utmContent, utmTerm)
	}

	var customRows, cubeRows, visitorRows, blockRows, closeVersion int
	if err := pool.QueryRow(ctx, `
		select
		  (select count(*) from custom_closed where site_id = 1 and day = '2026-10-07'),
		  (select count(*) from cube_day where site_id = 1 and day = '2026-10-07'),
		  (select count(*) from visitor_day where site_id = 1 and day = '2026-10-07'),
		  (select count(*) from rollup_block_done where site_id = 1 and k = 1 and start_day = '2026-10-06'),
		  (select version from day_close where site_id = 1 and day = '2026-10-07')`).Scan(
		&customRows, &cubeRows, &visitorRows, &blockRows, &closeVersion,
	); err != nil {
		t.Fatal(err)
	}
	if customRows != 1 || cubeRows != 3 || visitorRows != 2 || blockRows != 1 || closeVersion != 1 {
		t.Fatalf("custom=%d cube=%d visitor=%d block=%d version=%d", customRows, cubeRows, visitorRows, blockRows, closeVersion)
	}

	before := rowCounts(t, pool)
	if _, err := closer.CloseDay(ctx, pool, []int32{1}, fixtureDays()[1]); err != nil {
		t.Fatal(err)
	}
	after := rowCounts(t, pool)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("re-close changed counts %v -> %v", before, after)
	}
	if err := pool.QueryRow(ctx, `select version from day_close where site_id = 1 and day = '2026-10-07'`).Scan(&closeVersion); err != nil {
		t.Fatal(err)
	}
	if closeVersion != 2 {
		t.Fatalf("close version=%d, want 2", closeVersion)
	}
}

func TestVisitorDayExactForVisitorLevelFilters(t *testing.T) {
	pool, _ := testpg.New(t)
	loadFixture(t, pool)
	if _, err := closer.CloseDay(context.Background(), pool, []int32{1}, fixtureDays()[1]); err != nil {
		t.Fatal(err)
	}

	assertEqualQuery(t, pool,
		`select coalesce(sum(visitors),0) from visitor_day where site_id=1 and country='IN'`,
		`select count(*) from sessions_closed where site_id=1 and country='IN' and flags & 1 = 1`)
}

func TestCloseDayBatchRecordsRowsPerSite(t *testing.T) {
	pool, _ := testpg.New(t)
	loadFixture(t, pool)
	if _, err := closer.CloseDay(context.Background(), pool, []int32{2, 1, 2}, fixtureDays()[1]); err != nil {
		t.Fatal(err)
	}

	rows := make(map[int32]int64)
	result, err := pool.Query(context.Background(), `
		select site_id, rows from day_close where day = '2026-10-07' order by site_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer result.Close()
	for result.Next() {
		var siteID int32
		var count int64
		if err := result.Scan(&siteID, &count); err != nil {
			t.Fatal(err)
		}
		rows[siteID] = count
	}
	if err := result.Err(); err != nil {
		t.Fatal(err)
	}
	if want := map[int32]int64{1: 4, 2: 1}; !reflect.DeepEqual(rows, want) {
		t.Fatalf("day_close rows=%v, want %v", rows, want)
	}
}

func TestCloseDayIncludesEventsAfterLocalMidnightInStartingSession(t *testing.T) {
	pool, _ := testpg.New(t)
	loadFixture(t, pool)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
insert into sessions
  (site_id, retention_class, session_id, visitor_id, start, last, local_day,
   entry_path, exit_path, source_id, country, browser_id, os_id, device,
   pageviews, events, engagement_ms, bounce)
values
  (3, 30, 50, 500, '2026-10-08 11:50Z', '2026-10-08 12:10Z', '2026-10-07', 1, 2, 10, 'IN', 20, 30, 1, 2, 0, 0, false);
insert into events
  (site_id, retention_class, local_day, ts, visitor_id, session_id, kind,
   name_id, path_id, source_id, country, browser_id, os_id, device)
values
  (3, 30, '2026-10-07', '2026-10-08 11:50Z', 500, 50, 0, 0, 1, 10, 'IN', 20, 30, 1),
  (3, 30, '2026-10-08', '2026-10-08 12:10Z', 500, 50, 0, 0, 2, 10, 'IN', 20, 30, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := closer.CloseDay(ctx, pool, []int32{3}, fixtureDays()[1]); err != nil {
		t.Fatal(err)
	}

	var pageviews int
	var sequence []int32
	if err := pool.QueryRow(ctx, `
		select pageviews, seq from sessions_closed
		where site_id = 3 and day = '2026-10-07' and session_id = 50`).Scan(&pageviews, &sequence); err != nil {
		t.Fatal(err)
	}
	if pageviews != 2 || len(sequence) != 2 {
		t.Fatalf("pageviews=%d seq=%v, want two cross-midnight pageviews", pageviews, sequence)
	}
}

func TestCloseDayBuildsCompletedMonth(t *testing.T) {
	pool, _ := testpg.New(t)
	loadFixture(t, pool)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		insert into day_close (site_id, day, status, version)
		select 1, day::date, 'done', 1
		from generate_series('2026-10-01'::date, '2026-10-31'::date, interval '1 day') day
		where day::date <> '2026-10-07'::date`); err != nil {
		t.Fatal(err)
	}
	if _, err := closer.CloseDay(ctx, pool, []int32{1}, fixtureDays()[1]); err != nil {
		t.Fatal(err)
	}

	var done int
	if err := pool.QueryRow(ctx, `
		select count(*) from rollup_month_done
		where site_id = 1 and month = '2026-10-01'`).Scan(&done); err != nil {
		t.Fatal(err)
	}
	if done != 1 {
		t.Fatalf("month completion rows=%d, want 1", done)
	}
	assertEqualQuery(t, pool,
		`select coalesce(sum(pageviews), 0) from rollup_month_dim where site_id=1 and month='2026-10-01'`,
		`select coalesce(sum(pageviews), 0) from rollup_day_dim where site_id=1 and day='2026-10-07'`)
}

func loadFixture(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsurePartitions(ctx, pool, utc(2026, 10, 5, 0, 0), utc(2026, 10, 9, 0, 0)); err != nil {
		t.Fatal(err)
	}

	_, err := pool.Exec(ctx, `
insert into sessions
  (site_id, retention_class, session_id, visitor_id, start, last, local_day,
   entry_path, exit_path, source_id, country, browser_id, os_id, device,
   pageviews, events, engagement_ms, bounce)
values
  (1, 30, 10, 100, '2026-10-07 10:00Z', '2026-10-07 10:01:30Z', '2026-10-07', 1, 2, 10, 'IN', 20, 30, 1, 2, 0, 5000, false),
  (1, 30, 11, 100, '2026-10-07 12:00Z', '2026-10-07 12:00:10Z', '2026-10-07', 0, 0, 11, 'US', 21, 31, 2, 0, 1, 0, false),
  (1, 30, 20, 200, '2026-10-07 14:00Z', '2026-10-07 14:00:00Z', '2026-10-07', 1, 1, 10, 'US', 20, 30, 1, 1, 0, 0, true),
  (1, 30, 30, 300, '2026-10-08 13:00Z', '2026-10-08 13:00:00Z', '2026-10-07', 9, 9, 10, 'US', 20, 30, 1, 1, 0, 0, true),
  (2, 30, 40, 400, '2026-10-07 15:00Z', '2026-10-07 15:00:00Z', '2026-10-07', 3, 3, 12, 'NP', 22, 32, 1, 1, 0, 0, true);

insert into events
  (site_id, retention_class, local_day, ts, visitor_id, session_id, kind,
   name_id, path_id, source_id, utm_content_id, utm_term_id,
   country, browser_id, os_id, device, engagement_ms, scroll_pct, props)
values
  (1, 30, '2026-10-07', '2026-10-07 10:00:00Z', 100, 10, 0, 0,   1, 10, 41, 42, 'IN', 20, 30, 1, 0,    0, null),
  (1, 30, '2026-10-07', '2026-10-07 10:00:20Z', 100, 10, 2, 0,   1, 10, 0,  0,  'IN', 20, 30, 1, 1000, 25, null),
  (1, 30, '2026-10-07', '2026-10-07 10:00:30Z', 100, 10, 0, 0,   2, 10, 0,  0,  'IN', 20, 30, 1, 0,    0, null),
  (1, 30, '2026-10-07', '2026-10-07 10:01:30Z', 100, 10, 2, 0,   2, 10, 0,  0,  'IN', 20, 30, 1, 4000, 80, null),
  (1, 30, '2026-10-07', '2026-10-07 12:00:00Z', 100, 11, 1, 100, 0, 11, 51, 52, 'US', 21, 31, 2, 0,    0, '{"plan":"pro"}'),
  (1, 30, '2026-10-07', '2026-10-07 14:00:00Z', 200, 20, 0, 0,   1, 10, 0,  0,  'US', 20, 30, 1, 0,    0, null),
  (1, 30, '2026-10-07', '2026-10-08 13:00:00Z', 300, 30, 0, 0,   9, 10, 0,  0,  'US', 20, 30, 1, 0,    0, null),
  (2, 30, '2026-10-07', '2026-10-07 15:00:00Z', 400, 40, 0, 0,   3, 12, 0,  0,  'NP', 22, 32, 1, 0,    0, null)`)
	if err != nil {
		t.Fatal(err)
	}
}

func fixtureDays() []time.Time {
	return []time.Time{
		time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC),
	}
}

func rowCounts(t *testing.T, pool *pgxpool.Pool) map[string]int {
	t.Helper()
	result := make(map[string]int)
	for _, table := range []string{
		"sessions_closed", "custom_closed", "cube_day", "visitor_day", "rollup_day",
		"rollup_day_dim", "rollup_block_dim", "rollup_block_done", "rollup_month_dim",
	} {
		var count int
		if err := pool.QueryRow(context.Background(), "select count(*) from "+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		result[table] = count
	}
	return result
}

func assertEqualQuery(t *testing.T, pool *pgxpool.Pool, left, right string) {
	t.Helper()
	var got, want int64
	if err := pool.QueryRow(context.Background(), left).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), right).Scan(&want); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("left=%d right=%d", got, want)
	}
}

func mustLocation(t *testing.T, name string) *time.Location {
	t.Helper()
	location, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return location
}

func has(days map[time.Time][]int32, site int32, day string) bool {
	for candidate, sites := range days {
		if candidate.Format(time.DateOnly) != day {
			continue
		}
		for _, candidateSite := range sites {
			if candidateSite == site {
				return true
			}
		}
	}
	return false
}

func utc(year int, month time.Month, day, hour, minute int) time.Time {
	return time.Date(year, month, day, hour, minute, 0, 0, time.UTC)
}
