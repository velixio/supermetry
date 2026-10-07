package platformhttp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"panel/internal/db"
	"panel/internal/platform/authz"
	platformhttp "panel/internal/platform/http"
	"panel/internal/platform/sitesvc"
	"panel/internal/platform/store"
	"panel/internal/testpg"
)

func TestSiteExclusionAPI(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var userID, orgID int64
	if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash) values ('owner@example.test','Owner','hash') returning id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Example','example') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into memberships(org_id,user_id,role) values ($1,$2,'owner')`, orgID, userID); err != nil {
		t.Fatal(err)
	}
	data := &store.Store{Pool: pool}
	registry := sitesvc.NewRegistry(data, 0)
	t.Cleanup(registry.Close)
	sites := sitesvc.New(data, registry)
	principal := authz.Principal{UserID: userID, OrgID: orgID, Role: authz.RoleOwner}
	site, err := sites.Create(ctx, principal, "Example", []string{"example.test"}, "UTC", 30)
	if err != nil {
		t.Fatal(err)
	}
	handler := platformhttp.New(platformhttp.Deps{
		Authenticator: actorAuthenticator{"owner": principal}, Sites: registry,
		Mutations: platformhttp.Services{Sites: sites}, Store: data,
	})
	fresh := authenticatedRequest(http.MethodGet, fmt.Sprintf("/api/private/sites/%d", site.ID), "")
	freshResponse := httptest.NewRecorder()
	handler.ServeHTTP(freshResponse, fresh)
	if freshResponse.Code != http.StatusOK || !strings.Contains(freshResponse.Body.String(), `"exclude_ips":[]`) || !strings.Contains(freshResponse.Body.String(), `"exclude_paths":[]`) {
		t.Fatalf("fresh GET status=%d body=%s, want empty arrays", freshResponse.Code, freshResponse.Body.String())
	}

	patch := authenticatedRequest(http.MethodPatch, fmt.Sprintf("/api/private/sites/%d", site.ID),
		`{"exclude_ips":["203.0.113.7","2001:db8::/32"],"exclude_paths":["/admin/*","/preview/**"]}`)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, patch)
	if response.Code != http.StatusOK {
		t.Fatalf("PATCH status=%d body=%s", response.Code, response.Body.String())
	}
	var patched struct {
		ExcludeIPs   []string `json:"exclude_ips"`
		ExcludePaths []string `json:"exclude_paths"`
	}
	if err := json.NewDecoder(response.Body).Decode(&patched); err != nil {
		t.Fatal(err)
	}
	if strings.Join(patched.ExcludeIPs, ",") != "203.0.113.7/32,2001:db8::/32" || strings.Join(patched.ExcludePaths, ",") != "/admin/*,/preview/**" {
		t.Fatalf("PATCH exclusions = %+v", patched)
	}

	get := authenticatedRequest(http.MethodGet, fmt.Sprintf("/api/private/sites/%d", site.ID), "")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, get)
	if response.Code != http.StatusOK {
		t.Fatalf("GET status=%d body=%s", response.Code, response.Body.String())
	}
	var loaded struct {
		ExcludeIPs   []string `json:"exclude_ips"`
		ExcludePaths []string `json:"exclude_paths"`
	}
	if err := json.NewDecoder(response.Body).Decode(&loaded); err != nil {
		t.Fatal(err)
	}
	if strings.Join(loaded.ExcludeIPs, ",") != strings.Join(patched.ExcludeIPs, ",") || strings.Join(loaded.ExcludePaths, ",") != strings.Join(patched.ExcludePaths, ",") {
		t.Fatalf("GET exclusions = %+v, want %+v", loaded, patched)
	}

	tests := []struct {
		name  string
		body  string
		entry string
	}{
		{name: "bad IP", body: `{"exclude_ips":["203.0.113.0/99"]}`, entry: "203.0.113.0/99"},
		{name: "bad path", body: `{"exclude_paths":["admin/**"]}`, entry: "admin/**"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := authenticatedRequest(http.MethodPatch, fmt.Sprintf("/api/private/sites/%d", site.ID), test.body)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest || response.Header().Get("Content-Type") != "application/problem+json" {
				t.Fatalf("status=%d content-type=%q body=%s", response.Code, response.Header().Get("Content-Type"), response.Body.String())
			}
			var problem struct {
				Detail string `json:"detail"`
			}
			if err := json.NewDecoder(response.Body).Decode(&problem); err != nil || !strings.Contains(problem.Detail, test.entry) {
				t.Fatalf("problem=%+v error=%v, want entry %q", problem, err, test.entry)
			}
		})
	}
}

func TestCurrentIPUsesResolvedClientAddress(t *testing.T) {
	principal := authz.Principal{UserID: 1, OrgID: 1, Role: authz.RoleOwner}
	handler := platformhttp.New(platformhttp.Deps{
		Authenticator:  actorAuthenticator{"owner": principal},
		TrustedProxies: []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")},
	})
	tests := []struct {
		name      string
		remote    string
		forwarded string
		want      string
	}{
		{name: "untrusted peer ignores forwarded header", remote: "198.51.100.7:1234", forwarded: "203.0.113.9", want: "198.51.100.7"},
		{name: "trusted proxy walks from nearest hop", remote: "192.0.2.10:1234", forwarded: "203.0.113.8, 10.0.0.2, 192.0.2.11", want: "203.0.113.8"},
		{name: "mapped IPv4 is unmapped", remote: "[::ffff:198.51.100.7]:1234", want: "198.51.100.7"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := authenticatedRequest(http.MethodGet, "/api/private/me/ip", "")
			request.RemoteAddr = test.remote
			request.Header.Set("X-Forwarded-For", test.forwarded)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			var body struct {
				IP string `json:"ip"`
			}
			if err := json.NewDecoder(response.Body).Decode(&body); err != nil || body.IP != test.want {
				t.Fatalf("body=%+v error=%v, want IP %q", body, err, test.want)
			}
		})
	}
}

func authenticatedRequest(method, path, body string) *http.Request {
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.Header.Set("X-Test-Actor", "owner")
	if method != http.MethodGet {
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-CSRF-Token", "token")
		request.AddCookie(&http.Cookie{Name: "panel_csrf", Value: "token"})
	}
	return request
}
