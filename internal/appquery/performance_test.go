package appquery_test

import (
	"context"
	"errors"
	"math"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/appquery"
	"supermetry/internal/db"
	"supermetry/internal/spanrollup"
	"supermetry/internal/spans"
	"supermetry/internal/testpg"
)

var perfNow = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

type perfEnv struct {
	pool  *pgxpool.Pool
	q     *appquery.Querier
	scope appquery.Scope
}

func perfSetup(t *testing.T) *perfEnv {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into orgs(name,slug) values('Org','org'),('Other','other')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into sites(id,org_id,public_id,name,timezone,retention_class,kind) overriding system value
values(7,1,'perfsite0001','App','UTC',30,'app'),(9,2,'perfsite0002','Other App','UTC',30,'app')`); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureDailyPartitions(ctx, pool, "spans", time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureAppEventPartitions(ctx, pool, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	return &perfEnv{pool: pool, q: appquery.New(pool, func() time.Time { return perfNow }),
		scope: appquery.Scope{SiteID: 7, OrgID: 1, TZ: time.UTC, RetentionDays: 30}}
}

var perfSeq atomic.Uint32

func perfSpan(site int32, start time.Time, durUS int64, name, op, desc, status string, segment bool) spans.Span {
	n := perfSeq.Add(1)
	s := spans.Span{SiteID: site, Start: start, DurationUS: durUS, Name: name, Op: op, Description: desc, Status: status,
		Release: "1.0", Environment: "prod", Platform: "go", IsSegment: segment}
	s.TraceID[0], s.TraceID[1], s.TraceID[2] = byte(n), byte(n>>8), byte(n>>16)
	s.SpanID[0], s.SpanID[1], s.SpanID[2] = byte(n), byte(n>>8), byte(n>>16)
	s.SegmentID = s.SpanID
	return s
}

func (e *perfEnv) write(t *testing.T, batch ...spans.Span) {
	t.Helper()
	w := spans.NewWriter(e.pool, spans.WriterConfig{Batch: 1 << 30, Flush: time.Hour, QueueSpans: 1 << 20,
		Now: func() time.Time { return perfNow }, Location: func(int32) *time.Location { return time.UTC }}, nil, nil)
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

func (e *perfEnv) roll(t *testing.T, hours ...string) {
	t.Helper()
	for _, h := range hours {
		ts, err := time.Parse("2006-01-02T15:04", h)
		if err != nil {
			t.Fatal(err)
		}
		if err := spanrollup.RecomputeHour(context.Background(), e.pool, 7, time.UTC, ts); err != nil {
			t.Fatal(err)
		}
	}
}

func today() appquery.Filter {
	return appquery.Filter{From: time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)}
}

func within(got, want float64) bool { return math.Abs(got-want)/want <= 0.0488 }

func at(h, m, s int) time.Time { return time.Date(2026, 11, 18, h, m, s, 0, time.UTC) }

// itemsFixture: 100 GET /items segments of 1..100 ms (5 failed) in the closed
// hour 10:00 and 10 of 200 ms in the open hour 12:00.
func itemsFixture(e *perfEnv, t *testing.T) {
	var batch []spans.Span
	for i := 1; i <= 100; i++ {
		status := ""
		if i%20 == 0 {
			status = "internal_error"
		}
		batch = append(batch, perfSpan(7, at(10, 0, 0).Add(time.Duration(i)*time.Second), int64(i)*1000, "GET /items", "http.server", "", status, true))
	}
	for i := 0; i < 10; i++ {
		batch = append(batch, perfSpan(7, at(12, 0, 10+i), 200_000, "GET /items", "http.server", "", "", true))
	}
	e.write(t, batch...)
}

func TestTransactionsClosedAndOpenHours(t *testing.T) {
	e := perfSetup(t)
	itemsFixture(e, t)
	ctx := context.Background()

	// Nothing rolled up yet: every hour is read from raw spans.
	raw, err := e.q.Transactions(ctx, e.scope, today(), "p95", 10)
	if err != nil {
		t.Fatal(err)
	}
	e.roll(t, "2026-11-18T10:00") // 10:00 closed, 12:00 stays open
	got, err := e.q.Transactions(ctx, e.scope, today(), "p95", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(raw) != 1 {
		t.Fatalf("rows raw=%d rolled=%d, want 1", len(raw), len(got))
	}
	if *raw[0].ErrorRate != *got[0].ErrorRate || raw[0].P95 != got[0].P95 || raw[0].P50 != got[0].P50 || raw[0].P99 != got[0].P99 || raw[0].Count != got[0].Count || raw[0].Errors != got[0].Errors {
		t.Errorf("closed and open hours disagree:\nraw    %+v\nrolled %+v", raw[0], got[0])
	}
	r := got[0]
	if r.Name != "GET /items" || r.Count != 110 || r.Errors != 5 {
		t.Fatalf("row = %+v", r)
	}
	if !within(r.P50, 55) {
		t.Errorf("p50 = %v ms, want 55 within 4.88%%", r.P50)
	}
	if !within(r.P99, 200) {
		t.Errorf("p99 = %v ms, want 200 within 4.88%%", r.P99)
	}
	if r.ErrorRate == nil || math.Abs(*r.ErrorRate-5.0/110) > 1e-9 {
		t.Errorf("error rate = %v, want %v", r.ErrorRate, 5.0/110)
	}
	// The window runs from 00:00 to now (12:00): 720 minutes.
	if math.Abs(r.Throughput-110.0/720) > 1e-9 {
		t.Errorf("throughput = %v, want %v", r.Throughput, 110.0/720)
	}
}

func TestTransactionsDirtyHourReadsRaw(t *testing.T) {
	e := perfSetup(t)
	itemsFixture(e, t)
	e.roll(t, "2026-11-18T10:00")
	// A late span lands in the rolled hour and marks it dirty: reads must see it.
	e.write(t, perfSpan(7, at(10, 30, 0), 10_000, "GET /items", "http.server", "", "", true))
	if err := spanrollup.MarkDirty(context.Background(), e.pool, 7, []time.Time{at(10, 0, 0)}); err != nil {
		t.Fatal(err)
	}
	got, err := e.q.Transactions(context.Background(), e.scope, today(), "p95", 10)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Count != 111 {
		t.Errorf("count = %d, want 111 (dirty hour read from raw)", got[0].Count)
	}
}

func TestTransactionsSortAndLimit(t *testing.T) {
	e := perfSetup(t)
	e.write(t,
		perfSpan(7, at(10, 0, 1), 10_000, "A", "http.server", "", "", true),
		perfSpan(7, at(10, 0, 2), 900_000, "B", "http.server", "", "", true),
		perfSpan(7, at(10, 0, 3), 900_000, "B", "http.server", "", "internal_error", true),
		perfSpan(7, at(10, 0, 4), 500_000, "C", "http.server", "", "", true))
	ctx := context.Background()
	rows, _ := e.q.Transactions(ctx, e.scope, today(), "p95", 2)
	if len(rows) != 2 || rows[0].Name != "B" || rows[1].Name != "C" {
		t.Errorf("by p95 = %+v", rows)
	}
	rows, _ = e.q.Transactions(ctx, e.scope, today(), "errors", 1)
	if len(rows) != 1 || rows[0].Name != "B" {
		t.Errorf("by errors = %+v", rows)
	}
	if _, err := e.q.Transactions(ctx, e.scope, today(), "bogus", 5); !errors.Is(err, appquery.ErrInvalid) {
		t.Errorf("bad sort err = %v", err)
	}
}

func TestSiteScoped(t *testing.T) {
	e := perfSetup(t)
	itemsFixture(e, t)
	other := e.scope
	other.SiteID, other.OrgID = 9, 2
	rows, err := e.q.Transactions(context.Background(), other, today(), "p95", 10)
	if err != nil || len(rows) != 0 {
		t.Fatalf("other site rows = %v err = %v", rows, err)
	}
	if _, err := e.q.Transaction(context.Background(), other, today(), "GET /items"); !errors.Is(err, appquery.ErrNotFound) {
		t.Errorf("other site detail err = %v, want ErrNotFound", err)
	}
	// Another site's trace is not found either.
	var raw []byte
	if err := e.pool.QueryRow(context.Background(), `select trace_id from spans limit 1`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var tid [16]byte
	copy(tid[:], raw)
	if _, err := e.q.Trace(context.Background(), other, tid); !errors.Is(err, appquery.ErrNotFound) {
		t.Errorf("other site trace err = %v, want ErrNotFound", err)
	}
}

func TestTraceTreeAndLinkedError(t *testing.T) {
	e := perfSetup(t)
	ctx := context.Background()
	var tid [16]byte
	tid[0] = 0xab
	seg := perfSpan(7, at(10, 0, 0), 100_000, "GET /items", "http.server", "", "", true)
	db1 := perfSpan(7, at(10, 0, 0).Add(10*time.Millisecond), 40_000, "", "db", "select * from items", "", false)
	q1 := perfSpan(7, at(10, 0, 0).Add(20*time.Millisecond), 10_000, "", "db.row", "row", "", false)
	for _, s := range []*spans.Span{&seg, &db1, &q1} {
		s.TraceID = tid
		s.SegmentID = seg.SpanID
	}
	db1.ParentID = &seg.SpanID
	q1.ParentID = &db1.SpanID
	e.write(t, q1, seg, db1) // insertion order must not matter
	var issue int64
	if err := e.pool.QueryRow(ctx, `insert into app_issues(site_id,fingerprint,kind,error_type,title,first_seen,last_seen,event_count)
values(7,'fpx','error','Err','Boom in handler',$1,$1,1) returning id`, at(10, 0, 0)).Scan(&issue); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `insert into app_events(site_id,ts,received_at,event_id,issue_id,kind,trace_id,span_id)
values(7,$1,$1,'evt1',$2,'error',$3,$4)`, at(10, 0, 0).Add(15*time.Millisecond), issue, tid[:], db1.SpanID[:]); err != nil {
		t.Fatal(err)
	}
	tr, err := e.q.Trace(ctx, e.scope, tid)
	if err != nil {
		t.Fatal(err)
	}
	var depths []int
	for _, s := range tr.Spans {
		depths = append(depths, s.Depth)
	}
	if len(depths) != 3 || depths[0] != 0 || depths[1] != 1 || depths[2] != 2 {
		t.Fatalf("depths = %v, want [0 1 2]", depths)
	}
	if tr.Spans[1].StartOffsetMS != 10 || tr.Spans[2].StartOffsetMS != 20 || tr.DurationMS != 100 {
		t.Errorf("offsets %v %v duration %v", tr.Spans[1].StartOffsetMS, tr.Spans[2].StartOffsetMS, tr.DurationMS)
	}
	if len(tr.Errors) != 1 || tr.Errors[0].IssueID == nil || *tr.Errors[0].IssueID != issue || tr.Errors[0].Title != "Boom in handler" {
		t.Errorf("errors = %+v", tr.Errors)
	}
	if tr.Truncated {
		t.Error("small trace marked truncated")
	}
}

func TestTraceTruncated(t *testing.T) {
	e := perfSetup(t)
	var tid [16]byte
	tid[0] = 0xcd
	var batch []spans.Span
	for i := 0; i < appquery.MaxTraceSpans+1; i++ {
		s := perfSpan(7, at(10, 0, 0).Add(time.Duration(i)*time.Millisecond), 1000, "", "task", "t", "", false)
		s.TraceID = tid
		batch = append(batch, s)
	}
	e.write(t, batch...)
	tr, err := e.q.Trace(context.Background(), e.scope, tid)
	if err != nil {
		t.Fatal(err)
	}
	if !tr.Truncated || len(tr.Spans) != appquery.MaxTraceSpans {
		t.Errorf("truncated=%v spans=%d", tr.Truncated, len(tr.Spans))
	}
}

func TestRegressions(t *testing.T) {
	e := perfSetup(t)
	var batch []spans.Span
	add := func(release, name string, n int, durUS int64) {
		for i := 0; i < n; i++ {
			s := perfSpan(7, at(10, 0, 0).Add(time.Duration(len(batch))*time.Millisecond), durUS, name, "http.server", "", "", true)
			s.Release = release
			batch = append(batch, s)
		}
	}
	add("1.0", "GET /slowed", 40, 100_000)
	add("2.0", "GET /slowed", 40, 130_000)
	add("1.0", "GET /few", 40, 100_000)
	add("2.0", "GET /few", 25, 300_000) // too few samples on the target side
	add("1.0", "GET /same", 40, 100_000)
	add("2.0", "GET /same", 40, 105_000)
	e.write(t, batch...)
	e.roll(t, "2026-11-18T10:00")
	rows, err := e.q.Regressions(context.Background(), e.scope, today(), "1.0", "2.0")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Name != "GET /slowed" {
		t.Fatalf("rows = %+v", rows)
	}
	// Bucket estimates are within 4.88%, so the delta is 0.30 within that band.
	if rows[0].Delta < 0.2 || rows[0].Delta > 0.4 {
		t.Errorf("delta = %v, want about 0.30", rows[0].Delta)
	}
	if _, err := e.q.Regressions(context.Background(), e.scope, today(), "1.0", "1.0"); !errors.Is(err, appquery.ErrInvalid) {
		t.Errorf("same release err = %v", err)
	}
}

func TestNetworkFailures(t *testing.T) {
	e := perfSetup(t)
	desc := "GET https://api.example.com/v1/items/*"
	status := func(n int16) *int16 { return &n }
	var batch []spans.Span
	for i := 0; i < 4; i++ {
		s := perfSpan(7, at(10, 0, i), 20_000, "", "http.client", desc, "ok", false)
		s.HTTPStatus = status(200)
		if i == 0 {
			s.HTTPStatus = status(404)
		}
		batch = append(batch, s)
	}
	e.write(t, batch...)
	e.roll(t, "2026-11-18T10:00")
	rows, err := e.q.NetworkFailures(context.Background(), e.scope, today())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Description != desc || rows[0].Count != 4 || rows[0].Errors != 1 || rows[0].ErrorRate == nil || *rows[0].ErrorRate != 0.25 {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestOverviewAndVitalsLink(t *testing.T) {
	e := perfSetup(t)
	itemsFixture(e, t)
	ctx := context.Background()
	o, err := e.q.PerformanceOverview(ctx, e.scope, today())
	if err != nil {
		t.Fatal(err)
	}
	if o.Transactions != 110 || o.Errors != 5 || len(o.Slowest) != 1 || len(o.MostFailing) != 1 || o.VitalsLink != nil {
		t.Fatalf("overview = %+v", o)
	}
	if _, err := e.pool.Exec(ctx, `insert into sites(id,org_id,public_id,name,timezone,retention_class,kind,settings) overriding system value
values(8,1,'perfweb00001','Web','UTC',30,'web','{"app_link":7}')`); err != nil {
		t.Fatal(err)
	}
	lcp := 2400.0
	q := e.q.WithVitals(func(_ context.Context, site int32, _ appquery.Filter) (*float64, *float64, *float64, error) {
		if site != 8 {
			t.Errorf("vitals for site %d", site)
		}
		return &lcp, nil, nil, nil
	})
	o, err = q.PerformanceOverview(ctx, e.scope, today())
	if err != nil {
		t.Fatal(err)
	}
	if o.VitalsLink == nil || o.VitalsLink.SiteID != 8 || o.VitalsLink.LCPp75 == nil || *o.VitalsLink.LCPp75 != 2400 {
		t.Errorf("vitals link = %+v", o.VitalsLink)
	}
	// The other organisation's app does not see this org's web site.
	other := e.scope
	other.SiteID, other.OrgID = 9, 2
	if o, _ = q.PerformanceOverview(ctx, other, today()); o.VitalsLink != nil {
		t.Errorf("other org sees vitals link %+v", o.VitalsLink)
	}
}

func TestTransactionDetail(t *testing.T) {
	e := perfSetup(t)
	ctx := context.Background()
	var batch []spans.Span
	for i := 0; i < 3; i++ {
		seg := perfSpan(7, at(10, 0, i), int64(100+i*50)*1000, "GET /items", "http.server", "", "", true)
		if i == 0 {
			seg.AppStartType = "cold"
			ms, b := int32(1000), spans.Bucket(1_000_000)
			seg.AppStartMS, seg.AppStartBucket = &ms, &b
		}
		child := perfSpan(7, at(10, 0, i).Add(time.Millisecond), 30_000, "", "db", "select * from items where id = ?", "", false)
		child.TraceID, child.SegmentID, child.ParentID = seg.TraceID, seg.SpanID, &seg.SpanID
		batch = append(batch, seg, child)
	}
	e.write(t, batch...)
	e.roll(t, "2026-11-18T10:00")
	d, err := e.q.Transaction(ctx, e.scope, today(), "GET /items")
	if err != nil {
		t.Fatal(err)
	}
	if d.Summary.Count != 3 || len(d.Series) != 1 || d.Series[0].Count != 3 || d.Series[0].P95 == nil {
		t.Errorf("summary/series = %+v / %+v", d.Summary, d.Series)
	}
	if len(d.Spans) != 1 || d.Spans[0].Op != "db" || d.Spans[0].Count != 3 || d.Spans[0].TotalMS != 90 {
		t.Errorf("span groups = %+v", d.Spans)
	}
	if len(d.Slowest) != 3 || d.Slowest[0].DurationMS != 200 || len(d.Slowest[0].TraceID) != 32 {
		t.Errorf("slowest = %+v", d.Slowest)
	}
	if len(d.AppStart) != 1 || d.AppStart[0].Type != "cold" || !within(d.AppStart[0].P50, 1000) {
		t.Errorf("app start = %+v", d.AppStart)
	}
	if _, err := e.q.Transaction(ctx, e.scope, today(), "nope"); !errors.Is(err, appquery.ErrNotFound) {
		t.Errorf("missing transaction err = %v", err)
	}
}
