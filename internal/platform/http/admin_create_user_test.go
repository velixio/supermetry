package platformhttp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
	if strings.Contains(response.Body.String(), "password") && !strings.Contains(response.Body.String(), "password_reset_required") {
		t.Fatalf("response leaks password: %s", response.Body)
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
