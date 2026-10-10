package platformhttp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	platformauth "supermetry/internal/platform/auth"
	"supermetry/internal/platform/mail"
)

type accountHTTPClock struct {
	now time.Time
}

func (c *accountHTTPClock) Now() time.Time {
	return c.now
}

func (c *accountHTTPClock) Advance(d time.Duration) {
	c.now = c.now.Add(d)
}

func TestTOTPHTTPRequiresReauthenticationAndSupportsRecoveryLifecycle(t *testing.T) {
	clock := &accountHTTPClock{now: time.Now()}
	fixture := newAccountHTTPFixture(t, &mail.Fake{}, platformauth.WithClock(clock))
	for _, route := range []struct {
		method string
		path   string
	}{
		{method: http.MethodDelete, path: "/api/private/me/totp"},
		{method: http.MethodPost, path: "/api/private/me/totp/recovery-codes"},
	} {
		request := httptest.NewRequest(route.method, route.path, nil)
		request.Header.Set("X-CSRF-Token", "token")
		request.AddCookie(&http.Cookie{Name: "supermetry_csrf", Value: "token"})
		response := httptest.NewRecorder()
		fixture.handler.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("anonymous %s %s status=%d body=%s", route.method, route.path, response.Code, response.Body.String())
		}
	}
	if got := fixture.request(t, http.MethodPost, "/api/private/me/totp", `{"current_password":"wrong"}`, true); got.Code != http.StatusUnauthorized {
		t.Fatalf("wrong enrollment password status=%d body=%s", got.Code, got.Body.String())
	}
	enrolled := fixture.request(t, http.MethodPost, "/api/private/me/totp", `{"current_password":"pw-123456789"}`, true)
	if enrolled.Code != http.StatusCreated {
		t.Fatalf("enroll status=%d body=%s", enrolled.Code, enrolled.Body.String())
	}
	var enrollment struct {
		URL           string   `json:"url"`
		RecoveryCodes []string `json:"recovery_codes"`
	}
	if err := json.Unmarshal(enrolled.Body.Bytes(), &enrollment); err != nil {
		t.Fatal(err)
	}
	code := currentTOTPCode(t, enrollment.URL, clock.Now())
	if got := fixture.request(t, http.MethodPatch, "/api/private/me/totp", `{"code":"`+code+`"}`, true); got.Code != http.StatusNoContent {
		t.Fatalf("confirm status=%d body=%s", got.Code, got.Body.String())
	}

	missing := fixture.request(t, http.MethodPost, "/api/private/me/totp/recovery-codes", `{"current_password":"pw-123456789"}`, true)
	if missing.Code != http.StatusUnauthorized {
		t.Fatalf("regenerate without factor status=%d body=%s", missing.Code, missing.Body.String())
	}
	clock.Advance(time.Minute)
	regenerated := fixture.request(t, http.MethodPost, "/api/private/me/totp/recovery-codes", `{"current_password":"pw-123456789","code":"`+enrollment.RecoveryCodes[0]+`"}`, true)
	if regenerated.Code != http.StatusOK {
		t.Fatalf("regenerate status=%d body=%s", regenerated.Code, regenerated.Body.String())
	}
	var recovery struct {
		RecoveryCodes []string `json:"recovery_codes"`
	}
	if err := json.Unmarshal(regenerated.Body.Bytes(), &recovery); err != nil {
		t.Fatal(err)
	}
	if len(recovery.RecoveryCodes) != 10 || recovery.RecoveryCodes[0] == enrollment.RecoveryCodes[0] {
		t.Fatalf("regenerated recovery codes = %v", recovery.RecoveryCodes)
	}
	disabled := fixture.request(t, http.MethodDelete, "/api/private/me/totp", `{"current_password":"pw-123456789","code":"`+recovery.RecoveryCodes[0]+`"}`, true)
	if disabled.Code != http.StatusNoContent {
		t.Fatalf("disable status=%d body=%s", disabled.Code, disabled.Body.String())
	}
	user, err := fixture.store.UserByID(context.Background(), fixture.userID)
	if err != nil {
		t.Fatal(err)
	}
	if user.TOTPEnabled {
		t.Fatal("TOTP remains enabled")
	}
}

func currentTOTPCode(t *testing.T, otpauthURL string, now time.Time) string {
	t.Helper()
	parsed, err := url.Parse(otpauthURL)
	if err != nil {
		t.Fatal(err)
	}
	code, err := totp.GenerateCode(parsed.Query().Get("secret"), now)
	if err != nil {
		t.Fatal(err)
	}
	return code
}
