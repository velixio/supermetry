package platformhttp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"supermetry/internal/db"
	platformauth "supermetry/internal/platform/auth"
	"supermetry/internal/platform/authz"
	platformcrypto "supermetry/internal/platform/crypto"
	platformhttp "supermetry/internal/platform/http"
	"supermetry/internal/platform/mail"
	"supermetry/internal/platform/store"
	"supermetry/internal/platform/users"
	"supermetry/internal/testpg"
)

type accountHTTPFixture struct {
	handler http.Handler
	auth    *platformauth.Auth
	store   *store.Store
	userID  int64
	cookie  string
	mailer  *mail.Fake
}

func newAccountHTTPFixture(t *testing.T, sender mail.Sender, options ...platformauth.Option) accountHTTPFixture {
	t.Helper()
	pool, _ := testpg.New(t)
	if err := db.Migrate(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	data := &store.Store{Pool: pool}
	box, err := platformcrypto.NewSecretBox([]byte("0123456789abcdef0123456789abcdef"), "supermetry/totp/v1")
	if err != nil {
		t.Fatal(err)
	}
	authService := platformauth.New(data, sender, box, "https://supermetry.example", options...)
	userID, err := users.New(data).Create(context.Background(), "owner@example.test", "Owner", "pw-123456789")
	if err != nil {
		t.Fatal(err)
	}
	cookie, err := authService.Login(context.Background(), "owner@example.test", "pw-123456789", "", "test")
	if err != nil {
		t.Fatal(err)
	}
	principal := authz.Principal{UserID: userID, OrgID: 1, Role: authz.RoleOwner}
	return accountHTTPFixture{
		handler: platformhttp.New(platformhttp.Deps{Auth: authService, Store: data, Authenticator: actorAuthenticator{"owner": principal}}),
		auth:    authService, store: data, userID: userID, cookie: cookie,
		mailer: sender.(*mail.Fake),
	}
}

func (f accountHTTPFixture) request(t *testing.T, method, path, body string, csrf bool) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Test-Actor", "owner")
	request.AddCookie(&http.Cookie{Name: platformauth.CookieName, Value: f.cookie})
	if csrf {
		request.Header.Set("X-CSRF-Token", "token")
		request.AddCookie(&http.Cookie{Name: "supermetry_csrf", Value: "token"})
	}
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	return response
}

func TestAccountSelfServiceHTTP(t *testing.T) {
	t.Run("name update and CSRF", func(t *testing.T) {
		fixture := newAccountHTTPFixture(t, &mail.Fake{})
		if got := fixture.request(t, http.MethodPatch, "/api/private/me", `{"name":"Updated"}`, false); got.Code != http.StatusForbidden {
			t.Fatalf("missing CSRF status=%d body=%s", got.Code, got.Body.String())
		}
		got := fixture.request(t, http.MethodPatch, "/api/private/me", `{"name":"Updated"}`, true)
		if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"name":"Updated"`) {
			t.Fatalf("status=%d body=%s", got.Code, got.Body.String())
		}
	})

	t.Run("email change and wrong password", func(t *testing.T) {
		fixture := newAccountHTTPFixture(t, &mail.Fake{})
		wrong := fixture.request(t, http.MethodPost, "/api/private/me/email", `{"new_email":"new@example.test","password":"wrong"}`, true)
		if wrong.Code != http.StatusUnauthorized {
			t.Fatalf("wrong password status=%d body=%s", wrong.Code, wrong.Body.String())
		}
		got := fixture.request(t, http.MethodPost, "/api/private/me/email", `{"new_email":"new@example.test","password":"pw-123456789"}`, true)
		if got.Code != http.StatusAccepted || len(fixture.mailer.Sent) != 1 {
			t.Fatalf("status=%d messages=%d body=%s", got.Code, len(fixture.mailer.Sent), got.Body.String())
		}
	})

	t.Run("password change and wrong password", func(t *testing.T) {
		fixture := newAccountHTTPFixture(t, &mail.Fake{})
		wrong := fixture.request(t, http.MethodPost, "/api/private/me/password", `{"current_password":"wrong","new_password":"new-password-1"}`, true)
		if wrong.Code != http.StatusUnauthorized {
			t.Fatalf("wrong password status=%d body=%s", wrong.Code, wrong.Body.String())
		}
		got := fixture.request(t, http.MethodPost, "/api/private/me/password", `{"current_password":"pw-123456789","new_password":"new-password-1"}`, true)
		if got.Code != http.StatusNoContent {
			t.Fatalf("status=%d body=%s", got.Code, got.Body.String())
		}
	})

	t.Run("preferences update and validation", func(t *testing.T) {
		fixture := newAccountHTTPFixture(t, &mail.Fake{})
		updated := fixture.request(t, http.MethodPatch, "/api/private/me", `{"preferences":{"landing_page":"overview","sites_view":"table","last_site_id":null}}`, true)
		if updated.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", updated.Code, updated.Body.String())
		}
		var updateBody struct {
			Preferences store.UserPreferences `json:"preferences"`
		}
		if err := json.Unmarshal(updated.Body.Bytes(), &updateBody); err != nil {
			t.Fatal(err)
		}
		if updateBody.Preferences.LandingPage != "overview" || updateBody.Preferences.SitesView != "table" || updateBody.Preferences.LastSiteID != nil {
			t.Fatalf("preferences=%+v", updateBody.Preferences)
		}
		persisted, err := fixture.store.UserByID(context.Background(), fixture.userID)
		if err != nil || persisted.Preferences != updateBody.Preferences {
			t.Fatalf("persisted preferences=%+v err=%v", persisted.Preferences, err)
		}
		for _, body := range []string{
			`{"preferences":{"landing_page":"invalid"}}`,
			`{"preferences":{"sites_view":"invalid"}}`,
			`{"preferences":{"last_site_id":999999}}`,
		} {
			if got := fixture.request(t, http.MethodPatch, "/api/private/me", body, true); got.Code != http.StatusBadRequest {
				t.Fatalf("body=%s status=%d response=%s", body, got.Code, got.Body.String())
			}
		}
	})
}

func TestChangePasswordHTTPPasswordLengthBoundary(t *testing.T) {
	fixture := newAccountHTTPFixture(t, &mail.Fake{})
	for _, boundary := range []struct {
		name     string
		password string
		status   int
	}{
		{name: "five characters rejected", password: "short", status: http.StatusBadRequest},
		{name: "six characters accepted", password: "sixsix", status: http.StatusNoContent},
	} {
		t.Run(boundary.name, func(t *testing.T) {
			body := fmt.Sprintf(`{"current_password":"pw-123456789","new_password":%q}`, boundary.password)
			result := fixture.request(t, http.MethodPost, "/api/private/me/password", body, true)
			if result.Code != boundary.status {
				t.Fatalf("status=%d body=%s, want %d", result.Code, result.Body.String(), boundary.status)
			}
		})
	}
}

func TestEmailChangeWithoutSMTPIsConflict(t *testing.T) {
	pool, _ := testpg.New(t)
	if err := db.Migrate(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	data := &store.Store{Pool: pool}
	box, _ := platformcrypto.NewSecretBox([]byte("0123456789abcdef0123456789abcdef"), "supermetry/totp/v1")
	authService := platformauth.New(data, disabledSender{}, box, "https://supermetry.example")
	userID, _ := users.New(data).Create(context.Background(), "owner@example.test", "Owner", "pw-123456789")
	handler := platformhttp.New(platformhttp.Deps{Auth: authService, Store: data, Authenticator: actorAuthenticator{"owner": {UserID: userID, OrgID: 1, Role: authz.RoleOwner}}})
	request := httptest.NewRequest(http.MethodPost, "/api/private/me/email", strings.NewReader(`{"new_email":"new@example.test","password":"pw-123456789"}`))
	request.Header.Set("X-Test-Actor", "owner")
	request.Header.Set("X-CSRF-Token", "token")
	request.AddCookie(&http.Cookie{Name: "supermetry_csrf", Value: "token"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusConflict || response.Header().Get("Content-Type") != "application/problem+json" || !strings.Contains(response.Body.String(), "SMTP") {
		t.Fatalf("status=%d content-type=%q body=%s", response.Code, response.Header().Get("Content-Type"), response.Body.String())
	}
}

type disabledSender struct{}

func (disabledSender) Send(context.Context, mail.Message) error { return nil }
func (disabledSender) Enabled() bool                            { return false }

func TestEmailChangeConfirmRequiresSession(t *testing.T) {
	fixture := newAccountHTTPFixture(t, &mail.Fake{})
	got := fixture.request(t, http.MethodPost, "/api/private/me/email", `{"new_email":"new@example.test","password":"pw-123456789"}`, true)
	if got.Code != http.StatusAccepted || len(fixture.mailer.Sent) != 1 {
		t.Fatalf("start status=%d body=%s", got.Code, got.Body.String())
	}
	text := fixture.mailer.Sent[0].Text
	link := text[strings.Index(text, "/api/private/auth/email-change/confirm"):]
	anonymous := httptest.NewRequest(http.MethodGet, link, nil)
	response := httptest.NewRecorder()
	fixture.handler.ServeHTTP(response, anonymous)
	if response.Code != http.StatusSeeOther || !strings.HasPrefix(response.Header().Get("Location"), "/login?next=") {
		t.Fatalf("anonymous status=%d location=%q", response.Code, response.Header().Get("Location"))
	}
	token := link[strings.Index(link, "token=")+len("token="):]
	if got := fixture.request(t, http.MethodPost, "/api/private/auth/email-change/confirm", `{"token":"`+token+`"}`, false); got.Code != http.StatusForbidden {
		t.Fatalf("confirm without CSRF status=%d", got.Code)
	}
	if got := fixture.request(t, http.MethodGet, link, "", false); got.Code != http.StatusSeeOther || got.Header().Get("Location") != "/account/profile?email_changed=1" {
		t.Fatalf("confirm status=%d location=%q", got.Code, got.Header().Get("Location"))
	}
	user, err := fixture.store.UserByID(context.Background(), fixture.userID)
	if err != nil || user.Email != "new@example.test" {
		t.Fatalf("user=%+v err=%v", user, err)
	}
}

func TestCreateOrgHTTP(t *testing.T) {
	fixture := newAccountHTTPFixture(t, &mail.Fake{})
	if got := fixture.request(t, http.MethodPost, "/api/private/orgs", `{"name":"Second"}`, false); got.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF status=%d body=%s", got.Code, got.Body.String())
	}
	for _, body := range []string{`{"name":"   "}`, `{"name":"` + strings.Repeat("x", 81) + `"}`, `{}`} {
		if got := fixture.request(t, http.MethodPost, "/api/private/orgs", body, true); got.Code != http.StatusBadRequest {
			t.Fatalf("body %q status=%d", body, got.Code)
		}
	}
	got := fixture.request(t, http.MethodPost, "/api/private/orgs", `{"name":" Second Org "}`, true)
	if got.Code != http.StatusCreated || !strings.Contains(got.Body.String(), `"name":"Second Org"`) || !strings.Contains(got.Body.String(), `"slug":"second-org"`) {
		t.Fatalf("create status=%d body=%s", got.Code, got.Body.String())
	}
	var role string
	if err := fixture.store.Pool.QueryRow(context.Background(), `select m.role from memberships m join orgs o on o.id=m.org_id where o.slug='second-org' and m.user_id=$1`, fixture.userID).Scan(&role); err != nil || role != "owner" {
		t.Fatalf("membership role=%q err=%v", role, err)
	}
}

func TestCreateOrgLimitAppliesToEveryoneExceptInstanceAdmins(t *testing.T) {
	fixture := newAccountHTTPFixture(t, &mail.Fake{})
	principal := authz.Principal{UserID: fixture.userID, OrgID: 1, Role: authz.RoleOwner}
	fixture.handler = platformhttp.New(platformhttp.Deps{
		Auth: fixture.auth, Store: fixture.store, MaxOrgsPerUser: 2,
		Authenticator: actorAuthenticator{"owner": principal},
	})
	for _, name := range []string{"One", "Two"} {
		if got := fixture.request(t, http.MethodPost, "/api/private/orgs", `{"name":"`+name+`"}`, true); got.Code != http.StatusCreated {
			t.Fatalf("create %s status=%d body=%s", name, got.Code, got.Body.String())
		}
	}
	if got := fixture.request(t, http.MethodPost, "/api/private/orgs", `{"name":"Three"}`, true); got.Code != http.StatusForbidden {
		t.Fatalf("third org status=%d body=%s", got.Code, got.Body.String())
	}
	if _, err := fixture.store.Pool.Exec(context.Background(), `update users set is_admin=true where id=$1`, fixture.userID); err != nil {
		t.Fatal(err)
	}
	if got := fixture.request(t, http.MethodPost, "/api/private/orgs", `{"name":"Three"}`, true); got.Code != http.StatusCreated {
		t.Fatalf("instance admin status=%d body=%s", got.Code, got.Body.String())
	}
}
