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
	if got := serveCSRF(env.handler, http.MethodPost, env.ownerRestorePath(), "owner"); got.Code != http.StatusNoContent {
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

func (e adminOrgDeleteEnv) restorePath() string { return e.path() + "/restore" }
func (e adminOrgDeleteEnv) ownerRestorePath() string {
	return fmt.Sprintf("/api/private/me/orgs/%d/restore", e.orgID)
}

func (e adminOrgDeleteEnv) ownerListsDeleted(t *testing.T) int {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/private/me/orgs/deleted", nil)
	request.Header.Set("X-Test-Actor", "owner")
	response := httptest.NewRecorder()
	e.handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("list deleted status=%d body=%s", response.Code, response.Body.String())
	}
	var body []json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", response.Body.String(), err)
	}
	return len(body)
}

func TestOwnerDeleteOwnerRestore(t *testing.T) {
	env := newAdminOrgDeleteEnv(t)
	env.addSite(t)
	services := platformhttp.Services{Store: &store.Store{Pool: env.pool}, Sites: env.sites}
	if err := services.DeleteOrg(context.Background(), env.owner, env.orgID); err != nil {
		t.Fatal(err)
	}
	if n := env.ownerListsDeleted(t); n != 1 {
		t.Fatalf("owner deleted list=%d, want 1", n)
	}
	if got := serveCSRF(env.handler, http.MethodPost, env.ownerRestorePath(), "owner"); got.Code != http.StatusNoContent {
		t.Fatalf("owner restore status=%d body=%s, want 204", got.Code, got.Body.String())
	}
}

func TestAdminDeletedOrgOwnerCanRestore(t *testing.T) {
	env := newAdminOrgDeleteEnv(t)
	env.addSite(t)
	if got := serveCSRF(env.handler, http.MethodDelete, env.path(), "admin"); got.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d", got.Code)
	}
	if n := env.ownerListsDeleted(t); n != 1 {
		t.Fatalf("owner deleted list=%d, want 1", n)
	}
	if got := serveCSRF(env.handler, http.MethodPost, env.ownerRestorePath(), "owner"); got.Code != http.StatusNoContent {
		t.Fatalf("owner restore status=%d body=%s, want 204", got.Code, got.Body.String())
	}
}

func TestAdminRestoresAdminDeletedOrg(t *testing.T) {
	env := newAdminOrgDeleteEnv(t)
	env.addSite(t)
	if got := serveCSRF(env.handler, http.MethodDelete, env.path(), "admin"); got.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d", got.Code)
	}
	if got := serveCSRF(env.handler, http.MethodPost, env.restorePath(), "admin"); got.Code != http.StatusNoContent {
		t.Fatalf("admin restore status=%d body=%s, want 204", got.Code, got.Body.String())
	}
	var live bool
	if err := env.pool.QueryRow(context.Background(), `select o.deleted_at is null and s.deleted_at is null from orgs o join sites s on s.org_id=o.id where o.id=$1`, env.orgID).Scan(&live); err != nil || !live {
		t.Fatalf("org and site live=%v err=%v after admin restore", live, err)
	}
	var n int
	if err := env.pool.QueryRow(context.Background(), `select count(*) from audit_log where action='instance.organization.restored' and target_id=$1`, env.orgID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("restore audit rows=%d err=%v, want 1", n, err)
	}
}

func TestAdminRestoresOwnerDeletedOrg(t *testing.T) {
	env := newAdminOrgDeleteEnv(t)
	env.addSite(t)
	services := platformhttp.Services{Store: &store.Store{Pool: env.pool}, Sites: env.sites}
	if err := services.DeleteOrg(context.Background(), env.owner, env.orgID); err != nil {
		t.Fatal(err)
	}
	if got := serveCSRF(env.handler, http.MethodPost, env.restorePath(), "admin"); got.Code != http.StatusNoContent {
		t.Fatalf("admin restore status=%d body=%s, want 204", got.Code, got.Body.String())
	}
	// Restoring a live org is not restorable.
	if got := serveCSRF(env.handler, http.MethodPost, env.restorePath(), "admin"); got.Code != http.StatusConflict {
		t.Fatalf("second restore status=%d, want 409", got.Code)
	}
}

func TestAdminRestoreErrors(t *testing.T) {
	env := newAdminOrgDeleteEnv(t)
	env.addSite(t)
	if got := serveCSRF(env.handler, http.MethodDelete, env.path(), "admin"); got.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d", got.Code)
	}
	for _, actor := range []string{"owner", "member"} {
		if got := serveCSRF(env.handler, http.MethodPost, env.restorePath(), actor); got.Code != http.StatusForbidden {
			t.Fatalf("%s restore status=%d, want 403", actor, got.Code)
		}
	}
	if got := serveCSRF(env.handler, http.MethodPost, "/api/private/admin/organizations/999999/restore", "admin"); got.Code != http.StatusNotFound {
		t.Fatalf("unknown org status=%d, want 404", got.Code)
	}
	// Past the grace period: 409.
	if _, err := env.pool.Exec(context.Background(), `update orgs set deleted_at=now()-interval '30 days' where id=$1`, env.orgID); err != nil {
		t.Fatal(err)
	}
	if got := serveCSRF(env.handler, http.MethodPost, env.restorePath(), "admin"); got.Code != http.StatusConflict || !strings.Contains(got.Body.String(), "not_restorable") {
		t.Fatalf("expired status=%d body=%s, want 409 not_restorable", got.Code, got.Body.String())
	}
	// Erased: 404.
	if _, err := env.pool.Exec(context.Background(), `update orgs set erased_at=now() where id=$1`, env.orgID); err != nil {
		t.Fatal(err)
	}
	if got := serveCSRF(env.handler, http.MethodPost, env.restorePath(), "admin"); got.Code != http.StatusNotFound {
		t.Fatalf("erased status=%d, want 404", got.Code)
	}
}

func TestAdminGetDeletedOrganization(t *testing.T) {
	env := newAdminOrgDeleteEnv(t)
	env.addSite(t)
	get := func() *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, env.path(), nil)
		request.Header.Set("X-Test-Actor", "admin")
		response := httptest.NewRecorder()
		env.handler.ServeHTTP(response, request)
		return response
	}
	var live map[string]any
	if got := get(); got.Code != http.StatusOK {
		t.Fatalf("live status=%d", got.Code)
	} else if err := json.Unmarshal(got.Body.Bytes(), &live); err != nil || live["deleted_at"] != nil || live["restorable_until"] != nil {
		t.Fatalf("live body=%s err=%v", got.Body.String(), err)
	}
	if got := serveCSRF(env.handler, http.MethodDelete, env.path(), "admin"); got.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d", got.Code)
	}
	got := get()
	if got.Code != http.StatusOK {
		t.Fatalf("deleted status=%d body=%s, want 200", got.Code, got.Body.String())
	}
	var body struct {
		Name            string     `json:"name"`
		Slug            string     `json:"slug"`
		DeletedAt       *time.Time `json:"deleted_at"`
		RestorableUntil *time.Time `json:"restorable_until"`
	}
	if err := json.Unmarshal(got.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Name != "Doomed" || body.Slug != "doomed" || body.DeletedAt == nil || body.RestorableUntil == nil || !body.RestorableUntil.After(*body.DeletedAt) {
		t.Fatalf("body=%s", got.Body.String())
	}
	if _, err := env.pool.Exec(context.Background(), `update orgs set erased_at=now() where id=$1`, env.orgID); err != nil {
		t.Fatal(err)
	}
	if got := get(); got.Code != http.StatusNotFound {
		t.Fatalf("erased status=%d, want 404", got.Code)
	}
}
