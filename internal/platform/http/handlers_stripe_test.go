package platformhttp_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"supermetry/internal/integrations"
	"supermetry/internal/integrations/stripe"
	"supermetry/internal/platform/authz"
	platformhttp "supermetry/internal/platform/http"
)

type fakeStripeService struct {
	calls  []string
	update stripe.Update
	err    error
}

func (f *fakeStripeService) Get(context.Context, authz.Principal, int32) (stripe.Status, error) {
	f.calls = append(f.calls, "get")
	return stripe.Status{Enabled: true, WebhookURL: "https://stats.example.test/api/public/v1/integrations/stripe/webhook/x"}, f.err
}

func (f *fakeStripeService) Enable(context.Context, authz.Principal, int32) (stripe.Status, error) {
	f.calls = append(f.calls, "enable")
	return stripe.Status{Enabled: true}, f.err
}

func (f *fakeStripeService) Configure(_ context.Context, _ authz.Principal, _ int32, update stripe.Update) (stripe.Status, error) {
	f.calls = append(f.calls, "configure")
	f.update = update
	return stripe.Status{Enabled: true, SecretConfigured: true}, f.err
}

func (f *fakeStripeService) Disconnect(context.Context, authz.Principal, int32) error {
	f.calls = append(f.calls, "disconnect")
	return f.err
}

func stripeHandler(service *fakeStripeService) http.Handler {
	var stripeService platformhttp.StripeService
	if service != nil {
		stripeService = service
	}
	return platformhttp.New(platformhttp.Deps{
		Authenticator: actorAuthenticator{
			"owner":  {UserID: 1, OrgID: 1, Role: authz.RoleOwner},
			"admin":  {UserID: 2, OrgID: 1, Role: authz.RoleAdmin},
			"editor": {UserID: 3, OrgID: 1, Role: authz.RoleEditor, SiteIDs: []int32{10}},
			"viewer": {UserID: 4, OrgID: 1, Role: authz.RoleViewer, SiteIDs: []int32{10}},
			"other":  {UserID: 5, OrgID: 2, Role: authz.RoleOwner},
		},
		Sites: siteRegistry{
			10: {ID: 10, OrgID: 1, PublicID: "siteaaaaaaaa", TZ: time.UTC},
			20: {ID: 20, OrgID: 2, PublicID: "sitebbbbbbbb", TZ: time.UTC},
		},
		Stripe: stripeService,
	})
}

func TestStripeRoutesEnforceAuthorization(t *testing.T) {
	const path = "/api/private/sites/10/integrations/stripe"
	const secret = `{"signing_secret":"whsec_abcdefghijklmnopqrstuvwxyz"}`
	tests := []struct {
		name   string
		actor  string
		method string
		path   string
		body   string
		status int
	}{
		{"signed out get", "", http.MethodGet, path, "", http.StatusUnauthorized},
		{"signed out enable", "", http.MethodPost, path, "", http.StatusUnauthorized},
		{"owner reads", "owner", http.MethodGet, path, "", http.StatusOK},
		{"editor reads", "editor", http.MethodGet, path, "", http.StatusOK},
		{"viewer cannot read", "viewer", http.MethodGet, path, "", http.StatusForbidden},
		{"other org cannot read", "other", http.MethodGet, path, "", http.StatusNotFound},
		{"admin enables", "admin", http.MethodPost, path, "", http.StatusOK},
		{"editor cannot enable", "editor", http.MethodPost, path, "", http.StatusForbidden},
		{"owner configures", "owner", http.MethodPatch, path, secret, http.StatusOK},
		{"editor cannot configure", "editor", http.MethodPatch, path, secret, http.StatusForbidden},
		{"unknown field rejected", "owner", http.MethodPatch, path, `{"signing_secret":"whsec_x","api_key":"sk_live"}`, http.StatusBadRequest},
		{"owner disconnects", "owner", http.MethodDelete, path, "", http.StatusNoContent},
		{"editor cannot disconnect", "editor", http.MethodDelete, path, "", http.StatusForbidden},
		{"wrong site", "owner", http.MethodDelete, "/api/private/sites/20/integrations/stripe", "", http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeStripeService{}
			request := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			if tt.actor != "" {
				request.Header.Set("X-Test-Actor", tt.actor)
			}
			request.Header.Set("Content-Type", "application/json")
			if tt.method != http.MethodGet {
				request.Header.Set("X-CSRF-Token", "token")
				request.AddCookie(&http.Cookie{Name: "supermetry_csrf", Value: "token"})
			}
			response := httptest.NewRecorder()
			stripeHandler(service).ServeHTTP(response, request)
			if response.Code != tt.status {
				t.Fatalf("status=%d body=%s, want %d", response.Code, response.Body.String(), tt.status)
			}
			if tt.status >= 400 && len(service.calls) != 0 {
				t.Fatalf("service reached on a rejected request: %v", service.calls)
			}
		})
	}
}

func TestStripeConfigurePassesUpdateAndMapsErrors(t *testing.T) {
	service := &fakeStripeService{}
	response := requestAs(t, stripeHandler(service), "owner", http.MethodPatch, "/api/private/sites/10/integrations/stripe",
		`{"signing_secret":"whsec_abcdefghijklmnopqrstuvwxyz","record_payment_intents":true}`)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"secret_configured":true`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if service.update.SigningSecret == nil || *service.update.SigningSecret != "whsec_abcdefghijklmnopqrstuvwxyz" ||
		service.update.RecordPaymentIntents == nil || !*service.update.RecordPaymentIntents {
		t.Fatalf("update = %+v", service.update)
	}
	if strings.Contains(response.Body.String(), "whsec_") {
		t.Fatal("response echoes the signing secret")
	}
	for err, status := range map[error]int{
		integrations.ErrInvalid: http.StatusBadRequest, integrations.ErrNotFound: http.StatusNotFound, integrations.ErrForbidden: http.StatusForbidden,
	} {
		response := requestAs(t, stripeHandler(&fakeStripeService{err: err}), "owner", http.MethodPost, "/api/private/sites/10/integrations/stripe", "")
		if response.Code != status {
			t.Fatalf("%v -> %d, want %d", err, response.Code, status)
		}
	}
	response = requestAs(t, stripeHandler(nil), "owner", http.MethodGet, "/api/private/sites/10/integrations/stripe", "")
	if response.Code == http.StatusOK {
		t.Fatalf("missing service answered %d", response.Code)
	}
}
