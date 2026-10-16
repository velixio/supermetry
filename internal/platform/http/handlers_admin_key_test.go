package platformhttp_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/db"
	"supermetry/internal/platform/admin"
	platformhttp "supermetry/internal/platform/http"
	"supermetry/internal/platform/store"
	"supermetry/internal/testpg"
)

const testAdminKey = "admin-key-0123456789abcdefghijklmnopqrstuv"

type adminKeyEnv struct {
	pool          *pgxpool.Pool
	handler       http.Handler
	serviceUserID int64
	memberID      int64
}

func newAdminKeyEnv(t *testing.T, key string) adminKeyEnv {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	serviceUserID, err := store.AdminServiceUserID(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	var memberID int64
	if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash) values ('member@example.test','Member','hash') returning id`).Scan(&memberID); err != nil {
		t.Fatal(err)
	}
	// A real instance admin exists too, so "last admin" checks never involve the system user.
	// It is also a valid cookie actor ("admin"), so tests can prove the key never falls back to it.
	var rootID int64
	if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash,is_admin) values ('root@example.test','Root','hash',true) returning id`).Scan(&rootID); err != nil {
		t.Fatal(err)
	}
	data := &store.Store{Pool: pool}
	handler := platformhttp.New(platformhttp.Deps{
		Authenticator:      actorAuthenticator{"admin": {UserID: rootID, SiteIDs: []int32{}}},
		Store:              data,
		Admin:              admin.New(data),
		Signup:             "invite",
		AdminAPIKey:        key,
		AdminServiceUserID: serviceUserID,
	})
	return adminKeyEnv{pool: pool, handler: handler, serviceUserID: serviceUserID, memberID: memberID}
}

func serveBearer(handler http.Handler, method, path, body, key string) *httptest.ResponseRecorder {
	return serveAuthorization(handler, method, path, body, "Bearer "+key, "")
}

// serveAuthorization sends an Authorization header and, when actor is set, a
// valid cookie-session actor alongside it.
func serveAuthorization(handler http.Handler, method, path, body, authorization, actor string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", authorization)
	if actor != "" {
		request.Header.Set("X-Test-Actor", actor)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestAdminKeyListsUsers(t *testing.T) {
	env := newAdminKeyEnv(t, testAdminKey)
	ctx := context.Background()

	var isAdmin, disabled bool
	if err := env.pool.QueryRow(ctx, `select is_admin, disabled_at is not null from users where id=$1`, env.serviceUserID).Scan(&isAdmin, &disabled); err != nil {
		t.Fatal(err)
	}
	if !isAdmin || !disabled {
		t.Fatalf("system user is_admin=%v disabled=%v, want both true", isAdmin, disabled)
	}

	list := serveBearer(env.handler, http.MethodGet, "/api/private/admin/users", "", testAdminKey)
	if list.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s, want 200", list.Code, list.Body.String())
	}
	if !strings.Contains(list.Body.String(), "member@example.test") {
		t.Fatalf("list body misses member: %s", list.Body.String())
	}
	if strings.Contains(list.Body.String(), store.AdminServiceEmail) {
		t.Fatalf("list body exposes the system user: %s", list.Body.String())
	}

	patch := serveBearer(env.handler, http.MethodPatch, fmt.Sprintf("/api/private/admin/users/%d", env.memberID), `{"password_reset_required":true}`, testAdminKey)
	if patch.Code >= http.StatusBadRequest {
		t.Fatalf("patch status=%d body=%s", patch.Code, patch.Body.String())
	}
	var actor int64
	if err := env.pool.QueryRow(ctx, `select actor_user_id from audit_log where action='instance.user.updated' and target_id=$1 order by id desc limit 1`, env.memberID).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	if actor != env.serviceUserID {
		t.Fatalf("audit actor=%d, want system user %d", actor, env.serviceUserID)
	}

	self := serveBearer(env.handler, http.MethodPatch, fmt.Sprintf("/api/private/admin/users/%d", env.serviceUserID), `{"disabled":false}`, testAdminKey)
	if self.Code != http.StatusNotFound {
		t.Fatalf("patch system user status=%d body=%s, want 404", self.Code, self.Body.String())
	}
}

func TestAdminKeyRejectedOutsideAdmin(t *testing.T) {
	env := newAdminKeyEnv(t, testAdminKey)
	response := serveBearer(env.handler, http.MethodGet, "/api/private/orgs/1", "", testAdminKey)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s, want 401", response.Code, response.Body.String())
	}
}

func TestAdminKeyWrongKey(t *testing.T) {
	env := newAdminKeyEnv(t, testAdminKey)
	response := serveBearer(env.handler, http.MethodGet, "/api/private/admin/users", "", testAdminKey+"x")
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s, want 401", response.Code, response.Body.String())
	}
	withSession := serveAuthorization(env.handler, http.MethodGet, "/api/private/admin/users", "", "Bearer "+testAdminKey+"x", "admin")
	if withSession.Code != http.StatusUnauthorized {
		t.Fatalf("wrong bearer with valid session status=%d body=%s, want 401 (no cookie fallback)", withSession.Code, withSession.Body.String())
	}
}

func TestAdminKeyNonBearerSchemeFallsThroughToSession(t *testing.T) {
	env := newAdminKeyEnv(t, testAdminKey)
	response := serveAuthorization(env.handler, http.MethodGet, "/api/private/admin/users", "", "Basic x", "admin")
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want 200", response.Code, response.Body.String())
	}
}

func TestAdminKeyShortKeyDisabled(t *testing.T) {
	short := "0123456789abcdef"
	env := newAdminKeyEnv(t, short)
	response := serveBearer(env.handler, http.MethodGet, "/api/private/admin/users", "", short)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s, want 401", response.Code, response.Body.String())
	}
	withSession := serveAuthorization(env.handler, http.MethodGet, "/api/private/admin/users", "", "Bearer "+short, "admin")
	if withSession.Code != http.StatusUnauthorized {
		t.Fatalf("short bearer with valid session status=%d body=%s, want 401 (no cookie fallback)", withSession.Code, withSession.Body.String())
	}
}
