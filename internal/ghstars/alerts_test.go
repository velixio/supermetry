package ghstars

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"supermetry/internal/notifications"
	"supermetry/internal/platform/authz"
)

// The test clock is 2026-10-10T12:00:00Z, so the six hour window is
// (06:00, 12:00] and the baseline is (2026-09-26T06:00, 2026-10-10T06:00],
// which holds 56 six hour slots.
const (
	windowEvent = "2026-10-10T08:00:00Z"
	baseEvent   = "2026-10-01T00:00:00Z"
)

var userSeq int64 = 1000

// addEvents inserts n events of one action, one minute apart from start.
func addEvents(t *testing.T, svc *Service, repoID int64, action, source string, n int, start string) {
	t.Helper()
	first := userSeq
	userSeq += int64(n)
	_, err := svc.pool.Exec(context.Background(), `
		insert into github_star_events(repo_id,user_id,login,action,occurred_at,source)
		select $1, $2 + g, 'secretlogin' || g, $3, $4::timestamptz + g * interval '1 minute', $5
		from generate_series(0, $6 - 1) g`, repoID, first, action, start, source, n)
	if err != nil {
		t.Fatal(err)
	}
}

func alertRepoFixture(t *testing.T, svc *Service, orgID int64, name string, verified int) int64 {
	t.Helper()
	id := insertRepoRow(t, svc.pool, orgID, name)
	if _, err := svc.pool.Exec(context.Background(), `update github_star_repos set backfill_complete=true, verified_stars=$2 where id=$1`, id, verified); err != nil {
		t.Fatal(err)
	}
	return id
}

func setAlertVerified(t *testing.T, svc *Service, id int64, stars int) {
	t.Helper()
	if _, err := svc.pool.Exec(context.Background(), `update github_star_repos set verified_stars=$2 where id=$1`, id, stars); err != nil {
		t.Fatal(err)
	}
}

func evaluate(t *testing.T, svc *Service) {
	t.Helper()
	if err := svc.EvaluateAlerts(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func kinds(events []notifications.Recorded) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = string(e.Event.Kind) + " " + e.Event.Key
	}
	return out
}

func TestMilestoneFiresOnlyWhenCrossedUpwardAndNeverOnFirstSight(t *testing.T) {
	svc, _ := newTestService(t)
	fake := &notifications.Fake{}
	svc.Notifier = fake
	org := newOrg(t, svc, "milestones")
	// Enabling a repository that already has 5,000 stars must not replay the
	// ten milestones below it.
	big := alertRepoFixture(t, svc, org, "acme/big", 5000)
	evaluate(t, svc)
	if got := fake.Events(); len(got) != 0 {
		t.Fatalf("first evaluation sent %v", kinds(got))
	}
	evaluate(t, svc)
	setAlertVerified(t, svc, big, 5100)
	evaluate(t, svc)
	if got := fake.Events(); len(got) != 0 {
		t.Fatalf("no milestone crossed, sent %v", kinds(got))
	}

	small := alertRepoFixture(t, svc, org, "acme/small", 2400)
	evaluate(t, svc) // baseline
	setAlertVerified(t, svc, small, 2600)
	evaluate(t, svc)
	want := fmt.Sprintf("star_milestone %d:2500", small)
	if got := kinds(fake.Events()); len(got) != 1 || got[0] != want {
		t.Fatalf("events = %v, want [%s]", got, want)
	}
	evaluate(t, svc)
	if len(fake.Events()) != 1 {
		t.Fatalf("an unchanged count must not resend: %v", kinds(fake.Events()))
	}
	// One jump over three milestones (90 -> 600) reports only the highest.
	jump := alertRepoFixture(t, svc, org, "acme/jump", 90)
	evaluate(t, svc)
	setAlertVerified(t, svc, jump, 600)
	evaluate(t, svc)
	got := fake.Events()
	if len(got) != 2 || got[1].Event.Key != fmt.Sprintf("%d:500", jump) {
		t.Fatalf("events = %v", kinds(got))
	}
	body := got[1].Event.Title + got[1].Event.Body
	if !strings.Contains(body, "acme/jump") || !strings.Contains(body, "https://github.com/acme/jump") || strings.Contains(body, "secretlogin") {
		t.Fatalf("message = %q", body)
	}
}

func TestSpikeAndSurgeThresholdsFromHandComputedFixtures(t *testing.T) {
	tests := []struct {
		name       string
		action     string
		base       int // events in the 14 days before the window
		window     int
		wantKind   notifications.Kind
		wantAlerts int
	}{
		// 112 stars / 56 slots = 2 per slot; 3 x 2 = 6 is under the floor of 10.
		{"spike below floor", ActionStarred, 112, 9, notifications.StarSpike, 0},
		{"spike at floor", ActionStarred, 112, 10, notifications.StarSpike, 1},
		// 560 / 56 = 10 per slot; 3 x 10 = 30 beats the floor.
		{"spike below 3x average", ActionStarred, 560, 29, notifications.StarSpike, 0},
		{"spike at 3x average", ActionStarred, 560, 30, notifications.StarSpike, 1},
		// 56 / 56 = 1 per slot; 3 is under the floor of 5.
		{"surge below floor", ActionUnstarred, 56, 4, notifications.UnstarSurge, 0},
		{"surge at floor", ActionUnstarred, 56, 5, notifications.UnstarSurge, 1},
		// 280 / 56 = 5 per slot; 15 beats the floor.
		{"surge below 3x average", ActionUnstarred, 280, 14, notifications.UnstarSurge, 0},
		{"surge at 3x average", ActionUnstarred, 280, 15, notifications.UnstarSurge, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _ := newTestService(t)
			fake := &notifications.Fake{}
			svc.Notifier = fake
			org := newOrg(t, svc, "rates")
			id := alertRepoFixture(t, svc, org, "acme/rates", 50)
			addEvents(t, svc, id, tt.action, SourceWebhook, tt.base, baseEvent)
			addEvents(t, svc, id, tt.action, SourceWebhook, tt.window, windowEvent)
			evaluate(t, svc)
			var matching []notifications.Recorded
			for _, e := range fake.Events() {
				if e.Event.Kind == tt.wantKind {
					matching = append(matching, e)
				}
			}
			if len(matching) != tt.wantAlerts {
				t.Fatalf("%s alerts = %v, want %d", tt.wantKind, kinds(fake.Events()), tt.wantAlerts)
			}
			if tt.wantAlerts == 1 {
				e := matching[0]
				prefix := "spike"
				if tt.wantKind == notifications.UnstarSurge {
					prefix = "unstar"
				}
				if want := fmt.Sprintf("%d:%s:2026-10-10:2", id, prefix); e.Event.Key != want {
					t.Fatalf("key = %q, want %q", e.Event.Key, want)
				}
				if e.OrgID != org || strings.Contains(e.Event.Title+e.Event.Body, "secretlogin") || !strings.Contains(e.Event.Body, "https://github.com/acme/rates") {
					t.Fatalf("event = %+v", e)
				}
			}
		})
	}
}

func TestImportedHistoryAndUnfinishedImportsRaiseNoRateAlerts(t *testing.T) {
	svc, _ := newTestService(t)
	fake := &notifications.Fake{}
	svc.Notifier = fake
	org := newOrg(t, svc, "import")
	// 20 stars dated inside the window but imported by the backfill.
	imported := alertRepoFixture(t, svc, org, "acme/imported", 50)
	addEvents(t, svc, imported, ActionStarred, SourceBackfill, 20, windowEvent)
	// A real spike, but the history import has not finished.
	unfinished := alertRepoFixture(t, svc, org, "acme/unfinished", 50)
	addEvents(t, svc, unfinished, ActionStarred, SourceWebhook, 40, windowEvent)
	if _, err := svc.pool.Exec(context.Background(), `update github_star_repos set backfill_complete=false where id=$1`, unfinished); err != nil {
		t.Fatal(err)
	}
	evaluate(t, svc)
	if got := fake.Events(); len(got) != 0 {
		t.Fatalf("sent %v", kinds(got))
	}
}

func TestAlertsAreScopedToTheOwningOrganization(t *testing.T) {
	svc, _ := newTestService(t)
	fake := &notifications.Fake{}
	svc.Notifier = fake
	orgA, orgB := newOrg(t, svc, "org-a"), newOrg(t, svc, "org-b")
	repoA := alertRepoFixture(t, svc, orgA, "acme/tool", 50)
	repoB := alertRepoFixture(t, svc, orgB, "acme/tool", 50) // same GitHub repo, other org
	addEvents(t, svc, repoA, ActionStarred, SourceWebhook, 12, windowEvent)
	addEvents(t, svc, repoB, ActionStarred, SourceWebhook, 2, windowEvent)
	evaluate(t, svc)
	got := fake.Events()
	if len(got) != 1 || got[0].OrgID != orgA || got[0].Event.Kind != notifications.StarSpike {
		t.Fatalf("events = %+v", got)
	}
	// A disabled repository and a deleted organization stay silent.
	if _, err := svc.pool.Exec(context.Background(), `update github_star_repos set enabled=false where id=$1`, repoA); err != nil {
		t.Fatal(err)
	}
	evaluate(t, svc)
	if len(fake.Events()) != 1 {
		t.Fatalf("disabled repo evaluated: %v", kinds(fake.Events()))
	}
}

func TestNilNotifierIsANoop(t *testing.T) {
	svc, _ := newTestService(t)
	org := newOrg(t, svc, "quiet")
	id := alertRepoFixture(t, svc, org, "acme/quiet", 50)
	addEvents(t, svc, id, ActionStarred, SourceWebhook, 40, windowEvent)
	evaluate(t, svc)
	var n int
	if err := svc.pool.QueryRow(context.Background(), `select count(*) from github_star_alert_state`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("state rows = %d, %v", n, err)
	}
}

type recordingSender struct {
	mu   sync.Mutex
	sent []string
}

func (r *recordingSender) SendMessage(_ context.Context, _, _, text string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, text)
	return nil
}

func TestSameAlertIsDeliveredOnceThroughTheRealNotifier(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	org := newOrg(t, svc, "dedup")
	var userID int64
	if err := svc.pool.QueryRow(ctx, `insert into users(email,name,password_hash) values ('o@example.test','O','h') returning id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	sender := &recordingSender{}
	notifier, err := notifications.NewService(svc.pool, svc.box, notifications.Options{Sender: sender, Now: svc.Now})
	if err != nil {
		t.Fatal(err)
	}
	token := "123456789:AAEhBOweik6ad9r_QXMENQjcrTu-oSYZ0abc"
	_, err = notifier.SaveTelegram(ctx, authz.Principal{UserID: userID, OrgID: org, Role: authz.RoleOwner}, notifications.TelegramInput{
		Token: &token, ChatID: "-100", Enabled: true,
		Events: map[notifications.Kind]bool{notifications.StarSpike: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	svc.Notifier = notifier
	id := alertRepoFixture(t, svc, org, "acme/dedup", 50)
	addEvents(t, svc, id, ActionStarred, SourceWebhook, 12, windowEvent)
	evaluate(t, svc)
	evaluate(t, svc) // the next 10 minute run sees the same slot
	if err := notifier.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 || !strings.Contains(sender.sent[0], "gained 12 stars") {
		t.Fatalf("delivered = %q", sender.sent)
	}
}

func TestHighestCrossedAndFormatCount(t *testing.T) {
	for _, c := range []struct{ prev, cur, want int }{
		{0, 99, 0}, {99, 100, 100}, {100, 100, 0}, {100, 249, 0}, {100, 250, 250},
		{0, 100000, 100000}, {99999, 100001, 100000}, {200000, 300000, 0}, {600, 400, 0},
	} {
		if got := highestCrossed(c.prev, c.cur); got != c.want {
			t.Errorf("highestCrossed(%d,%d) = %d, want %d", c.prev, c.cur, got, c.want)
		}
	}
	for n, want := range map[int]string{100: "100", 1000: "1,000", 25000: "25,000", 100000: "100,000"} {
		if got := formatCount(n); got != want {
			t.Errorf("formatCount(%d) = %q", n, got)
		}
	}
}
