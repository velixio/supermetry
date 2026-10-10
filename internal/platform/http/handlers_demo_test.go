package platformhttp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/db"
	"supermetry/internal/demo"
	"supermetry/internal/platform/authz"
	platformhttp "supermetry/internal/platform/http"
	"supermetry/internal/platform/orgs"
	"supermetry/internal/platform/sitesvc"
	"supermetry/internal/platform/store"
	"supermetry/internal/privacy"
	"supermetry/internal/testpg"
)

type demoHTTPFixture struct {
	handler    http.Handler
	data       *store.Store
	registry   *sitesvc.Registry
	owner      authz.Principal
	orgID      int64
	heartbeats *[]int32
}

type demoHTTPService struct {
	data      *store.Store
	registry  interface{ Invalidate() error }
	heartbeat func(int32)
}

func (s demoHTTPService) Create(ctx context.Context, principal authz.Principal, orgID int64, timezone string) (store.Site, error) {
	if principal.OrgID != orgID || !authz.Can(principal, authz.ActionSiteCreate, 0) {
		return store.Site{}, sitesvc.ErrForbidden
	}
	if timezone == "" {
		timezone = "UTC"
	}
	if _, err := time.LoadLocation(timezone); err != nil {
		return store.Site{}, demo.ErrInvalidTimezone
	}
	var created store.Site
	err := s.data.Tx(ctx, func(tx pgx.Tx) error {
		var err error
		created, err = store.CreateDemoSite(ctx, tx, store.Site{
			OrgID: orgID, Name: "Demo project",
			AllowedHosts: []string{"www.demo.example", "docs.demo.example"},
			Timezone:     timezone, RetentionClass: 90,
		}, time.Now().UTC().AddDate(0, 0, -89))
		return err
	})
	if err != nil {
		return store.Site{}, err
	}
	if err := s.registry.Invalidate(); err != nil {
		return store.Site{}, err
	}
	return created, nil
}

func (demoHTTPService) Viewed(int32) {}

func (s demoHTTPService) Heartbeat(siteID int32) {
	if s.heartbeat != nil {
		s.heartbeat(siteID)
	}
}

func newDemoHTTPFixture(t *testing.T) *demoHTTPFixture {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var ownerID, viewerID, orgID int64
	if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash) values ('demo-owner@demo.example','Owner','hash') returning id`).Scan(&ownerID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash) values ('demo-viewer@demo.example','Viewer','hash') returning id`).Scan(&viewerID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Demo API','demo-api') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into memberships(org_id,user_id,role) values ($1,$2,'owner'),($1,$3,'viewer')`, orgID, ownerID, viewerID); err != nil {
		t.Fatal(err)
	}
	data := &store.Store{Pool: pool}
	registry := sitesvc.NewRegistry(data, 0)
	t.Cleanup(registry.Close)
	owner := authz.Principal{UserID: ownerID, OrgID: orgID, Role: authz.RoleOwner}
	viewer := authz.Principal{UserID: viewerID, OrgID: orgID, Role: authz.RoleViewer}
	key := authz.Principal{KeyID: 1, OrgID: orgID, Role: authz.RoleKey, Scopes: []string{"stats:read"}}
	heartbeats := make([]int32, 0, 1)
	demoService := demoHTTPService{data: data, registry: registry, heartbeat: func(siteID int32) {
		heartbeats = append(heartbeats, siteID)
	}}
	return &demoHTTPFixture{
		handler: platformhttp.New(platformhttp.Deps{
			TrackingMode:  privacy.Identified,
			Authenticator: actorAuthenticator{"owner": owner, "viewer": viewer, "key": key},
			Sites:         registry, Mutations: platformhttp.Services{Sites: sitesvc.New(data, registry, privacy.Identified)},
			Store: data, Orgs: orgs.New(data), Demo: demoService,
			BaseURL: "https://supermetry.example", TrackerPath: "/js/script.js",
		}),
		data: data, registry: registry, owner: owner, orgID: orgID, heartbeats: &heartbeats,
	}
}

func (f *demoHTTPFixture) request(t *testing.T, actor, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("X-Test-Actor", actor)
	if method != http.MethodGet {
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-CSRF-Token", "token")
		request.AddCookie(&http.Cookie{Name: "supermetry_csrf", Value: "token"})
	}
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	return response
}

func (f *demoHTTPFixture) create(t *testing.T, timezone string) map[string]any {
	t.Helper()
	response := f.request(t, "owner", http.MethodPost, fmt.Sprintf("/api/private/orgs/%d/demo", f.orgID), `{"timezone":"`+timezone+`"}`)
	if response.Code != http.StatusAccepted {
		t.Fatalf("create status=%d body=%s", response.Code, response.Body.String())
	}
	var site map[string]any
	if err := json.NewDecoder(response.Body).Decode(&site); err != nil {
		t.Fatal(err)
	}
	return site
}

func TestDemoCreateHTTP(t *testing.T) {
	f := newDemoHTTPFixture(t)
	created := f.create(t, "Asia/Kathmandu")
	if created["is_demo"] != true || created["demo_ready"] != false || created["timezone"] != "Asia/Kathmandu" {
		t.Fatalf("created site=%v", created)
	}
	publicID, _ := created["public_id"].(string)
	if publicID == "" {
		t.Fatalf("created site has no public_id: %v", created)
	}

	duplicate := f.request(t, "owner", http.MethodPost, fmt.Sprintf("/api/private/orgs/%d/demo", f.orgID), `{"timezone":"UTC"}`)
	if duplicate.Code != http.StatusConflict {
		t.Fatalf("duplicate status=%d body=%s", duplicate.Code, duplicate.Body.String())
	}
	var conflict struct {
		Error  string `json:"error"`
		SiteID string `json:"site_id"`
	}
	if err := json.NewDecoder(duplicate.Body).Decode(&conflict); err != nil {
		t.Fatal(err)
	}
	if conflict.Error != "demo_exists" || conflict.SiteID != publicID {
		t.Fatalf("conflict=%+v, want site %q", conflict, publicID)
	}
}

func TestDemoCreateAuthorizationAndTimezoneHTTP(t *testing.T) {
	f := newDemoHTTPFixture(t)
	viewer := f.request(t, "viewer", http.MethodPost, fmt.Sprintf("/api/private/orgs/%d/demo", f.orgID), `{"timezone":"UTC"}`)
	if viewer.Code != http.StatusForbidden {
		t.Fatalf("viewer status=%d body=%s", viewer.Code, viewer.Body.String())
	}
	badTimezone := f.request(t, "owner", http.MethodPost, fmt.Sprintf("/api/private/orgs/%d/demo", f.orgID), `{"timezone":"Mars/Olympus"}`)
	if badTimezone.Code != http.StatusBadRequest || !strings.Contains(badTimezone.Body.String(), `"error":"invalid_timezone"`) {
		t.Fatalf("bad timezone status=%d body=%s", badTimezone.Code, badTimezone.Body.String())
	}
}

func TestDemoPromptDismissHTTP(t *testing.T) {
	f := newDemoHTTPFixture(t)
	for range 2 {
		response := f.request(t, "owner", http.MethodPost, "/api/private/me/demo-prompt/dismiss", "")
		if response.Code != http.StatusNoContent {
			t.Fatalf("dismiss status=%d body=%s", response.Code, response.Body.String())
		}
	}
	response := f.request(t, "owner", http.MethodGet, "/api/private/me", "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"demo_prompt_dismissed":true`) {
		t.Fatalf("me status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestDemoSiteFieldsAndPatchHTTP(t *testing.T) {
	f := newDemoHTTPFixture(t)
	created := f.create(t, "UTC")
	publicID := created["public_id"].(string)
	stored, ok, err := store.DemoSiteForOrg(context.Background(), f.data.Pool, f.orgID)
	if err != nil || !ok {
		t.Fatalf("load demo: ok=%v err=%v", ok, err)
	}
	if err := store.SetDemoSeeded(context.Background(), f.data.Pool, stored.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.data.Pool.Exec(context.Background(), `update sites set settings=jsonb_set(settings,'{demo_seed_failed_at}',to_jsonb('2026-10-10T12:00:00Z'::text)),demo_started_at=null where id=$1`, stored.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.registry.Invalidate(); err != nil {
		t.Fatal(err)
	}

	list := f.request(t, "owner", http.MethodGet, "/api/private/sites", "")
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"is_demo":true`) || !strings.Contains(list.Body.String(), `"demo_ready":true`) || !strings.Contains(list.Body.String(), `"demo_failed":true`) || !strings.Contains(list.Body.String(), `"demo_expires_at":`) {
		t.Fatalf("list status=%d body=%s", list.Code, list.Body.String())
	}
	var privateList struct {
		Data []struct {
			DemoExpiresAt time.Time `json:"demo_expires_at"`
		} `json:"data"`
	}
	if err := json.NewDecoder(strings.NewReader(list.Body.String())).Decode(&privateList); err != nil {
		t.Fatal(err)
	}
	var createdAt time.Time
	if err := f.data.Pool.QueryRow(context.Background(), `select created_at from sites where id=$1`, stored.ID).Scan(&createdAt); err != nil {
		t.Fatal(err)
	}
	if len(privateList.Data) != 1 || !privateList.Data[0].DemoExpiresAt.Equal(createdAt.Add(demo.DemoLifetime)) {
		t.Fatalf("demo expiry=%v, want %v", privateList.Data, createdAt.Add(demo.DemoLifetime))
	}
	public := f.request(t, "key", http.MethodGet, fmt.Sprintf("/api/public/v1/sites/%d", stored.ID), "")
	if public.Code != http.StatusOK || strings.Contains(public.Body.String(), "demo_expires_at") {
		t.Fatalf("public status=%d body=%s", public.Code, public.Body.String())
	}
	patch := f.request(t, "owner", http.MethodPatch, fmt.Sprintf("/api/private/sites/%d", stored.ID), `{"is_demo":false}`)
	if patch.Code != http.StatusOK || !strings.Contains(patch.Body.String(), `"is_demo":true`) || !strings.Contains(patch.Body.String(), `"demo_ready":true`) {
		t.Fatalf("patch status=%d body=%s", patch.Code, patch.Body.String())
	}
	loaded, ok, err := store.DemoSiteForOrg(context.Background(), f.data.Pool, f.orgID)
	if err != nil || !ok || !loaded.IsDemo || loaded.PublicID != publicID || loaded.DemoSeededAt == nil {
		t.Fatalf("stored demo=%+v ok=%v err=%v", loaded, ok, err)
	}
}

func TestDemoHeartbeatHTTP(t *testing.T) {
	f := newDemoHTTPFixture(t)
	f.create(t, "UTC")
	stored, ok, err := store.DemoSiteForOrg(context.Background(), f.data.Pool, f.orgID)
	if err != nil || !ok {
		t.Fatalf("load demo: ok=%v err=%v", ok, err)
	}
	var realSite, foreignSite int32
	if err := f.data.Pool.QueryRow(context.Background(), `
		insert into sites(org_id,public_id,name,timezone,retention_class)
		values ($1,'demoreal0001','Real','UTC',30) returning id`, f.orgID).Scan(&realSite); err != nil {
		t.Fatal(err)
	}
	var foreignOrg int64
	if err := f.data.Pool.QueryRow(context.Background(), `insert into orgs(name,slug) values ('Foreign demo','foreign-demo') returning id`).Scan(&foreignOrg); err != nil {
		t.Fatal(err)
	}
	if err := f.data.Pool.QueryRow(context.Background(), `
		insert into sites(org_id,public_id,name,timezone,retention_class,is_demo)
		values ($1,'demoforeign1','Foreign demo','UTC',90,true) returning id`, foreignOrg).Scan(&foreignSite); err != nil {
		t.Fatal(err)
	}
	if err := f.registry.Invalidate(); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name   string
		siteID int32
		want   int
	}{
		{name: "demo", siteID: stored.ID, want: http.StatusNoContent},
		{name: "real site", siteID: realSite, want: http.StatusNotFound},
		{name: "another organization", siteID: foreignSite, want: http.StatusNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := f.request(t, "owner", http.MethodPost, fmt.Sprintf("/api/private/sites/%d/demo/heartbeat", test.siteID), "")
			if response.Code != test.want {
				t.Fatalf("status=%d body=%s, want %d", response.Code, response.Body.String(), test.want)
			}
		})
	}
	if got := *f.heartbeats; len(got) != 1 || got[0] != stored.ID {
		t.Fatalf("heartbeat calls = %v, want [%d]", got, stored.ID)
	}
}
