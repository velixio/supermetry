package platformhttp_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/db"
	"supermetry/internal/platform/admin"
	"supermetry/internal/platform/authz"
	platformhttp "supermetry/internal/platform/http"
	"supermetry/internal/platform/orgs"
	"supermetry/internal/platform/sitesvc"
	"supermetry/internal/platform/store"
	"supermetry/internal/testpg"
)

type adminOrgDeleteEnv struct {
	pool    *pgxpool.Pool
	handler http.Handler
	sites   *sitesvc.Sites
	owner   authz.Principal
	orgID   int64
}

func newAdminOrgDeleteEnv(t *testing.T) adminOrgDeleteEnv {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var rootID, ownerID, memberID, orgID int64
	for email, id := range map[string]*int64{"root@example.test": &rootID, "owner@example.test": &ownerID, "member@example.test": &memberID} {
		if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash,is_admin) values ($1,'U','hash',$1='root@example.test') returning id`, email).Scan(id); err != nil {
			t.Fatal(err)
		}
	}
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Doomed','doomed') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into memberships(org_id,user_id,role) values ($1,$2,'owner')`, orgID, ownerID); err != nil {
		t.Fatal(err)
	}
	data := &store.Store{Pool: pool}
	registry := sitesvc.NewRegistry(data, 0)
	t.Cleanup(registry.Close)
	sites := sitesvc.New(data, registry)
	owner := authz.Principal{UserID: ownerID, OrgID: orgID, Role: authz.RoleOwner}
	handler := platformhttp.New(platformhttp.Deps{
		Authenticator: actorAuthenticator{
			"admin":  {UserID: rootID, SiteIDs: []int32{}},
			"owner":  owner,
			"member": {UserID: memberID, SiteIDs: []int32{}},
		},
		Store:     data,
		Admin:     admin.New(data),
		Orgs:      orgs.New(data, registry),
		Mutations: platformhttp.Services{Store: data, Sites: sites},
		Now:       time.Now,
	})
	return adminOrgDeleteEnv{pool: pool, handler: handler, sites: sites, owner: owner, orgID: orgID}
}

func (e adminOrgDeleteEnv) addSite(t *testing.T) {
	t.Helper()
	if _, err := e.sites.Create(context.Background(), e.owner, sitesvc.CreateInput{Name: "real", Hosts: []string{"real.example.test"}, Timezone: "UTC", RetentionDays: 30, RevenueCurrency: "USD"}); err != nil {
		t.Fatal(err)
	}
}

// serveCSRF sends a mutating session request with a matching CSRF cookie and header.
func serveCSRF(handler http.Handler, method, path, actor string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, nil)
	request.Header.Set("X-Test-Actor", actor)
	request.Header.Set("X-CSRF-Token", "t")
	request.AddCookie(&http.Cookie{Name: "supermetry_csrf", Value: "t"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func (e adminOrgDeleteEnv) path() string {
	return fmt.Sprintf("/api/private/admin/organizations/%d", e.orgID)
}

func (e adminOrgDeleteEnv) auditRows(t *testing.T) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), `select count(*) from audit_log where action='instance.organization.deleted' and target_type='organization' and target_id=$1`, e.orgID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestAdminDeleteOrgRestorable(t *testing.T) {
	env := newAdminOrgDeleteEnv(t)
	env.addSite(t)
	if got := serveCSRF(env.handler, http.MethodDelete, env.path(), "admin"); got.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s, want 204", got.Code, got.Body.String())
	}
	ctx := context.Background()
	var deleted, siteDeleted, withOrg bool
	if err := env.pool.QueryRow(ctx, `select o.deleted_at is not null, s.deleted_at is not null, s.deleted_with_org from orgs o join sites s on s.org_id=o.id where o.id=$1`, env.orgID).Scan(&deleted, &siteDeleted, &withOrg); err != nil {
		t.Fatal(err)
	}
	if !deleted || !siteDeleted || !withOrg {
		t.Fatalf("org deleted=%v site deleted=%v deleted_with_org=%v, want all true", deleted, siteDeleted, withOrg)
	}
	var actor int64
	if err := env.pool.QueryRow(ctx, `select coalesce(actor_user_id,0) from audit_log where action='instance.organization.deleted' and target_id=$1`, env.orgID).Scan(&actor); err != nil || actor == 0 {
		t.Fatalf("instance audit actor=%d err=%v", actor, err)
	}
	if got := serveCSRF(env.handler, http.MethodPost, fmt.Sprintf("/api/private/me/orgs/%d/restore", env.orgID), "owner"); got.Code != http.StatusNoContent {
		t.Fatalf("restore status=%d body=%s, want 204", got.Code, got.Body.String())
	}
	if err := env.pool.QueryRow(ctx, `select deleted_at is not null from orgs where id=$1`, env.orgID).Scan(&deleted); err != nil || deleted {
		t.Fatalf("org still deleted after restore: deleted=%v err=%v", deleted, err)
	}
}

func TestAdminDeleteOrgDemoOnlyErasedNow(t *testing.T) {
	env := newAdminOrgDeleteEnv(t)
	ctx := context.Background()
	if _, err := env.pool.Exec(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class,is_demo) values ($1,'onlydemo0001','Demo','UTC',90,true)`, env.orgID); err != nil {
		t.Fatal(err)
	}
	if got := serveCSRF(env.handler, http.MethodDelete, env.path(), "admin"); got.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s, want 204", got.Code, got.Body.String())
	}
	var withOrg bool
	if err := env.pool.QueryRow(ctx, `select deleted_with_org from sites where org_id=$1`, env.orgID).Scan(&withOrg); err != nil || withOrg {
		t.Fatalf("demo site deleted_with_org=%v err=%v, want false (erased at once)", withOrg, err)
	}
	var scheduled bool
	if err := env.pool.QueryRow(ctx, `select (metadata->>'scheduled')::boolean from audit_log where action='instance.organization.deleted' and target_id=$1`, env.orgID).Scan(&scheduled); err != nil || scheduled {
		t.Fatalf("audit scheduled=%v err=%v, want false", scheduled, err)
	}
}

func TestAdminDeleteOrgTwice404(t *testing.T) {
	env := newAdminOrgDeleteEnv(t)
	if got := serveCSRF(env.handler, http.MethodDelete, env.path(), "admin"); got.Code != http.StatusNoContent {
		t.Fatalf("first status=%d body=%s, want 204", got.Code, got.Body.String())
	}
	if got := serveCSRF(env.handler, http.MethodDelete, env.path(), "admin"); got.Code != http.StatusNotFound {
		t.Fatalf("second status=%d body=%s, want 404", got.Code, got.Body.String())
	}
	if n := env.auditRows(t); n != 1 {
		t.Fatalf("audit rows=%d, want 1", n)
	}
	missing := serveCSRF(env.handler, http.MethodDelete, "/api/private/admin/organizations/999999", "admin")
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing org status=%d, want 404", missing.Code)
	}
}

func TestAdminDeleteOrgNonAdmin403(t *testing.T) {
	env := newAdminOrgDeleteEnv(t)
	for _, actor := range []string{"member", "owner"} {
		if got := serveCSRF(env.handler, http.MethodDelete, env.path(), actor); got.Code != http.StatusForbidden {
			t.Fatalf("%s status=%d body=%s, want 403", actor, got.Code, got.Body.String())
		}
	}
	var deleted bool
	if err := env.pool.QueryRow(context.Background(), `select deleted_at is not null from orgs where id=$1`, env.orgID).Scan(&deleted); err != nil || deleted {
		t.Fatalf("org deleted=%v err=%v after forbidden requests", deleted, err)
	}
	if n := env.auditRows(t); n != 0 {
		t.Fatalf("audit rows=%d, want 0", n)
	}
}

func TestAdminDeleteOrgWithAdminKey(t *testing.T) {
	env := newAdminKeyEnv(t, testAdminKey)
	ctx := context.Background()
	var orgID int64
	if err := env.pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Keyed','keyed') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	data := &store.Store{Pool: env.pool}
	handler := platformhttp.New(platformhttp.Deps{
		Store: data, Admin: admin.New(data), Mutations: platformhttp.Services{Store: data},
		AdminAPIKey: testAdminKey, AdminServiceUserID: env.serviceUserID,
	})
	got := serveBearer(handler, http.MethodDelete, fmt.Sprintf("/api/private/admin/organizations/%d", orgID), "", testAdminKey)
	if got.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s, want 204", got.Code, got.Body.String())
	}
	var actor int64
	if err := env.pool.QueryRow(ctx, `select actor_user_id from audit_log where action='instance.organization.deleted' and target_id=$1`, orgID).Scan(&actor); err != nil || actor != env.serviceUserID {
		t.Fatalf("audit actor=%d err=%v, want system user %d", actor, err, env.serviceUserID)
	}
}
