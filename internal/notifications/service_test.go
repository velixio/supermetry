package notifications

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/db"
	"supermetry/internal/platform/authz"
	platformcrypto "supermetry/internal/platform/crypto"
	"supermetry/internal/testpg"
)

type sentMessage struct{ token, chat, text string }

type fakeSender struct {
	mu   sync.Mutex
	sent []sentMessage
	err  error
}

func (f *fakeSender) SendMessage(_ context.Context, token, chat, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, sentMessage{token, chat, text})
	return nil
}

func (f *fakeSender) messages() []sentMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]sentMessage(nil), f.sent...)
}

func (f *fakeSender) setErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

type fixture struct {
	svc    *Service
	pool   *pgxpool.Pool
	sender *fakeSender
	clock  *time.Time
	owner  authz.Principal
	other  authz.Principal
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var userID, orgA, orgB int64
	if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash) values ('owner@example.test','Owner','hash') returning id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	for slug, dst := range map[string]*int64{"a-org": &orgA, "b-org": &orgB} {
		if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ($1,$1) returning id`, slug).Scan(dst); err != nil {
			t.Fatal(err)
		}
	}
	box, err := platformcrypto.NewSecretBox([]byte("01234567890123456789012345678901"), "supermetry/org-notifications/v1")
	if err != nil {
		t.Fatal(err)
	}
	sender := &fakeSender{}
	clock := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	svc, err := NewService(pool, box, Options{Sender: sender, Now: func() time.Time { return clock }})
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{svc: svc, pool: pool, sender: sender, clock: &clock,
		owner: authz.Principal{UserID: userID, OrgID: orgA, Role: authz.RoleOwner},
		other: authz.Principal{UserID: userID, OrgID: orgB, Role: authz.RoleOwner}}
	svc.now = func() time.Time { return *f.clock }
	return f
}

func (f *fixture) save(t *testing.T, p authz.Principal, events ...Kind) Channel {
	t.Helper()
	token := testToken
	in := TelegramInput{Token: &token, ChatID: testChat, Enabled: true, Events: map[Kind]bool{}}
	for _, k := range events {
		in.Events[k] = true
	}
	c, err := f.svc.SaveTelegram(context.Background(), p, in)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func (f *fixture) notify(t *testing.T, orgID int64, e Event) {
	t.Helper()
	f.svc.Notify(context.Background(), orgID, e)
	if err := f.svc.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestSealedTokenRoundTripAndNeverExposed(t *testing.T) {
	f := newFixture(t)
	c := f.save(t, f.owner, StarMilestone)
	if !c.TokenSet || c.Events[StarMilestone] != true || c.Events[StarSpike] != false {
		t.Fatalf("channel = %+v", c)
	}
	var sealed []byte
	if err := f.pool.QueryRow(context.Background(), `select config_enc from org_notification_channels where id=$1`, c.ID).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(sealed), "AAEhBOweik6ad9r") {
		t.Fatal("token stored in plaintext")
	}
	plain, err := f.svc.box.Open(sealed)
	if err != nil || string(plain) != testToken {
		t.Fatalf("Open() = %q, %v", plain, err)
	}
	listed, err := f.svc.List(context.Background(), f.owner.OrgID)
	if err != nil || len(listed) != 1 {
		t.Fatalf("List() = %+v, %v", listed, err)
	}
	if strings.Contains(strings.ToLower(marshal(t, listed)), "aaehbo") {
		t.Fatal("token in list output")
	}
	// Updating without a token keeps the stored one.
	if _, err := f.svc.SaveTelegram(context.Background(), f.owner, TelegramInput{ChatID: "-42", Enabled: true, Events: map[Kind]bool{StarSpike: true}}); err != nil {
		t.Fatal(err)
	}
	f.notify(t, f.owner.OrgID, Event{Kind: StarSpike, Key: "k1", Title: "t"})
	if m := f.sender.messages(); len(m) != 1 || m[0].token != testToken || m[0].chat != "-42" {
		t.Fatalf("messages = %+v", m)
	}
	var audits int
	if err := f.pool.QueryRow(context.Background(), `select count(*) from audit_log where target_type='org_notification_channel' and metadata::text like '%AAEh%'`).Scan(&audits); err != nil || audits != 0 {
		t.Fatalf("audit rows with token = %d, %v", audits, err)
	}
}

func TestSaveValidation(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	bad := "nope"
	good := testToken
	for name, in := range map[string]TelegramInput{
		"no token on create": {ChatID: testChat},
		"bad token":          {Token: &bad, ChatID: testChat},
		"bad chat":           {Token: &good, ChatID: "x y"},
		"unknown event":      {Token: &good, ChatID: testChat, Events: map[Kind]bool{"nope": true}},
	} {
		if _, err := f.svc.SaveTelegram(ctx, f.owner, in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	editor := f.owner
	editor.Role = authz.RoleEditor
	if _, err := f.svc.SaveTelegram(ctx, editor, TelegramInput{Token: &good, ChatID: testChat}); !errors.Is(err, ErrForbidden) {
		t.Errorf("editor err = %v", err)
	}
}

func TestDefaultsOffAndDedup(t *testing.T) {
	f := newFixture(t)
	f.save(t, f.owner, StarMilestone)
	f.notify(t, f.owner.OrgID, Event{Kind: StarSpike, Key: "a", Title: "spike"})
	if len(f.sender.messages()) != 0 {
		t.Fatal("kind that was not ticked must not be sent")
	}
	f.notify(t, f.owner.OrgID, Event{Kind: StarMilestone, Key: "repo:100", Title: "100 stars", Body: "o/r"})
	*f.clock = f.clock.Add(time.Hour)
	f.notify(t, f.owner.OrgID, Event{Kind: StarMilestone, Key: "repo:100", Title: "100 stars"})
	if m := f.sender.messages(); len(m) != 1 || m[0].text != "100 stars\n\no/r" {
		t.Fatalf("messages = %+v", m)
	}
	f.notify(t, f.owner.OrgID, Event{Kind: StarMilestone, Key: "repo:500", Title: "500 stars"})
	if len(f.sender.messages()) != 2 {
		t.Fatal("a different key must be sent")
	}
}

func TestThrottlePerOrgAndKind(t *testing.T) {
	f := newFixture(t)
	f.save(t, f.owner, StarMilestone, StarSpike)
	f.notify(t, f.owner.OrgID, Event{Kind: StarMilestone, Key: "1", Title: "one"})
	*f.clock = f.clock.Add(9 * time.Minute)
	f.notify(t, f.owner.OrgID, Event{Kind: StarMilestone, Key: "2", Title: "two"})
	f.notify(t, f.owner.OrgID, Event{Kind: StarSpike, Key: "3", Title: "other kind"})
	if len(f.sender.messages()) != 2 {
		t.Fatalf("messages = %+v", f.sender.messages())
	}
	*f.clock = f.clock.Add(2 * time.Minute)
	f.notify(t, f.owner.OrgID, Event{Kind: StarMilestone, Key: "2", Title: "two again"})
	if len(f.sender.messages()) != 3 {
		t.Fatalf("a throttled key must be sendable once the window passes: %+v", f.sender.messages())
	}
}

func TestOrgIsolation(t *testing.T) {
	f := newFixture(t)
	f.save(t, f.owner, StarMilestone)
	f.notify(t, f.other.OrgID, Event{Kind: StarMilestone, Key: "x", Title: "for org B"})
	if len(f.sender.messages()) != 0 {
		t.Fatal("org B has no channel; nothing may be sent to org A's")
	}
	if l, _ := f.svc.List(context.Background(), f.other.OrgID); len(l) != 0 {
		t.Fatalf("org B list = %+v", l)
	}
	if err := f.svc.DeleteTelegram(context.Background(), f.other); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete other org = %v", err)
	}
	if err := f.svc.TestTelegram(context.Background(), f.other); !errors.Is(err, ErrNotFound) {
		t.Fatalf("test other org = %v", err)
	}
	f.save(t, f.other, StarMilestone)
	f.notify(t, f.other.OrgID, Event{Kind: StarMilestone, Key: "x", Title: "for org B"})
	if len(f.sender.messages()) != 1 {
		t.Fatal("same key in another org is independent")
	}
}

func TestFailuresAutoDisableAndScrub(t *testing.T) {
	f := newFixture(t)
	f.save(t, f.owner, StarMilestone)
	f.sender.setErr(errors.New("boom " + testToken))
	for i := 0; i < MaxConsecutiveFailures; i++ {
		*f.clock = f.clock.Add(time.Hour)
		f.notify(t, f.owner.OrgID, Event{Kind: StarMilestone, Key: "k", Title: "t"})
		c, _ := f.svc.List(context.Background(), f.owner.OrgID)
		if want := i + 1; c[0].ConsecutiveFailures != want {
			t.Fatalf("failures after %d = %d", want, c[0].ConsecutiveFailures)
		}
		if strings.Contains(c[0].LastError, "AAEh") {
			t.Fatalf("last_error leaks the token: %q", c[0].LastError)
		}
	}
	c, _ := f.svc.List(context.Background(), f.owner.OrgID)
	if c[0].Enabled || !strings.Contains(c[0].LastError, "disabled after 10 consecutive failures") {
		t.Fatalf("channel = %+v", c[0])
	}
	// Disabled channels receive nothing, even after recovery.
	f.sender.setErr(nil)
	*f.clock = f.clock.Add(time.Hour)
	f.notify(t, f.owner.OrgID, Event{Kind: StarMilestone, Key: "later", Title: "t"})
	if len(f.sender.messages()) != 0 {
		t.Fatal("disabled channel must not send")
	}
	// Re-enabling through save clears the failure state.
	if _, err := f.svc.SaveTelegram(context.Background(), f.owner, TelegramInput{ChatID: testChat, Enabled: true, Events: map[Kind]bool{StarMilestone: true}}); err != nil {
		t.Fatal(err)
	}
	c, _ = f.svc.List(context.Background(), f.owner.OrgID)
	if !c[0].Enabled || c[0].ConsecutiveFailures != 0 || c[0].LastError != "" {
		t.Fatalf("channel after re-enable = %+v", c[0])
	}
}

func TestSuccessResetsFailuresAndFailedSendDoesNotConsumeKey(t *testing.T) {
	f := newFixture(t)
	f.save(t, f.owner, StarMilestone)
	f.sender.setErr(errors.New("down"))
	f.notify(t, f.owner.OrgID, Event{Kind: StarMilestone, Key: "k", Title: "t"})
	f.sender.setErr(nil)
	f.notify(t, f.owner.OrgID, Event{Kind: StarMilestone, Key: "k", Title: "t"}) // no clock advance: not throttled
	if len(f.sender.messages()) != 1 {
		t.Fatalf("messages = %+v", f.sender.messages())
	}
	c, _ := f.svc.List(context.Background(), f.owner.OrgID)
	if c[0].ConsecutiveFailures != 0 || c[0].LastOKAt == nil || c[0].LastError != "" {
		t.Fatalf("channel = %+v", c[0])
	}
}

func TestTestMessageRateLimitAndScrubbedError(t *testing.T) {
	f := newFixture(t)
	f.save(t, f.owner)
	ctx := context.Background()
	f.sender.setErr(errors.New("rejected " + testToken))
	err := f.svc.TestTelegram(ctx, f.owner)
	if err == nil || strings.Contains(err.Error(), "AAEh") {
		t.Fatalf("err = %v", err)
	}
	if err := f.svc.TestTelegram(ctx, f.owner); !errors.Is(err, ErrTooSoon) {
		t.Fatalf("second test = %v, want ErrTooSoon", err)
	}
	*f.clock = f.clock.Add(31 * time.Second)
	f.sender.setErr(nil)
	if err := f.svc.TestTelegram(ctx, f.owner); err != nil {
		t.Fatal(err)
	}
	c, _ := f.svc.List(ctx, f.owner.OrgID)
	if c[0].LastOKAt == nil || c[0].LastError != "" {
		t.Fatalf("channel = %+v", c[0])
	}
}

func TestDeleteAndOrgCascade(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.save(t, f.owner, StarMilestone)
	f.notify(t, f.owner.OrgID, Event{Kind: StarMilestone, Key: "k", Title: "t"})
	if err := f.svc.DeleteTelegram(ctx, f.owner); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.DeleteTelegram(ctx, f.owner); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete = %v", err)
	}
	f.save(t, f.owner, StarMilestone)
	if _, err := f.pool.Exec(ctx, `delete from orgs where id=$1`, f.owner.OrgID); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := f.pool.QueryRow(ctx, `select (select count(*) from org_notification_channels)+(select count(*) from org_notification_log)`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rows left after org delete = %d, %v", n, err)
	}
}

func TestNotifyIgnoresBadEvents(t *testing.T) {
	f := newFixture(t)
	f.save(t, f.owner, StarMilestone)
	for _, e := range []Event{{Kind: "nope", Key: "k", Title: "t"}, {Kind: StarMilestone, Title: "no key"}, {Kind: StarMilestone, Key: "k"}} {
		f.notify(t, f.owner.OrgID, e)
	}
	f.notify(t, 0, Event{Kind: StarMilestone, Key: "k", Title: "t"})
	if len(f.sender.messages()) != 0 {
		t.Fatal("invalid events must be ignored")
	}
}

func TestFakeAndNoop(t *testing.T) {
	var fake Fake
	var n Notifier = &fake
	n.Notify(context.Background(), 7, Event{Kind: StarSpike, Key: "k"})
	if got := fake.Events(); len(got) != 1 || got[0].OrgID != 7 {
		t.Fatalf("Events() = %+v", got)
	}
	Noop{}.Notify(context.Background(), 1, Event{})
}
