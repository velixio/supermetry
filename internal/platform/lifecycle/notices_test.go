package lifecycle_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/db"
	"supermetry/internal/platform/lifecycle"
	"supermetry/internal/platform/mail"
	"supermetry/internal/platform/store"
	"supermetry/internal/testpg"
)

func TestNoticesSiteNoticeAndReminderAreSentOnce(t *testing.T) {
	fixture := newNoticesFixture(t)
	deletedAt := time.Date(2026, time.October, 1, 12, 0, 0, 0, time.UTC)
	fixture.addMember("owner@example.test", "owner", false)
	fixture.addMember("admin@example.test", "admin", false)
	fixture.addMember("editor@example.test", "editor", false)
	fixture.addMember("deleted@example.test", "owner", true)
	fixture.addSite("site-once001", "Example Site", deletedAt, false, false)
	fixture.now = deletedAt

	service := lifecycle.NewNotices(fixture.pool, fixture.sender, "https://app.example.test/", func() time.Time { return fixture.now })
	job := service.Job()
	if job.Name != "deletion-notices" || job.Every != 5*time.Minute {
		t.Fatalf("Job() = (%q, %s), want deletion-notices every 5m", job.Name, job.Every)
	}
	if err := job.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := job.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertMessages(t, fixture.sender.Sent, 2, "Example Site is scheduled for deletion on 2026-10-08")
	assertRecipients(t, fixture.sender.Sent, "owner@example.test", "admin@example.test")
	for _, message := range fixture.sender.Sent {
		if !strings.Contains(message.Text, "2026-10-08 12:00 UTC") || !strings.Contains(message.Text, "https://app.example.test/app/example/sites") {
			t.Fatalf("scheduled message text = %q", message.Text)
		}
	}

	fixture.now = deletedAt.Add(lifecycle.DeletionGrace - lifecycle.ReminderLead)
	if err := service.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertMessages(t, fixture.sender.Sent[2:], 2, "Reminder: Example Site will be deleted in 3 days")

	fixture.now = deletedAt.Add(5 * 24 * time.Hour)
	if err := service.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(fixture.sender.Sent) != 4 {
		t.Fatalf("messages after T+5d = %d, want 4", len(fixture.sender.Sent))
	}
}

func TestNoticesSkipDemoWithOrgAndRestoredSites(t *testing.T) {
	fixture := newNoticesFixture(t)
	deletedAt := time.Date(2026, time.October, 1, 12, 0, 0, 0, time.UTC)
	fixture.addMember("owner@example.test", "owner", false)
	fixture.addSite("demo-site001", "Demo", deletedAt, true, false)
	fixture.addSite("with-org0002", "With org", deletedAt, false, true)
	restoredID := fixture.addSite("restore-site", "Restored", deletedAt, false, false)
	fixture.now = deletedAt
	service := lifecycle.NewNotices(fixture.pool, fixture.sender, "https://app.example.test", func() time.Time { return fixture.now })

	if err := service.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(fixture.sender.Sent) != 1 || !strings.Contains(fixture.sender.Sent[0].Subject, "Restored") {
		t.Fatalf("initial messages = %+v, want restored-site notice only", fixture.sender.Sent)
	}
	if err := pgx.BeginFunc(context.Background(), fixture.pool, func(tx pgx.Tx) error {
		return store.RestoreSite(context.Background(), tx, fixture.orgID, restoredID, deletedAt.Add(time.Hour))
	}); err != nil {
		t.Fatal(err)
	}
	fixture.now = deletedAt.Add(4 * 24 * time.Hour)
	if err := service.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(fixture.sender.Sent) != 1 {
		t.Fatalf("messages after restore = %d, want 1", len(fixture.sender.Sent))
	}
}

func TestNoticesDeleteRestoreDeleteStartsFreshSchedule(t *testing.T) {
	fixture := newNoticesFixture(t)
	firstDeletion := time.Date(2026, time.October, 1, 12, 0, 0, 0, time.UTC)
	fixture.addMember("owner@example.test", "owner", false)
	siteID := fixture.addSite("again-site01", "Again", firstDeletion, false, false)
	fixture.now = firstDeletion
	service := lifecycle.NewNotices(fixture.pool, fixture.sender, "https://app.example.test", func() time.Time { return fixture.now })
	if err := service.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := pgx.BeginFunc(context.Background(), fixture.pool, func(tx pgx.Tx) error {
		return store.RestoreSite(context.Background(), tx, fixture.orgID, siteID, firstDeletion.Add(time.Hour))
	}); err != nil {
		t.Fatal(err)
	}

	secondDeletion := firstDeletion.Add(5 * 24 * time.Hour)
	if err := pgx.BeginFunc(context.Background(), fixture.pool, func(tx pgx.Tx) error {
		return store.SoftDeleteSiteAt(context.Background(), tx, fixture.orgID, siteID, secondDeletion)
	}); err != nil {
		t.Fatal(err)
	}
	fixture.now = secondDeletion
	if err := service.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	fixture.now = secondDeletion.Add(lifecycle.DeletionGrace - lifecycle.ReminderLead)
	if err := service.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(fixture.sender.Sent) != 3 {
		t.Fatalf("messages across two deletion lifecycles = %d, want 3", len(fixture.sender.Sent))
	}
	if fixture.sender.Sent[1].Subject != "Again is scheduled for deletion on 2026-10-13" || fixture.sender.Sent[2].Subject != "Reminder: Again will be deleted in 3 days" {
		t.Fatalf("second lifecycle messages = %+v", fixture.sender.Sent[1:])
	}
}

func TestNoticesOrgNoticeListsSitesAndRemindsOwners(t *testing.T) {
	deletedAt := time.Date(2026, time.October, 1, 12, 0, 0, 0, time.UTC)
	fixture := newDeletedOrgNoticesFixture(t, deletedAt)
	fixture.addMember("owner@example.test", "owner", false)
	fixture.addMember("admin@example.test", "admin", false)
	fixture.addSite("org-alpha001", "Alpha", deletedAt, false, true)
	fixture.addSite("org-beta0001", "Beta", deletedAt, false, true)
	fixture.now = deletedAt
	service := lifecycle.NewNotices(fixture.pool, fixture.sender, "https://app.example.test", func() time.Time { return fixture.now })

	if err := service.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertMessages(t, fixture.sender.Sent, 1, "Example Org is scheduled for deletion on 2026-10-08")
	assertRecipients(t, fixture.sender.Sent, "owner@example.test")
	if !strings.Contains(fixture.sender.Sent[0].Text, "- Alpha\n- Beta") || !strings.Contains(fixture.sender.Sent[0].Text, "https://app.example.test/app/account") {
		t.Fatalf("org notice text = %q", fixture.sender.Sent[0].Text)
	}

	fixture.now = deletedAt.Add(lifecycle.DeletionGrace - lifecycle.ReminderLead)
	if err := service.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertMessages(t, fixture.sender.Sent[1:], 1, "Reminder: Example Org will be deleted in 3 days")
}

func TestNoticesFailureLeavesTargetForRetry(t *testing.T) {
	fixture := newNoticesFixture(t)
	deletedAt := time.Date(2026, time.October, 1, 12, 0, 0, 0, time.UTC)
	fixture.addMember("owner@example.test", "owner", false)
	siteID := fixture.addSite("retry-site01", "Retry", deletedAt, false, false)
	secondSiteID := fixture.addSite("second-site1", "Second", deletedAt, false, false)
	fixture.now = deletedAt
	deliveryErr := errors.New("550 owner@example.test rejected")
	fixture.sender.failures = map[string]int{"owner@example.test": 1}
	fixture.sender.failure = deliveryErr
	service := lifecycle.NewNotices(fixture.pool, fixture.sender, "https://app.example.test", func() time.Time { return fixture.now })

	err := service.Run(context.Background())
	if !errors.Is(err, deliveryErr) {
		t.Fatalf("first Run() error = %v, want delivery failure", err)
	}
	if strings.Contains(err.Error(), "owner@example.test") {
		t.Fatalf("first Run() exposed recipient in error: %v", err)
	}
	var notifiedAt *time.Time
	if err := fixture.pool.QueryRow(context.Background(), `select deletion_notified_at from sites where id=$1`, siteID).Scan(&notifiedAt); err != nil {
		t.Fatal(err)
	}
	if notifiedAt != nil {
		t.Fatalf("failed target notified_at = %v, want nil", notifiedAt)
	}
	if err := service.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(fixture.sender.attempts) != 3 || len(fixture.sender.Sent) != 2 {
		t.Fatalf("retry attempts=%v sent=%v", fixture.sender.attempts, fixture.sender.Sent)
	}
	assertSiteNoticeMarks(t, fixture.pool, secondSiteID, true, false)
}

func TestNoticesRetryOnlyFailedRecipientAndRedactLog(t *testing.T) {
	fixture := newNoticesFixture(t)
	deletedAt := time.Date(2026, time.October, 1, 12, 0, 0, 0, time.UTC)
	fixture.addMember("first@example.test", "owner", false)
	fixture.addMember("bad@example.test", "admin", false)
	fixture.addMember("last@example.test", "admin", false)
	siteID := fixture.addSite("partial-site", "Partial", deletedAt, false, false)
	fixture.now = deletedAt
	fixture.sender.failures = map[string]int{"bad@example.test": 1}
	fixture.sender.failure = errors.New("550 bad@example.test rejected")
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	service := lifecycle.NewNotices(fixture.pool, fixture.sender, "https://app.example.test", func() time.Time { return fixture.now })

	if err := service.Run(context.Background()); err == nil {
		t.Fatal("first Run() error = nil, want partial delivery failure")
	}
	if strings.Contains(logs.String(), "@") {
		t.Fatalf("failure log exposed an address: %q", logs.String())
	}
	if err := service.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, want := fmt.Sprint(fixture.sender.attempts), "[first@example.test bad@example.test last@example.test bad@example.test]"; got != want {
		t.Fatalf("attempts = %s, want %s", got, want)
	}
	assertSiteNoticeMarks(t, fixture.pool, siteID, true, false)
	var sends int
	if err := fixture.pool.QueryRow(context.Background(), `select count(*) from deletion_notice_sends where target_type='site' and target_id=$1 and kind='scheduled'`, siteID).Scan(&sends); err != nil {
		t.Fatal(err)
	}
	if sends != 3 {
		t.Fatalf("successful recipient sends = %d, want 3", sends)
	}
}

func TestNoticesDisabledMarksNothingAndLateEnableSendsOnlyReminder(t *testing.T) {
	fixture := newNoticesFixture(t)
	deletedAt := time.Date(2026, time.October, 1, 12, 0, 0, 0, time.UTC)
	fixture.addMember("owner@example.test", "owner", false)
	siteID := fixture.addSite("late-site001", "Late SMTP", deletedAt, false, false)
	fixture.now = deletedAt.Add(5 * 24 * time.Hour)
	fixture.sender.enabled = false
	service := lifecycle.NewNotices(fixture.pool, fixture.sender, "https://app.example.test", func() time.Time { return fixture.now })

	if err := service.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertSiteNoticeMarks(t, fixture.pool, siteID, false, false)

	fixture.sender.enabled = true
	if err := service.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertMessages(t, fixture.sender.Sent, 1, "Reminder: Late SMTP will be deleted in 3 days")
	if strings.Count(fixture.sender.Sent[0].Text, "in 3 days") != 1 {
		t.Fatalf("late reminder body has stale relative time: %q", fixture.sender.Sent[0].Text)
	}
	assertSiteNoticeMarks(t, fixture.pool, siteID, true, true)
}

func TestNoticesWithoutRecipientsRemainPending(t *testing.T) {
	fixture := newNoticesFixture(t)
	deletedAt := time.Date(2026, time.October, 1, 12, 0, 0, 0, time.UTC)
	siteID := fixture.addSite("empty-site01", "No Owners", deletedAt, false, false)
	fixture.now = deletedAt
	service := lifecycle.NewNotices(fixture.pool, fixture.sender, "https://app.example.test", func() time.Time { return fixture.now })

	if err := service.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(fixture.sender.Sent) != 0 {
		t.Fatalf("messages without recipients = %+v", fixture.sender.Sent)
	}
	assertSiteNoticeMarks(t, fixture.pool, siteID, false, false)
}

type noticesFixture struct {
	pool   *pgxpool.Pool
	sender *noticesSender
	orgID  int64
	now    time.Time
}

func newNoticesFixture(t *testing.T) *noticesFixture {
	t.Helper()
	return newNoticesFixtureWithOrg(t, nil)
}

func newDeletedOrgNoticesFixture(t *testing.T, deletedAt time.Time) *noticesFixture {
	t.Helper()
	return newNoticesFixtureWithOrg(t, &deletedAt)
}

func newNoticesFixtureWithOrg(t *testing.T, deletedAt *time.Time) *noticesFixture {
	t.Helper()
	pool, _ := testpg.New(t)
	if err := db.Migrate(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	var orgID int64
	if err := pool.QueryRow(context.Background(), `insert into orgs(name,slug,deleted_at) values ('Example Org','example',$1) returning id`, deletedAt).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	return &noticesFixture{pool: pool, sender: &noticesSender{enabled: true}, orgID: orgID}
}

func (f *noticesFixture) addMember(email, role string, deleted bool) int64 {
	var deletedAt *time.Time
	if deleted {
		value := time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)
		deletedAt = &value
	}
	var userID int64
	if err := f.pool.QueryRow(context.Background(), `insert into users(email,name,password_hash,deleted_at) values ($1,$1,'hash',$2) returning id`, email, deletedAt).Scan(&userID); err != nil {
		panic(err)
	}
	if _, err := f.pool.Exec(context.Background(), `insert into memberships(org_id,user_id,role) values ($1,$2,$3)`, f.orgID, userID, role); err != nil {
		panic(err)
	}
	return userID
}

func (f *noticesFixture) addSite(publicID, name string, deletedAt time.Time, demo, withOrg bool) int32 {
	var siteID int32
	if err := f.pool.QueryRow(context.Background(), `
		insert into sites(org_id,public_id,name,timezone,retention_class,deleted_at,is_demo,deleted_with_org)
		values ($1,$2,$3,'UTC',30,$4,$5,$6) returning id`, f.orgID, publicID, name, deletedAt, demo, withOrg).Scan(&siteID); err != nil {
		panic(err)
	}
	return siteID
}

type noticesSender struct {
	mail.Fake
	enabled  bool
	failures map[string]int
	failure  error
	attempts []string
}

func (s *noticesSender) Enabled() bool { return s.enabled }

func (s *noticesSender) Send(ctx context.Context, message mail.Message) error {
	s.attempts = append(s.attempts, message.To)
	if s.failures[message.To] > 0 {
		s.failures[message.To]--
		return s.failure
	}
	return s.Fake.Send(ctx, message)
}

func assertMessages(t *testing.T, messages []mail.Message, count int, subject string) {
	t.Helper()
	if len(messages) != count {
		t.Fatalf("messages = %d, want %d: %+v", len(messages), count, messages)
	}
	for _, message := range messages {
		if message.Subject != subject {
			t.Fatalf("subject = %q, want %q", message.Subject, subject)
		}
	}
}

func assertRecipients(t *testing.T, messages []mail.Message, recipients ...string) {
	t.Helper()
	got := make([]string, len(messages))
	for index, message := range messages {
		got[index] = message.To
	}
	if fmt.Sprint(got) != fmt.Sprint(recipients) {
		t.Fatalf("recipients = %v, want %v", got, recipients)
	}
}

func assertSiteNoticeMarks(t *testing.T, pool *pgxpool.Pool, siteID int32, notified, reminded bool) {
	t.Helper()
	var gotNotified, gotReminded bool
	if err := pool.QueryRow(context.Background(), `select deletion_notified_at is not null,deletion_reminded_at is not null from sites where id=$1`, siteID).Scan(&gotNotified, &gotReminded); err != nil {
		t.Fatal(err)
	}
	if gotNotified != notified || gotReminded != reminded {
		t.Fatalf("notice marks = (%t, %t), want (%t, %t)", gotNotified, gotReminded, notified, reminded)
	}
}
