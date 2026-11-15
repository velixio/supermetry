package appingest

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/db"
	"supermetry/internal/testpg"
)

func writerFixture(t *testing.T) (*pgxpool.Pool, *Intake, *Writer) {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into orgs(name,slug) values('Org','org');
insert into sites(id,org_id,public_id,name,timezone,retention_class,kind) overriding system value
values(7,1,'appsite00001','App','UTC',30,'app')`); err != nil {
		t.Fatal(err)
	}
	in := NewIntake(Config{QueueSize: 2000})
	return pool, in, NewWriter(pool, in, WriterConfig{})
}

func queued(id, fingerprint, release string, ts time.Time, occurrences int) Queued {
	e := ev(id)
	e.Fingerprint = fingerprint
	e.Release = release
	e.TS = ts
	e.Occurrences = occurrences
	e.ErrorType = "RuntimeError"
	e.Frames = "a.py:1 f"
	return Queued{SiteID: 7, ReceivedAt: testNow, Event: e}
}

type issueRow struct {
	ID                               int64
	Status                           string
	EventCount                       int64
	FirstSeen, LastSeen              time.Time
	FirstRelease, LastRelease, Title string
	ResolvedIn                       *string
}

func readIssue(t *testing.T, pool *pgxpool.Pool, fingerprint string) issueRow {
	t.Helper()
	var r issueRow
	err := pool.QueryRow(context.Background(), `select id,status,event_count,first_seen,last_seen,first_release,last_release,title,resolved_in_release
from app_issues where site_id=7 and fingerprint=$1`, fingerprint).
		Scan(&r.ID, &r.Status, &r.EventCount, &r.FirstSeen, &r.LastSeen, &r.FirstRelease, &r.LastRelease, &r.Title, &r.ResolvedIn)
	if err != nil {
		t.Fatalf("read issue %s: %v", fingerprint, err)
	}
	return r
}

func TestWriterCreatesAndIncrementsIssue(t *testing.T) {
	pool, _, w := writerFixture(t)
	ctx := context.Background()
	t1 := testNow.Add(-2 * time.Hour)
	if err := w.flush(ctx, []Queued{queued("a", "fp1", "1.0.0", t1, 2), queued("b", "fp1", "1.0.0", t1.Add(time.Minute), 3)}); err != nil {
		t.Fatal(err)
	}
	issue := readIssue(t, pool, "fp1")
	if issue.EventCount != 5 || issue.Status != "open" || issue.Title != "RuntimeError in a.py f" {
		t.Fatalf("issue = %+v", issue)
	}
	if err := w.flush(ctx, []Queued{queued("c", "fp1", "1.0.1", t1.Add(time.Hour), 1)}); err != nil {
		t.Fatal(err)
	}
	issue = readIssue(t, pool, "fp1")
	if issue.EventCount != 6 || issue.LastRelease != "1.0.1" || issue.FirstRelease != "1.0.0" {
		t.Fatalf("after second flush: %+v", issue)
	}
	var events, withIssue int
	if err := pool.QueryRow(ctx, `select count(*), count(*) filter (where issue_id=$1) from app_events`, issue.ID).Scan(&events, &withIssue); err != nil {
		t.Fatal(err)
	}
	if events != 3 || withIssue != 3 {
		t.Fatalf("events=%d withIssue=%d", events, withIssue)
	}
}

func TestWriterOutOfOrderKeepsFirstAndLastSeen(t *testing.T) {
	pool, _, w := writerFixture(t)
	ctx := context.Background()
	late := testNow.Add(-1 * time.Hour)
	early := testNow.Add(-5 * time.Hour)
	if err := w.flush(ctx, []Queued{queued("a", "fp", "2.0.0", late, 1)}); err != nil {
		t.Fatal(err)
	}
	if err := w.flush(ctx, []Queued{queued("b", "fp", "1.0.0", early, 1)}); err != nil {
		t.Fatal(err)
	}
	issue := readIssue(t, pool, "fp")
	if !issue.FirstSeen.Equal(early) || !issue.LastSeen.Equal(late) {
		t.Fatalf("first=%v last=%v", issue.FirstSeen, issue.LastSeen)
	}
	if issue.FirstRelease != "1.0.0" || issue.LastRelease != "2.0.0" || issue.EventCount != 2 {
		t.Fatalf("issue = %+v", issue)
	}
	// Within one batch the order of arrival does not matter either.
	if err := w.flush(ctx, []Queued{queued("d", "fp2", "9.0.0", late, 1), queued("c", "fp2", "8.0.0", early, 1)}); err != nil {
		t.Fatal(err)
	}
	issue = readIssue(t, pool, "fp2")
	if !issue.FirstSeen.Equal(early) || !issue.LastSeen.Equal(late) || issue.FirstRelease != "8.0.0" || issue.LastRelease != "9.0.0" {
		t.Fatalf("one batch: %+v", issue)
	}
}

func TestWriterRegression(t *testing.T) {
	pool, _, w := writerFixture(t)
	ctx := context.Background()
	for _, fp := range []string{"same", "newer", "unset", "ignored"} {
		if err := w.flush(ctx, []Queued{queued("seed-"+fp, fp, "1.0.0", testNow.Add(-time.Hour), 1)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `update app_issues set status='resolved', resolved_in_release='1.2.0' where fingerprint in ('same','newer');
update app_issues set status='resolved', resolved_in_release=null where fingerprint='unset';
update app_issues set status='ignored' where fingerprint='ignored'`); err != nil {
		t.Fatal(err)
	}
	in := testNow.Add(-time.Minute)
	batch := []Queued{
		queued("1", "same", "1.2.0", in, 1), // reported in the release that fixed it: not a regression
		queued("2", "newer", "1.3.0", in, 1),
		queued("3", "unset", "1.0.0", in, 1),
		queued("4", "ignored", "1.3.0", in, 1),
	}
	if err := w.flush(ctx, batch); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"same": "resolved", "newer": "regressed", "unset": "regressed", "ignored": "ignored"}
	for fp, status := range want {
		if got := readIssue(t, pool, fp).Status; got != status {
			t.Errorf("%s: status %q, want %q", fp, got, status)
		}
	}
}

func TestWriterHashesIdsAndNeverStoresRawOnes(t *testing.T) {
	pool, _, w := writerFixture(t)
	ctx := context.Background()
	a := queued("a", "fp", "1", testNow.Add(-time.Hour), 1)
	a.Event.InstallID, a.Event.SessionID = "install-raw-123", "session-raw-456"
	b := queued("b", "fp", "1", testNow.Add(-time.Minute), 1)
	b.Event.InstallID = "install-raw-123"
	s := queued("s", "", "1", testNow.Add(-time.Minute), 1)
	s.Event.Kind = "session"
	s.Event.InstallID = "install-raw-123"
	if err := w.flush(ctx, []Queued{a, b}); err != nil {
		t.Fatal(err)
	}
	if err := w.flush(ctx, []Queued{s}); err != nil {
		t.Fatal(err)
	}
	var secret []byte
	if err := pool.QueryRow(ctx, `select secret from app_hash_secrets where site_id=7`).Scan(&secret); err != nil || len(secret) != 32 {
		t.Fatalf("secret: %v len=%d", err, len(secret))
	}
	mac := func(v string) []byte {
		h := hmac.New(sha256.New, secret)
		h.Write([]byte(v))
		return h.Sum(nil)
	}
	var matches, total int
	if err := pool.QueryRow(ctx, `select count(*) filter (where install_hash=$1), count(*) from app_events`, mac("install-raw-123")).Scan(&matches, &total); err != nil {
		t.Fatal(err)
	}
	if matches != 3 || total != 3 {
		t.Fatalf("stable install hash: matches=%d total=%d", matches, total)
	}
	var sessionMatches int
	if err := pool.QueryRow(ctx, `select count(*) from app_events where session_hash=$1`, mac("session-raw-456")).Scan(&sessionMatches); err != nil || sessionMatches != 1 {
		t.Fatalf("session hash: %v %d", err, sessionMatches)
	}
	var leaked int
	if err := pool.QueryRow(ctx, `select count(*) from app_events e where e::text like '%raw-123%' or e::text like '%raw-456%'`).Scan(&leaked); err != nil || leaked != 0 {
		t.Fatalf("raw id leaked into app_events: %v %d", err, leaked)
	}
	var sessionIssues int
	if err := pool.QueryRow(ctx, `select count(*) from app_events where kind='session' and issue_id is not null`).Scan(&sessionIssues); err != nil || sessionIssues != 0 {
		t.Fatalf("session pings must not create issues: %v %d", err, sessionIssues)
	}
	var issues int
	if err := pool.QueryRow(ctx, `select count(*) from app_issues`).Scan(&issues); err != nil || issues != 1 {
		t.Fatalf("issues: %v %d", err, issues)
	}
}

func TestWriterCreatesPartitionsForLateEvents(t *testing.T) {
	pool, _, w := writerFixture(t)
	ctx := context.Background()
	old := time.Date(2026, 3, 5, 10, 0, 0, 0, time.UTC)
	if err := w.flush(ctx, []Queued{queued("a", "fp", "1", old, 1)}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pool.QueryRow(ctx, `select count(*) from app_events_202603`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("march partition: %v n=%d", err, n)
	}
}

func TestWriterSkipsEventsOfDeletedSites(t *testing.T) {
	pool, _, w := writerFixture(t)
	ctx := context.Background()
	gone := queued("g", "fp", "1", testNow.Add(-time.Minute), 1)
	gone.SiteID = 99
	if err := w.flush(ctx, []Queued{gone, queued("ok", "fp", "1", testNow.Add(-time.Minute), 1)}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pool.QueryRow(ctx, `select count(*) from app_events`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("rows: %v %d", err, n)
	}
}

func TestWriterRunFlushesOnSizeAndOnShutdown(t *testing.T) {
	pool, in, _ := writerFixture(t)
	w := NewWriter(pool, in, WriterConfig{BatchSize: 3, FlushEvery: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	site := appSite(nil)
	for i := range 3 {
		in.Accept(site, "k", testNow, ev(fmt.Sprint("size", i)))
	}
	waitFor(t, func() bool { return countEvents(t, pool) == 3 })
	in.Accept(site, "k", testNow, ev("tail1"))
	in.Accept(site, "k", testNow, ev("tail2"))
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := countEvents(t, pool); got != 5 {
		t.Fatalf("rows after shutdown = %d, want 5", got)
	}
}

func TestWriterRunFlushesOnInterval(t *testing.T) {
	pool, in, _ := writerFixture(t)
	w := NewWriter(pool, in, WriterConfig{BatchSize: 500, FlushEvery: 50 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	in.Accept(appSite(nil), "k", testNow, ev("one"))
	waitFor(t, func() bool { return countEvents(t, pool) == 1 })
	cancel()
	<-done
}

func countEvents(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `select count(*) from app_events`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}
