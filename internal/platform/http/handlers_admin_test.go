package platformhttp_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"supermetry/internal/db"
	"supermetry/internal/platform/admin"
	"supermetry/internal/platform/authz"
	platformhttp "supermetry/internal/platform/http"
	"supermetry/internal/platform/store"
	"supermetry/internal/testpg"
)

func TestAdminRouteAuthorizationMatrix(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	ids := map[string]int64{}
	for _, actor := range []struct {
		name  string
		email string
		admin bool
	}{{"instance-admin", "instance-admin@example.test", true}, {"owner", "owner@example.test", false}, {"member", "member@example.test", false}} {
		var userID int64
		if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash,is_admin) values ($1,$2,'hash',$3) returning id`, actor.email, actor.name, actor.admin).Scan(&userID); err != nil {
			t.Fatal(err)
		}
		ids[actor.name] = userID
	}
	var orgID int64
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Acme','acme') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into memberships(org_id,user_id,role) values ($1,$2,'owner'),($1,$3,'viewer'),($1,$4,'viewer')`, orgID, ids["owner"], ids["member"], ids["instance-admin"]); err != nil {
		t.Fatal(err)
	}
	var siteID int32
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class) values ($1,'adminsite001','Site','UTC',30) returning id`, orgID).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	authenticator := actorAuthenticator{
		"instance-admin": {UserID: ids["instance-admin"], SiteIDs: []int32{}},
		"owner":          {UserID: ids["owner"], OrgID: orgID, Role: authz.RoleOwner},
		"member":         {UserID: ids["member"], OrgID: orgID, Role: authz.RoleViewer},
	}
	handler := platformhttp.New(platformhttp.Deps{Authenticator: authenticator, Store: &store.Store{Pool: pool}, Admin: admin.New(&store.Store{Pool: pool}), Signup: "invite", MaxSitesPerOrg: 10})
	routes := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/private/admin/users?search=member&limit=10", ""},
		{http.MethodPatch, fmt.Sprintf("/api/private/admin/users/%d", ids["member"]), `{"password_reset_required":false}`},
		{http.MethodGet, "/api/private/admin/organizations?limit=10", ""},
		{http.MethodGet, fmt.Sprintf("/api/private/admin/organizations/%d", orgID), ""},
		{http.MethodGet, "/api/private/admin/sites?limit=10", ""},
		{http.MethodGet, "/api/private/admin/settings", ""},
		{http.MethodGet, "/api/private/admin/update", ""},
		{http.MethodPost, "/api/private/admin/update/apply", `{}`},
		{http.MethodPatch, "/api/private/admin/settings", `{"signup_mode":"invite","replay_visible":false}`},
		{http.MethodGet, "/api/private/admin/security/2fa-policy", ""},
		{http.MethodPut, "/api/private/admin/security/2fa-policy", `{"scope":"off","grace_days":7}`},
		{http.MethodPut, fmt.Sprintf("/api/private/admin/security/2fa-exemptions/%d", ids["member"]), `{"reason":"temporary recovery"}`},
		{http.MethodDelete, fmt.Sprintf("/api/private/admin/security/2fa-exemptions/%d", ids["member"]), ""},
		{http.MethodGet, "/api/private/admin/audit?limit=10", ""},
	}
	for _, route := range routes {
		for _, actor := range []string{"anonymous", "member", "owner", "instance-admin"} {
			t.Run(route.method+" "+route.path+" as "+actor, func(t *testing.T) {
				request := httptest.NewRequest(route.method, route.path, strings.NewReader(route.body))
				request.Header.Set("Content-Type", "application/json")
				if actor != "anonymous" {
					request.Header.Set("X-Test-Actor", actor)
				}
				if route.method != http.MethodGet {
					request.Header.Set("X-CSRF-Token", "token")
					request.AddCookie(&http.Cookie{Name: "supermetry_csrf", Value: "token"})
				}
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				switch actor {
				case "anonymous":
					if response.Code != http.StatusUnauthorized {
						t.Fatalf("status=%d body=%s, want 401", response.Code, response.Body.String())
					}
				case "instance-admin":
					if route.path == "/api/private/admin/update/apply" {
						if response.Code != http.StatusConflict {
							t.Fatalf("status=%d body=%s, want 409 for unmanaged deployment", response.Code, response.Body.String())
						}
					} else if response.Code >= http.StatusBadRequest {
						t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
					}
				default:
					if response.Code != http.StatusForbidden {
						t.Fatalf("status=%d body=%s, want 403", response.Code, response.Body.String())
					}
				}
			})
		}
	}
	_ = siteID
}

func TestAdminRoutesRateLimitAndValidateInputs(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var userID int64
	if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash,is_admin) values ('limited-admin@example.test','Admin','hash',true) returning id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	data := &store.Store{Pool: pool}
	handler := platformhttp.New(platformhttp.Deps{Authenticator: actorAuthenticator{"admin": {UserID: userID, SiteIDs: []int32{}}}, Store: data, Admin: admin.New(data), Signup: "invite"})

	invalid := httptest.NewRequest(http.MethodGet, "/api/private/admin/users?limit=101", nil)
	invalid.Header.Set("X-Test-Actor", "admin")
	invalidResponse := httptest.NewRecorder()
	handler.ServeHTTP(invalidResponse, invalid)
	if invalidResponse.Code != http.StatusBadRequest {
		t.Fatalf("invalid limit status=%d body=%s", invalidResponse.Code, invalidResponse.Body.String())
	}

	for attempt := 1; attempt <= 120; attempt++ {
		request := httptest.NewRequest(http.MethodGet, "/api/private/admin/settings", nil)
		request.Header.Set("X-Test-Actor", "admin")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if attempt < 120 && response.Code != http.StatusOK {
			t.Fatalf("attempt %d status=%d body=%s", attempt, response.Code, response.Body.String())
		}
		if attempt == 120 && response.Code != http.StatusTooManyRequests {
			t.Fatalf("attempt %d status=%d body=%s, want 429", attempt, response.Code, response.Body.String())
		}
	}
}
