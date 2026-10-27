package platformhttp_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/db"
	"supermetry/internal/platform/admin"
	platformauth "supermetry/internal/platform/auth"
	"supermetry/internal/platform/authz"
	platformcrypto "supermetry/internal/platform/crypto"
	platformhttp "supermetry/internal/platform/http"
	"supermetry/internal/platform/sitesvc"
	"supermetry/internal/platform/store"
	"supermetry/internal/testpg"
)

type userDeleteEnv struct {
	pool    *pgxpool.Pool
	handler http.Handler
	data    *store.Store
	ids     map[string]int64
	orgs    map[string]int64
}

// newUserDeleteEnv builds: admin (instance admin, the caller), admin2 (second
// admin), sole (sole owner of live org "solo"), co1 and co2 (co-owners of
// "shared"), member (viewer in "shared"), plain (no organizations).
func newUserDeleteEnv(t *testing.T) userDeleteEnv {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	env := userDeleteEnv{pool: pool, data: &store.Store{Pool: pool}, ids: map[string]int64{}, orgs: map[string]int64{}}
	for _, name := range []string{"admin", "admin2", "sole", "co1", "co2", "member", "plain"} {
		var id int64
		isAdmin := strings.HasPrefix(name, "admin")
		if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash,is_admin) values ($1,'U','hash',$2) returning id`, name+"@example.test", isAdmin).Scan(&id); err != nil {
			t.Fatal(err)
		}
		env.ids[name] = id
	}
	for _, slug := range []string{"solo", "shared"} {
		var id int64
		if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ($1,$1) returning id`, slug).Scan(&id); err != nil {
			t.Fatal(err)
		}
		env.orgs[slug] = id
	}
	for _, m := range []struct{ user, org, role string }{
		{"sole", "solo", "owner"}, {"co1", "shared", "owner"}, {"co2", "shared", "owner"},
		{"member", "shared", "viewer"}, {"sole", "shared", "viewer"},
	} {
		if _, err := pool.Exec(ctx, `insert into memberships(org_id,user_id,role) values ($1,$2,$3)`, env.orgs[m.org], env.ids[m.user], m.role); err != nil {
			t.Fatal(err)
		}
	}
	registry := sitesvc.NewRegistry(env.data, 0)
	t.Cleanup(registry.Close)
	env.handler = platformhttp.New(platformhttp.Deps{
		Authenticator: actorAuthenticator{
			"admin": {UserID: env.ids["admin"], SiteIDs: []int32{}},
			"sole":  {UserID: env.ids["sole"], OrgID: env.orgs["solo"], Role: authz.RoleOwner},
		},
		Store:     env.data,
		Admin:     admin.New(env.data),
		Mutations: platformhttp.Services{Store: env.data, Sites: sitesvc.New(env.data, registry)},
		Now:       time.Now,
	})
	return env
}

func (e userDeleteEnv) del(t *testing.T, actor, user, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodDelete, "/api/private/admin/users/"+user, strings.NewReader(body))
	request.Header.Set("X-Test-Actor", actor)
	request.Header.Set("X-CSRF-Token", "t")
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: "supermetry_csrf", Value: "t"})
	response := httptest.NewRecorder()
	e.handler.ServeHTTP(response, request)
	return response
}

func (e userDeleteEnv) idStr(name string) string { return fmt.Sprint(e.ids[name]) }

func (e userDeleteEnv) tombstoned(t *testing.T, name string) bool {
	t.Helper()
	var deleted bool
	if err := e.pool.QueryRow(context.Background(), `select deleted_at is not null from users where id=$1`, e.ids[name]).Scan(&deleted); err != nil {
		t.Fatal(err)
	}
	return deleted
}

func (e userDeleteEnv) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestAdminDeleteUserPlainMemberEndsSessionsAndFreesEmail(t *testing.T) {
	env := newUserDeleteEnv(t)
	ctx := context.Background()
	if _, err := env.pool.Exec(ctx, `insert into user_sessions(id_hash,user_id,expires_at,device_label) values (decode(repeat('ab',32),'hex'),$1,now()+interval '1 day','test')`, env.ids["member"]); err != nil {
		t.Fatal(err)
	}
	got := env.del(t, "admin", env.idStr("member"), `{"email":"MEMBER@example.test"}`)
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"orgs_erasing":0`) || !strings.Contains(got.Body.String(), `"deleted"`) {
		t.Fatalf("status=%d body=%s", got.Code, got.Body.String())
	}
	if !env.tombstoned(t, "member") {
		t.Fatal("user not tombstoned")
	}
	if n := env.count(t, `select count(*) from user_sessions where user_id=$1`, env.ids["member"]); n != 0 {
		t.Fatalf("sessions=%d, want 0", n)
	}
	if n := env.count(t, `select count(*) from memberships where user_id=$1`, env.ids["member"]); n != 0 {
		t.Fatalf("memberships=%d, want 0", n)
	}
	var meta string
	if err := env.pool.QueryRow(ctx, `select metadata::text from audit_log where action='instance.user.deleted' and target_id=$1`, env.ids["member"]).Scan(&meta); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(meta, "@") || !strings.Contains(meta, `"orgs_erased": 0`) {
		t.Fatalf("audit metadata=%s", meta)
	}
	// The address can sign up again through the real signup path.
	box, err := platformcrypto.NewSecretBox([]byte(strings.Repeat("k", 32)), "supermetry/totp/v1")
	if err != nil {
		t.Fatal(err)
	}
	a := platformauth.New(env.data, nil, box, "https://supermetry.example")
	if _, err := a.Signup(ctx, "member@example.test", "Again", "pw-123456789-long", "Again org", ""); err != nil {
		t.Fatalf("signup with the freed email: %v", err)
	}
}

func TestAdminDeleteUserSoleOwnerErasesOrg(t *testing.T) {
	env := newUserDeleteEnv(t)
	for _, body := range []string{`{"email":"sole@example.test"}`} { // default is true
		got := env.del(t, "admin", env.idStr("sole"), body)
		if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"orgs_erasing":1`) {
			t.Fatalf("status=%d body=%s", got.Code, got.Body.String())
		}
	}
	if !env.tombstoned(t, "sole") {
		t.Fatal("user not tombstoned")
	}
	if n := env.count(t, `select count(*) from orgs where id=$1 and deleted_at is not null and erase_requested_at is not null`, env.orgs["solo"]); n != 1 {
		t.Fatal("solo org not erase-requested")
	}
	if n := env.count(t, `select count(*) from orgs where id=$1 and (deleted_at is not null or erase_requested_at is not null)`, env.orgs["shared"]); n != 0 {
		t.Fatal("shared org touched")
	}
	// Member of "shared" as viewer is gone from it.
	if n := env.count(t, `select count(*) from memberships where org_id=$1`, env.orgs["shared"]); n != 3 {
		t.Fatalf("shared memberships=%d, want 3", n)
	}
	if n := env.count(t, `select count(*) from audit_log where action='instance.organization.erase_now' and target_id=$1`, env.orgs["solo"]); n != 1 {
		t.Fatal("erase audit missing")
	}
}

func TestAdminDeleteUserSoleOwnerKeepOrgsRefused(t *testing.T) {
	env := newUserDeleteEnv(t)
	got := env.del(t, "admin", env.idStr("sole"), `{"email":"sole@example.test","erase_owned_orgs":false}`)
	want := fmt.Sprintf(`{"error":"last_owner","orgs":[{"id":%d,"slug":"solo"}]}`, env.orgs["solo"])
	if got.Code != http.StatusConflict || strings.TrimSpace(got.Body.String()) != want {
		t.Fatalf("status=%d body=%s, want 409 %s", got.Code, got.Body.String(), want)
	}
	if env.tombstoned(t, "sole") {
		t.Fatal("user tombstoned despite refusal")
	}
	if n := env.count(t, `select count(*) from memberships where user_id=$1`, env.ids["sole"]); n != 2 {
		t.Fatalf("memberships=%d, want 2", n)
	}
}

func TestAdminDeleteUserCoOwnedOrgOnlyLosesMember(t *testing.T) {
	env := newUserDeleteEnv(t)
	got := env.del(t, "admin", env.idStr("co1"), `{"email":"co1@example.test","erase_owned_orgs":true}`)
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"orgs_erasing":0`) {
		t.Fatalf("status=%d body=%s", got.Code, got.Body.String())
	}
	if n := env.count(t, `select count(*) from orgs where id=$1 and deleted_at is null and erase_requested_at is null`, env.orgs["shared"]); n != 1 {
		t.Fatal("co-owned org changed")
	}
	if n := env.count(t, `select count(*) from memberships where org_id=$1`, env.orgs["shared"]); n != 3 {
		t.Fatalf("shared memberships=%d, want 3 (co2, member, sole)", n)
	}
}

func TestAdminDeleteUserRefusals(t *testing.T) {
	env := newUserDeleteEnv(t)
	var systemID int64
	if err := env.pool.QueryRow(context.Background(), `select id from users where is_system limit 1`).Scan(&systemID); err != nil {
		t.Fatal(err)
	}
	var systemEmail string
	if err := env.pool.QueryRow(context.Background(), `select email from users where id=$1`, systemID).Scan(&systemEmail); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, actor, user, body string
		want                    int
	}{
		{"wrong email", "admin", env.idStr("plain"), `{"email":"other@example.test"}`, 400},
		{"missing email", "admin", env.idStr("plain"), `{}`, 400},
		{"unknown user", "admin", "999999", `{"email":"x@example.test"}`, 404},
		{"instance admin", "admin", env.idStr("admin2"), `{"email":"admin2@example.test"}`, 409},
		{"self", "admin", env.idStr("admin"), `{"email":"admin@example.test"}`, 409},
		{"system user", "admin", fmt.Sprint(systemID), fmt.Sprintf(`{"email":%q}`, systemEmail), 409},
		{"not an admin", "sole", env.idStr("plain"), `{"email":"plain@example.test"}`, 403},
	}
	for _, c := range cases {
		if got := env.del(t, c.actor, c.user, c.body); got.Code != c.want {
			t.Errorf("%s: status=%d body=%s, want %d", c.name, got.Code, got.Body.String(), c.want)
		}
	}
	if env.tombstoned(t, "plain") || env.tombstoned(t, "admin2") || env.tombstoned(t, "admin") {
		t.Fatal("a refused request deleted a user")
	}
}

func TestAdminDeleteUserLastLiveOrgIsAllOrNothing(t *testing.T) {
	env := newUserDeleteEnv(t)
	ctx := context.Background()
	// "shared" is deleted, so "solo" becomes the only live organization.
	if _, err := env.pool.Exec(ctx, `update orgs set deleted_at=now() where id=$1`, env.orgs["shared"]); err != nil {
		t.Fatal(err)
	}
	got := env.del(t, "admin", env.idStr("sole"), `{"email":"sole@example.test"}`)
	if got.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s, want 409", got.Code, got.Body.String())
	}
	if env.tombstoned(t, "sole") {
		t.Fatal("user tombstoned")
	}
	if n := env.count(t, `select count(*) from orgs where id=$1 and deleted_at is null and erase_requested_at is null`, env.orgs["solo"]); n != 1 {
		t.Fatal("solo org changed")
	}
	if n := env.count(t, `select count(*) from audit_log where action in ('instance.user.deleted','instance.organization.erase_now')`); n != 0 {
		t.Fatalf("audit rows=%d, want 0", n)
	}
	if n := env.count(t, `select count(*) from memberships where user_id=$1`, env.ids["sole"]); n != 2 {
		t.Fatalf("memberships=%d, want 2", n)
	}
}

func adminKeyUserHandler(env adminKeyEnv) http.Handler {
	data := &store.Store{Pool: env.pool}
	return platformhttp.New(platformhttp.Deps{
		Store: data, Admin: admin.New(data), Mutations: platformhttp.Services{Store: data},
		AdminAPIKey: testAdminKey, AdminServiceUserID: env.serviceUserID,
	})
}

func TestAdminDeleteUserWithAdminKey(t *testing.T) {
	env := newAdminKeyEnv(t, testAdminKey)
	handler := adminKeyUserHandler(env)
	got := serveBearer(handler, http.MethodDelete, fmt.Sprintf("/api/private/admin/users/%d", env.memberID), `{"email":"member@example.test"}`, testAdminKey)
	if got.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", got.Code, got.Body.String())
	}
	var deleted bool
	var actor int64
	if err := env.pool.QueryRow(context.Background(), `select (select deleted_at is not null from users where id=$1), (select actor_user_id from audit_log where action='instance.user.deleted')`, env.memberID).Scan(&deleted, &actor); err != nil || !deleted || actor != env.serviceUserID {
		t.Fatalf("deleted=%v actor=%d want %d err=%v", deleted, actor, env.serviceUserID, err)
	}
}

func TestAdminDeleteUserLastAdminRefused(t *testing.T) {
	env := newAdminKeyEnv(t, testAdminKey)
	var rootID int64
	if err := env.pool.QueryRow(context.Background(), `select id from users where email='root@example.test'`).Scan(&rootID); err != nil {
		t.Fatal(err)
	}
	got := serveBearer(adminKeyUserHandler(env), http.MethodDelete, fmt.Sprintf("/api/private/admin/users/%d", rootID), `{"email":"root@example.test"}`, testAdminKey)
	if got.Code != http.StatusConflict || !strings.Contains(got.Body.String(), "last enabled instance administrator") {
		t.Fatalf("status=%d body=%s", got.Code, got.Body.String())
	}
}
