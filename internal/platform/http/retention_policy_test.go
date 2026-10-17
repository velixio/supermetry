package platformhttp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"supermetry/internal/db"
	platformauth "supermetry/internal/platform/auth"
	"supermetry/internal/platform/authz"
	platformcrypto "supermetry/internal/platform/crypto"
	platformhttp "supermetry/internal/platform/http"
	"supermetry/internal/platform/mail"
	"supermetry/internal/platform/sitesvc"
	"supermetry/internal/platform/store"
	"supermetry/internal/testpg"
)

type retentionEnv struct {
	handler http.Handler
	service *sitesvc.Sites
}

func newRetentionEnv(t *testing.T, defaultDays int, forever bool) retentionEnv {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var userID, orgID int64
	if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash) values ('you@example.com','Owner','hash') returning id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Retention','retention-policy') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into memberships(org_id,user_id,role) values ($1,$2,'owner')`, orgID, userID); err != nil {
		t.Fatal(err)
	}
	data := &store.Store{Pool: pool}
	registry := sitesvc.NewRegistry(data, 0)
	t.Cleanup(registry.Close)
	service := sitesvc.New(data, registry)
	service.SetRetentionPolicy(defaultDays, forever)
	box, err := platformcrypto.NewSecretBox([]byte("0123456789abcdef0123456789abcdef"), "supermetry/totp/v1")
	if err != nil {
		t.Fatal(err)
	}
	principal := authz.Principal{UserID: userID, OrgID: orgID, Role: authz.RoleOwner}
	handler := platformhttp.New(platformhttp.Deps{
		Authenticator: actorAuthenticator{"owner": principal}, Sites: registry,
		Mutations: platformhttp.Services{Sites: service}, Store: data,
		Auth:            platformauth.New(data, &mail.Fake{}, box, "https://supermetry.example"),
		Signup:          "invite",
		RetentionPolicy: service.RetentionPolicy,
		SupportURL:      "https://help.example/contact",
	})
	return retentionEnv{handler: handler, service: service}
}

func (e retentionEnv) serve(request *http.Request) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	e.handler.ServeHTTP(response, request)
	return response
}

func TestCreateSiteUsesTheConfiguredDefaultRetention(t *testing.T) {
	e := newRetentionEnv(t, 90, true)
	response := e.serve(authenticatedRequest(http.MethodPost, "/api/private/sites", `{"name":"Default","hosts":["default.example.test"],"timezone":"UTC"}`))
	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	var site struct {
		RetentionDays        int    `json:"retention_days"`
		RevenueCurrencyState string `json:"revenue_currency_state"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &site); err != nil {
		t.Fatal(err)
	}
	if site.RetentionDays != 90 {
		t.Fatalf("retention_days = %d, want the configured default 90", site.RetentionDays)
	}
	if site.RevenueCurrencyState != "unconfigured" {
		t.Fatalf("revenue_currency_state = %q, want unconfigured when no currency is sent", site.RevenueCurrencyState)
	}
	explicit := e.serve(authenticatedRequest(http.MethodPost, "/api/private/sites", `{"name":"Explicit","hosts":["explicit.example.test"],"timezone":"UTC","retention_days":7,"revenue_currency":"EUR"}`))
	if explicit.Code != http.StatusCreated {
		t.Fatalf("explicit status=%d body=%s", explicit.Code, explicit.Body)
	}
	if err := json.Unmarshal(explicit.Body.Bytes(), &site); err != nil || site.RetentionDays != 7 {
		t.Fatalf("explicit retention_days = %d, %v", site.RetentionDays, err)
	}
}

func TestForeverRetentionIsRejectedWhenDisabled(t *testing.T) {
	e := newRetentionEnv(t, 90, false)
	response := e.serve(authenticatedRequest(http.MethodPost, "/api/private/sites", `{"name":"Forever","hosts":["forever.example.test"],"timezone":"UTC","retention_days":0,"revenue_currency":"USD"}`))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("create forever status=%d body=%s", response.Code, response.Body)
	}
	created := e.serve(authenticatedRequest(http.MethodPost, "/api/private/sites", `{"name":"Kept","hosts":["kept.example.test"],"timezone":"UTC","retention_days":30,"revenue_currency":"USD"}`))
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body)
	}
	var site struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &site); err != nil {
		t.Fatal(err)
	}
	patch := e.serve(authenticatedRequest(http.MethodPatch, fmt.Sprintf("/api/private/sites/%d", site.ID), `{"retention_days":0}`))
	if patch.Code != http.StatusBadRequest {
		t.Fatalf("patch forever status=%d body=%s", patch.Code, patch.Body)
	}
}

func TestAuthConfigReportsRetentionPolicyAndSupportURL(t *testing.T) {
	e := newRetentionEnv(t, 90, false)
	response := e.serve(httptest.NewRequest(http.MethodGet, "/api/private/auth/config", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	var body struct {
		DefaultRetentionDays *int    `json:"default_retention_days"`
		RetentionForever     *bool   `json:"retention_forever"`
		SupportURL           *string `json:"support_url"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.DefaultRetentionDays == nil || *body.DefaultRetentionDays != 90 || body.RetentionForever == nil || *body.RetentionForever {
		t.Fatalf("policy = %v / %v, want 90 / false", body.DefaultRetentionDays, body.RetentionForever)
	}
	if body.SupportURL == nil || *body.SupportURL != "https://help.example/contact" {
		t.Fatalf("support_url = %v", body.SupportURL)
	}
}

func TestAuthConfigDefaultsWithoutAPolicy(t *testing.T) {
	pool, _ := testpg.New(t)
	if err := db.Migrate(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	box, err := platformcrypto.NewSecretBox([]byte("0123456789abcdef0123456789abcdef"), "supermetry/totp/v1")
	if err != nil {
		t.Fatal(err)
	}
	handler := platformhttp.New(platformhttp.Deps{Auth: platformauth.New(&store.Store{Pool: pool}, &mail.Fake{}, box, "https://supermetry.example"), Signup: "invite"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/private/auth/config", nil))
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["default_retention_days"] != float64(30) || body["retention_forever"] != true {
		t.Fatalf("defaults = %v / %v", body["default_retention_days"], body["retention_forever"])
	}
	if _, present := body["support_url"]; present {
		t.Fatalf("support_url present without configuration: %v", body["support_url"])
	}
}
