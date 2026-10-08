package api_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"panel/internal/api"
)

// TestEveryPlatformRouteIsForwarded guards the explicit forward list in
// registerPlatformRoutes: a route registered by the platform handlers but
// missing from that list silently answers 404 (it happened to the snippet
// and current-IP endpoints).
func TestEveryPlatformRouteIsForwarded(t *testing.T) {
	files, err := filepath.Glob("../platform/http/*.go")
	if err != nil {
		t.Fatal(err)
	}
	pattern := regexp.MustCompile(`mux\.HandleFunc\("(GET|POST|PUT|PATCH|DELETE) (/api/[^"]+)"`)
	param := regexp.MustCompile(`\{[^}]+\}`)
	// Served by the analytics router itself; the platform copy is shadowed.
	servedHere := map[string]bool{"GET /api/public/v1/stats/summary": true}
	var routes [][2]string
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		source, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range pattern.FindAllStringSubmatch(string(source), -1) {
			if servedHere[match[1]+" "+match[2]] {
				continue
			}
			routes = append(routes, [2]string{match[1], param.ReplaceAllString(match[2], "1")})
		}
	}
	if len(routes) < 50 {
		t.Fatalf("found %d platform routes, want the full set (did the registration style change?)", len(routes))
	}

	platform := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(218) })
	router := api.NewRouter(api.Deps{
		Platform:    platform,
		PublicAuth:  principalAuth{principal: api.Principal{OrgID: 7, Scopes: []string{"stats:read"}}},
		PrivateAuth: principalAuth{principal: api.Principal{OrgID: 7}},
	})
	for _, route := range routes {
		request := httptest.NewRequest(route[0], route[1], nil)
		if strings.HasPrefix(route[1], "/api/private/") && route[0] != http.MethodGet {
			request.Header.Set("X-CSRF-Token", "token")
			request.AddCookie(&http.Cookie{Name: "panel_csrf", Value: "token"})
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != 218 {
			t.Errorf("%s %s answered %d, not forwarded to the platform handler", route[0], route[1], response.Code)
		}
	}
}
