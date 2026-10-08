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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"panel/internal/db"
	"panel/internal/platform/authz"
	platformhttp "panel/internal/platform/http"
	"panel/internal/platform/sitesvc"
	"panel/internal/platform/store"
	coresites "panel/internal/sites"
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
		BaseURL: "https://panel.example", TrackerPath: "/js/script.js",
	})
	fresh := authenticatedRequest(http.MethodGet, fmt.Sprintf("/api/private/sites/%d", site.ID), "")
	freshResponse := httptest.NewRecorder()
	handler.ServeHTTP(freshResponse, fresh)
	if freshResponse.Code != http.StatusOK || !strings.Contains(freshResponse.Body.String(), `"exclude_ips":[]`) ||
		!strings.Contains(freshResponse.Body.String(), `"exclude_paths":[]`) ||
		!strings.Contains(freshResponse.Body.String(), `"exclude_countries":[]`) ||
		!strings.Contains(freshResponse.Body.String(), `"tracker_url":"https://panel.example/js/`+site.PublicID+`.js"`) ||
		!strings.Contains(freshResponse.Body.String(), `"tracker":{"outbound":true`) {
		t.Fatalf("fresh GET status=%d body=%s, want empty arrays", freshResponse.Code, freshResponse.Body.String())
	}

	patch := authenticatedRequest(http.MethodPatch, fmt.Sprintf("/api/private/sites/%d", site.ID),
		`{"exclude_ips":["203.0.113.7","2001:db8::/32"],"exclude_paths":["/admin/*","/preview/**"],"exclude_countries":["ru"," RU "],"tracker":{"hash":true}}`)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, patch)
	if response.Code != http.StatusOK {
		t.Fatalf("PATCH status=%d body=%s", response.Code, response.Body.String())
	}
	var patched struct {
		ExcludeIPs       []string                  `json:"exclude_ips"`
		ExcludePaths     []string                  `json:"exclude_paths"`
		ExcludeCountries []string                  `json:"exclude_countries"`
		Tracker          coresites.TrackerSettings `json:"tracker"`
		TrackerURL       string                    `json:"tracker_url"`
	}
	if err := json.NewDecoder(response.Body).Decode(&patched); err != nil {
		t.Fatal(err)
	}
	if strings.Join(patched.ExcludeIPs, ",") != "203.0.113.7/32,2001:db8::/32" ||
		strings.Join(patched.ExcludePaths, ",") != "/admin/*,/preview/**" ||
		strings.Join(patched.ExcludeCountries, ",") != "RU" || !patched.Tracker.Hash ||
		patched.TrackerURL != "https://panel.example/js/"+site.PublicID+".js" {
		t.Fatalf("PATCH exclusions = %+v", patched)
	}

	get := authenticatedRequest(http.MethodGet, fmt.Sprintf("/api/private/sites/%d", site.ID), "")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, get)
	if response.Code != http.StatusOK {
		t.Fatalf("GET status=%d body=%s", response.Code, response.Body.String())
	}
	var loaded struct {
		ExcludeIPs       []string                  `json:"exclude_ips"`
		ExcludePaths     []string                  `json:"exclude_paths"`
		ExcludeCountries []string                  `json:"exclude_countries"`
		Tracker          coresites.TrackerSettings `json:"tracker"`
	}
	if err := json.NewDecoder(response.Body).Decode(&loaded); err != nil {
		t.Fatal(err)
	}
	if strings.Join(loaded.ExcludeIPs, ",") != strings.Join(patched.ExcludeIPs, ",") ||
		strings.Join(loaded.ExcludePaths, ",") != strings.Join(patched.ExcludePaths, ",") ||
		strings.Join(loaded.ExcludeCountries, ",") != "RU" || !loaded.Tracker.Hash {
		t.Fatalf("GET exclusions = %+v, want %+v", loaded, patched)
	}

	tests := []struct {
		name  string
		body  string
		entry string
	}{
		{name: "bad IP", body: `{"exclude_ips":["203.0.113.0/99"]}`, entry: "203.0.113.0/99"},
		{name: "bad path", body: `{"exclude_paths":["admin/**"]}`, entry: "admin/**"},
		{name: "bad country", body: `{"exclude_countries":["USA"]}`, entry: "USA"},
		{name: "unknown tracker field", body: `{"tracker":{"unknown":true}}`, entry: "tracker"},
		{name: "non-boolean tracker field", body: `{"tracker":{"hash":"yes"}}`, entry: "tracker"},
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
	var storedTracker, storedCountries string
	if err := pool.QueryRow(ctx, `select settings->'tracker', settings->'exclude_countries' from sites where id=$1`, site.ID).Scan(&storedTracker, &storedCountries); err != nil {
		t.Fatal(err)
	}
	if storedTracker != `{"hash": true}` || storedCountries != `["RU"]` {
		t.Fatalf("invalid PATCH changed settings: tracker=%s countries=%s", storedTracker, storedCountries)
	}
}

func TestProfilesSettingIsOwnerAdminOnlyAndAudited(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var userID, orgID int64
	if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash) values ('profiles-owner@example.test','Owner','hash') returning id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Profiles Settings','profiles-settings') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	data := &store.Store{Pool: pool}
	registry := sitesvc.NewRegistry(data, 0)
	t.Cleanup(registry.Close)
	sites := sitesvc.New(data, registry)
	owner := authz.Principal{UserID: userID, OrgID: orgID, Role: authz.RoleOwner}
	site, err := sites.Create(ctx, owner, "Example", []string{"example.test"}, "UTC", 30)
	if err != nil {
		t.Fatal(err)
	}
	handler := platformhttp.New(platformhttp.Deps{
		Authenticator: actorAuthenticator{
			"owner":  owner,
			"admin":  {UserID: userID, OrgID: orgID, Role: authz.RoleAdmin},
			"editor": {UserID: userID, OrgID: orgID, Role: authz.RoleEditor},
		},
		Sites: registry, Mutations: platformhttp.Services{Sites: sites}, Store: data,
	})
	patch := func(actor string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/private/sites/%d", site.ID), strings.NewReader(`{"profiles_enabled":true}`))
		request.Header.Set("X-Test-Actor", actor)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-CSRF-Token", "token")
		request.AddCookie(&http.Cookie{Name: "panel_csrf", Value: "token"})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	if response := patch("editor"); response.Code != http.StatusForbidden {
		t.Fatalf("editor PATCH status=%d body=%s", response.Code, response.Body.String())
	}
	response := patch("admin")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"profiles_enabled":true`) {
		t.Fatalf("admin PATCH status=%d body=%s", response.Code, response.Body.String())
	}
	loaded, ok := registry.Site(site.ID)
	if !ok || !loaded.ProfilesEnabled {
		t.Fatalf("registry site=%+v found=%t", loaded, ok)
	}
	var audits int
	if err := pool.QueryRow(ctx, `select count(*) from audit_log where action='site.updated' and target_id=$1 and metadata->>'profiles_enabled'='true'`, site.ID).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits != 1 {
		t.Fatalf("profile setting audit rows=%d, want 1", audits)
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

func TestRefetchSiteIconAuthorizationAndRateLimit(t *testing.T) {
	const siteID = int32(10)
	owner := authz.Principal{UserID: 1, OrgID: 1, Role: authz.RoleOwner}
	admin := authz.Principal{UserID: 2, OrgID: 1, Role: authz.RoleAdmin}
	editor := authz.Principal{UserID: 3, OrgID: 1, Role: authz.RoleEditor}
	mutations := &iconMutations{}
	handler := platformhttp.New(platformhttp.Deps{
		Authenticator: actorAuthenticator{"owner": owner, "admin": admin, "editor": editor},
		Sites: siteRegistry{siteID: {
			ID: siteID, OrgID: 1, PublicID: "abcdefgh2345", TZ: time.UTC,
			AllowedHosts: []string{"example.test"},
		}},
		Mutations: mutations,
	})

	request := func(actor string) *httptest.ResponseRecorder {
		req := authenticatedRequest(http.MethodPost, fmt.Sprintf("/api/private/sites/%d/icon/refetch", siteID), "")
		req.Header.Set("X-Test-Actor", actor)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	if response := request("editor"); response.Code != http.StatusForbidden {
		t.Fatalf("editor status=%d body=%s", response.Code, response.Body.String())
	}
	if calls := mutations.calls.Load(); calls != 0 {
		t.Fatalf("refresh calls after editor request=%d, want 0", calls)
	}
	response := request("owner")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"icon_url":"https://example.test/icon.svg"`) {
		t.Fatalf("owner status=%d body=%s", response.Code, response.Body.String())
	}
	response = request("admin")
	if response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") == "" {
		t.Fatalf("second status=%d retry-after=%q body=%s", response.Code, response.Header().Get("Retry-After"), response.Body.String())
	}
	if calls := mutations.calls.Load(); calls != 1 {
		t.Fatalf("refresh calls=%d, want 1", calls)
	}
}

func TestCreateSiteReturnsBeforeIconDiscoveryFinishes(t *testing.T) {
	principal := authz.Principal{UserID: 1, OrgID: 1, Role: authz.RoleOwner}
	started := make(chan struct{})
	release := make(chan struct{})
	mutations := &blockingIconMutations{started: started, release: release}
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	handler := platformhttp.New(platformhttp.Deps{
		Authenticator: actorAuthenticator{"owner": principal},
		Mutations:     mutations,
		BaseURL:       "https://panel.example",
		TrackerPath:   "/js/script.js",
	})
	request := authenticatedRequest(http.MethodPost, "/api/private/sites", `{"name":"Example","hosts":["example.test"],"timezone":"UTC","retention_days":30}`)
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(response, request)
		close(done)
	}()

	select {
	case <-done:
		if response.Code != http.StatusCreated {
			t.Fatalf("create status=%d body=%s", response.Code, response.Body.String())
		}
		if body := response.Body.String(); !strings.Contains(body, `"exclude_countries":[]`) ||
			!strings.Contains(body, `"tracker":{"outbound":true`) ||
			!strings.Contains(body, `"tracker_url":"https://panel.example/js/abcdefgh2345.js"`) {
			t.Fatalf("create body=%s, want tracker contract", body)
		}
	case <-time.After(time.Second):
		t.Fatal("create blocked on icon discovery")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("background icon discovery did not start")
	}
}

func TestCreateSiteLimitsConcurrentIconDiscovery(t *testing.T) {
	principal := authz.Principal{UserID: 1, OrgID: 1, Role: authz.RoleOwner}
	release := make(chan struct{})
	mutations := &concurrentIconMutations{started: make(chan struct{}, 10), release: release}
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	handler := platformhttp.New(platformhttp.Deps{
		Authenticator: actorAuthenticator{"owner": principal},
		Mutations:     mutations,
	})

	var requests sync.WaitGroup
	for index := 0; index < 10; index++ {
		requests.Add(1)
		go func() {
			defer requests.Done()
			request := authenticatedRequest(http.MethodPost, "/api/private/sites", `{"name":"Example","hosts":["example.test"],"timezone":"UTC","retention_days":30}`)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusCreated {
				t.Errorf("create status=%d body=%s", response.Code, response.Body.String())
			}
		}()
	}
	requests.Wait()
	for index := 0; index < 4; index++ {
		select {
		case <-mutations.started:
		case <-time.After(time.Second):
			t.Fatalf("started discoveries=%d, want 4", index)
		}
	}
	select {
	case <-mutations.started:
		t.Fatal("more than 4 icon discoveries started")
	case <-time.After(50 * time.Millisecond):
	}
	if calls := mutations.calls.Load(); calls != 4 {
		t.Fatalf("icon discovery calls=%d, want 4", calls)
	}
	if maximum := mutations.maximum.Load(); maximum > 4 {
		t.Fatalf("maximum concurrent icon discoveries=%d, want at most 4", maximum)
	}
	close(release)
}

type iconMutations struct {
	noopSiteMutations
	calls atomic.Int32
}

func (m *iconMutations) RefreshSiteIcon(context.Context, authz.Principal, int32) (coresites.Site, error) {
	m.calls.Add(1)
	return coresites.Site{
		ID: 10, OrgID: 1, PublicID: "abcdefgh2345", TZ: time.UTC,
		AllowedHosts: []string{"example.test"}, IconURL: "https://example.test/icon.svg",
	}, nil
}

type blockingIconMutations struct {
	noopSiteMutations
	started chan struct{}
	release chan struct{}
}

type concurrentIconMutations struct {
	noopSiteMutations
	created atomic.Int32
	calls   atomic.Int32
	active  atomic.Int32
	maximum atomic.Int32
	started chan struct{}
	release chan struct{}
}

func (m *concurrentIconMutations) CreateSite(context.Context, authz.Principal, platformhttp.CreateSiteRequest) (coresites.Site, error) {
	id := m.created.Add(1)
	return coresites.Site{ID: id, OrgID: 1, PublicID: "abcdefgh2345", TZ: time.UTC, AllowedHosts: []string{"example.test"}}, nil
}

func (m *concurrentIconMutations) RefreshSiteIcon(ctx context.Context, _ authz.Principal, _ int32) (coresites.Site, error) {
	m.calls.Add(1)
	active := m.active.Add(1)
	for maximum := m.maximum.Load(); active > maximum && !m.maximum.CompareAndSwap(maximum, active); maximum = m.maximum.Load() {
	}
	m.started <- struct{}{}
	defer m.active.Add(-1)
	select {
	case <-m.release:
		return coresites.Site{}, nil
	case <-ctx.Done():
		return coresites.Site{}, ctx.Err()
	}
}

func (m *blockingIconMutations) CreateSite(context.Context, authz.Principal, platformhttp.CreateSiteRequest) (coresites.Site, error) {
	return coresites.Site{ID: 10, OrgID: 1, PublicID: "abcdefgh2345", TZ: time.UTC, AllowedHosts: []string{"example.test"}}, nil
}

func (m *blockingIconMutations) RefreshSiteIcon(ctx context.Context, _ authz.Principal, _ int32) (coresites.Site, error) {
	close(m.started)
	select {
	case <-m.release:
		return coresites.Site{}, nil
	case <-ctx.Done():
		return coresites.Site{}, ctx.Err()
	}
}

type noopSiteMutations struct{}

func (noopSiteMutations) CreateSite(context.Context, authz.Principal, platformhttp.CreateSiteRequest) (coresites.Site, error) {
	return coresites.Site{}, nil
}

func (noopSiteMutations) UpdateSite(context.Context, authz.Principal, int32, sitesvc.SitePatch) (coresites.Site, error) {
	return coresites.Site{}, nil
}

func (noopSiteMutations) DeleteSite(context.Context, authz.Principal, int32) error { return nil }

func (noopSiteMutations) CreateAPIKey(context.Context, authz.Principal, platformhttp.CreateAPIKeyRequest) (platformhttp.CreatedAPIKey, error) {
	return platformhttp.CreatedAPIKey{}, nil
}

func (noopSiteMutations) DeleteOrg(context.Context, authz.Principal, int64) error { return nil }

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
