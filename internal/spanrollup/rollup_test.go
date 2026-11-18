package spanrollup_test

import (
	"context"
	"fmt"
	"math"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/db"
	"supermetry/internal/sites"
	"supermetry/internal/spanrollup"
	"supermetry/internal/spans"
	"supermetry/internal/testpg"
)

var testNow = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

type env struct {
	pool *pgxpool.Pool
	loc  *time.Location
	now  time.Time
}

func setup(t *testing.T, tz string) *env {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into orgs(name,slug) values('Org','org')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into sites(id,org_id,public_id,name,timezone,retention_class,kind) overriding system value
values(7,1,'appsite00001',$1,$2,30,'app')`, "App", tz); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureDailyPartitions(ctx, pool, "spans", time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		t.Fatal(err)
	}
	return &env{pool: pool, loc: loc, now: testNow}
}

var spanSeq atomic.Uint32

func mk(start time.Time, durUS int64, name, op, desc, status string, segment bool) spans.Span {
	n := spanSeq.Add(1)
	s := spans.Span{SiteID: 7, Start: start, DurationUS: durUS, Name: name, Op: op, Description: desc, Status: status,
		Release: "1.0", Environment: "prod", Platform: "go", IsSegment: segment}
	s.TraceID[0], s.TraceID[1], s.TraceID[2] = byte(n), byte(n>>8), byte(n>>16)
	s.SpanID[0], s.SpanID[1], s.SpanID[2] = byte(n), byte(n>>8), byte(n>>16)
	s.SegmentID = s.SpanID
	return s
}

// write stores spans through the real writer, so span_groups and onLate run.
func (e *env) write(t *testing.T, late func(int32, []time.Time), batch ...spans.Span) {
	t.Helper()
	w := spans.NewWriter(e.pool, spans.WriterConfig{
		// One flush, at shutdown: no size or timer wake-up can race the cancel.
		Batch: 1 << 30, Flush: time.Hour, QueueSpans: 1 << 20,
		Now:      func() time.Time { return e.now },
		Location: func(int32) *time.Location { return e.loc },
	}, nil, late)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	if !w.Enqueue(batch) {
		t.Fatal("enqueue refused")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func (e *env) hour(s string) time.Time {
	v, err := time.Parse("2006-01-02T15:04", s)
	if err != nil {
		panic(err)
	}
	return v
}

type hourly struct {
	count, errors, sumUS int64
	buckets              []int16
	counts               []int64
}

func (e *env) row(t *testing.T, hour string, segment bool, name, op string) (hourly, bool) {
	t.Helper()
	var h hourly
	err := e.pool.QueryRow(context.Background(), `
select count, errors, sum_us, buckets, bucket_counts from span_hourly
where site_id = 7 and local_hour = $1 and is_segment = $2 and name = $3 and op = $4`,
		e.hour(hour), segment, name, op).Scan(&h.count, &h.errors, &h.sumUS, &h.buckets, &h.counts)
	if err != nil {
		return hourly{}, false
	}
	return h, true
}

func (h hourly) percentile(p float64) float64 {
	m := map[int16]int64{}
	for i, b := range h.buckets {
		m[b] = h.counts[i]
	}
	return spans.Percentiles(m, p)[0]
}

func (e *env) dump(t *testing.T) string {
	t.Helper()
	rows, err := e.pool.Query(context.Background(), `
select local_hour::text, is_segment, name, op, group_hash, release, count, errors, sum_us, buckets::text, bucket_counts::text
from span_hourly where site_id = 7 order by 1, 2, 3, 4, 5`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := ""
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			t.Fatal(err)
		}
		out += fmt.Sprintln(vals...)
	}
	return out
}

func within(got, want float64) bool { return math.Abs(got-want)/want <= 0.0488 }

func TestRecomputeHourHistogramAndGroups(t *testing.T) {
	e := setup(t, "UTC")
	var batch []spans.Span
	base := time.Date(2026, 11, 18, 7, 0, 0, 0, time.UTC)
	for i := 1; i <= 100; i++ {
		status := "ok"
		if i <= 5 {
			status = "internal_error"
		}
		batch = append(batch, mk(base.Add(time.Duration(i)*time.Second), int64(i)*1000, "GET /items", "http.server", "GET /items", status, true))
	}
	for i := 0; i < 3; i++ {
		batch = append(batch, mk(base.Add(time.Duration(i)*time.Minute), 2000, "", "db.query", "select * from users where id = ?", "ok", false))
	}
	e.write(t, nil, batch...)
	ctx := context.Background()
	if err := spanrollup.RecomputeHour(ctx, e.pool, 7, e.loc, e.hour("2026-11-18T07:00")); err != nil {
		t.Fatal(err)
	}
	seg, ok := e.row(t, "2026-11-18T07:00", true, "GET /items", "http.server")
	if !ok || seg.count != 100 || seg.errors != 5 || seg.sumUS != 5_050_000 {
		t.Fatalf("segment row = %+v ok=%v", seg, ok)
	}
	for _, c := range []struct{ p, want float64 }{{0.5, 49_896}, {0.95, 97_234}} {
		if got := seg.percentile(c.p); math.Abs(got-c.want) > 1 {
			t.Fatalf("p%v = %v, want %v", c.p, got, c.want)
		}
	}
	child, ok := e.row(t, "2026-11-18T07:00", false, "", "db.query")
	if !ok || child.count != 3 || child.errors != 0 {
		t.Fatalf("child row = %+v ok=%v", child, ok)
	}
	var groups int
	if err := e.pool.QueryRow(ctx, `select count(*) from span_groups where site_id = 7 and op = 'db.query'`).Scan(&groups); err != nil || groups != 1 {
		t.Fatalf("span_groups = %d, %v", groups, err)
	}
	var rows int
	if err := e.pool.QueryRow(ctx, `select count(*) from span_hourly`).Scan(&rows); err != nil || rows != 2 {
		t.Fatalf("span_hourly rows = %d, %v", rows, err)
	}
}

func TestRecomputeHourIdempotent(t *testing.T) {
	e := setup(t, "UTC")
	base := time.Date(2026, 11, 18, 7, 0, 0, 0, time.UTC)
	var batch []spans.Span
	for i := 1; i <= 20; i++ {
		batch = append(batch, mk(base.Add(time.Duration(i)*time.Second), int64(i)*1000, "GET /a", "http.server", "", "ok", true))
		batch = append(batch, mk(base.Add(time.Duration(i)*time.Second), 500, "", "http.client", "GET https://x/*", "ok", false))
	}
	e.write(t, nil, batch...)
	ctx := context.Background()
	if err := spanrollup.RecomputeHour(ctx, e.pool, 7, e.loc, e.hour("2026-11-18T07:00")); err != nil {
		t.Fatal(err)
	}
	first := e.dump(t)
	if err := spanrollup.RecomputeHour(ctx, e.pool, 7, e.loc, e.hour("2026-11-18T07:00")); err != nil {
		t.Fatal(err)
	}
	if second := e.dump(t); second != first || first == "" {
		t.Fatalf("recompute changed rows:\n%s\nvs\n%s", first, second)
	}
}

func TestNetworkFailuresCountAsErrors(t *testing.T) {
	e := setup(t, "UTC")
	base := time.Date(2026, 11, 18, 7, 0, 0, 0, time.UTC)
	c := mk(base, 100, "", "http.client", "GET https://x/*", "ok", false)
	status := int16(404)
	c.HTTPStatus = &status
	ok := mk(base, 100, "", "http.client", "GET https://x/*", "ok", false)
	e.write(t, nil, c, ok)
	if err := spanrollup.RecomputeHour(context.Background(), e.pool, 7, e.loc, e.hour("2026-11-18T07:00")); err != nil {
		t.Fatal(err)
	}
	row, found := e.row(t, "2026-11-18T07:00", false, "", "http.client")
	if !found || row.count != 2 || row.errors != 1 {
		t.Fatalf("row = %+v found=%v", row, found)
	}
}

type registry struct{ site sites.Site }

func (r registry) ByPublicID(string) (sites.Site, bool) { return r.site, true }
func (r registry) Site(int32) (sites.Site, bool)        { return r.site, true }
func (r registry) All() []sites.Site                    { return []sites.Site{r.site} }

func (e *env) job() func(ctx context.Context) error {
	reg := registry{site: sites.Site{ID: 7, Kind: sites.KindApp, App: &sites.AppConfig{}, TZ: e.loc}}
	return spanrollup.Job(e.pool, reg, func() time.Time { return e.now }).Run
}

func TestLateSpanReopensHour(t *testing.T) {
	e := setup(t, "UTC")
	base := time.Date(2026, 11, 18, 7, 0, 0, 0, time.UTC)
	var batch []spans.Span
	for i := 1; i <= 100; i++ {
		batch = append(batch, mk(base.Add(time.Duration(i)*time.Second), int64(i)*1000, "GET /items", "http.server", "", "ok", true))
	}
	e.write(t, nil, batch...)
	ctx := context.Background()
	if err := e.job()(ctx); err != nil {
		t.Fatal(err)
	}
	before, ok := e.row(t, "2026-11-18T07:00", true, "GET /items", "http.server")
	if !ok || before.count != 100 {
		t.Fatalf("after first job: %+v ok=%v", before, ok)
	}
	var state string
	if err := e.pool.QueryRow(ctx, `select status from span_hour_state where site_id=7 and local_hour='2026-11-18 07:00'`).Scan(&state); err != nil || state != "done" {
		t.Fatalf("state = %q, %v", state, err)
	}

	// A span three hours late, reported through the writer's onLate callback.
	e.now = time.Date(2026, 11, 18, 10, 30, 0, 0, time.UTC)
	late := func(site int32, hours []time.Time) {
		if err := spanrollup.MarkDirty(ctx, e.pool, site, hours); err != nil {
			t.Error(err)
		}
	}
	e.write(t, late, mk(base.Add(30*time.Minute), 900_000, "GET /items", "http.server", "", "ok", true))
	if err := e.pool.QueryRow(ctx, `select status from span_hour_state where site_id=7 and local_hour='2026-11-18 07:00'`).Scan(&state); err != nil || state != "dirty" {
		t.Fatalf("state after late span = %q, %v", state, err)
	}
	open, err := spanrollup.OpenHours(ctx, e.pool, 7, e.hour("2026-11-18T06:00"), e.hour("2026-11-18T09:00"))
	if err != nil || len(open) != 2 || !open[1].Equal(e.hour("2026-11-18T07:00")) {
		t.Fatalf("open hours = %v, %v (dirty and never-rolled hours read raw)", open, err)
	}
	if err := e.job()(ctx); err != nil {
		t.Fatal(err)
	}
	after, _ := e.row(t, "2026-11-18T07:00", true, "GET /items", "http.server")
	if after.count != 101 {
		t.Fatalf("count after rebuild = %d, want 101", after.count)
	}
	if after.percentile(1) == before.percentile(1) {
		t.Fatalf("max unchanged after a 900 ms span: %v", after.percentile(1))
	}
	if err := e.pool.QueryRow(ctx, `select status from span_hour_state where site_id=7 and local_hour='2026-11-18 07:00'`).Scan(&state); err != nil || state != "done" {
		t.Fatalf("state after rebuild = %q, %v", state, err)
	}
}

// localDayTotals checks, for every local day of the data, that the rollup's
// segment counts equal the exact count of raw spans whose local date is that
// day.
func (e *env) checkDayTotals(t *testing.T, all []spans.Span) {
	t.Helper()
	want := map[string]int64{}
	for _, s := range all {
		want[s.Start.In(e.loc).Format("2006-01-02")]++
	}
	rows, err := e.pool.Query(context.Background(), `
select local_hour::date::text, sum(count) from span_hourly where site_id = 7 and is_segment group by 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]int64{}
	for rows.Next() {
		var d string
		var n int64
		if err := rows.Scan(&d, &n); err != nil {
			t.Fatal(err)
		}
		got[d] = n
	}
	for d, n := range want {
		if got[d] != n {
			t.Errorf("day %s: rollup %d, raw %d", d, got[d], n)
		}
	}
	for d := range got {
		if _, ok := want[d]; !ok {
			t.Errorf("rollup has day %s with no raw spans", d)
		}
	}
}

func TestKathmanduDayTotals(t *testing.T) {
	e := setup(t, "Asia/Kathmandu")
	// Every 20 minutes over 2026-11-16T12:00Z .. 2026-11-18T12:00Z, so spans
	// fall on both sides of the +05:45 local midnights (18:15Z).
	var all []spans.Span
	for ts := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC); ts.Before(time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)); ts = ts.Add(20 * time.Minute) {
		all = append(all, mk(ts, 1000, "GET /k", "http.server", "", "ok", true))
	}
	e.write(t, nil, all...)
	ctx := context.Background()
	for _, h := range hoursBetween(e.hour("2026-11-16T17:00"), e.hour("2026-11-18T19:00")) {
		if err := spanrollup.RecomputeHour(ctx, e.pool, 7, e.loc, h); err != nil {
			t.Fatal(err)
		}
	}
	// Only whole local days: restrict the comparison to days fully covered.
	var covered []spans.Span
	for _, s := range all {
		l := s.Start.In(e.loc)
		if !l.Before(time.Date(2026, 11, 17, 0, 0, 0, 0, e.loc)) && l.Before(time.Date(2026, 11, 18, 0, 0, 0, 0, e.loc)) {
			covered = append(covered, s)
		}
	}
	var n17 int64
	if err := e.pool.QueryRow(ctx, `select sum(count) from span_hourly where site_id = 7 and is_segment and local_hour::date = '2026-11-17'`).Scan(&n17); err != nil {
		t.Fatal(err)
	}
	if n17 != int64(len(covered)) || n17 != 72 {
		t.Fatalf("2026-11-17 rollup = %d, raw = %d (want 72)", n17, len(covered))
	}
	// The first local hour of the 17th starts at 2026-11-16T18:15Z.
	first, ok := e.row(t, "2026-11-17T00:00", true, "GET /k", "http.server")
	if !ok || first.count != 3 {
		t.Fatalf("local 00:00 row = %+v ok=%v, want 3 spans (18:20, 18:40, 19:00Z)", first, ok)
	}
}

func hoursBetween(from, to time.Time) []time.Time {
	var out []time.Time
	for h := from; h.Before(to); h = h.Add(time.Hour) {
		out = append(out, h)
	}
	return out
}

func TestNewYorkFallBack(t *testing.T) {
	e := setup(t, "America/New_York")
	e.now = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	var all []spans.Span
	// Local 2026-11-01 runs from 04:00Z to 2026-11-02T05:00Z (25 hours).
	for ts := time.Date(2026, 11, 1, 3, 0, 0, 0, time.UTC); ts.Before(time.Date(2026, 11, 2, 6, 0, 0, 0, time.UTC)); ts = ts.Add(30 * time.Minute) {
		all = append(all, mk(ts, 1000, "GET /ny", "http.server", "", "ok", true))
	}
	e.write(t, nil, all...)
	ctx := context.Background()
	for _, h := range hoursBetween(e.hour("2026-10-31T22:00"), e.hour("2026-11-02T03:00")) {
		if err := spanrollup.RecomputeHour(ctx, e.pool, 7, e.loc, h); err != nil {
			t.Fatal(err)
		}
	}
	rep, ok := e.row(t, "2026-11-01T01:00", true, "GET /ny", "http.server")
	// 05:00Z..07:00Z at 30-minute spacing is 4 spans in one repeated local hour row.
	if !ok || rep.count != 4 {
		t.Fatalf("repeated 01:00 row = %+v ok=%v, want 4 spans from both UTC hours", rep, ok)
	}
	var n int
	if err := e.pool.QueryRow(ctx, `select count(*) from span_hourly where local_hour = '2026-11-01 01:00'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("rows for the repeated hour = %d, %v", n, err)
	}
	var day int64
	if err := e.pool.QueryRow(ctx, `select sum(count) from span_hourly where is_segment and local_hour::date = '2026-11-01'`).Scan(&day); err != nil {
		t.Fatal(err)
	}
	var want int64
	for _, s := range all {
		if s.Start.In(e.loc).Format("2006-01-02") == "2026-11-01" {
			want++
		}
	}
	if day != want || want != 50 {
		t.Fatalf("local day 2026-11-01: rollup %d, raw %d (want 50 = 25 h x 2)", day, want)
	}
}

func TestAppStartRows(t *testing.T) {
	e := setup(t, "UTC")
	base := time.Date(2026, 11, 18, 7, 0, 0, 0, time.UTC)
	var batch []spans.Span
	for _, ms := range []int32{1000, 3000} {
		s := mk(base, 5_000_000, "LaunchActivity", "app.start", "", "ok", true)
		b := spans.Bucket(int64(ms) * 1000)
		s.AppStartType, s.AppStartMS, s.AppStartBucket = "cold", &ms, &b
		batch = append(batch, s)
	}
	e.write(t, nil, batch...)
	if err := spanrollup.RecomputeHour(context.Background(), e.pool, 7, e.loc, e.hour("2026-11-18T07:00")); err != nil {
		t.Fatal(err)
	}
	row, ok := e.row(t, "2026-11-18T07:00", false, "LaunchActivity", "app.start.cold")
	if !ok || row.count != 2 || row.errors != 0 || row.sumUS != 4_000_000 {
		t.Fatalf("app start row = %+v ok=%v", row, ok)
	}
	if p50 := row.percentile(0.5); !within(p50, 1_000_000) {
		t.Fatalf("p50 = %v, want within 4.88%% of 1,000,000 us", p50)
	}
}

func TestExplainRollupRead(t *testing.T) {
	e := setup(t, "Asia/Kathmandu")
	ctx := context.Background()
	hour := e.hour("2026-11-18T08:00")
	// 30,000 spans in the local hour (02:15Z..03:15Z) plus neighbours.
	start := time.Date(2026, 11, 18, 2, 0, 0, 0, time.UTC)
	for chunk := 0; chunk < 3; chunk++ {
		var batch []spans.Span
		for i := chunk * 30_000; i < (chunk+1)*30_000; i++ {
			seg := i%3 == 0
			batch = append(batch, mk(start.Add(time.Duration(i)*40*time.Millisecond), int64(1+i%5000)*1000, fmt.Sprintf("GET /r%d", i%20), "http.server", fmt.Sprintf("d%d", i%7), "ok", seg))
		}
		e.write(t, nil, batch...)
	}
	if _, err := e.pool.Exec(ctx, `analyze spans`); err != nil {
		t.Fatal(err)
	}
	args, ok := spanrollup.RecomputeArgs(7, hour, e.loc)
	if !ok {
		t.Fatal("no bounds")
	}
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	for i, stmt := range spanrollup.RecomputeStatements() {
		t.Logf("statement %d", i+1)
		rows, err := tx.Query(ctx, "explain (analyze, buffers, costs off, timing off, summary on) "+stmt, args...)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				t.Fatal(err)
			}
			t.Log(line)
		}
		rows.Close()
	}
}
