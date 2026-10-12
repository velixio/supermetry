package platformhttp_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"supermetry/internal/platform/authz"
	platformhttp "supermetry/internal/platform/http"
	"supermetry/internal/platform/sitesvc"
)

func TestSitesOverviewRequiresAuthenticationAndValidatesQuery(t *testing.T) {
	handler := platformhttp.New(platformhttp.Deps{
		Authenticator: actorAuthenticator{"member": {UserID: 41, Role: authz.RoleViewer}},
		Overview:      sitesvc.NewOverview(nil, nil),
	})

	anonymous := httptest.NewRequest(http.MethodGet, "/api/private/sites/overview", nil)
	anonymousResponse := httptest.NewRecorder()
	handler.ServeHTTP(anonymousResponse, anonymous)
	if anonymousResponse.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status=%d body=%s", anonymousResponse.Code, anonymousResponse.Body.String())
	}

	for _, path := range []string{
		"/api/private/sites/overview?period=year",
		"/api/private/sites/overview?limit=0",
		"/api/private/sites/overview?limit=201",
	} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("X-Test-Actor", "member")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
}

func TestSitesOverviewHasBoundedPerUserRateLimit(t *testing.T) {
	handler := platformhttp.New(platformhttp.Deps{
		Authenticator: actorAuthenticator{"member": {UserID: 42, Role: authz.RoleViewer}},
		Overview:      sitesvc.NewOverview(nil, nil),
	})
	for requestNumber := 1; requestNumber <= 121; requestNumber++ {
		request := httptest.NewRequest(http.MethodGet, "/api/private/sites/overview?period=invalid", nil)
		request.Header.Set("X-Test-Actor", "member")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if requestNumber <= 120 && response.Code != http.StatusBadRequest {
			t.Fatalf("request %d status=%d body=%s", requestNumber, response.Code, response.Body.String())
		}
		if requestNumber == 121 {
			if response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") == "" {
				t.Fatalf("rate limited status=%d retry-after=%q body=%s", response.Code, response.Header().Get("Retry-After"), response.Body.String())
			}
		}
	}
}
