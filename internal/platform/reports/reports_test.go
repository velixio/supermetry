package reports

import (
	"context"
	"testing"
	"time"

	"panel/internal/api"
	"panel/internal/db"
	"panel/internal/platform/authz"
	"panel/internal/platform/mail"
	"panel/internal/platform/store"
	"panel/internal/sites"
	"panel/internal/testpg"
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
	enabled bool
}

func (f *fakeSender) Enabled() bool { return f.enabled }

type fakeEngine struct {
	calls int
}

func (e *fakeEngine) Query(_ context.Context, request api.QueryRequest) (api.QueryResponse, error) {
	e.calls++
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
