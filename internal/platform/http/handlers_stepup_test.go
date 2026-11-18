package platformhttp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	platformauth "supermetry/internal/platform/auth"
	"supermetry/internal/platform/authz"
	platformcrypto "supermetry/internal/platform/crypto"
	platformhttp "supermetry/internal/platform/http"
	"supermetry/internal/platform/keys"
	"supermetry/internal/platform/mail"
	"supermetry/internal/privacy"
)

type stepUpClock struct{ now time.Time }

func (c *stepUpClock) Now() time.Time { return c.now }

type zipExporter struct{}

func (zipExporter) Write(_ context.Context, w io.Writer, _ int32, _, _ time.Time) error {
	_, err := w.Write([]byte("PK\x03\x04"))
	return err
}

type stepUpEnv struct {
	f       lifecycleHTTPFixture
	handler http.Handler
	clock   *stepUpClock
	cookie  string
	keys    *keys.Keys
}

func newStepUpEnv(t *testing.T) *stepUpEnv {
	t.Helper()
	f := newLifecycleHTTPFixture(t)
	box, err := platformcrypto.NewSecretBox([]byte("0123456789abcdef0123456789abcdef"), "supermetry/totp/v1")
	if err != nil {
		t.Fatal(err)
	}
	clock := &stepUpClock{now: time.Now()}
	authService := platformauth.New(f.data, &mail.Fake{}, box, "https://supermetry.example", platformauth.WithClock(clock))
	cookie, err := authService.Login(context.Background(), "lifecycle-owner@example.test", "pw-123456789", "", "test")
	if err != nil {
		t.Fatal(err)
	}
	keyService := keys.New(f.data)
	t.Cleanup(keyService.Close)
	handler := platformhttp.New(platformhttp.Deps{
		TrackingMode: privacy.Identified,
		Auth:         authService, Authenticator: platformhttp.NewAuthenticator(authService, keyService, nil, f.orgs),
		Store: f.data, Sites: f.registry, Keys: keyService, Orgs: f.orgs, Exporter: zipExporter{},
		Mutations: platformhttp.Services{Sites: f.sites, Keys: keyService, Store: f.data},
	})
	return &stepUpEnv{f: f, handler: handler, clock: clock, cookie: cookie, keys: keyService}
}

func (e *stepUpEnv) do(method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Supermetry-Org", itoa64(e.f.orgID))
	req.AddCookie(&http.Cookie{Name: platformauth.CookieName, Value: e.cookie})
	if method != http.MethodGet {
		req.Header.Set("X-CSRF-Token", "token")
		req.AddCookie(&http.Cookie{Name: "supermetry_csrf", Value: "token"})
	}
	response := httptest.NewRecorder()
	e.handler.ServeHTTP(response, req)
	return response
}

func (e *stepUpEnv) expectReauthRequired(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	var body struct {
		Code   string `json:"code"`
		Status int    `json:"status"`
	}
	_ = json.Unmarshal(response.Body.Bytes(), &body)
	if response.Code != http.StatusForbidden || body.Code != "reauth_required" || body.Status != http.StatusForbidden {
		t.Fatalf("status=%d body=%s, want 403 reauth_required", response.Code, response.Body.String())
	}
}

func TestStepUpGuardsSensitiveActions(t *testing.T) {
	org := func(e *stepUpEnv) string { return "/api/private/orgs/" + itoa64(e.f.orgID) }
	tests := []struct {
		name string
		// call returns the request to make; the siteID and key are created per run.
		call   func(e *stepUpEnv, siteID int32, keyID int64) (method, path, body string)
		wantOK int
	}{
		{"create API key", func(e *stepUpEnv, _ int32, _ int64) (string, string, string) {
			return http.MethodPost, org(e) + "/api-keys", `{"name":"ci","scopes":["stats:read"]}`
		}, http.StatusCreated},
		{"roll API key", func(e *stepUpEnv, _ int32, keyID int64) (string, string, string) {
			return http.MethodPost, org(e) + "/api-keys/" + itoa64(keyID) + "/roll", `{"grace_hours":1}`
		}, http.StatusCreated},
		{"change member role", func(e *stepUpEnv, _ int32, _ int64) (string, string, string) {
			return http.MethodPatch, org(e) + "/members/" + itoa64(e.f.viewerID), `{"role":"editor"}`
		}, http.StatusNoContent},
		{"remove member", func(e *stepUpEnv, _ int32, _ int64) (string, string, string) {
			return http.MethodDelete, org(e) + "/members/" + itoa64(e.f.viewerID), ""
		}, http.StatusNoContent},
		{"export site", func(_ *stepUpEnv, siteID int32, _ int64) (string, string, string) {
			return http.MethodGet, "/api/private/sites/" + itoa32(siteID) + "/export?from=2026-10-01&to=2026-10-02", ""
		}, http.StatusOK},
		{"delete site", func(_ *stepUpEnv, siteID int32, _ int64) (string, string, string) {
			return http.MethodDelete, "/api/private/sites/" + itoa32(siteID), ""
		}, http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newStepUpEnv(t)
			siteID := e.f.createSite(t, "StepUp")
			owner := authz.Principal{UserID: e.f.ownerID, OrgID: e.f.orgID, Role: authz.RoleOwner}
			_, info, err := e.keys.Create(context.Background(), owner, "old", []string{"stats:read"}, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			method, path, body := tt.call(e, siteID, info.ID)

			// A session that just signed in counts as recently authenticated.
			// Let it go stale to start from "reauth required".
			e.clock.now = e.clock.now.Add(platformauth.StepUpWindow + time.Second)
			e.expectReauthRequired(t, e.do(method, path, body))

			// A wrong password does not open the window.
			bad := e.do(http.MethodPost, "/api/private/auth/reauth", `{"password":"not-the-password"}`)
			if bad.Code != http.StatusUnauthorized || !strings.Contains(bad.Body.String(), `"code":"invalid_credentials"`) {
				t.Fatalf("bad reauth status=%d body=%s", bad.Code, bad.Body.String())
			}
			e.expectReauthRequired(t, e.do(method, path, body))

			good := e.do(http.MethodPost, "/api/private/auth/reauth", `{"password":"pw-123456789"}`)
			if good.Code != http.StatusOK || !strings.Contains(good.Body.String(), `"valid_for_seconds":600`) {
				t.Fatalf("reauth status=%d body=%s", good.Code, good.Body.String())
			}
			if got := e.do(method, path, body); got.Code != tt.wantOK {
				t.Fatalf("after reauth status=%d body=%s, want %d", got.Code, got.Body.String(), tt.wantOK)
			}

			if tt.name == "delete site" {
				return // the site is gone, so there is nothing left to guard
			}
			// The window closes again after ten minutes.
			e.clock.now = e.clock.now.Add(platformauth.StepUpWindow + time.Second)
			e.expectReauthRequired(t, e.do(method, path, body))
		})
	}
}

func TestStepUpFreshSignInIsEnoughAndKeysAreExempt(t *testing.T) {
	e := newStepUpEnv(t)
	// Signing in is itself a recent authentication.
	created := e.do(http.MethodPost, "/api/private/orgs/"+itoa64(e.f.orgID)+"/api-keys", `{"name":"fresh","scopes":["stats:read"]}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("fresh session status=%d body=%s", created.Code, created.Body.String())
	}

	// An API key with keys:manage is exempt but stays inside its own scopes.
	e.clock.now = e.clock.now.Add(time.Hour)
	owner := authz.Principal{UserID: e.f.ownerID, OrgID: e.f.orgID, Role: authz.RoleOwner}
	secret, _, err := e.keys.Create(context.Background(), owner, "automation", []string{"keys:manage", "stats:read"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+secret)
		response := httptest.NewRecorder()
		e.handler.ServeHTTP(response, req)
		return response
	}
	if got := call(http.MethodPost, "/api/public/v1/api-keys", `{"name":"child","scopes":["stats:read"]}`); got.Code != http.StatusCreated {
		t.Fatalf("key-created key status=%d body=%s", got.Code, got.Body.String())
	}
	if got := call(http.MethodPost, "/api/public/v1/api-keys", `{"name":"child","scopes":["sites:write"]}`); got.Code != http.StatusForbidden || strings.Contains(got.Body.String(), "reauth_required") {
		t.Fatalf("an escalating child must still be a plain 403, got %d %s", got.Code, got.Body.String())
	}
	var listed struct {
		Data []struct{ ID int64 } `json:"data"`
	}
	list := call(http.MethodGet, "/api/public/v1/api-keys", "")
	if list.Code != http.StatusOK {
		t.Fatalf("list status=%d", list.Code)
	}
	_ = json.Unmarshal(list.Body.Bytes(), &listed)
	if len(listed.Data) == 0 {
		t.Fatal("no keys listed")
	}
}

func TestPublicKeyExpiryChoicesAndHygieneFields(t *testing.T) {
	e := newStepUpEnv(t)
	owner := authz.Principal{UserID: e.f.ownerID, OrgID: e.f.orgID, Role: authz.RoleOwner}
	secret, _, err := e.keys.Create(context.Background(), owner, "automation", []string{"keys:manage", "stats:read"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+secret)
		response := httptest.NewRecorder()
		e.handler.ServeHTTP(response, req)
		return response
	}
	type created struct {
		ID        int64      `json:"id"`
		Key       string     `json:"key"`
		ExpiresAt *time.Time `json:"expires_at"`
	}
	create := func(body string) (created, int) {
		response := call(http.MethodPost, "/api/public/v1/api-keys", body)
		var out created
		_ = json.Unmarshal(response.Body.Bytes(), &out)
		return out, response.Code
	}

	def, code := create(`{"name":"default","scopes":["stats:read"]}`)
	if code != http.StatusCreated || def.ExpiresAt == nil || time.Until(*def.ExpiresAt) < 89*24*time.Hour || time.Until(*def.ExpiresAt) > 91*24*time.Hour {
		t.Fatalf("default expiry: %d %+v", code, def)
	}
	if !strings.HasPrefix(def.Key, "smkapi_") {
		t.Fatalf("key format %q", def.Key)
	}
	never, code := create(`{"name":"never","scopes":["stats:read"],"never_expires":true}`)
	if code != http.StatusCreated || never.ExpiresAt != nil {
		t.Fatalf("never: %d %+v", code, never)
	}
	days, code := create(`{"name":"days","scopes":["stats:read"],"expires_in_days":30}`)
	if code != http.StatusCreated || days.ExpiresAt == nil || time.Until(*days.ExpiresAt) < 29*24*time.Hour || time.Until(*days.ExpiresAt) > 31*24*time.Hour {
		t.Fatalf("days: %d %+v", code, days)
	}
	for name, body := range map[string]string{
		"conflicting choices": `{"name":"x","scopes":["stats:read"],"never_expires":true,"expires_in_days":5}`,
		"zero days":           `{"name":"x","scopes":["stats:read"],"expires_in_days":0}`,
		"too many days":       `{"name":"x","scopes":["stats:read"],"expires_in_days":4000}`,
	} {
		if got := call(http.MethodPost, "/api/public/v1/api-keys", body); got.Code != http.StatusBadRequest {
			t.Errorf("%s: status=%d body=%s, want 400", name, got.Code, got.Body.String())
		}
	}

	// A key expiring in 10 days is flagged; a never-expiring key says so.
	soon := time.Now().Add(10 * 24 * time.Hour)
	if _, _, err := e.keys.Create(context.Background(), owner, "soon", []string{"stats:read"}, nil, &soon); err != nil {
		t.Fatal(err)
	}
	var list struct {
		Data []struct {
			Name            string  `json:"name"`
			Prefix          string  `json:"prefix"`
			NeverExpires    bool    `json:"never_expires"`
			ExpiringSoon    bool    `json:"expiring_soon"`
			LastUsedCountry *string `json:"last_used_country"`
			LastUsedClient  *string `json:"last_used_client"`
		} `json:"data"`
	}
	response := call(http.MethodGet, "/api/public/v1/api-keys", "")
	if response.Code != http.StatusOK {
		t.Fatalf("list status=%d", response.Code)
	}
	if err := json.Unmarshal(response.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	flags := map[string][2]bool{}
	for _, item := range list.Data {
		flags[item.Name] = [2]bool{item.NeverExpires, item.ExpiringSoon}
		if item.Prefix != "" {
			t.Errorf("the public list must not echo any part of a secret: %s prefix %q", item.Name, item.Prefix)
		}
	}
	if flags["soon"] != [2]bool{false, true} || flags["never"] != [2]bool{true, false} || flags["default"] != [2]bool{false, false} {
		t.Fatalf("flags = %v", flags)
	}
	if !strings.Contains(response.Body.String(), `"last_used_country":null`) {
		t.Fatalf("an unused key reports null last-use metadata: %s", response.Body.String())
	}
}

func TestPublicRollKeyAndNoEscalation(t *testing.T) {
	e := newStepUpEnv(t)
	owner := authz.Principal{UserID: e.f.ownerID, OrgID: e.f.orgID, Role: authz.RoleOwner}
	secret, _, err := e.keys.Create(context.Background(), owner, "automation", []string{"keys:manage", "stats:read"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, target, _ := e.keys.Create(context.Background(), owner, "reports", []string{"stats:read"}, nil, nil)
	_, broad, _ := e.keys.Create(context.Background(), owner, "broad", []string{"stats:read", "sites:write"}, nil, nil)
	call := func(path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+secret)
		response := httptest.NewRecorder()
		e.handler.ServeHTTP(response, req)
		return response
	}
	rolled := call(fmt.Sprintf("/api/public/v1/api-keys/%d/roll", target.ID), `{"grace_hours":24}`)
	if rolled.Code != http.StatusCreated {
		t.Fatalf("roll status=%d body=%s", rolled.Code, rolled.Body.String())
	}
	var body platformhttp.RolledAPIKey
	if err := json.Unmarshal(rolled.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.OldKeyID != target.ID || !strings.HasPrefix(body.Key, "smkapi_") || body.OldKeyExpiresAt == nil || time.Until(*body.OldKeyExpiresAt) > 25*time.Hour {
		t.Fatalf("roll response = %+v", body)
	}
	if got := call(fmt.Sprintf("/api/public/v1/api-keys/%d/roll", target.ID), `{"grace_hours":3}`); got.Code != http.StatusBadRequest {
		t.Fatalf("invalid grace status=%d body=%s", got.Code, got.Body.String())
	}
	if got := call(fmt.Sprintf("/api/public/v1/api-keys/%d/roll", broad.ID), ``); got.Code != http.StatusNotFound {
		t.Fatalf("rolling a broader key status=%d body=%s, want 404", got.Code, got.Body.String())
	}
}

func TestPrivateKeyListUsesSnakeCaseWithPrefix(t *testing.T) {
	e := newStepUpEnv(t)
	owner := authz.Principal{UserID: e.f.ownerID, OrgID: e.f.orgID, Role: authz.RoleOwner}
	if _, _, err := e.keys.CreateDefaultExpiry(context.Background(), owner, "listed", []string{"stats:read"}, nil); err != nil {
		t.Fatal(err)
	}
	response := e.do(http.MethodGet, "/api/private/orgs/"+itoa64(e.f.orgID)+"/api-keys", "")
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	for _, field := range []string{`"prefix":"`, `"never_expires":false`, `"expiring_soon":false`, `"last_used_client":null`, `"expires_at":"`} {
		if !strings.Contains(response.Body.String(), field) {
			t.Errorf("list is missing %s: %s", field, response.Body.String())
		}
	}
}
