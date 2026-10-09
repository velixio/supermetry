package platformhttp_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"supermetry/internal/platform/authz"
	platformcrypto "supermetry/internal/platform/crypto"
	platformhttp "supermetry/internal/platform/http"
	"supermetry/internal/platform/mail"
)

func TestDeleteOrgRequiresPasswordAndEnabledSecondFactor(t *testing.T) {
	fixture := newAccountHTTPFixture(t, &mail.Fake{})
	principal := authz.Principal{UserID: fixture.userID, OrgID: 1, Role: authz.RoleOwner}
	handler := platformhttp.New(platformhttp.Deps{
		Auth: fixture.auth, Store: fixture.store, Authenticator: actorAuthenticator{"owner": principal}, Mutations: noopMutations{},
	})
	request := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodDelete, "/api/private/orgs/1", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Test-Actor", "owner")
		req.Header.Set("X-CSRF-Token", "token")
		req.AddCookie(&http.Cookie{Name: "supermetry_csrf", Value: "token"})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}

	if got := request(`{"password":"wrong"}`); got.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password status=%d body=%s, want 401", got.Code, got.Body.String())
	}
	if got := request(`{"password":"pw-123456789"}`); got.Code != http.StatusNoContent {
		t.Fatalf("correct password status=%d body=%s, want 204", got.Code, got.Body.String())
	}

	box, err := platformcrypto.NewSecretBox([]byte("0123456789abcdef0123456789abcdef"), "panel/totp/v1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.Pool.Exec(context.Background(), `update users set totp_enabled=true,totp_secret_enc=$2 where id=$1`, fixture.userID, box.Seal([]byte("JBSWY3DPEHPK3PXP"))); err != nil {
		t.Fatal(err)
	}
	if got := request(`{"password":"pw-123456789"}`); got.Code != http.StatusBadRequest {
		t.Fatalf("missing TOTP status=%d body=%s, want 400", got.Code, got.Body.String())
	}
}
