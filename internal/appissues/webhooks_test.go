package appissues_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"supermetry/internal/appissues"
)

type fakeSender struct {
	fail  int // fail this many sends first
	sends []appissues.WebhookMessage
	chans []int64
}

func (s *fakeSender) Send(_ context.Context, channelID int64, m appissues.WebhookMessage) error {
	if s.fail > 0 {
		s.fail--
		return errors.New("down")
	}
	s.sends = append(s.sends, m)
	s.chans = append(s.chans, channelID)
	return nil
}

func (f fixture) channel(t *testing.T, org int) int64 {
	t.Helper()
	var id int64
	if err := f.pool.QueryRow(context.Background(), `insert into notification_channels(org_id,kind,name,enabled,config) values ($1,'webhook','hook',true,'{}') returning id`, org).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestWebhookSettingsValidation(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	own, foreign := f.channel(t, 1), f.channel(t, 2)
	for name, in := range map[string]appissues.WebhookSettings{
		"foreign channel": {ChannelIDs: []int64{foreign}, Kinds: []string{"created"}},
		"unknown kind":    {ChannelIDs: []int64{own}, Kinds: []string{"deleted"}},
		"no such channel": {ChannelIDs: []int64{9999}, Kinds: []string{"created"}},
	} {
		if err := f.svc.SetWebhookSettings(ctx, 1, 7, in); !errors.Is(err, appissues.ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if err := f.svc.SetWebhookSettings(ctx, 1, 7, appissues.WebhookSettings{ChannelIDs: []int64{own}, Kinds: []string{"resolved", "created", "resolved"}}); err != nil {
		t.Fatal(err)
	}
	got, err := f.svc.WebhookSettings(ctx, 7)
	if err != nil || len(got.ChannelIDs) != 1 || strings.Join(got.Kinds, ",") != "created,resolved" {
		t.Fatalf("settings = %+v %v", got, err)
	}
	empty, err := f.svc.WebhookSettings(ctx, 8)
	if err != nil || empty.ChannelIDs == nil || empty.Kinds == nil || len(empty.Kinds) != 0 {
		t.Fatalf("unset settings = %+v %v", empty, err)
	}
}

func TestWebhookFiresOncePerSubscribedKindWithCleanPayload(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	ch := f.channel(t, 1)
	if err := f.svc.SetWebhookSettings(ctx, 1, 7, appissues.WebhookSettings{ChannelIDs: []int64{ch}, Kinds: []string{"resolved", "assigned", "regressed", "created", "github_linked"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `update app_issues set culprit='main.go', level='error' where id=$1`, f.issue); err != nil {
		t.Fatal(err)
	}
	// Subscribed: resolved, assigned, github_linked. Not subscribed: priority.
	for _, c := range []appissues.Change{
		{Status: s("resolved")}, {Assignee: i64(f.alice)}, {Priority: s("high")},
		{GitHubURL: s("https://github.com/acme/app/issues/12")},
	} {
		if _, err := f.svc.Update(ctx, 7, []int64{f.issue}, f.actor, c); err != nil {
			t.Fatal(err)
		}
	}
	// System events: first sight and regression, from the ingest writer.
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := appissues.RecordSystem(ctx, tx, 7, f.other, appissues.KindFirstSeen, map[string]any{"release": "1"}); err != nil {
		t.Fatal(err)
	}
	if err := appissues.RecordSystem(ctx, tx, 7, f.other, appissues.KindRegressed, nil); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	// RecordSystem stamps wall-clock time while the service clock is fixed at
	// testNow, so rows written after that date would never be due.
	if _, err := f.pool.Exec(ctx, `update app_issue_webhook_outbox set next_at = least(next_at, $1)`, testNow); err != nil {
		t.Fatal(err)
	}
	snd := &fakeSender{}
	// RecordSystem stamps rows with the wall clock, while f.svc runs on the fixed
	// testNow. Deliver with a clock past both so the test does not depend on the
	// date it runs.
	deliverAt := testNow
	if wall := time.Now(); wall.After(deliverAt) {
		deliverAt = wall
	}
	deliverer := appissues.New(f.pool, func() time.Time { return deliverAt.Add(time.Minute) })
	if err := deliverer.DeliverWebhooks(ctx, snd, "https://app.example.test"); err != nil {
		t.Fatal(err)
	}
	var types []string
	for _, m := range snd.sends {
		types = append(types, m.Type)
	}
	if got := strings.Join(types, ","); got != "resolved,assigned,github_linked,created,regressed" {
		t.Fatalf("types = %s", got)
	}
	m := snd.sends[0]
	raw, _ := json.Marshal(m.Payload)
	for _, bad := range []string{"user_hash", "@", "install", "email"} {
		if strings.Contains(strings.ToLower(string(raw)), bad) {
			t.Errorf("payload contains %q: %s", bad, raw)
		}
	}
	var p struct {
		Type  string `json:"type"`
		Issue struct {
			ID      int64  `json:"id"`
			Title   string `json:"title"`
			Culprit string `json:"culprit"`
			Level   string `json:"level"`
			Status  string `json:"status"`
			URL     string `json:"url"`
			Events  int64  `json:"events"`
		} `json:"issue"`
		Actor struct {
			Kind string `json:"kind"`
			Name string `json:"name"`
		} `json:"actor"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	if p.Type != "resolved" || p.Issue.ID != f.issue || p.Issue.Title != "Title" || p.Issue.Culprit != "main.go" || p.Issue.Level != "error" || p.Issue.Events != 5 ||
		!strings.HasPrefix(p.Issue.URL, "https://app.example.test/app/org/") || !strings.HasSuffix(p.Issue.URL, "/issues/"+itoa(f.issue)) {
		t.Fatalf("payload = %s", raw)
	}
	if p.Actor.Kind != "user" || p.Actor.Name != "" { // the test users are named by their emails: the name is withheld
		t.Fatalf("actor = %+v", p.Actor)
	}
	if last := snd.sends[len(snd.sends)-1]; last.Payload.Actor.Kind != "system" {
		t.Fatalf("system actor = %+v", last.Payload.Actor)
	}
	// A second run sends nothing.
	snd2 := &fakeSender{}
	if err := f.svc.DeliverWebhooks(ctx, snd2, ""); err != nil || len(snd2.sends) != 0 {
		t.Fatalf("redelivered %d: %v", len(snd2.sends), err)
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

func TestWebhookRetriesWithBackoffThenGivesUp(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	now := testNow
	svc := appissues.New(f.pool, func() time.Time { return now })
	ch := f.channel(t, 1)
	if err := svc.SetWebhookSettings(ctx, 1, 7, appissues.WebhookSettings{ChannelIDs: []int64{ch}, Kinds: []string{"resolved"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Update(ctx, 7, []int64{f.issue}, f.actor, appissues.Change{Status: s("resolved")}); err != nil {
		t.Fatal(err)
	}
	snd := &fakeSender{fail: 1}
	if err := svc.DeliverWebhooks(ctx, snd, ""); err != nil || len(snd.sends) != 0 {
		t.Fatalf("first run: %v %d", err, len(snd.sends))
	}
	// Not due yet: nothing is sent.
	if err := svc.DeliverWebhooks(ctx, snd, ""); err != nil || len(snd.sends) != 0 {
		t.Fatalf("early retry: %v %d", err, len(snd.sends))
	}
	now = now.Add(time.Minute)
	if err := svc.DeliverWebhooks(ctx, snd, ""); err != nil || len(snd.sends) != 1 {
		t.Fatalf("retry: %v %d", err, len(snd.sends))
	}
	// A permanently failing receiver is dropped after the attempt limit.
	if _, err := svc.Update(ctx, 7, []int64{f.other}, f.actor, appissues.Change{Status: s("resolved")}); err != nil {
		t.Fatal(err)
	}
	bad := &fakeSender{fail: 1000}
	for i := 0; i < 12; i++ {
		now = now.Add(24 * time.Hour)
		if err := svc.DeliverWebhooks(ctx, bad, ""); err != nil {
			t.Fatal(err)
		}
	}
	var open int
	if err := f.pool.QueryRow(ctx, `select count(*) from app_issue_webhook_outbox where done_at is null`).Scan(&open); err != nil || open != 0 {
		t.Fatalf("undelivered rows = %d %v", open, err)
	}
}
