package platformhttp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/db"
	platformauth "supermetry/internal/platform/auth"
	"supermetry/internal/platform/authz"
	platformcrypto "supermetry/internal/platform/crypto"
	platformhttp "supermetry/internal/platform/http"
	"supermetry/internal/platform/mail"
	"supermetry/internal/platform/orgs"
	"supermetry/internal/platform/sitesvc"
	"supermetry/internal/platform/store"
	"supermetry/internal/platform/users"
	"supermetry/internal/testpg"
)

type lifecycleHTTPFixture struct {
	handler  http.Handler
	data     *store.Store
	sites    *sitesvc.Sites
	registry *sitesvc.Registry
	orgs     *orgs.Orgs
	orgID    int64
	ownerID  int64
	adminID  int64
	viewerID int64
	now      time.Time
}

func newLifecycleHTTPFixture(t *testing.T) lifecycleHTTPFixture {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	data := &store.Store{Pool: pool}
	userService := users.New(data)
	createUser := func(email, name string) int64 {
		t.Helper()
		id, err := userService.Create(ctx, email, name, "pw-123456789")
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	ownerID := createUser("lifecycle-owner@example.test", "Owner")
	adminID := createUser("lifecycle-admin@example.test", "Admin")
	viewerID := createUser("lifecycle-viewer@example.test", "Viewer")
	var orgID int64
	if err := data.Tx(ctx, func(tx pgx.Tx) error {
		var err error
		orgID, err = store.CreateOrg(ctx, tx, "Lifecycle", ownerID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into memberships(org_id,user_id,role) values ($1,$2,'admin'),($1,$3,'viewer')`, orgID, adminID, viewerID); err != nil {
		t.Fatal(err)
	}
	registry := sitesvc.NewRegistry(data, 0)
	t.Cleanup(registry.Close)
	siteService := sitesvc.New(data, registry)
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	siteService.SetClock(func() time.Time { return now })
	orgService := orgs.New(data, registry)
	actors := actorAuthenticator{
		"owner":  {UserID: ownerID, OrgID: orgID, Role: authz.RoleOwner},
		"admin":  {UserID: adminID, OrgID: orgID, Role: authz.RoleAdmin},
		"viewer": {UserID: viewerID, OrgID: orgID, Role: authz.RoleViewer},
	}
	services := platformhttp.Services{Sites: siteService, Store: data}
	return lifecycleHTTPFixture{
		handler: platformhttp.New(platformhttp.Deps{
			Now: func() time.Time { return now }, Authenticator: actors, Store: data, Sites: registry, Mutations: services, Orgs: orgService,
		}),
		data: data, sites: siteService, registry: registry, orgs: orgService,
		orgID: orgID, ownerID: ownerID, adminID: adminID, viewerID: viewerID, now: now,
	}
}

func (f lifecycleHTTPFixture) request(t *testing.T, actor, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-Actor", actor)
	if method != http.MethodGet && method != http.MethodHead {
		req.Header.Set("X-CSRF-Token", "token")
		req.AddCookie(&http.Cookie{Name: "supermetry_csrf", Value: "token"})
	}
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, req)
	return response
}

func (f lifecycleHTTPFixture) createSite(t *testing.T, name string) int32 {
	t.Helper()
	site, err := f.sites.Create(context.Background(), authz.Principal{UserID: f.ownerID, OrgID: f.orgID, Role: authz.RoleOwner}, name, []string{strings.ToLower(name) + ".example.test"}, "UTC", 30, "USD")
	if err != nil {
		t.Fatal(err)
	}
	return site.ID
}

func TestDeletedSiteLifecycleHTTP(t *testing.T) {
	f := newLifecycleHTTPFixture(t)
	siteID := f.createSite(t, "Real")

	deleted := f.request(t, "admin", http.MethodDelete, "/api/private/sites/"+itoa32(siteID), "")
	if deleted.Code != http.StatusOK || !strings.Contains(deleted.Body.String(), `"scheduled":true`) || !strings.Contains(deleted.Body.String(), `"purge_at":"2026-10-18T12:00:00Z"`) {
		t.Fatalf("delete status=%d body=%s", deleted.Code, deleted.Body.String())
	}

	listed := f.request(t, "owner", http.MethodGet, "/api/private/orgs/"+itoa64(f.orgID)+"/sites/deleted", "")
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"name":"Real"`) || !strings.Contains(listed.Body.String(), `"deleted_at":"2026-10-11T12:00:00Z"`) {
		t.Fatalf("list status=%d body=%s", listed.Code, listed.Body.String())
	}
	if got := f.request(t, "viewer", http.MethodGet, "/api/private/orgs/"+itoa64(f.orgID)+"/sites/deleted", ""); got.Code != http.StatusForbidden {
		t.Fatalf("viewer list status=%d body=%s", got.Code, got.Body.String())
	}
	if got := f.request(t, "owner", http.MethodGet, "/api/private/orgs/999999/sites/deleted", ""); got.Code != http.StatusNotFound {
		t.Fatalf("foreign org list status=%d body=%s", got.Code, got.Body.String())
	}

	restored := f.request(t, "admin", http.MethodPost, "/api/private/sites/"+itoa32(siteID)+"/restore", "")
	if restored.Code != http.StatusNoContent {
		t.Fatalf("restore status=%d body=%s", restored.Code, restored.Body.String())
	}
	conflict := f.request(t, "owner", http.MethodPost, "/api/private/sites/"+itoa32(siteID)+"/restore", "")
	if conflict.Code != http.StatusConflict || conflict.Body.String() != "{\"error\":\"not_restorable\"}\n" {
		t.Fatalf("second restore status=%d body=%s", conflict.Code, conflict.Body.String())
	}
	if got := f.request(t, "viewer", http.MethodPost, "/api/private/sites/"+itoa32(siteID)+"/restore", ""); got.Code != http.StatusForbidden {
		t.Fatalf("viewer restore status=%d body=%s", got.Code, got.Body.String())
	}
	if got := f.request(t, "owner", http.MethodPost, "/api/private/sites/999999/restore", ""); got.Code != http.StatusNotFound {
		t.Fatalf("foreign site restore status=%d body=%s", got.Code, got.Body.String())
	}
}

func TestRestoreSiteLimitAndDeletedOrganizationHTTP(t *testing.T) {
	f := newLifecycleHTTPFixture(t)
	f.sites.SetSiteLimit(1)
	firstID := f.createSite(t, "First")
	if got := f.request(t, "owner", http.MethodDelete, "/api/private/sites/"+itoa32(firstID), ""); got.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", got.Code, got.Body.String())
	}
	_ = f.createSite(t, "Second")
	limited := f.request(t, "owner", http.MethodPost, "/api/private/sites/"+itoa32(firstID)+"/restore", "")
	if limited.Code != http.StatusConflict || limited.Body.String() != "{\"error\":\"site_limit\"}\n" {
		t.Fatalf("limited restore status=%d body=%s", limited.Code, limited.Body.String())
	}
	if _, err := f.data.Pool.Exec(context.Background(), `update orgs set deleted_at=$2 where id=$1`, f.orgID, f.now); err != nil {
		t.Fatal(err)
	}
	blocked := f.request(t, "owner", http.MethodPost, "/api/private/sites/"+itoa32(firstID)+"/restore", "")
	if blocked.Code != http.StatusConflict || blocked.Body.String() != "{\"error\":\"not_restorable\"}\n" {
		t.Fatalf("deleted-org restore status=%d body=%s", blocked.Code, blocked.Body.String())
	}
}

func TestDemoSiteDeleteResponseIsImmediate(t *testing.T) {
	f := newLifecycleHTTPFixture(t)
	var siteID int32
	if err := f.data.Pool.QueryRow(context.Background(), `
		insert into sites(org_id,public_id,name,allowed_hosts,timezone,retention_class,is_demo)
		values ($1,'demo-delete1','Demo',array['demo.example.test'],'UTC',90,true) returning id`, f.orgID).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	if err := f.registry.Invalidate(); err != nil {
		t.Fatal(err)
	}
	got := f.request(t, "owner", http.MethodDelete, "/api/private/sites/"+itoa32(siteID), "")
	if got.Code != http.StatusOK || got.Body.String() != "{\"purge_at\":null,\"scheduled\":false}\n" {
		t.Fatalf("status=%d body=%s", got.Code, got.Body.String())
	}
}

func TestRenameOrganizationHTTP(t *testing.T) {
	f := newLifecycleHTTPFixture(t)
	path := "/api/private/orgs/" + itoa64(f.orgID)
	got := f.request(t, "admin", http.MethodPatch, path, `{"name":"  Renamed organization  "}`)
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"name":"Renamed organization"`) || !strings.Contains(got.Body.String(), `"slug":"lifecycle"`) {
		t.Fatalf("rename status=%d body=%s", got.Code, got.Body.String())
	}
	if got := f.request(t, "owner", http.MethodPatch, path, `{"name":""}`); got.Code != http.StatusBadRequest || got.Body.String() != "{\"error\":\"invalid_name\"}\n" {
		t.Fatalf("invalid name status=%d body=%s", got.Code, got.Body.String())
	}
	if got := f.request(t, "viewer", http.MethodPatch, path, `{"name":"No"}`); got.Code != http.StatusForbidden {
		t.Fatalf("viewer rename status=%d body=%s", got.Code, got.Body.String())
	}
	if got := f.request(t, "owner", http.MethodPatch, "/api/private/orgs/999999", `{"name":"No"}`); got.Code != http.StatusNotFound {
		t.Fatalf("foreign org rename status=%d body=%s", got.Code, got.Body.String())
	}
}

func TestDeletedOrganizationAccountLifecycleHTTP(t *testing.T) {
	f := newLifecycleHTTPFixture(t)
	_ = f.createSite(t, "Scheduled")

	box, err := platformcrypto.NewSecretBox([]byte("0123456789abcdef0123456789abcdef"), "supermetry/totp/v1")
	if err != nil {
		t.Fatal(err)
	}
	authService := platformauth.New(f.data, &mail.Fake{}, box, "https://supermetry.example")
	cookie, err := authService.Login(context.Background(), "lifecycle-owner@example.test", "pw-123456789", "", "test")
	if err != nil {
		t.Fatal(err)
	}
	handler := platformhttp.New(platformhttp.Deps{
		Auth: authService, Authenticator: platformhttp.NewAuthenticator(authService, nil, nil, f.orgs),
		Store: f.data, Sites: f.registry, Mutations: platformhttp.Services{Sites: f.sites, Store: f.data}, Orgs: f.orgs,
	})
	request := func(method, path, body string, orgHeader bool) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: platformauth.CookieName, Value: cookie})
		if orgHeader {
			req.Header.Set("X-Supermetry-Org", itoa64(f.orgID))
		}
		if method != http.MethodGet {
			req.Header.Set("X-CSRF-Token", "token")
			req.AddCookie(&http.Cookie{Name: "supermetry_csrf", Value: "token"})
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}

	deleted := request(http.MethodDelete, "/api/private/orgs/"+itoa64(f.orgID), `{"password":"pw-123456789"}`, true)
	if deleted.Code != http.StatusOK || !strings.Contains(deleted.Body.String(), `"scheduled":true`) || !strings.Contains(deleted.Body.String(), `"purge_at"`) {
		t.Fatalf("delete status=%d body=%s", deleted.Code, deleted.Body.String())
	}
	listed := request(http.MethodGet, "/api/private/me/orgs/deleted", "", false)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"name":"Lifecycle"`) || !strings.Contains(listed.Body.String(), `"sites":["Scheduled"]`) {
		t.Fatalf("list status=%d body=%s", listed.Code, listed.Body.String())
	}
	f.orgs.SetSiteLimit(1)
	if _, err := f.data.Pool.Exec(context.Background(), `insert into sites(org_id,public_id,name,timezone,retention_class) values ($1,'limitlive001','Limit live','UTC',30)`, f.orgID); err != nil {
		t.Fatal(err)
	}
	limited := request(http.MethodPost, "/api/private/me/orgs/"+itoa64(f.orgID)+"/restore", "", false)
	if limited.Code != http.StatusConflict || limited.Body.String() != "{\"error\":\"site_limit\"}\n" {
		t.Fatalf("limited restore status=%d body=%s", limited.Code, limited.Body.String())
	}
	f.orgs.SetSiteLimit(0)
	restored := request(http.MethodPost, "/api/private/me/orgs/"+itoa64(f.orgID)+"/restore", "", false)
	if restored.Code != http.StatusNoContent {
		t.Fatalf("restore status=%d body=%s", restored.Code, restored.Body.String())
	}
	conflict := request(http.MethodPost, "/api/private/me/orgs/"+itoa64(f.orgID)+"/restore", "", false)
	if conflict.Code != http.StatusConflict || conflict.Body.String() != "{\"error\":\"not_restorable\"}\n" {
		t.Fatalf("second restore status=%d body=%s", conflict.Code, conflict.Body.String())
	}
}

func TestRestoreOrganizationHidesNonOwnerAsNotFound(t *testing.T) {
	f := newLifecycleHTTPFixture(t)
	_ = f.createSite(t, "Scheduled")
	if err := (platformhttp.Services{Sites: f.sites, Store: f.data}).DeleteOrg(context.Background(), authz.Principal{UserID: f.ownerID, OrgID: f.orgID, Role: authz.RoleOwner}, f.orgID); err != nil {
		t.Fatal(err)
	}
	handler := platformhttp.New(platformhttp.Deps{
		Authenticator: actorAuthenticator{"viewer": {UserID: f.viewerID}}, Store: f.data, Orgs: f.orgs,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/private/me/orgs/"+itoa64(f.orgID)+"/restore", nil)
	req.Header.Set("X-Test-Actor", "viewer")
	req.Header.Set("X-CSRF-Token", "token")
	req.AddCookie(&http.Cookie{Name: "supermetry_csrf", Value: "token"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func itoa32(value int32) string { return itoa64(int64(value)) }

func itoa64(value int64) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
