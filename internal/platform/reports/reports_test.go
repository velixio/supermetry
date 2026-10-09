package reports

import (
	"context"
	"errors"
	"testing"
	"time"

	"supermetry/internal/api"
	"supermetry/internal/db"
	"supermetry/internal/platform/authz"
	"supermetry/internal/platform/mail"
	"supermetry/internal/platform/store"
	"supermetry/internal/privacy"
	"supermetry/internal/sites"
	"supermetry/internal/testpg"
)

func TestDueUsesSiteLocalSchedule(t *testing.T) {
	service, _, _, siteID, _ := newReportsTest(t, true)
	location, err := time.LoadLocation("Asia/Kathmandu")
	if err != nil {
		t.Fatal(err)
	}

	before := time.Date(2026, 10, 5, 7, 59, 59, 0, location).UTC()
	due, err := service.Due(before)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 0 {
		t.Fatalf("Due() before 08:00 = %+v, want none", due)
	}

	atSchedule := time.Date(2026, 10, 5, 8, 0, 0, 0, location).UTC()
	due, err = service.Due(atSchedule)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].SiteID != siteID {
		t.Fatalf("Due() at 08:00 = %+v, want site %d", due, siteID)
	}
}

func TestJobSendsEachPeriodOnce(t *testing.T) {
	fixed := time.Date(2026, 10, 5, 8, 15, 0, 0, time.UTC)
	service, sender, engine, _, _ := newReportsTest(t, true)
	service.now = func() time.Time { return fixed }

	job := service.Job()
	if job.Every != 15*time.Minute {
		t.Fatalf("job interval = %s", job.Every)
	}
	if err := job.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := job.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(sender.Sent) != 1 {
		t.Fatalf("sent messages = %d, want 1", len(sender.Sent))
	}
	if sender.Sent[0].To != "owner@example.test" {
		t.Fatalf("recipient = %q", sender.Sent[0].To)
	}
	if engine.calls != 6 {
		t.Fatalf("query calls = %d, want 6", engine.calls)
	}
}

func TestJobSkipsDisabledMail(t *testing.T) {
	service, sender, engine, _, _ := newReportsTest(t, false)

	if err := service.Job().Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(sender.Sent) != 0 {
		t.Fatalf("sent messages = %d, want 0", len(sender.Sent))
	}
	if engine.calls != 0 {
		t.Fatalf("query calls = %d, want 0", engine.calls)
	}
}

type fakeSender struct {
	mail.Fake
	enabled  bool
	failTo   map[string]error
	attempts []string
}

func (f *fakeSender) Enabled() bool { return f.enabled }

func (f *fakeSender) Send(ctx context.Context, message mail.Message) error {
	f.attempts = append(f.attempts, message.To)
	if err := f.failTo[message.To]; err != nil {
		return err
	}
	return f.Fake.Send(ctx, message)
}

type fakeEngine struct {
	calls   int
	onQuery func()
}

func (e *fakeEngine) Query(_ context.Context, request api.QueryRequest) (api.QueryResponse, error) {
	e.calls++
	if e.onQuery != nil {
		e.onQuery()
	}
	response := api.QueryResponse{Totals: map[string]float64{"visitors": 100, "pageviews": 250}}
	if request.Dimensions == nil || len(*request.Dimensions) == 0 {
		return response, nil
	}
	dimension := (*request.Dimensions)[0]
	response.Rows = []map[string]any{{dimension: "first", "visitors": float64(10), "pageviews": float64(20)}}
	return response, nil
}

type staticRegistry struct {
	site sites.Site
}

func (r staticRegistry) ByPublicID(id string) (sites.Site, bool) {
	return r.site, id == r.site.PublicID
}

func (r staticRegistry) Site(id int32) (sites.Site, bool) { return r.site, id == r.site.ID }
func (r staticRegistry) All() []sites.Site                { return []sites.Site{r.site} }

func newReportsTest(t *testing.T, enabled bool) (*Reports, *fakeSender, *fakeEngine, int32, int64) {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var userID, orgID int64
	if err := pool.QueryRow(ctx, `insert into users(email, name, password_hash) values ('owner@example.test', 'Owner', 'hash') returning id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into orgs(name, slug) values ('Example', 'example') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into memberships(org_id, user_id, role) values ($1, $2, 'owner')`, orgID, userID); err != nil {
		t.Fatal(err)
	}
	var siteID int32
	if err := pool.QueryRow(ctx, `
		insert into sites(org_id, public_id, name, timezone, retention_class)
		values ($1, 'example-site', 'Example Site', 'Asia/Kathmandu', 30)
		returning id`, orgID).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	data := &store.Store{Pool: pool}
	sender := &fakeSender{enabled: enabled}
	engine := &fakeEngine{}
	registry := staticRegistry{site: sites.Site{ID: siteID, PublicID: "example-site", TZ: mustLocation(t, "Asia/Kathmandu")}}
	service := New(data, engine, registry, sender)
	service.SetTrackingMode(privacy.Identified)
	principal := authz.Principal{UserID: userID, OrgID: orgID, Role: authz.RoleOwner}
	if err := service.Subscribe(ctx, principal, siteID, Weekly); err != nil {
		t.Fatal(err)
	}
	return service, sender, engine, siteID, userID
}

func mustLocation(t *testing.T, name string) *time.Location {
	t.Helper()
	location, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return location
}

func TestJobRechecksAccessAfterRendering(t *testing.T) {
	service, sender, engine, _, userID := newReportsTest(t, true)
	service.now = func() time.Time { return time.Date(2026, 10, 5, 8, 15, 0, 0, time.UTC) }
	engine.onQuery = func() {
		engine.onQuery = nil
		if _, err := service.store.Pool.Exec(context.Background(), `update memberships set role='viewer',site_ids='{}',billing_admin=true where user_id=$1`, userID); err != nil {
			t.Fatal(err)
		}
	}
	if err := service.Job().Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(sender.Sent) != 0 {
		t.Fatal("delivered analytics after access was revoked during rendering")
	}
}

func TestJobContinuesAfterOneReportDeliveryFails(t *testing.T) {
	fixed := time.Date(2026, 10, 5, 8, 15, 0, 0, time.UTC)
	service, sender, _, siteID, _ := newReportsTest(t, true)
	service.now = func() time.Time { return fixed }
	sender.failTo = map[string]error{"owner@example.test": errors.New("mailbox unavailable")}

	ctx := context.Background()
	var userID, orgID int64
	if err := service.store.Pool.QueryRow(ctx, `insert into users(email, name, password_hash) values ('member@example.test', 'Member', 'hash') returning id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := service.store.Pool.QueryRow(ctx, `select org_id from sites where id=$1`, siteID).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.store.Pool.Exec(ctx, `insert into memberships(org_id, user_id, role) values ($1, $2, 'viewer')`, orgID, userID); err != nil {
		t.Fatal(err)
	}
	if err := service.Subscribe(ctx, authz.Principal{UserID: userID, OrgID: orgID, Role: authz.RoleViewer}, siteID, Weekly); err != nil {
		t.Fatal(err)
	}

	err := service.Job().Run(ctx)
	if err == nil || !errors.Is(err, sender.failTo["owner@example.test"]) {
		t.Fatalf("Run() error = %v, want mailbox failure", err)
	}
	if got, want := sender.attempts, []string{"owner@example.test", "member@example.test"}; !equalStrings(got, want) {
		t.Fatalf("delivery attempts = %v, want %v", got, want)
	}
	if len(sender.Sent) != 1 || sender.Sent[0].To != "member@example.test" {
		t.Fatalf("successful deliveries = %+v, want member", sender.Sent)
	}

	var ownerSentAt, memberSentAt *time.Time
	if err := service.store.Pool.QueryRow(ctx, `select rs.last_sent_at from report_subscriptions rs join users u on u.id=rs.user_id where rs.site_id=$1 and u.email='owner@example.test'`, siteID).Scan(&ownerSentAt); err != nil {
		t.Fatal(err)
	}
	if err := service.store.Pool.QueryRow(ctx, `select rs.last_sent_at from report_subscriptions rs join users u on u.id=rs.user_id where rs.site_id=$1 and u.email='member@example.test'`, siteID).Scan(&memberSentAt); err != nil {
		t.Fatal(err)
	}
	if ownerSentAt != nil {
		t.Fatalf("failed report last_sent_at = %v, want nil", ownerSentAt)
	}
	if memberSentAt == nil || !memberSentAt.Equal(fixed) {
		t.Fatalf("successful report last_sent_at = %v, want %v", memberSentAt, fixed)
	}

	if err := service.Job().Run(ctx); err == nil {
		t.Fatal("second Run() error = nil, want mailbox failure retry")
	}
	if got, want := sender.attempts, []string{"owner@example.test", "member@example.test", "owner@example.test"}; !equalStrings(got, want) {
		t.Fatalf("delivery attempts after retry = %v, want %v", got, want)
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
