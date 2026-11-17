package appissues_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"supermetry/internal/appissues"
	"supermetry/internal/db"
)

func (f fixture) event(t *testing.T, id string, issue int64, ts time.Time) {
	t.Helper()
	if err := db.EnsureAppEventPartitions(context.Background(), f.pool, ts.AddDate(0, -1, 0), ts.AddDate(0, 1, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(context.Background(), `insert into app_events(site_id,ts,received_at,event_id,issue_id,kind,level,release,platform,occurrences)
values (7,$1,$1,$2,$3,'error','error','1','web',1)`, ts, id, issue); err != nil {
		t.Fatal(err)
	}
}

func TestFeedbackLinksToTheIssueOfItsEventAndKeepsUnknownUnlinked(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.event(t, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", f.issue, testNow.Add(-time.Minute))
	w := appissues.NewFeedbackWriter(f.pool, 0)
	hash := []byte("0123456789abcdef0123456789abcdef")
	err := w.Write(ctx, []appissues.FeedbackItem{
		{SiteID: 7, At: testNow, EventID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", EmailHash: hash, Message: "it broke", URL: "/checkout"},
		{SiteID: 7, At: testNow, EventID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Message: "unknown event"},
		{SiteID: 7, At: testNow, Message: "no event at all"},
		{SiteID: 4040, At: testNow, Message: "site gone"},
	})
	if err != nil {
		t.Fatal(err)
	}
	list, err := f.svc.Feedback(ctx, 7, 0, 50, time.Time{})
	if err != nil || len(list) != 3 {
		t.Fatalf("list = %+v %v", list, err)
	}
	byMsg := map[string]appissues.Feedback{}
	for _, fb := range list {
		byMsg[fb.Message] = fb
		if fb.Name != nil {
			t.Errorf("name stored: %q", *fb.Name)
		}
	}
	if got := byMsg["it broke"]; got.IssueID == nil || *got.IssueID != f.issue || !got.HasContact || got.URL == nil || *got.URL != "/checkout" {
		t.Fatalf("linked = %+v", got)
	}
	if got := byMsg["unknown event"]; got.IssueID != nil || got.HasContact {
		t.Fatalf("unknown = %+v", got)
	}
	// The issue view lists only its own report; another site's issue is not found.
	own, err := f.svc.Feedback(ctx, 7, f.issue, 50, time.Time{})
	if err != nil || len(own) != 1 || own[0].Message != "it broke" {
		t.Fatalf("issue list = %+v %v", own, err)
	}
	if _, err := f.svc.Feedback(ctx, 7, f.foreign, 50, time.Time{}); err == nil {
		t.Fatal("foreign issue was readable")
	}
	// The event arrives after its report: a later write links it.
	f.event(t, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", f.other, time.Now().Add(-time.Second))
	if _, err := f.pool.Exec(ctx, `update app_feedback set at = now() where event_id = 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb'`); err != nil {
		t.Fatal(err)
	}
	if err := w.Write(ctx, nil); err != nil {
		t.Fatal(err)
	}
	var linked *int64
	if err := f.pool.QueryRow(ctx, `select issue_id from app_feedback where event_id = 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb'`).Scan(&linked); err != nil || linked == nil || *linked != f.other {
		t.Fatalf("late link = %v %v", linked, err)
	}
	// Reads never return the hash.
	var raw string
	if err := f.pool.QueryRow(ctx, `select count(*)::text from app_feedback where email_hash is not null`).Scan(&raw); err != nil || raw != "1" {
		t.Fatalf("hash rows = %s %v", raw, err)
	}
}

func TestFeedbackWriterQueueIsBounded(t *testing.T) {
	f := newFixture(t)
	w := appissues.NewFeedbackWriter(f.pool, 2)
	ok := 0
	for range 5 {
		if w.Add(appissues.FeedbackItem{SiteID: 7, At: testNow, Message: "x"}) {
			ok++
		}
	}
	if ok != 2 {
		t.Fatalf("accepted %d, want 2", ok)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var n int
	if err := f.pool.QueryRow(context.Background(), `select count(*) from app_feedback`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("flushed on shutdown = %d %v", n, err)
	}
	if w.Add(appissues.FeedbackItem{SiteID: 7, Message: strings.Repeat("y", 3)}) {
		t.Fatal("add after shutdown succeeded")
	}
}

func TestImpactCountsTheLast30DaysOnly(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.event(t, "i1", f.issue, testNow.Add(-time.Hour))
	f.event(t, "i2", f.issue, testNow.Add(-2*time.Hour))
	f.event(t, "i3", f.issue, testNow.AddDate(0, 0, -40))
	if _, err := f.pool.Exec(ctx, `update app_events set user_hash = case event_id when 'i1' then '\x01'::bytea else '\x02'::bytea end,
 session_hash = '\x0a', release = case event_id when 'i2' then '2' else '1' end where event_id in ('i1','i2','i3')`); err != nil {
		t.Fatal(err)
	}
	got, err := f.svc.Impact(ctx, 7, f.issue)
	if err != nil || got.Events != 2 || got.Users != 2 || got.Sessions != 1 || len(got.Releases) != 2 || len(got.Platforms) != 1 || got.Platforms[0].Value != "web" || got.Days != 30 {
		t.Fatalf("impact = %+v %v", got, err)
	}
	if _, err := f.svc.Impact(ctx, 7, f.foreign); err == nil {
		t.Fatal("foreign issue readable")
	}
}
