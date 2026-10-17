package platformhttp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"supermetry/internal/platform/admin"
	platformauth "supermetry/internal/platform/auth"
	platformhttp "supermetry/internal/platform/http"
	"supermetry/internal/platform/mail"
	"supermetry/internal/platform/store"
)

type disabledMail struct{ mail.Fake }

func (*disabledMail) Enabled() bool { return false }

// resetHandler serves the env's database with a reset sender over sender.
func (e adminOrgDeleteEnv) resetHandler(t *testing.T, sender mail.Sender) http.Handler {
	t.Helper()
	var rootID int64
	if err := e.pool.QueryRow(context.Background(), `select id from users where email='root@example.test'`).Scan(&rootID); err != nil {
		t.Fatal(err)
	}
	data := &store.Store{Pool: e.pool}
	service := admin.New(data)
	service.SetResetStarter(platformauth.New(data, sender, nil, "https://app.example.test").StartReset)
	return platformhttp.New(platformhttp.Deps{
		Authenticator: actorAuthenticator{"admin": {UserID: rootID, SiteIDs: []int32{}}},
		Store:         data,
		Admin:         service,
		Mutations:     platformhttp.Services{Store: data},
		Now:           time.Now,
	})
}

func postAdminUser(handler http.Handler, actor, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/api/private/admin/users", strings.NewReader(body))
	request.Header.Set("X-Test-Actor", actor)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CSRF-Token", "t")
	request.AddCookie(&http.Cookie{Name: "supermetry_csrf", Value: "t"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestAdminCreateUserWithOrg(t *testing.T) {
	e := newAdminOrgDeleteEnv(t)
	if got := postAdminUser(e.handler, "member", `{"email":"x@x.test"}`); got.Code != http.StatusForbidden {
		t.Fatalf("non-admin status = %d", got.Code)
	}
	response := postAdminUser(e.handler, "admin", `{"email":"New@X.test","name":"New","org_name":"Newco","send_reset":true}`)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", response.Code, response.Body)
	}
	var user struct {
		ID          int64  `json:"id"`
		Email       string `json:"email"`
		Memberships []struct {
			OrgName string `json:"org_name"`
			Role    string `json:"role"`
		} `json:"memberships"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &user); err != nil {
		t.Fatal(err)
	}
	if user.Email != "new@x.test" || len(user.Memberships) != 1 || user.Memberships[0].Role != "owner" || user.Memberships[0].OrgName != "Newco" {
		t.Fatalf("user = %+v", user)
	}
	var raw map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"password", "password_hash", "reset_url"} {
		if _, found := raw[key]; found {
			t.Fatalf("response has %q: %s", key, response.Body)
		}
	}
}

func TestAdminCreateUserResetMailEnabled(t *testing.T) {
	e := newAdminOrgDeleteEnv(t)
	fake := &mail.Fake{}
	response := postAdminUser(e.resetHandler(t, fake), "admin", `{"email":"mailed@x.test","send_reset":true}`)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", response.Code, response.Body)
	}
	if strings.Contains(response.Body.String(), "reset_url") {
		t.Fatalf("reset_url present with mail enabled: %s", response.Body)
	}
	if len(fake.Sent) != 1 || fake.Sent[0].To != "mailed@x.test" {
		t.Fatalf("sent = %+v", fake.Sent)
	}
}

func TestAdminCreateUserResetURLWithoutMail(t *testing.T) {
	e := newAdminOrgDeleteEnv(t)
	response := postAdminUser(e.resetHandler(t, &disabledMail{}), "admin", `{"email":"linked@x.test","send_reset":true}`)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", response.Code, response.Body)
	}
	var body struct {
		ResetURL string `json:"reset_url"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(body.ResetURL, "https://app.example.test/reset/") || len(body.ResetURL) < 80 {
		t.Fatalf("reset_url = %q", body.ResetURL)
	}
	if got := postAdminUser(e.resetHandler(t, &disabledMail{}), "admin", `{"email":"plain@x.test"}`); strings.Contains(got.Body.String(), "reset_url") {
		t.Fatalf("reset_url without send_reset: %s", got.Body)
	}
}

func TestAdminCreateUserNoOrg(t *testing.T) {
	e := newAdminOrgDeleteEnv(t)
	response := postAdminUser(e.handler, "admin", `{"email":"solo@x.test","name":"Solo"}`)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", response.Code, response.Body)
	}
	var n int
	if err := e.pool.QueryRow(context.Background(), `select count(*) from memberships m join users u on u.id=m.user_id where u.email='solo@x.test'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("memberships=%d err=%v", n, err)
	}
}

func TestAdminCreateUserDuplicateEmail(t *testing.T) {
	e := newAdminOrgDeleteEnv(t)
	ctx := context.Background()
	if got := postAdminUser(e.handler, "admin", `{"email":"a@x.test","name":"A"}`); got.Code != http.StatusCreated {
		t.Fatalf("first status = %d", got.Code)
	}
	var before string
	if err := e.pool.QueryRow(ctx, `select password_hash from users where email='a@x.test'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if got := postAdminUser(e.handler, "admin", `{"email":"A@x.test","name":"Other"}`); got.Code != http.StatusConflict {
		t.Fatalf("duplicate status = %d body=%s", got.Code, got.Body)
	}
	var after string
	if err := e.pool.QueryRow(ctx, `select password_hash from users where email='a@x.test'`).Scan(&after); err != nil || after != before {
		t.Fatalf("hash changed err=%v", err)
	}
}

func TestAdminCreateUserBadEmail(t *testing.T) {
	e := newAdminOrgDeleteEnv(t)
	for _, body := range []string{`{"email":"not-an-email"}`, `{"email":""}`, `{}`, `{"email":"a@x.test","bogus":1}`} {
		if got := postAdminUser(e.handler, "admin", body); got.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d body=%s", body, got.Code, got.Body)
		}
	}
}
