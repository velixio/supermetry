package alerts

import (
	"testing"
	"time"

	"supermetry/internal/sentryingest/ingesttest"
	"supermetry/internal/sites"
)

// The Node unhandled-exception fixture arrives through the Sentry front door
// and fires the existing new-issue alert once; a duplicate event adds nothing.
func TestSentryEnvelopeFiresNewIssueAlertOnce(t *testing.T) {
	ingestAt := time.Date(2026, 11, 17, 13, 20, 0, 0, time.UTC)
	env := ingesttest.New(t, ingestAt)
	envelope := ingesttest.Envelope(t, "node/unhandled-exception", nil)
	if code := env.Post(t, envelope); code != 200 {
		t.Fatalf("status %d", code)
	}

	if _, err := env.Pool.Exec(t.Context(), `insert into alerts(site_id, kind, threshold, channel_ids, created_at) values ($1, 'app_new_issue', 1, '{1}', $2)`, ingesttest.SiteID, ingestAt.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	alert := Alert{ID: 1, CreatedAt: ingestAt.Add(-time.Hour), Kind: AlertAppNewIssue, Threshold: 1, SiteID: ingesttest.SiteID, Enabled: true, ChannelIDs: []int64{1}}
	repo := &fakeRepository{alerts: []Alert{alert}}
	deliverer := &fakeDeliverer{}
	now := ingestAt.Add(2 * time.Minute)
	store := NewAppStore(env.Pool)
	eval := NewEvaluator(repo, &fakeMetricSource{}, staticRegistry{site: sites.Site{ID: ingesttest.SiteID, Kind: sites.KindApp, TZ: time.UTC}}, nil, deliverer, func() time.Time { return now })
	eval.WithApp(store, store)

	run := func() {
		t.Helper()
		if err := eval.Run(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	run()
	if len(deliverer.notifications) != 1 {
		t.Fatalf("deliveries = %d, want 1", len(deliverer.notifications))
	}
	app := deliverer.notifications[0].App
	if app == nil || app.IssueTitle != "Error: fixture unhandled exception" || app.Release != "fixture@1.0.0" {
		t.Fatalf("notification = %+v", app)
	}

	// The same event again (an SDK retry) and a later evaluation: still one.
	if code := env.Post(t, envelope); code != 200 {
		t.Fatalf("duplicate status %d", code)
	}
	now = now.Add(5 * time.Minute)
	run()
	if len(deliverer.notifications) != 1 {
		t.Fatalf("after duplicate: deliveries = %d, want 1", len(deliverer.notifications))
	}
	var issues, events int
	if err := env.Pool.QueryRow(t.Context(), `select (select count(*) from app_issues), (select count(*) from app_events)`).Scan(&issues, &events); err != nil || issues != 1 || events != 1 {
		t.Fatalf("issues=%d events=%d err=%v", issues, events, err)
	}
}
