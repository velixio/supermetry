package platformhttp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"panel/internal/api"
	"panel/internal/config"
	"panel/internal/db"
	"panel/internal/ingest"
	platformauth "panel/internal/platform/auth"
	"panel/internal/platform/authz"
	platformcrypto "panel/internal/platform/crypto"
	platformhttp "panel/internal/platform/http"
	"panel/internal/platform/mail"
	"panel/internal/platform/orgs"
	"panel/internal/platform/sitesvc"
	"panel/internal/platform/store"
	coresites "panel/internal/sites"
	"panel/internal/testpg"
)

type actorAuthenticator map[string]authz.Principal

func (a actorAuthenticator) Authenticate(r *http.Request) (authz.Principal, bool) {
	p, ok := a[r.Header.Get("X-Test-Actor")]
	return p, ok
}

type siteRegistry map[int32]coresites.Site

func (r siteRegistry) ByPublicID(id string) (coresites.Site, bool) {
	for _, site := range r {
		if site.PublicID == id {
			return site, true
		}
	}
	return coresites.Site{}, false
}

func (r siteRegistry) Site(id int32) (coresites.Site, bool) { site, ok := r[id]; return site, ok }
func (r siteRegistry) All() []coresites.Site {
	result := make([]coresites.Site, 0, len(r))
	for _, site := range r {
		result = append(result, site)
	}
	return result
}

type noopMutations struct{}

type routeQuery struct{}

func (routeQuery) Query(context.Context, api.QueryRequest) (api.QueryResponse, error) {
	return api.QueryResponse{Columns: []string{}, Rows: []map[string]any{}, Totals: map[string]float64{"visitors": 7}, Meta: api.QueryMeta{SourcesUsed: []string{}}}, nil
}

func (noopMutations) CreateSite(context.Context, authz.Principal, platformhttp.CreateSiteRequest) (coresites.Site, error) {
	return coresites.Site{ID: 10, OrgID: 1, PublicID: "abcdefgh2345", TZ: time.UTC}, nil
}

func (noopMutations) UpdateSite(context.Context, authz.Principal, int32, sitesvc.SitePatch) (coresites.Site, error) {
	return coresites.Site{ID: 10, OrgID: 1, PublicID: "abcdefgh2345", TZ: time.UTC}, nil
}

func (noopMutations) DeleteSite(context.Context, authz.Principal, int32) error { return nil }

func (noopMutations) CreateAPIKey(context.Context, authz.Principal, platformhttp.CreateAPIKeyRequest) (platformhttp.CreatedAPIKey, error) {
	return platformhttp.CreatedAPIKey{ID: 1, Key: "shown-once"}, nil
}

func (noopMutations) DeleteOrg(context.Context, authz.Principal, int64) error { return nil }

func TestRouteAuthorizationMatrix(t *testing.T) {
	const (
		org1  = int64(1)
		org2  = int64(2)
		siteA = int32(10)
		siteB = int32(11)
		siteC = int32(20)
	)
	authenticator := actorAuthenticator{
		"owner":  {UserID: 1, OrgID: org1, Role: authz.RoleOwner},
		"admin":  {UserID: 2, OrgID: org1, Role: authz.RoleAdmin},
		"editor": {UserID: 3, OrgID: org1, Role: authz.RoleEditor},
		"viewer": {UserID: 4, OrgID: org1, Role: authz.RoleViewer, SiteIDs: []int32{siteA}},
		"key":    {KeyID: 5, OrgID: org1, Role: authz.RoleKey, Scopes: []string{"stats:read"}},
		"share":  {ShareID: 6, OrgID: org1, Role: authz.RoleShare, SiteIDs: []int32{siteA}},
	}
	handler := platformhttp.New(platformhttp.Deps{
		Authenticator: authenticator,
		Sites: siteRegistry{
			siteA: {ID: siteA, OrgID: org1, PublicID: "siteaaaaaaaa", TZ: time.UTC},
			siteB: {ID: siteB, OrgID: org1, PublicID: "sitebbbbbbbb", TZ: time.UTC},
			siteC: {ID: siteC, OrgID: org2, PublicID: "sitecccccccc", TZ: time.UTC},
		},
		Mutations: noopMutations{},
		Query:     routeQuery{},
		Alerts:    &fakeAlertsService{},
	})

	routes := []struct {
		method string
		path   string
		body   string
		allow  map[string]bool
	}{
		{http.MethodGet, fmt.Sprintf("/api/private/sites/%d", siteA), "", allowed("owner", "admin", "editor", "viewer")},
		{http.MethodGet, fmt.Sprintf("/api/private/sites/%d", siteB), "", allowed("owner", "admin", "editor")},
		{http.MethodGet, fmt.Sprintf("/api/private/sites/%d", siteC), "", allowed()},
		{http.MethodPatch, fmt.Sprintf("/api/private/sites/%d", siteA), `{"name":"Updated"}`, allowed("owner", "admin", "editor")},
		{http.MethodPost, fmt.Sprintf("/api/private/orgs/%d/api-keys", org1), `{"name":"CI","scopes":["stats:read"]}`, allowed("owner", "admin")},
		{http.MethodGet, fmt.Sprintf("/api/private/orgs/%d/audit", org1), "", allowed("owner", "admin")},
		{http.MethodDelete, fmt.Sprintf("/api/private/orgs/%d", org1), "", allowed("owner")},
		{http.MethodGet, fmt.Sprintf("/api/public/v1/stats/summary?site_id=%d&period=7d", siteA), "", allowed("key")},
		{http.MethodGet, fmt.Sprintf("/api/public/v1/stats/summary?site_id=%d&period=7d", siteC), "", allowed()},
		{http.MethodGet, fmt.Sprintf("/api/private/share/demo/stats/summary?site_id=%d&period=7d", siteA), "", allowed("share")},
		{http.MethodGet, fmt.Sprintf("/api/private/orgs/%d/channels", org1), "", allowed("owner", "admin", "editor")},
		{http.MethodPost, fmt.Sprintf("/api/private/orgs/%d/channels", org1), `{"kind":"email","name":"Ops","config":{"recipients":["ops@example.test"]}}`, allowed("owner", "admin")},
		{http.MethodGet, fmt.Sprintf("/api/private/sites/%d/alerts", siteA), "", allowed("owner", "admin", "editor")},
		{http.MethodPost, fmt.Sprintf("/api/private/sites/%d/alerts", siteA), `{"kind":"traffic_spike","threshold":2,"channel_ids":[1]}`, allowed("owner", "admin", "editor")},
	}

	actors := []string{"owner", "admin", "editor", "viewer", "key", "share", "anonymous"}
	for _, route := range routes {
		for _, actor := range actors {
			name := route.method + " " + route.path + " as " + actor
			t.Run(name, func(t *testing.T) {
				request := httptest.NewRequest(route.method, route.path, strings.NewReader(route.body))
				request.Header.Set("Content-Type", "application/json")
				if actor != "anonymous" {
					request.Header.Set("X-Test-Actor", actor)
				}
				if strings.HasPrefix(route.path, "/api/private/") && route.method != http.MethodGet {
					request.Header.Set("X-CSRF-Token", "token")
					request.AddCookie(&http.Cookie{Name: "panel_csrf", Value: "token"})
				}
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)

				want := route.allow[actor]
				if want && response.Code >= http.StatusBadRequest {
					t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
				}
				if !want && response.Code != http.StatusNotFound && response.Code != http.StatusUnauthorized && response.Code != http.StatusForbidden {
					t.Fatalf("status=%d body=%s; want denial", response.Code, response.Body.String())
				}
				if !want && strings.Contains(route.path, fmt.Sprint(siteC)) && actor != "anonymous" && response.Code != http.StatusNotFound {
					t.Fatalf("cross-org status=%d; want 404", response.Code)
				}
			})
		}
	}
}

func TestAnonymousAuthRouteMatrix(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	data := &store.Store{Pool: pool}
	box, err := platformcrypto.NewSecretBox([]byte("0123456789abcdef0123456789abcdef"), "panel/totp/v1")
	if err != nil {
		t.Fatal(err)
	}
	authService := platformauth.New(data, &mail.Fake{}, box, "https://panel.example")

	tests := []struct {
		name   string
		signup string
		method string
		path   string
		body   string
		want   int
	}{
		{name: "config is anonymous", signup: "open", method: http.MethodGet, path: "/api/private/auth/config", want: http.StatusOK},
		{name: "signup is anonymous when open", signup: "open", method: http.MethodPost, path: "/api/private/auth/signup", body: `{"email":"owner@example.test","name":"Owner","password":"pw-123456789","org_name":"Acme"}`, want: http.StatusCreated},
		{name: "signup is hidden when invite only", signup: "invite", method: http.MethodPost, path: "/api/private/auth/signup", body: `{"email":"other@example.test","name":"Other","password":"pw-123456789","org_name":"Other"}`, want: http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			platform := platformhttp.New(platformhttp.Deps{Auth: authService, Signup: tt.signup})
			handler := api.NewRouter(api.Deps{Platform: platform})
			request := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			request.Header.Set("Content-Type", "application/json")
			request.RemoteAddr = "192.0.2.10:12345"
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != tt.want {
				t.Fatalf("status=%d body=%s, want %d", response.Code, response.Body.String(), tt.want)
			}
		})
	}
}

func TestSignupCreatesOwnerOrganizationAuditAndSessionCookies(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	data := &store.Store{Pool: pool}
	box, err := platformcrypto.NewSecretBox([]byte("0123456789abcdef0123456789abcdef"), "panel/totp/v1")
	if err != nil {
		t.Fatal(err)
	}
	authService := platformauth.New(data, &mail.Fake{}, box, "https://panel.example")
	handler := platformhttp.New(platformhttp.Deps{Auth: authService, Signup: "open"})

	request := httptest.NewRequest(http.MethodPost, "/api/private/auth/signup", strings.NewReader(`{"email":"owner@example.test","name":"Owner","password":"pw-123456789","org_name":"Acme"}`))
	request.Header.Set("Content-Type", "application/json")
	request.RemoteAddr = "192.0.2.10:12345"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}

	var orgs, owners, audits, sessions int
	if err := pool.QueryRow(ctx, `select count(*) from orgs`).Scan(&orgs); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `select count(*) from memberships where role='owner'`).Scan(&owners); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `select count(*) from audit_log`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `select count(*) from user_sessions`).Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if orgs != 1 || owners != 1 || audits != 1 || sessions != 1 {
		t.Fatalf("orgs=%d owners=%d audits=%d sessions=%d", orgs, owners, audits, sessions)
	}

	cookies := response.Result().Cookies()
	var sessionCookie, csrfCookie *http.Cookie
	for _, cookie := range cookies {
		switch cookie.Name {
		case platformauth.CookieName:
			sessionCookie = cookie
		case "panel_csrf":
			csrfCookie = cookie
		}
	}
	if sessionCookie == nil || sessionCookie.Value == "" || !sessionCookie.HttpOnly {
		t.Fatalf("session cookie=%+v", sessionCookie)
	}
	if csrfCookie == nil || csrfCookie.Value == "" || csrfCookie.HttpOnly {
		t.Fatalf("csrf cookie=%+v", csrfCookie)
	}

	duplicate := httptest.NewRequest(http.MethodPost, "/api/private/auth/signup", strings.NewReader(`{"email":"OWNER@example.test","name":"Another","password":"pw-123456789","org_name":"Another"}`))
	duplicate.Header.Set("Content-Type", "application/json")
	duplicate.RemoteAddr = "192.0.2.10:12346"
	duplicateResponse := httptest.NewRecorder()
	handler.ServeHTTP(duplicateResponse, duplicate)
	invalid := httptest.NewRequest(http.MethodPost, "/api/private/auth/signup", strings.NewReader(`{"email":"","name":"Another","password":"pw-123456789","org_name":"Another"}`))
	invalid.Header.Set("Content-Type", "application/json")
	invalid.RemoteAddr = "192.0.2.10:12347"
	invalidResponse := httptest.NewRecorder()
	handler.ServeHTTP(invalidResponse, invalid)
	if duplicateResponse.Code != http.StatusBadRequest || invalidResponse.Code != http.StatusBadRequest || duplicateResponse.Body.String() != invalidResponse.Body.String() {
		t.Fatalf("duplicate status/body=%d/%q invalid=%d/%q", duplicateResponse.Code, duplicateResponse.Body.String(), invalidResponse.Code, invalidResponse.Body.String())
	}

	configRequest := httptest.NewRequest(http.MethodGet, "/api/private/auth/config", nil)
	configResponse := httptest.NewRecorder()
	handler.ServeHTTP(configResponse, configRequest)
	if configResponse.Code != http.StatusOK || configResponse.Body.String() != "{\"setup_required\":false,\"signup\":\"open\"}\n" {
		t.Fatalf("config status=%d body=%s", configResponse.Code, configResponse.Body.String())
	}
}

func TestMeReturnsUserMembershipsAndSites(t *testing.T) {
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
	site, err := sites.Create(ctx, principal, "Example site", []string{"example.test"}, "Asia/Kathmandu", 30)
	if err != nil {
		t.Fatal(err)
	}

	handler := platformhttp.New(platformhttp.Deps{
		Authenticator: actorAuthenticator{"owner": principal},
		Store:         data,
		Orgs:          orgs.New(data),
	})
	request := httptest.NewRequest(http.MethodGet, "/api/private/me", nil)
	request.Header.Set("X-Test-Actor", "owner")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		User struct {
			ID    int64  `json:"id"`
			Email string `json:"email"`
			Name  string `json:"name"`
		} `json:"user"`
		Memberships []struct {
			Org struct {
				ID   int64  `json:"id"`
				Name string `json:"name"`
				Slug string `json:"slug"`
			} `json:"org"`
			Role  authz.Role `json:"role"`
			Sites []struct {
				ID       int32  `json:"id"`
				PublicID string `json:"public_id"`
				Name     string `json:"name"`
				Timezone string `json:"timezone"`
			} `json:"sites"`
		} `json:"memberships"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.User.ID != userID || body.User.Email != "owner@example.test" || body.User.Name != "Owner" {
		t.Fatalf("user=%+v", body.User)
	}
	if len(body.Memberships) != 1 {
		t.Fatalf("memberships=%+v", body.Memberships)
	}
	membership := body.Memberships[0]
	if membership.Org.ID != orgID || membership.Org.Slug != "example" || membership.Role != authz.RoleOwner {
		t.Fatalf("membership=%+v", membership)
	}
	if len(membership.Sites) != 1 || membership.Sites[0].ID != site.ID || membership.Sites[0].PublicID != site.PublicID || membership.Sites[0].Timezone != "Asia/Kathmandu" {
		t.Fatalf("sites=%+v", membership.Sites)
	}
}

func TestMutationsWriteAudit(t *testing.T) {
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
		Authenticator: actorAuthenticator{"owner": principal},
		Sites:         registry,
		Mutations:     platformhttp.Services{Sites: sites},
	})

	request := httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/private/sites/%d", site.ID), bytes.NewBufferString(`{"name":"Renamed"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Test-Actor", "owner")
	request.Header.Set("X-CSRF-Token", "token")
	request.AddCookie(&http.Cookie{Name: "panel_csrf", Value: "token"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}

	var count int
	var actorID, targetID int64
	if err := pool.QueryRow(ctx, `select count(*), max(actor_user_id), max(target_id) from audit_log where action='site.updated'`).Scan(&count, &actorID, &targetID); err != nil {
		t.Fatal(err)
	}
	if count != 1 || actorID != userID || targetID != int64(site.ID) {
		t.Fatalf("audit count=%d actor=%d target=%d", count, actorID, targetID)
	}
}

func TestPrivateRequiresCSRF(t *testing.T) {
	principal := authz.Principal{UserID: 1, OrgID: 1, Role: authz.RoleOwner}
	handler := platformhttp.New(platformhttp.Deps{
		Authenticator: actorAuthenticator{"owner": principal},
		Sites:         siteRegistry{},
		Mutations:     noopMutations{},
	})
	request := httptest.NewRequest(http.MethodPost, "/api/private/sites", strings.NewReader(`{"name":"Example","hosts":["example.test"],"timezone":"UTC","retention_days":30}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Test-Actor", "owner")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusForbidden || response.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("status=%d content-type=%q body=%s", response.Code, response.Header().Get("Content-Type"), response.Body.String())
	}
	var problem struct {
		Status int `json:"status"`
	}
	if err := json.NewDecoder(response.Body).Decode(&problem); err != nil || problem.Status != http.StatusForbidden {
		t.Fatalf("problem=%+v error=%v", problem, err)
	}
}

func TestRoutesMountOnAnalyticsRouter(t *testing.T) {
	principal := authz.Principal{UserID: 1, OrgID: 1, Role: authz.RoleOwner}
	platform := platformhttp.New(platformhttp.Deps{
		Authenticator: actorAuthenticator{"owner": principal},
		Sites: siteRegistry{
			10: {ID: 10, OrgID: 1, PublicID: "siteaaaaaaaa", TZ: time.UTC},
		},
		Mutations: noopMutations{},
		Alerts:    &fakeAlertsService{},
	})
	router := api.NewRouter(api.Deps{
		Config:      config.Config{},
		RateLimiter: ingest.NewTokenBuckets(0, 30),
		Platform:    platform,
	})
	request := httptest.NewRequest(http.MethodGet, "/api/private/sites/10", nil)
	request.Header.Set("X-Test-Actor", "owner")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("X-Request-ID") == "" {
		t.Fatalf("status=%d request-id=%q body=%s", response.Code, response.Header().Get("X-Request-ID"), response.Body.String())
	}
	alertRequest := httptest.NewRequest(http.MethodGet, "/api/private/sites/10/alerts", nil)
	alertRequest.Header.Set("X-Test-Actor", "owner")
	alertResponse := httptest.NewRecorder()
	router.ServeHTTP(alertResponse, alertRequest)
	if alertResponse.Code != http.StatusOK || alertResponse.Header().Get("X-Request-ID") == "" {
		t.Fatalf("alert status=%d request-id=%q body=%s", alertResponse.Code, alertResponse.Header().Get("X-Request-ID"), alertResponse.Body.String())
	}
}

func TestShareStatsUsesQueryService(t *testing.T) {
	principal := authz.Principal{OrgID: 1, ShareID: 9, Role: authz.RoleShare, SiteIDs: []int32{10}}
	handler := platformhttp.New(platformhttp.Deps{
		Authenticator: actorAuthenticator{"share": principal},
		Sites: siteRegistry{
			10: {ID: 10, OrgID: 1, PublicID: "siteaaaaaaaa", TZ: time.UTC},
		},
		Query: routeQuery{},
	})
	request := httptest.NewRequest(http.MethodGet, "/api/private/share/demo/stats/summary?period=7d", nil)
	request.Header.Set("X-Test-Actor", "share")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"visitors":7`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestGoalAndFunnelMutationsWriteAudit(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var userID, orgID int64
	if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash) values ('routes@example.test','Routes','hash') returning id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Routes','routes') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into memberships(org_id,user_id,role) values ($1,$2,'owner')`, orgID, userID); err != nil {
		t.Fatal(err)
	}
	data := &store.Store{Pool: pool}
	registry := sitesvc.NewRegistry(data, 0)
	t.Cleanup(registry.Close)
	siteService := sitesvc.New(data, registry)
	principal := authz.Principal{UserID: userID, OrgID: orgID, Role: authz.RoleOwner}
	site, err := siteService.Create(ctx, principal, "Routes", []string{"routes.example.test"}, "UTC", 30)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `delete from audit_log`); err != nil {
		t.Fatal(err)
	}
	handler := platformhttp.New(platformhttp.Deps{
		Authenticator: actorAuthenticator{"owner": principal},
		Sites:         registry,
		Store:         data,
	})

	goalID := mutationID(t, handler, http.MethodPost, fmt.Sprintf("/api/private/sites/%d/goals", site.ID), `{"kind":"page","match":"exact","value":"/thanks","name":"Signup"}`)
	requestOK(t, handler, http.MethodPatch, fmt.Sprintf("/api/private/goals/%d", goalID), `{"kind":"page","match":"prefix","value":"/done","name":"Signup"}`, http.StatusOK)
	requestOK(t, handler, http.MethodDelete, fmt.Sprintf("/api/private/goals/%d", goalID), "", http.StatusNoContent)

	funnelID := mutationID(t, handler, http.MethodPost, fmt.Sprintf("/api/private/sites/%d/funnels", site.ID), `{"name":"Signup","steps":[{"type":"page","match":"exact","value":"/"},{"type":"page","match":"exact","value":"/thanks"}]}`)
	requestOK(t, handler, http.MethodPatch, fmt.Sprintf("/api/private/funnels/%d", funnelID), `{"name":"Signup v2","steps":[{"type":"page","match":"exact","value":"/"},{"type":"page","match":"prefix","value":"/done"}]}`, http.StatusOK)
	requestOK(t, handler, http.MethodDelete, fmt.Sprintf("/api/private/funnels/%d", funnelID), "", http.StatusNoContent)

	var count int
	if err := pool.QueryRow(ctx, `select count(*) from audit_log`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 6 {
		t.Fatalf("audit count=%d, want 6", count)
	}
}

func mutationID(t *testing.T, handler http.Handler, method, path, body string) int64 {
	t.Helper()
	response := requestOK(t, handler, method, path, body, http.StatusCreated)
	var result struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil || result.ID == 0 {
		t.Fatalf("decode mutation response: id=%d error=%v", result.ID, err)
	}
	return result.ID
}

func requestOK(t *testing.T, handler http.Handler, method, path, body string, want int) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Test-Actor", "owner")
	request.Header.Set("X-CSRF-Token", "token")
	request.AddCookie(&http.Cookie{Name: "panel_csrf", Value: "token"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != want {
		t.Fatalf("%s %s: status=%d body=%s", method, path, response.Code, response.Body.String())
	}
	return response
}

func allowed(actors ...string) map[string]bool {
	result := make(map[string]bool, len(actors))
	for _, actor := range actors {
		result[actor] = true
	}
	return result
}
