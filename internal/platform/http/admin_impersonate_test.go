package platformhttp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/db"
	"supermetry/internal/platform/admin"
	platformauth "supermetry/internal/platform/auth"
	platformcrypto "supermetry/internal/platform/crypto"
	platformhttp "supermetry/internal/platform/http"
	"supermetry/internal/platform/mail"
	"supermetry/internal/platform/orgs"
	"supermetry/internal/platform/sitesvc"
	"supermetry/internal/platform/store"
	"supermetry/internal/testpg"
)

const impersonateAdminPassword = "root-password-123456"

type impersonateClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *impersonateClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *impersonateClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type impersonateEnv struct {
	pool                                     *pgxpool.Pool
	handler                                  http.Handler
	clock                                    *impersonateClock
	rootID, targetID, otherAdminID, disabled int64
	serviceUserID, orgID                     int64
	adminCookie                              string
}

func newImpersonateEnv(t *testing.T) *impersonateEnv {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	hash, err := platformcrypto.HashPassword(impersonateAdminPassword)
	if err != nil {
		t.Fatal(err)
	}
	e := &impersonateEnv{pool: pool, clock: &impersonateClock{now: time.Now().UTC().Truncate(time.Second)}}
	insert := func(email string, isAdmin, disabled bool) int64 {
		var id int64
		if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash,is_admin,disabled_at)
			values ($1,'U',$2,$3,case when $4 then now() end) returning id`, email, hash, isAdmin, disabled).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	e.rootID = insert("root@example.test", true, false)
	e.targetID = insert("target@example.test", false, false)
	e.otherAdminID = insert("admin2@example.test", true, false)
	e.disabled = insert("disabled@example.test", false, true)
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Acme','acme') returning id`).Scan(&e.orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into memberships(org_id,user_id,role) values ($1,$2,'owner')`, e.orgID, e.targetID); err != nil {
		t.Fatal(err)
	}
	if e.serviceUserID, err = store.AdminServiceUserID(ctx, pool); err != nil {
		t.Fatal(err)
	}
	data := &store.Store{Pool: pool}
	registry := sitesvc.NewRegistry(data, 0)
	t.Cleanup(registry.Close)
	orgService := orgs.New(data, registry)
	authService := platformauth.New(data, &mail.Fake{}, nil, "https://app.example.test", platformauth.WithClock(e.clock))
	e.handler = platformhttp.New(platformhttp.Deps{
		Auth:               authService,
		Authenticator:      platformhttp.NewAuthenticator(authService, nil, nil, orgService),
		Store:              data,
		Admin:              admin.New(data),
		Orgs:               orgService,
		Sites:              registry,
		Mutations:          platformhttp.Services{Store: data, Sites: sitesvc.New(data, registry)},
		Now:                e.clock.Now,
		AdminAPIKey:        testAdminKey,
		AdminServiceUserID: e.serviceUserID,
	})
	if e.adminCookie, err = authService.Login(ctx, "root@example.test", impersonateAdminPassword, "", "test"); err != nil {
		t.Fatal(err)
	}
	return e
}

// serveSession sends a request carrying a session cookie and, for mutations, a
// matching CSRF cookie and header.
func (e *impersonateEnv) serveSession(method, path, body, cookie string, headers ...string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: platformauth.CookieName, Value: cookie})
	if method != http.MethodGet {
		request.Header.Set("X-CSRF-Token", "t")
		request.AddCookie(&http.Cookie{Name: "supermetry_csrf", Value: "t"})
	}
	for i := 0; i+1 < len(headers); i += 2 {
		request.Header.Set(headers[i], headers[i+1])
	}
	response := httptest.NewRecorder()
	e.handler.ServeHTTP(response, request)
	return response
}

func impersonatePath(userID int64) string {
	return "/api/private/admin/users/" + itoa64(userID) + "/impersonate"
}

// impersonate signs in as userID through the admin's cookie session and returns
// the response and the new session cookie, if any.
func (e *impersonateEnv) impersonate(t *testing.T, userID int64) (*httptest.ResponseRecorder, *http.Cookie) {
	t.Helper()
	response := e.serveSession(http.MethodPost, impersonatePath(userID), `{"password":"`+impersonateAdminPassword+`"}`, e.adminCookie)
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == platformauth.CookieName && cookie.Value != "" {
			return response, cookie
		}
	}
	return response, nil
}

type impersonatedMe struct {
	User struct {
		Email string `json:"email"`
	} `json:"user"`
	ImpersonatedByEmail *string `json:"impersonated_by_email"`
}

func decodeMe(t *testing.T, response *httptest.ResponseRecorder) (impersonatedMe, map[string]any) {
	t.Helper()
	var me impersonatedMe
	var raw map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &me); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(response.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	return me, raw
}

func TestImpersonateCreatesShortSession(t *testing.T) {
	e := newImpersonateEnv(t)
	// The admin's own session reports no impersonation.
	own := e.serveSession(http.MethodGet, "/api/private/me", "", e.adminCookie)
	if own.Code != http.StatusOK {
		t.Fatalf("admin me status=%d body=%s", own.Code, own.Body)
	}
	if me, raw := decodeMe(t, own); me.ImpersonatedByEmail != nil {
		t.Fatalf("admin me impersonated_by_email = %v", *me.ImpersonatedByEmail)
	} else if _, present := raw["impersonated_by_email"]; !present {
		t.Fatalf("impersonated_by_email missing from me: %s", own.Body)
	}
	// Cookie sessions must re-verify the admin's password.
	if got := e.serveSession(http.MethodPost, impersonatePath(e.targetID), `{"password":"wrong-password"}`, e.adminCookie); got.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password status=%d body=%s", got.Code, got.Body)
	}
	created := e.clock.Now()
	response, cookie := e.impersonate(t, e.targetID)
	if response.Code != http.StatusNoContent {
		t.Fatalf("impersonate status=%d body=%s", response.Code, response.Body)
	}
	if cookie == nil || cookie.Value == e.adminCookie || !cookie.HttpOnly || cookie.MaxAge != 3600 {
		t.Fatalf("session cookie = %+v", cookie)
	}
	var csrfSet bool
	for _, c := range response.Result().Cookies() {
		csrfSet = csrfSet || (c.Name == "supermetry_csrf" && c.Value != "")
	}
	if !csrfSet {
		t.Fatal("csrf cookie not set")
	}
	var impersonatedBy int64
	var hardExpiresAt, expiresAt time.Time
	if err := e.pool.QueryRow(context.Background(), `select impersonated_by,hard_expires_at,expires_at from user_sessions where id_hash=$1`,
		platformcrypto.HashToken(cookie.Value)).Scan(&impersonatedBy, &hardExpiresAt, &expiresAt); err != nil {
		t.Fatal(err)
	}
	if impersonatedBy != e.rootID || !hardExpiresAt.Equal(created.Add(time.Hour)) || !expiresAt.Equal(created.Add(time.Hour)) {
		t.Fatalf("session impersonated_by=%d hard_expires_at=%s expires_at=%s created=%s", impersonatedBy, hardExpiresAt, expiresAt, created)
	}
	// The impersonation replaces the admin's own browser session.
	if got := e.serveSession(http.MethodGet, "/api/private/me", "", e.adminCookie); got.Code != http.StatusUnauthorized {
		t.Fatalf("admin session after impersonation status=%d", got.Code)
	}
	me := e.serveSession(http.MethodGet, "/api/private/me", "", cookie.Value)
	if me.Code != http.StatusOK {
		t.Fatalf("me status=%d body=%s", me.Code, me.Body)
	}
	if body, _ := decodeMe(t, me); body.User.Email != "target@example.test" || body.ImpersonatedByEmail == nil || *body.ImpersonatedByEmail != "root@example.test" {
		t.Fatalf("me = %s", me.Body)
	}
	// Activity never extends the session past the hard expiry.
	for _, step := range []time.Duration{20 * time.Minute, 20 * time.Minute, 19 * time.Minute} {
		e.clock.Advance(step)
		if got := e.serveSession(http.MethodPatch, "/api/private/me", `{"name":"Still here"}`, cookie.Value); got.Code != http.StatusOK && got.Code != http.StatusNoContent {
			t.Fatalf("activity at +%s status=%d body=%s", e.clock.Now().Sub(created), got.Code, got.Body)
		}
	}
	// Even if some path pushed expires_at out, hard_expires_at still ends it.
	if _, err := e.pool.Exec(context.Background(), `update user_sessions set expires_at=expires_at+interval '90 days' where id_hash=$1`,
		platformcrypto.HashToken(cookie.Value)); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(time.Minute)
	if got := e.serveSession(http.MethodGet, "/api/private/me", "", cookie.Value); got.Code != http.StatusUnauthorized {
		t.Fatalf("me after 1h status=%d body=%s", got.Code, got.Body)
	}
}

func TestImpersonateAdminForbidden(t *testing.T) {
	e := newImpersonateEnv(t)
	if got, cookie := e.impersonate(t, e.otherAdminID); got.Code != http.StatusForbidden || cookie != nil {
		t.Fatalf("admin target status=%d body=%s", got.Code, got.Body)
	}
	if got, _ := e.impersonate(t, e.rootID); got.Code != http.StatusForbidden {
		t.Fatalf("self target status=%d body=%s", got.Code, got.Body)
	}
	// Password checks are rate limited per admin, so probe the rest with the key.
	if got := serveBearer(e.handler, http.MethodPost, impersonatePath(e.serviceUserID), "", testAdminKey); got.Code != http.StatusNotFound {
		t.Fatalf("system user status=%d body=%s", got.Code, got.Body)
	}
	if got := serveBearer(e.handler, http.MethodPost, impersonatePath(999999), "", testAdminKey); got.Code != http.StatusNotFound {
		t.Fatalf("unknown user status=%d body=%s", got.Code, got.Body)
	}
	var sessions int
	if err := e.pool.QueryRow(context.Background(), `select count(*) from user_sessions where impersonated_by is not null`).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatalf("impersonation sessions = %d err=%v", sessions, err)
	}
}

func TestImpersonateDisabledUser403(t *testing.T) {
	e := newImpersonateEnv(t)
	if got, cookie := e.impersonate(t, e.disabled); got.Code != http.StatusForbidden || cookie != nil {
		t.Fatalf("disabled target status=%d body=%s", got.Code, got.Body)
	}
}

func TestImpersonatedSessionCannotUseAdmin(t *testing.T) {
	e := newImpersonateEnv(t)
	_, cookie := e.impersonate(t, e.targetID)
	if cookie == nil {
		t.Fatal("no impersonation cookie")
	}
	if got := e.serveSession(http.MethodGet, "/api/private/admin/users", "", cookie.Value); got.Code != http.StatusForbidden {
		t.Fatalf("admin route status=%d body=%s", got.Code, got.Body)
	}
	// Even if the target is promoted afterwards, the impersonated session stays out.
	if _, err := e.pool.Exec(context.Background(), `update users set is_admin=true where id=$1`, e.targetID); err != nil {
		t.Fatal(err)
	}
	if got := e.serveSession(http.MethodGet, "/api/private/admin/users", "", cookie.Value); got.Code != http.StatusForbidden {
		t.Fatalf("admin route after promotion status=%d body=%s", got.Code, got.Body)
	}
}

func TestImpersonatedSessionCannotChangeAccountSecurity(t *testing.T) {
	e := newImpersonateEnv(t)
	_, cookie := e.impersonate(t, e.targetID)
	if cookie == nil {
		t.Fatal("no impersonation cookie")
	}
	org := itoa64(e.orgID)
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/private/me/password", `{"current_password":"x","new_password":"another-password-123"}`},
		{http.MethodPost, "/api/private/me/email", `{"email":"new@example.test","password":"x"}`},
		{http.MethodPost, "/api/private/me/totp", `{"password":"x"}`},
		{http.MethodPatch, "/api/private/me/totp", `{"code":"123456"}`},
		{http.MethodDelete, "/api/private/me/totp", `{"password":"x"}`},
		{http.MethodPost, "/api/private/me/totp/recovery-codes", `{"password":"x"}`},
		{http.MethodDelete, "/api/private/me", `{"password":"x"}`},
		{http.MethodDelete, "/api/private/me/sessions", ``},
		{http.MethodPost, "/api/private/auth/email-change/confirm", `{"token":"x"}`},
		{http.MethodPost, "/api/private/orgs/" + org + "/api-keys", `{"name":"k","scopes":["stats:read"]}`},
		// Nothing created during the session may outlive it.
		{http.MethodPost, "/api/private/orgs/" + org + "/invitations", `{"email":"guest@example.test","role":"viewer"}`},
		{http.MethodPatch, "/api/private/orgs/" + org + "/members/" + itoa64(e.targetID), `{"role":"admin"}`},
		{http.MethodPost, "/api/private/sites/1/shares", `{}`},
		{http.MethodPost, "/api/private/invitations/some-token/accept", `{}`},
		{http.MethodPost, "/api/private/orgs/" + org + "/channels", `{"name":"c","kind":"webhook","config":{"url":"https://hook.example.test"}}`},
		{http.MethodPost, "/api/private/sites/1/alerts", `{}`},
		{http.MethodPost, "/api/private/sites/1/reports", `{}`},
	} {
		if got := e.serveSession(tc.method, tc.path, tc.body, cookie.Value, "X-Supermetry-Org", org); got.Code != http.StatusForbidden {
			t.Errorf("%s %s status=%d body=%s", tc.method, tc.path, got.Code, got.Body)
		}
	}
	// Ordinary organization work still succeeds.
	if got := e.serveSession(http.MethodGet, "/api/private/orgs/"+org+"/members", "", cookie.Value, "X-Supermetry-Org", org); got.Code != http.StatusOK {
		t.Fatalf("members status=%d body=%s", got.Code, got.Body)
	}
	// Ending the session is a plain logout.
	if got := e.serveSession(http.MethodPost, "/api/private/auth/logout", "", cookie.Value); got.Code != http.StatusNoContent {
		t.Fatalf("logout status=%d body=%s", got.Code, got.Body)
	}
	if got := e.serveSession(http.MethodGet, "/api/private/me", "", cookie.Value); got.Code != http.StatusUnauthorized {
		t.Fatalf("me after logout status=%d", got.Code)
	}
}

func TestImpersonationEndsWhenAdminLosesAccess(t *testing.T) {
	for name, revoke := range map[string]string{
		"demoted":      `update users set is_admin=false where id=$1`,
		"soft deleted": `update users set deleted_at=now() where id=$1`,
	} {
		t.Run(name, func(t *testing.T) {
			e := newImpersonateEnv(t)
			_, cookie := e.impersonate(t, e.targetID)
			if cookie == nil {
				t.Fatal("no impersonation cookie")
			}
			if _, err := e.pool.Exec(context.Background(), revoke, e.rootID); err != nil {
				t.Fatal(err)
			}
			if got := e.serveSession(http.MethodGet, "/api/private/me", "", cookie.Value); got.Code != http.StatusUnauthorized {
				t.Fatalf("me after admin lost access status=%d body=%s", got.Code, got.Body)
			}
		})
	}
}

func TestImpersonateAuditBoth(t *testing.T) {
	e := newImpersonateEnv(t)
	if got, _ := e.impersonate(t, e.targetID); got.Code != http.StatusNoContent {
		t.Fatalf("impersonate status=%d body=%s", got.Code, got.Body)
	}
	ctx := context.Background()
	var instanceRows, orgRows int
	if err := e.pool.QueryRow(ctx, `select count(*) from audit_log where org_id is null and actor_user_id=$1
		and action='instance.user.impersonated' and target_type='user' and target_id=$2`, e.rootID, e.targetID).Scan(&instanceRows); err != nil {
		t.Fatal(err)
	}
	if err := e.pool.QueryRow(ctx, `select count(*) from audit_log where org_id=$3 and actor_user_id=$1
		and action='instance.user.impersonated' and target_type='user' and target_id=$2`, e.rootID, e.targetID, e.orgID).Scan(&orgRows); err != nil {
		t.Fatal(err)
	}
	if instanceRows != 1 || orgRows != 1 {
		t.Fatalf("instance audit rows=%d org audit rows=%d", instanceRows, orgRows)
	}
}

func TestImpersonateWithKeyReturnsToken(t *testing.T) {
	e := newImpersonateEnv(t)
	response := serveBearer(e.handler, http.MethodPost, impersonatePath(e.targetID), "", testAdminKey)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	if cookies := response.Result().Cookies(); len(cookies) != 0 {
		t.Fatalf("key path set cookies: %+v", cookies)
	}
	var body struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Token == "" || !body.ExpiresAt.Equal(e.clock.Now().Add(time.Hour)) {
		t.Fatalf("body = %s", response.Body)
	}
	me := e.serveSession(http.MethodGet, "/api/private/me", "", body.Token)
	if me.Code != http.StatusOK {
		t.Fatalf("me status=%d body=%s", me.Code, me.Body)
	}
	if got, _ := decodeMe(t, me); got.User.Email != "target@example.test" || got.ImpersonatedByEmail == nil {
		t.Fatalf("me = %s", me.Body)
	}
	if got := serveBearer(e.handler, http.MethodPost, impersonatePath(e.otherAdminID), "", testAdminKey); got.Code != http.StatusForbidden {
		t.Fatalf("key admin target status=%d", got.Code)
	}
	if got := serveBearer(e.handler, http.MethodPost, impersonatePath(e.serviceUserID), "", testAdminKey); got.Code != http.StatusNotFound {
		t.Fatalf("key system target status=%d", got.Code)
	}
}

func TestImpersonatePasswordResetRequired409(t *testing.T) {
	e := newImpersonateEnv(t)
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `update users set password_reset_required=true where id=$1`, e.targetID); err != nil {
		t.Fatal(err)
	}
	got, cookie := e.impersonate(t, e.targetID)
	if got.Code != http.StatusConflict || cookie != nil || !strings.Contains(got.Body.String(), "reset their password") {
		t.Fatalf("status=%d body=%s", got.Code, got.Body)
	}
	var sessions, audits int
	if err := e.pool.QueryRow(ctx, `select count(*) from user_sessions where impersonated_by is not null`).Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if err := e.pool.QueryRow(ctx, `select count(*) from audit_log where action='instance.user.impersonated'`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if sessions != 0 || audits != 0 {
		t.Fatalf("sessions=%d audits=%d", sessions, audits)
	}
	if own := e.serveSession(http.MethodGet, "/api/private/me", "", e.adminCookie); own.Code != http.StatusOK {
		t.Fatalf("admin session after refusal status=%d", own.Code)
	}
	if key := serveBearer(e.handler, http.MethodPost, impersonatePath(e.targetID), "", testAdminKey); key.Code != http.StatusConflict {
		t.Fatalf("key path status=%d body=%s", key.Code, key.Body)
	}
}
