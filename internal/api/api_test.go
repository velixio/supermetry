package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"panel/internal/sites"
	"strings"
	"testing"

	"panel/internal/api"
	"panel/internal/config"
	"panel/internal/ingest"
)

type queryStub struct{}

func (queryStub) Query(_ context.Context, req api.QueryRequest) (api.QueryResponse, error) {
	if req.DateRange != nil && req.DateRange.From.Time.After(req.DateRange.To.Time) {
		return api.QueryResponse{}, errors.New("date_range.from must not be after date_range.to")
	}
	return api.QueryResponse{
		Columns: []string{"page", "visitors"},
		Rows: []map[string]any{
			{"page": "/plain", "visitors": float64(4)},
			{"page": "/with,comma", "visitors": float64(2)},
		},
		Totals: map[string]float64{"visitors": 6},
		Meta:   api.QueryMeta{SourcesUsed: []string{"rollup"}},
	}, nil
}

func (queryStub) Funnel(context.Context, api.FunnelRequest) (api.FunnelResponse, error) {
	return api.FunnelResponse{}, nil
}

func (queryStub) Journey(context.Context, api.JourneyRequest) (api.JourneyResponse, error) {
	return api.JourneyResponse{}, nil
}

func (queryStub) Retention(context.Context, api.RetentionRequest) (api.RetentionResponse, error) {
	return api.RetentionResponse{}, nil
}

type principalAuth struct {
	principal api.Principal
}

func (a principalAuth) Authenticate(*http.Request) (api.Principal, bool) {
	return a.principal, true
}

func TestAPIConventions(t *testing.T) {
	srv := httptest.NewServer(newTestRouter())
	defer srv.Close()

	t.Run("public API requires bearer authentication", func(t *testing.T) {
		response := get(t, srv.URL+"/api/public/v1/stats/summary?site_id=1&period=30d", "")
		defer response.Body.Close()
		assertProblem(t, response, http.StatusUnauthorized)
	})

	t.Run("MCP requires bearer authentication", func(t *testing.T) {
		response := post(t, srv.URL+"/api/public/v1/mcp", "", `{"jsonrpc":"2.0","id":1,"method":"ping"}`)
		defer response.Body.Close()
		assertProblem(t, response, http.StatusUnauthorized)
	})

	t.Run("private API requires a session", func(t *testing.T) {
		response := get(t, srv.URL+"/api/private/stats/summary?site_id=1&period=30d", "")
		defer response.Body.Close()
		assertProblem(t, response, http.StatusUnauthorized)
	})

	t.Run("authenticated response has rate limit headers", func(t *testing.T) {
		response := get(t, srv.URL+"/api/public/v1/stats/summary?site_id=1&period=30d", "Bearer test-key")
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK || response.Header.Get("RateLimit-Limit") == "" || response.Header.Get("RateLimit-Remaining") == "" || response.Header.Get("RateLimit-Reset") == "" {
			t.Fatalf("status=%d headers=%v", response.StatusCode, response.Header)
		}
	})

	t.Run("bad query returns a detailed problem", func(t *testing.T) {
		response := post(t, srv.URL+"/api/public/v1/query", "Bearer test-key", `{"site_id":1,"date_range":{"from":"2026-10-05","to":"2026-10-01"},"metrics":["visitors"]}`)
		defer response.Body.Close()
		problem := assertProblem(t, response, http.StatusBadRequest)
		if problem.Detail == nil || *problem.Detail == "" {
			t.Fatalf("missing detail: %+v", problem)
		}
		if response.Header.Get("X-Request-ID") == "" {
			t.Fatal("missing request ID")
		}
	})

	t.Run("CSV follows the response column order and quotes fields", func(t *testing.T) {
		response := getAccept(t, srv.URL+"/api/public/v1/stats/breakdown?site_id=1&period=30d&dimension=page", "Bearer test-key", "text/csv")
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/csv") {
			t.Fatalf("status=%d content-type=%q", response.StatusCode, response.Header.Get("Content-Type"))
		}
		if got, want := string(body), "page,visitors\r\n/plain,4\r\n\"/with,comma\",2\r\n"; got != want {
			t.Fatalf("CSV:\n%s\nwant:\n%s", got, want)
		}
	})

	t.Run("published OpenAPI document has only public paths", func(t *testing.T) {
		response := get(t, srv.URL+"/api/public/openapi.json", "")
		defer response.Body.Close()
		var document struct {
			OpenAPI string                    `json:"openapi"`
			Paths   map[string]map[string]any `json:"paths"`
		}
		if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&document) != nil || document.OpenAPI == "" {
			t.Fatalf("status=%d document=%+v", response.StatusCode, document)
		}
		if _, ok := document.Paths["/api/public/v1/query"]; !ok {
			t.Fatal("public query path missing")
		}
		if _, ok := document.Paths["/api/public/v1/sites/{site_id}/segments"]; !ok {
			t.Fatal("public segments path missing")
		}
		if _, ok := document.Paths["/api/public/v1/sites/{site_id}/annotations"]; !ok {
			t.Fatal("public annotations path missing")
		}
		if _, ok := document.Paths["/api/public/v1/sites/{site_id}/profiles"]; !ok {
			t.Fatal("public profiles path missing")
		}
		if _, ok := document.Paths["/api/public/v1/profiles/{profile_id}"]; !ok {
			t.Fatal("public profile detail path missing")
		}
		if _, ok := document.Paths["/api/public/v1/sites/{site_id}/goals"]["post"]; !ok {
			t.Fatal("public goal creation operation missing")
		}
		if _, ok := document.Paths["/api/public/v1/profiles/{profile_id}"]["post"]; ok {
			t.Fatal("profile detail path unexpectedly publishes POST")
		}
		privateAccountPaths := map[string]bool{
			"/api/private/me":                        true,
			"/api/private/me/email":                  true,
			"/api/private/me/password":               true,
			"/api/private/auth/email-change/confirm": true,
		}
		for path := range document.Paths {
			if strings.HasPrefix(path, "/api/v1/") || (strings.HasPrefix(path, "/api/private/") && !privateAccountPaths[path]) {
				t.Fatalf("non-public path published: %s", path)
			}
		}
	})

	t.Run("exhausted rate limit returns retry advice", func(t *testing.T) {
		var response *http.Response
		for range 40 {
			if response != nil {
				response.Body.Close()
			}
			response = get(t, srv.URL+"/api/public/v1/stats/summary?site_id=1&period=30d", "Bearer test-key")
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusTooManyRequests || response.Header.Get("Retry-After") == "" {
			t.Fatalf("status=%d headers=%v", response.StatusCode, response.Header)
		}
	})
}

type orgSites map[int32]sites.Site

func (o orgSites) ByPublicID(string) (sites.Site, bool) { return sites.Site{}, false }
func (o orgSites) Site(id int32) (sites.Site, bool)     { s, ok := o[id]; return s, ok }
func (o orgSites) All() []sites.Site                    { return nil }

func TestAuthorizationScopeSiteAndCSRF(t *testing.T) {
	// Sites 1 and 2 belong to org 7; principals are scoped to org 7.
	registry := orgSites{1: {ID: 1, OrgID: 7}, 2: {ID: 2, OrgID: 7}}
	private := principalAuth{principal: api.Principal{OrgID: 7, Scopes: []string{"stats:read"}}}
	router := api.NewRouter(api.Deps{
		Config:      config.Config{EventPath: "/api/public/v1/events", TrackerPath: "/js/script.js"},
		Query:       queryStub{},
		PublicAuth:  principalAuth{principal: api.Principal{OrgID: 7, Scopes: []string{"stats:read"}, SiteIDs: []int32{2}}},
		PrivateAuth: private,
		Sites:       registry,
		RateLimiter: ingest.NewTokenBuckets(0, 30),
	})

	t.Run("public principal cannot read another site", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodGet, "/api/public/v1/stats/summary?site_id=1&period=30d", nil)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		// Foreign or unknown sites answer 404 so their existence is not revealed.
		assertRecorderProblem(t, response, http.StatusNotFound)
	})

	t.Run("public principal needs stats scope", func(t *testing.T) {
		noScopeRouter := api.NewRouter(api.Deps{
			Config:      config.Config{EventPath: "/api/public/v1/events", TrackerPath: "/js/script.js"},
			Query:       queryStub{},
			PublicAuth:  principalAuth{principal: api.Principal{Scopes: []string{"sites:write"}}},
			RateLimiter: ingest.NewTokenBuckets(0, 30),
		})
		request := httptest.NewRequest(http.MethodGet, "/api/public/v1/stats/summary?site_id=1&period=30d", nil)
		response := httptest.NewRecorder()
		noScopeRouter.ServeHTTP(response, request)
		assertRecorderProblem(t, response, http.StatusForbidden)
	})

	t.Run("private write requires CSRF token", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodPost, "/api/private/query", strings.NewReader(`{"site_id":1,"period":"30d","metrics":["visitors"]}`))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		assertRecorderProblem(t, response, http.StatusForbidden)
	})

	t.Run("private write accepts CSRF token", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodPost, "/api/private/query", strings.NewReader(`{"site_id":1,"period":"30d","metrics":["visitors"]}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-CSRF-Token", "present")
		request.AddCookie(&http.Cookie{Name: "panel_csrf", Value: "present"})
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	})
}

func TestPlatformSegmentRoutesAreForwarded(t *testing.T) {
	platform := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(218)
	})
	router := api.NewRouter(api.Deps{
		Platform:    platform,
		PublicAuth:  principalAuth{principal: api.Principal{OrgID: 7, Scopes: []string{"stats:read"}}},
		PrivateAuth: principalAuth{principal: api.Principal{OrgID: 7}},
	})
	tests := []struct {
		name   string
		method string
		path   string
	}{
		{name: "public list", method: http.MethodGet, path: "/api/public/v1/sites/1/segments"},
		{name: "public segment create", method: http.MethodPost, path: "/api/public/v1/sites/1/segments"},
		{name: "public segment update", method: http.MethodPatch, path: "/api/public/v1/segments/1"},
		{name: "public segment delete", method: http.MethodDelete, path: "/api/public/v1/segments/1"},
		{name: "public annotation create", method: http.MethodPost, path: "/api/public/v1/sites/1/annotations"},
		{name: "public annotation update", method: http.MethodPatch, path: "/api/public/v1/annotations/1"},
		{name: "public annotation delete", method: http.MethodDelete, path: "/api/public/v1/annotations/1"},
		{name: "private list", method: http.MethodGet, path: "/api/private/sites/1/segments"},
		{name: "private create", method: http.MethodPost, path: "/api/private/sites/1/segments"},
		{name: "private update", method: http.MethodPatch, path: "/api/private/segments/1"},
		{name: "private delete", method: http.MethodDelete, path: "/api/private/segments/1"},
		{name: "private import upload", method: http.MethodPost, path: "/api/private/sites/1/imports"},
		{name: "private import list", method: http.MethodGet, path: "/api/private/sites/1/imports"},
		{name: "private import delete", method: http.MethodDelete, path: "/api/private/imports/1"},
		{name: "private site export", method: http.MethodGet, path: "/api/private/sites/1/export?from=2026-10-01&to=2026-10-02"},
		{name: "public profile list", method: http.MethodGet, path: "/api/public/v1/sites/1/profiles"},
		{name: "public profile detail", method: http.MethodGet, path: "/api/public/v1/profiles/1"},
		{name: "private profile list", method: http.MethodGet, path: "/api/private/sites/1/profiles"},
		{name: "private profile detail", method: http.MethodGet, path: "/api/private/profiles/1"},
		{name: "private profile erase", method: http.MethodDelete, path: "/api/private/profiles/1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(tt.method, tt.path, nil)
			if strings.HasPrefix(tt.path, "/api/private/") && tt.method != http.MethodGet {
				request.Header.Set("X-CSRF-Token", "token")
				request.AddCookie(&http.Cookie{Name: "panel_csrf", Value: "token"})
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != 218 {
				t.Fatalf("status=%d body=%s, want platform response", response.Code, response.Body.String())
			}
		})
	}
}

func TestRouterRecoversWithoutExposingPanic(t *testing.T) {
	router := api.NewRouter(api.Deps{
		Config:      config.Config{AdminAPIKey: "test-key"},
		Query:       panicQuery{},
		RateLimiter: ingest.NewTokenBuckets(0, 30),
	})
	request := httptest.NewRequest(http.MethodGet, "/api/public/v1/stats/summary?site_id=1&period=30d", nil)
	request.Header.Set("Authorization", "Bearer test-key")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	assertRecorderProblem(t, response, http.StatusInternalServerError)
	if strings.Contains(response.Body.String(), "sensitive panic value") {
		t.Fatal("panic value exposed")
	}
}

type panicQuery struct{}

func (panicQuery) Query(context.Context, api.QueryRequest) (api.QueryResponse, error) {
	panic("sensitive panic value")
}

func (panicQuery) Funnel(context.Context, api.FunnelRequest) (api.FunnelResponse, error) {
	panic("sensitive panic value")
}

func (panicQuery) Journey(context.Context, api.JourneyRequest) (api.JourneyResponse, error) {
	panic("sensitive panic value")
}

func (panicQuery) Retention(context.Context, api.RetentionRequest) (api.RetentionResponse, error) {
	panic("sensitive panic value")
}

func TestRouterOperationalAndConfiguredRoutes(t *testing.T) {
	router := newTestRouter()
	tests := []struct {
		name   string
		method string
		path   string
		want   int
	}{
		{name: "event path", method: http.MethodPost, path: "/api/public/v1/events", want: http.StatusAccepted},
		{name: "tracker", method: http.MethodGet, path: "/js/script.js", want: http.StatusOK},
		{name: "health", method: http.MethodGet, path: "/healthz", want: http.StatusOK},
		{name: "ready", method: http.MethodGet, path: "/readyz", want: http.StatusOK},
		{name: "metrics", method: http.MethodGet, path: "/metrics", want: http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(tt.method, tt.path, nil)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != tt.want {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestConfigDefaultsToPublicEventPath(t *testing.T) {
	values := map[string]string{
		"PANEL_DATABASE_URL": "postgres://example.test/panel",
		"PANEL_BASE_URL":     "https://example.test",
		"PANEL_SECRET_KEY":   "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
	}
	cfg, err := config.Load(func(key string) string { return values[key] })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.EventPath != "/api/public/v1/events" {
		t.Fatalf("EventPath=%q", cfg.EventPath)
	}
}

func newTestRouter() http.Handler {
	return api.NewRouter(api.Deps{
		Config: config.Config{
			AdminAPIKey: "test-key",
			EventPath:   "/api/public/v1/events",
			TrackerPath: "/js/script.js",
		},
		Query: queryStub{},
		Ingest: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusAccepted)
		}),
		RateLimiter: ingest.NewTokenBuckets(0, 30),
	})
}

func get(t *testing.T, url, authorization string) *http.Response {
	t.Helper()
	return getAccept(t, url, authorization, "")
}

func getAccept(t *testing.T, url, authorization, accept string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", authorization)
	request.Header.Set("Accept", accept)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func post(t *testing.T, url, authorization, body string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", authorization)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func assertProblem(t *testing.T, response *http.Response, status int) api.Problem {
	t.Helper()
	if response.StatusCode != status || response.Header.Get("Content-Type") != "application/problem+json" {
		t.Fatalf("status=%d content-type=%q", response.StatusCode, response.Header.Get("Content-Type"))
	}
	var problem api.Problem
	if err := json.NewDecoder(response.Body).Decode(&problem); err != nil {
		t.Fatal(err)
	}
	if problem.Status != status || problem.Title == "" || problem.Type == "" {
		t.Fatalf("problem=%+v", problem)
	}
	return problem
}

func assertRecorderProblem(t *testing.T, response *httptest.ResponseRecorder, status int) {
	t.Helper()
	result := response.Result()
	defer result.Body.Close()
	assertProblem(t, result, status)
}
