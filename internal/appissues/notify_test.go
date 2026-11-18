package appissues_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"supermetry/internal/appissues"
	"supermetry/internal/platform/mail"
)

type failingSender struct {
	mu      sync.Mutex
	fail    bool
	enabled bool
	sent    []mail.Message
}

func (s *failingSender) Send(_ context.Context, m mail.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail {
		return errors.New("smtp down")
	}
	s.sent = append(s.sent, m)
	return nil
}
func (s *failingSender) Enabled() bool { return s.enabled }

type notifyFixture struct {
	wakeFixture
	sender   *failingSender
	notifier *appissues.Notifier
	dave     int64 // viewer on every project
}

func newNotifyFixture(t *testing.T) notifyFixture {
	t.Helper()
	w := newWakeFixture(t)
	n := notifyFixture{wakeFixture: w, sender: &failingSender{enabled: true}}
	n.notifier = appissues.NewNotifier(w.pool, n.sender, "https://stats.example.test", w.clock.Now)
	if err := w.pool.QueryRow(context.Background(), `insert into users(email,name,password_hash) values ('dave@example.test','Dave','h') returning id`).Scan(&n.dave); err != nil {
		t.Fatal(err)
	}
	if _, err := w.pool.Exec(context.Background(), `insert into memberships(org_id,user_id,role) values (1,$1,'viewer')`, n.dave); err != nil {
		t.Fatal(err)
	}
	// Activity written by fixture setup is not under test.
	if _, err := w.pool.Exec(context.Background(), `update app_issue_activity set notified_at = now()`); err != nil {
		t.Fatal(err)
	}
	return n
}

func (n notifyFixture) run(t *testing.T) int {
	t.Helper()
	sent, err := n.notifier.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return sent
}

func (n notifyFixture) recipients() []string {
	var out []string
	for _, m := range n.sender.sent {
		out = append(out, m.To)
	}
	return out
}

func (n notifyFixture) setPrefs(t *testing.T, user int64, p appissues.Prefs) {
	t.Helper()
	if err := appissues.SetPrefs(context.Background(), n.pool, user, 7, p); err != nil {
		t.Fatal(err)
	}
}

func TestNotifyAssignedGoesToAssigneeNeverTheActor(t *testing.T) {
	n := newNotifyFixture(t)
	ctx := context.Background()
	if _, err := n.svc.Update(ctx, 7, []int64{n.issue}, n.bobActorOn7(), appissues.Change{Assignee: i64(n.alice)}); err != nil {
		t.Fatal(err)
	}
	if n.run(t) != 1 || len(n.sender.sent) != 1 || n.sender.sent[0].To != "alice@example.test" {
		t.Fatalf("sent = %v, want one mail to alice", n.recipients())
	}
	m := n.sender.sent[0]
	if !strings.Contains(m.Subject, "assigned") || !strings.Contains(m.Text, "https://stats.example.test/app/org/") || !strings.Contains(m.Text, "/issues/") {
		t.Fatalf("mail = %q / %q", m.Subject, m.Text)
	}
	for _, bad := range []string{"utm_", "?", "pixel", "track"} {
		if strings.Contains(strings.ToLower(m.Text), bad) && bad != "?" {
			t.Errorf("mail text contains %q", bad)
		}
	}
	if strings.Contains(m.Text, "/issues/"+"?") {
		t.Error("issue link carries a query string")
	}
	// Sent once only.
	if n.run(t) != 0 || len(n.sender.sent) != 1 {
		t.Fatalf("second run sent again: %v", n.recipients())
	}
	// Assigning yourself notifies nobody.
	if _, err := n.svc.Update(ctx, 7, []int64{n.other}, n.actor, appissues.Change{Assignee: i64(n.alice)}); err != nil {
		t.Fatal(err)
	}
	if n.run(t) != 0 || len(n.sender.sent) != 1 {
		t.Fatalf("self-assignment mailed: %v", n.recipients())
	}
}

// bobActorOn7 is an admin actor other than alice (bob cannot reach site 7).
func (n notifyFixture) bobActorOn7() appissues.Actor {
	return appissues.Actor{UserID: n.dave, Admin: true}
}

func TestNotifyPrefsAreRespected(t *testing.T) {
	n := newNotifyFixture(t)
	ctx := context.Background()
	n.setPrefs(t, n.alice, appissues.Prefs{Assigned: false, Resolved: true, Regressed: false})
	if _, err := n.svc.Update(ctx, 7, []int64{n.issue}, n.bobActorOn7(), appissues.Change{Assignee: i64(n.alice)}); err != nil {
		t.Fatal(err)
	}
	if n.run(t) != 0 {
		t.Fatalf("assigned mail sent although switched off: %v", n.recipients())
	}
	// Resolved by someone else: on for alice here.
	if _, err := n.svc.Update(ctx, 7, []int64{n.issue}, n.bobActorOn7(), appissues.Change{Status: s("resolved")}); err != nil {
		t.Fatal(err)
	}
	if n.run(t) != 1 || !strings.Contains(n.sender.sent[0].Subject, "resolved") {
		t.Fatalf("resolved mail: %v", n.recipients())
	}
	// Resolved by the assignee herself: never mailed.
	if _, err := n.svc.Update(ctx, 7, []int64{n.issue}, n.actor, appissues.Change{Status: s("open")}); err != nil {
		t.Fatal(err)
	}
	if _, err := n.svc.Update(ctx, 7, []int64{n.issue}, n.actor, appissues.Change{Status: s("resolved")}); err != nil {
		t.Fatal(err)
	}
	if n.run(t) != 0 || len(n.sender.sent) != 1 {
		t.Fatalf("actor was notified of her own change: %v", n.recipients())
	}
}

func TestNotifyDefaultsAssignedAndRegressedOnResolvedOff(t *testing.T) {
	n := newNotifyFixture(t)
	ctx := context.Background()
	if _, err := n.pool.Exec(ctx, `update app_issues set assignee_user_id = $1 where id = $2`, n.alice, n.issue); err != nil {
		t.Fatal(err)
	}
	if _, err := n.svc.Update(ctx, 7, []int64{n.issue}, n.bobActorOn7(), appissues.Change{Status: s("resolved")}); err != nil {
		t.Fatal(err)
	}
	if n.run(t) != 0 {
		t.Fatalf("resolved mailed by default: %v", n.recipients())
	}
	// A regression recorded by the ingest writer (system, no actor).
	tx, err := n.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := appissues.RecordSystem(ctx, tx, 7, n.issue, appissues.KindRegressed, map[string]any{"release": "2.0"}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	// RecordSystem stamps wall time; the notifier's cutoff is relative to the fake clock.
	if _, err := n.pool.Exec(ctx, `update app_issue_activity set at = $1 where kind = 'regressed'`, n.clock.Now()); err != nil {
		t.Fatal(err)
	}
	if n.run(t) != 1 || n.sender.sent[0].To != "alice@example.test" || !strings.Contains(n.sender.sent[0].Subject, "regress") {
		t.Fatalf("regression mail: %v", n.recipients())
	}
}

func TestNotifyOnlyUsersWhoCanReadTheSite(t *testing.T) {
	n := newNotifyFixture(t)
	ctx := context.Background()
	// bob is limited to site 8; dave is a viewer on all sites; carol is in another org.
	for _, who := range []int64{n.bob, n.outsider, n.dave} {
		if _, err := n.pool.Exec(ctx, `update app_issues set assignee_user_id = $1 where id = $2`, who, n.issue); err != nil {
			t.Fatal(err)
		}
		if _, err := n.svc.Update(ctx, 7, []int64{n.issue}, n.actor, appissues.Change{Priority: s("high")}); err != nil {
			t.Fatal(err)
		}
		if _, err := n.pool.Exec(ctx, `insert into app_issue_activity(site_id,issue_id,at,actor_user_id,kind,data) values (7,$1,$2,null,'regressed','{}')`, n.issue, n.clock.Now()); err != nil {
			t.Fatal(err)
		}
		n.run(t)
		if _, err := n.pool.Exec(ctx, `update app_issues set priority = 'medium' where id = $1`, n.issue); err != nil {
			t.Fatal(err)
		}
	}
	if got := n.recipients(); len(got) != 1 || got[0] != "dave@example.test" {
		t.Fatalf("recipients = %v, want only the viewer dave", got)
	}
}

func TestNotifyRetriesFailuresAndDropsStaleOnes(t *testing.T) {
	n := newNotifyFixture(t)
	ctx := context.Background()
	if _, err := n.svc.Update(ctx, 7, []int64{n.issue}, n.bobActorOn7(), appissues.Change{Assignee: i64(n.alice)}); err != nil {
		t.Fatal(err)
	}
	n.sender.fail = true
	if _, err := n.notifier.Run(ctx); err == nil {
		t.Fatal("want the delivery error reported")
	}
	n.sender.fail = false
	if n.run(t) != 1 {
		t.Fatal("failed delivery was not retried")
	}
	// Disabled mail: nothing is sent and nothing is marked, up to a day.
	if _, err := n.svc.Update(ctx, 7, []int64{n.other}, n.bobActorOn7(), appissues.Change{Assignee: i64(n.alice)}); err != nil {
		t.Fatal(err)
	}
	n.sender.enabled = false
	n.clock.Advance(23 * time.Hour)
	if n.run(t) != 0 {
		t.Fatal("sent through a disabled sender")
	}
	n.sender.enabled = true
	if n.run(t) != 1 {
		t.Fatal("pending notification lost while mail was disabled")
	}
	// A notification older than a day is dropped, not sent late.
	if _, err := n.svc.Update(ctx, 7, []int64{n.issue}, n.bobActorOn7(), appissues.Change{Assignee: i64(0)}); err != nil {
		t.Fatal(err)
	}
	if _, err := n.svc.Update(ctx, 7, []int64{n.issue}, n.bobActorOn7(), appissues.Change{Assignee: i64(n.alice)}); err != nil {
		t.Fatal(err)
	}
	n.clock.Advance(25 * time.Hour)
	before := len(n.sender.sent)
	if n.run(t) != 0 || len(n.sender.sent) != before {
		t.Fatal("stale notification sent")
	}
	var open int
	_ = n.pool.QueryRow(ctx, `select count(*) from app_issue_activity where notified_at is null and kind in ('assigned','status','regressed')`).Scan(&open)
	if open != 0 {
		t.Fatalf("%d activity rows still pending", open)
	}
}

func TestNotifySubjectCannotCarryLineBreaks(t *testing.T) {
	n := newNotifyFixture(t)
	ctx := context.Background()
	if _, err := n.pool.Exec(ctx, `update app_issues set title = E'Boom\r\nBcc: x@example.test' where id = $1`, n.issue); err != nil {
		t.Fatal(err)
	}
	if _, err := n.svc.Update(ctx, 7, []int64{n.issue}, n.bobActorOn7(), appissues.Change{Assignee: i64(n.alice)}); err != nil {
		t.Fatal(err)
	}
	n.run(t)
	if len(n.sender.sent) != 1 || strings.ContainsAny(n.sender.sent[0].Subject, "\r\n") {
		t.Fatalf("subject = %q", n.sender.sent[0].Subject)
	}
}
