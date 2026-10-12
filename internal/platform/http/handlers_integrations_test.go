package platformhttp_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"supermetry/internal/integrations"
	"supermetry/internal/platform/authz"
	platformhttp "supermetry/internal/platform/http"
)

type fakeIntegrationsService struct {
	calls    []string
	err      error
	callback int32
	started  integrations.StartInput
	topArgs  struct {
		dimension string
		from, to  time.Time
		limit     int
	}
}

func (f *fakeIntegrationsService) List(context.Context, authz.Principal, int32) ([]integrations.CatalogEntry, error) {
	f.calls = append(f.calls, "list")
	return []integrations.CatalogEntry{{Provider: "search-console", Name: "Google Search Console", Available: true}}, f.err
}

func (f *fakeIntegrationsService) Start(_ context.Context, _ authz.Principal, _ int32, in integrations.StartInput) (string, error) {
	f.calls = append(f.calls, "start")
	f.started = in
	return "https://accounts.example.test/auth?state=s", f.err
}

func (f *fakeIntegrationsService) Callback(context.Context, authz.Principal, string, string, string, string) (int32, error) {
	f.calls = append(f.calls, "callback")
	return f.callback, f.err
}

func (f *fakeIntegrationsService) Disconnect(context.Context, authz.Principal, int32, string) error {
	f.calls = append(f.calls, "disconnect")
	return f.err
}

func (f *fakeIntegrationsService) Backfill(context.Context, authz.Principal, int32, string, int) error {
	f.calls = append(f.calls, "backfill")
	return f.err
}

func (f *fakeIntegrationsService) Top(_ context.Context, _ authz.Principal, _ int32, dimension string, from, to time.Time, limit int) (integrations.TopResult, error) {
	f.calls = append(f.calls, "top")
	f.topArgs.dimension, f.topArgs.from, f.topArgs.to, f.topArgs.limit = dimension, from, to, limit
	return integrations.TopResult{Dimension: dimension, Rows: []integrations.TopRow{}}, f.err
}

func integrationsHandler(service *fakeIntegrationsService) http.Handler {
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
		Integrations: service,
	})
}

func TestIntegrationRoutesEnforceAuthorization(t *testing.T) {
	const (
		connect = `{"property":"sc-domain:example.test"}`
		top     = "/api/private/sites/10/search-console/top?dimension=query&from=2026-10-01&to=2026-10-10"
	)
	tests := []struct {
		name   string
		actor  string
		method string
		path   string
		body   string
		status int
	}{
		{"signed out list", "", http.MethodGet, "/api/private/sites/10/integrations", "", http.StatusUnauthorized},
		{"signed out connect", "", http.MethodPost, "/api/private/sites/10/integrations/search-console/connect", connect, http.StatusUnauthorized},
		{"signed out disconnect", "", http.MethodDelete, "/api/private/sites/10/integrations/search-console", "", http.StatusUnauthorized},
		{"signed out callback", "", http.MethodGet, "/api/private/integrations/search-console/callback?state=s&code=c", "", http.StatusUnauthorized},
		{"signed out top", "", http.MethodGet, top, "", http.StatusUnauthorized},
		{"owner lists", "owner", http.MethodGet, "/api/private/sites/10/integrations", "", http.StatusOK},
		{"editor lists", "editor", http.MethodGet, "/api/private/sites/10/integrations", "", http.StatusOK},
		{"viewer cannot list", "viewer", http.MethodGet, "/api/private/sites/10/integrations", "", http.StatusForbidden},
		{"wrong site list", "owner", http.MethodGet, "/api/private/sites/20/integrations", "", http.StatusNotFound},
		{"admin connects", "admin", http.MethodPost, "/api/private/sites/10/integrations/search-console/connect", connect, http.StatusOK},
		{"editor cannot connect", "editor", http.MethodPost, "/api/private/sites/10/integrations/search-console/connect", connect, http.StatusForbidden},
		{"viewer cannot connect", "viewer", http.MethodPost, "/api/private/sites/10/integrations/search-console/connect", connect, http.StatusForbidden},
		{"wrong site connect", "owner", http.MethodPost, "/api/private/sites/20/integrations/search-console/connect", connect, http.StatusNotFound},
		{"other org cannot connect", "other", http.MethodPost, "/api/private/sites/10/integrations/search-console/connect", connect, http.StatusNotFound},
		{"owner disconnects", "owner", http.MethodDelete, "/api/private/sites/10/integrations/search-console", "", http.StatusNoContent},
		{"editor cannot disconnect", "editor", http.MethodDelete, "/api/private/sites/10/integrations/search-console", "", http.StatusForbidden},
		{"wrong site disconnect", "owner", http.MethodDelete, "/api/private/sites/20/integrations/search-console", "", http.StatusNotFound},
		{"owner backfills", "owner", http.MethodPost, "/api/private/sites/10/integrations/search-console/backfill", `{"days":30}`, http.StatusAccepted},
		{"editor cannot backfill", "editor", http.MethodPost, "/api/private/sites/10/integrations/search-console/backfill", `{"days":30}`, http.StatusForbidden},
		{"viewer reads top", "viewer", http.MethodGet, top, "", http.StatusOK},
		{"wrong site top", "owner", http.MethodGet, "/api/private/sites/20/search-console/top?dimension=query&from=2026-10-01&to=2026-10-10", "", http.StatusNotFound},
		{"other org top", "other", http.MethodGet, top, "", http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeIntegrationsService{}
			request := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			if tt.actor != "" {
				request.Header.Set("X-Test-Actor", tt.actor)
			}
			if tt.body != "" {
				request.Header.Set("Content-Type", "application/json")
			}
			if tt.method != http.MethodGet {
				request.Header.Set("X-CSRF-Token", "token")
				request.AddCookie(&http.Cookie{Name: "supermetry_csrf", Value: "token"})
			}
			response := httptest.NewRecorder()
			integrationsHandler(service).ServeHTTP(response, request)
			if response.Code != tt.status {
				t.Fatalf("status=%d body=%s, want %d", response.Code, response.Body.String(), tt.status)
			}
			if tt.status >= 400 && len(service.calls) != 0 {
				t.Fatalf("service reached on a rejected request: %v", service.calls)
			}
		})
	}
}

func TestIntegrationConnectPassesInputAndRejectsUnknownFields(t *testing.T) {
	service := &fakeIntegrationsService{}
	handler := integrationsHandler(service)
	response := requestAs(t, handler, "owner", http.MethodPost, "/api/private/sites/10/integrations/search-console/connect", `{"property":"sc-domain:example.test","backfill_days":90}`)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"authorization_url"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if service.started.Provider != "search-console" || service.started.Property != "sc-domain:example.test" || service.started.BackfillDays != 90 {
		t.Fatalf("input = %+v", service.started)
	}
	service.calls = nil
	response = requestAs(t, handler, "owner", http.MethodPost, "/api/private/sites/10/integrations/search-console/connect", `{"property":"x","access_token":"leak"}`)
	if response.Code != http.StatusBadRequest || len(service.calls) != 0 {
		t.Fatalf("unknown field status=%d calls=%v", response.Code, service.calls)
	}
}

func TestIntegrationErrorsMapToProblems(t *testing.T) {
	tests := []struct {
		err    error
		status int
	}{
		{integrations.ErrInvalid, http.StatusBadRequest},
		{integrations.ErrNotFound, http.StatusNotFound},
		{integrations.ErrForbidden, http.StatusForbidden},
		{integrations.ErrUnavailable, http.StatusConflict},
		{integrations.ErrRateLimited, http.StatusTooManyRequests},
	}
	for _, tt := range tests {
		service := &fakeIntegrationsService{err: tt.err}
		response := requestAs(t, integrationsHandler(service), "owner", http.MethodPost, "/api/private/sites/10/integrations/search-console/connect", `{"property":"sc-domain:example.test"}`)
		if response.Code != tt.status {
			t.Fatalf("%v -> %d, want %d", tt.err, response.Code, tt.status)
		}
		if tt.err == integrations.ErrRateLimited && response.Header().Get("Retry-After") == "" {
			t.Fatal("missing Retry-After")
		}
	}
}

func TestIntegrationCallbackRedirectsToSiteSettings(t *testing.T) {
	for _, tt := range []struct {
		name     string
		err      error
		site     int32
		status   int
		location string
	}{
		{"connected", nil, 10, http.StatusSeeOther, "/sites/10/settings/integrations?integration=search-console&status=connected"},
		{"denied", integrations.ErrDenied, 10, http.StatusSeeOther, "/sites/10/settings/integrations?integration=search-console&status=denied"},
		{"exchange failed", http.ErrAbortHandler, 10, http.StatusSeeOther, "/sites/10/settings/integrations?integration=search-console&status=error"},
		{"bad state", integrations.ErrInvalidState, 0, http.StatusBadRequest, ""},
		{"forbidden", integrations.ErrForbidden, 10, http.StatusForbidden, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeIntegrationsService{err: tt.err, callback: tt.site}
			request := httptest.NewRequest(http.MethodGet, "/api/private/integrations/search-console/callback?code=c&state=s", nil)
			request.Header.Set("X-Test-Actor", "owner")
			response := httptest.NewRecorder()
			integrationsHandler(service).ServeHTTP(response, request)
			if response.Code != tt.status || response.Header().Get("Location") != tt.location {
				t.Fatalf("status=%d location=%q body=%s", response.Code, response.Header().Get("Location"), response.Body.String())
			}
		})
	}
}

func TestSearchConsoleTopValidatesQuery(t *testing.T) {
	service := &fakeIntegrationsService{}
	handler := integrationsHandler(service)
	for _, path := range []string{
		"/api/private/sites/10/search-console/top?dimension=query&from=bad&to=2026-10-10",
		"/api/private/sites/10/search-console/top?dimension=query&from=2026-10-01",
		"/api/private/sites/10/search-console/top?dimension=query&from=2026-10-01&to=2026-10-10&limit=abc",
	} {
		if response := requestAs(t, handler, "viewer", http.MethodGet, path, ""); response.Code != http.StatusBadRequest {
			t.Fatalf("%s -> %d", path, response.Code)
		}
	}
	if len(service.calls) != 0 {
		t.Fatalf("service reached on invalid input: %v", service.calls)
	}
	response := requestAs(t, handler, "viewer", http.MethodGet, "/api/private/sites/10/search-console/top?dimension=page&from=2026-10-01&to=2026-10-10&limit=20", "")
	if response.Code != http.StatusOK || service.topArgs.dimension != "page" || service.topArgs.limit != 20 || service.topArgs.from.Format(time.DateOnly) != "2026-10-01" {
		t.Fatalf("status=%d args=%+v", response.Code, service.topArgs)
	}
}
