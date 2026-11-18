package platformhttp_test

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"supermetry/internal/api"
	"supermetry/internal/apiroutes"
	"supermetry/internal/config"
	"supermetry/internal/ingest"
	"supermetry/internal/platform/authz"
	platformhttp "supermetry/internal/platform/http"
)

type anyPrivateAuth struct{}

func (anyPrivateAuth) Authenticate(*http.Request) (api.Principal, bool) {
	return api.Principal{OrgID: 1, AllSites: true}, true
}

// anonymousPrivateRoutes are the only /api/private mutations that run without a
// session and so without a CSRF token. Each is a pre-login, token-bearing or
// share-password flow; adding to this list needs a security review.
var anonymousPrivateRoutes = map[string]bool{
	"POST /api/private/setup":                       true,
	"POST /api/private/auth/login":                  true,
	"POST /api/private/auth/signup":                 true,
	"POST /api/private/auth/password-reset":         true,
	"POST /api/private/auth/password-reset/confirm": true,
	"POST /api/private/invitations/{token}/accept":  true,
	"POST /api/private/share/{slug}/open":           true,
	"POST /api/private/share/{slug}/query":          true,
}

// TestCookieMutationsRequireCSRF enumerates every registered mutating route
// under /api/private (the only tree that honours the session cookie) and
// asserts that a request carrying a session cookie but no X-CSRF-Token is
// rejected with 403 before reaching a handler.
func TestCookieMutationsRequireCSRF(t *testing.T) {
	repo := filepath.Join("..", "..", "..")
	routes, unknown, err := apiroutes.Collect(filepath.Join(repo, "internal/api"), filepath.Join(repo, "internal/platform/http"))
	if err != nil || len(unknown) != 0 {
		t.Fatalf("collect routes: %v unknown=%v", err, unknown)
	}
	platform := platformhttp.New(platformhttp.Deps{Authenticator: allowAll{}, Sites: siteRegistry{}, Mutations: noopMutations{}})
	router := api.NewRouter(api.Deps{Config: config.Config{}, RateLimiter: ingest.NewTokenBuckets(0, 30), PrivateAuth: anyPrivateAuth{}, Platform: platform})

	param := regexp.MustCompile(`\{[^}]*\}`)
	checked := 0
	for _, route := range routes {
		switch route.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		default:
			continue
		}
		if !strings.HasPrefix(route.Pattern, "/api/private/") {
			continue
		}
		key := route.Method + " " + route.Pattern
		path := strings.TrimSuffix(param.ReplaceAllString(route.Pattern, "1"), "$")
		req := httptest.NewRequest(route.Method, path, strings.NewReader("{}"))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "supermetry_session", Value: "x"})
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		csrfRejected := rec.Code == http.StatusForbidden && strings.Contains(strings.ToLower(rec.Body.String()), "csrf")
		if anonymousPrivateRoutes[key] {
			continue
		}
		checked++
		if !csrfRejected {
			t.Errorf("%s: status %d without CSRF token, want 403 CSRF rejection (body %.120s)", key, rec.Code, rec.Body.String())
		}
	}
	if checked < 50 {
		t.Fatalf("only %d mutating private routes checked; route collection looks broken", checked)
	}
	t.Logf("%d mutating cookie routes reject a missing CSRF token", checked)
}

type allowAll struct{}

func (allowAll) Authenticate(*http.Request) (authz.Principal, bool) {
	return authz.Principal{UserID: 1, OrgID: 1, Role: authz.RoleOwner}, true
}
