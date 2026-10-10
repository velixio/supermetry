package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"supermetry/internal/api"
	"supermetry/internal/config"
	"supermetry/internal/ingest"
	"supermetry/internal/privacy"
	"supermetry/internal/query"
	"supermetry/internal/sites"
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

type normalizingQueryStub struct{ site sites.Site }

func (s normalizingQueryStub) Query(_ context.Context, req api.QueryRequest) (api.QueryResponse, error) {
	if _, problem := query.Normalize(req, s.site, time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)); problem != nil {
		return api.QueryResponse{}, errors.New(*problem.Detail)
	}
	return api.QueryResponse{}, nil
}

func (normalizingQueryStub) Funnel(context.Context, api.FunnelRequest) (api.FunnelResponse, error) {
	return api.FunnelResponse{}, nil
}

func (normalizingQueryStub) Journey(context.Context, api.JourneyRequest) (api.JourneyResponse, error) {
	return api.JourneyResponse{}, nil
}

func (normalizingQueryStub) Retention(context.Context, api.RetentionRequest) (api.RetentionResponse, error) {
	return api.RetentionResponse{}, nil
}

type principalAuth struct {
	principal api.Principal
}

func (a principalAuth) Authenticate(*http.Request) (api.Principal, bool) {
	return a.principal, true
}

type rejectingAuth struct{ calls int }

func (a *rejectingAuth) Authenticate(*http.Request) (api.Principal, bool) {
	a.calls++
	return api.Principal{}, false
}

type bearerAuth map[string]api.Principal

func (a bearerAuth) Authenticate(r *http.Request) (api.Principal, bool) {
	principal, ok := a[r.Header.Get("Authorization")]
	return principal, ok
}

type perKeyLimiter struct {
	calls map[string]int
}

func (l *perKeyLimiter) Allow(key string) bool {
	if l.calls == nil {
		l.calls = make(map[string]int)
	}
	l.calls[key]++
	return l.calls[key] == 1
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
		var envelope struct {
			Error struct {
				Code int `json:"code"`
			} `json:"error"`
		}
		if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusOK || envelope.Error.Code != -32001 || response.Header.Get("WWW-Authenticate") != "Bearer" {
			t.Fatalf("status=%d error=%+v headers=%v", response.StatusCode, envelope.Error, response.Header)
		}
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
		for path := range document.Paths {
			if strings.HasPrefix(path, "/api/v1/") || strings.HasPrefix(path, "/api/private/") {
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

func TestQueryFilterComplexityReturnsBadRequest(t *testing.T) {
	site := sites.Site{ID: 1, OrgID: 7, TZ: time.UTC}
	router := identifiedRouter(api.Deps{
		Config:      config.Config{EventPath: "/api/public/v1/events", TrackerPath: "/js/script.js"},
		Query:       normalizingQueryStub{site: site},
		PublicAuth:  principalAuth{principal: api.Principal{OrgID: 7, Scopes: []string{"stats:read"}}},
		PrivateAuth: principalAuth{principal: api.Principal{OrgID: 7}},
		Sites:       orgSites{1: site},
		RateLimiter: ingest.NewTokenBuckets(0, 30),
	})
	server := httptest.NewServer(router)
	defer server.Close()

	values := make([]string, 51)
	for i := range values {
		values[i] = "value"
	}
	filters := make([]api.Filter, 11)
	for i := range filters {
		filters[i] = api.Filter{Dimension: "country", Operator: api.Is, Values: []string{"US"}}
	}
	tests := []struct {
		name       string
		filters    []api.Filter
		wantStatus int
	}{
		{name: "50 values accepted", filters: []api.Filter{{Dimension: "page", Operator: api.Contains, Values: values[:50]}}, wantStatus: http.StatusOK},
		{name: "51 values rejected", filters: []api.Filter{{Dimension: "page", Operator: api.Contains, Values: values}}, wantStatus: http.StatusBadRequest},
		{name: "too many filters rejected", filters: filters, wantStatus: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			period := api.N7d
			requestBody, err := json.Marshal(api.QueryRequest{SiteId: 1, Period: &period, Metrics: []api.Metric{api.Visitors}, Filters: &tt.filters})
			if err != nil {
				t.Fatal(err)
			}
			response := post(t, server.URL+"/api/public/v1/query", "Bearer test-key", string(requestBody))
			defer response.Body.Close()
			if response.StatusCode != tt.wantStatus {
				body, _ := io.ReadAll(response.Body)
				t.Fatalf("status=%d body=%s, want %d", response.StatusCode, body, tt.wantStatus)
			}
			if tt.wantStatus == http.StatusBadRequest {
				assertProblem(t, response, http.StatusBadRequest)
			}
		})
	}
}

func TestPublicRateLimitUsesConfiguredLimiterPerAuthenticatedKey(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	limiter := &perKeyLimiter{}
	router := identifiedRouter(api.Deps{
		Config: config.Config{
			Tracking:       config.TrackingMode{Mode: privacy.Identified},
			TrustedProxies: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")},
		},
		Query: queryStub{},
		PublicAuth: bearerAuth{
			"Bearer key-a": {AllSites: true, Scopes: []string{"stats:read"}, CredentialID: "key:a"},
			"Bearer key-b": {AllSites: true, Scopes: []string{"stats:read"}, CredentialID: "key:b"},
		},
		RateLimiter:  limiter,
		Now:          func() time.Time { return now },
		RateLimitNow: func() time.Time { return now },
	})

	request := func(authorization string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, "/api/public/v1/stats/summary?site_id=1&period=30d", nil)
		request.RemoteAddr = "10.0.0.1:4321"
		request.Header.Set("X-Forwarded-For", "198.51.100.8")
		request.Header.Set("Authorization", authorization)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		return response
	}

	for _, authorization := range []string{"Bearer key-a", "Bearer key-b"} {
		if response := request(authorization); response.Code != http.StatusOK {
			t.Fatalf("first %s status=%d body=%s", authorization, response.Code, response.Body.String())
		}
	}
	if response := request("Bearer key-a"); response.Code != http.StatusTooManyRequests {
		t.Fatalf("second key-a status=%d, want 429", response.Code)
	}
	if got := len(limiter.calls); got != 2 {
		t.Fatalf("configured limiter keys=%d, want 2", got)
	}
}

func TestPublicRateLimitCanonicalizesAuthenticatedCredential(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	limiter := &perKeyLimiter{}
	principal := api.Principal{AllSites: true, Scopes: []string{"stats:read"}, CredentialID: "key:42"}
	router := identifiedRouter(api.Deps{
		Config: config.Config{Tracking: config.TrackingMode{Mode: privacy.Identified}},
		Query:  queryStub{},
		PublicAuth: bearerAuth{
			"Bearer same-key": principal,
			"bearer same-key": principal,
		},
		RateLimiter:  limiter,
		Now:          func() time.Time { return now },
		RateLimitNow: func() time.Time { return now },
	})

	request := func(authorization string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/public/v1/stats/summary?site_id=1&period=30d", nil)
		req.Header.Set("Authorization", authorization)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		return response
	}

	if response := request("Bearer same-key"); response.Code != http.StatusOK {
		t.Fatalf("first request status=%d body=%s", response.Code, response.Body.String())
	}
	if response := request("bearer same-key"); response.Code != http.StatusTooManyRequests {
		t.Fatalf("case-variant request status=%d, want 429", response.Code)
	}
	if got := len(limiter.calls); got != 1 {
		t.Fatalf("configured limiter keys=%d, want one canonical credential bucket", got)
	}
}

func TestPublicRateLimitChargesIPOnlyAfterAuthenticationFails(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	limiter := &perKeyLimiter{}
	router := identifiedRouter(api.Deps{
		Config: config.Config{
			Tracking:       config.TrackingMode{Mode: privacy.Identified},
			TrustedProxies: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")},
		},
		Query: queryStub{},
		PublicAuth: bearerAuth{
			"Bearer valid": {AllSites: true, Scopes: []string{"stats:read"}},
		},
		RateLimiter:  limiter,
		Now:          func() time.Time { return now },
		RateLimitNow: func() time.Time { return now },
	})

	for requestNumber := 1; requestNumber <= 31; requestNumber++ {
		request := httptest.NewRequest(http.MethodGet, "/api/public/v1/stats/summary?site_id=1&period=30d", nil)
		request.RemoteAddr = "10.0.0.1:4321"
		request.Header.Set("X-Forwarded-For", "198.51.100.8")
		request.Header.Set("Authorization", "Bearer invalid")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		want := http.StatusUnauthorized
		if requestNumber == 31 {
			want = http.StatusTooManyRequests
		}
		if response.Code != want {
			t.Fatalf("invalid request %d status=%d, want %d", requestNumber, response.Code, want)
		}
	}
	if got := len(limiter.calls); got != 0 {
		t.Fatalf("configured limiter charged %d failed-auth keys, want 0", got)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/public/v1/stats/summary?site_id=1&period=30d", nil)
	request.RemoteAddr = "10.0.0.1:4321"
	request.Header.Set("X-Forwarded-For", "198.51.100.8")
	request.Header.Set("Authorization", "Bearer valid")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("valid key after IP exhaustion status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestPublicRateLimitRejectsMissingAuthorizationBeforeAuthentication(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	authenticator := &rejectingAuth{}
	router := identifiedRouter(api.Deps{
		Config:       config.Config{Tracking: config.TrackingMode{Mode: privacy.Identified}},
		Query:        queryStub{},
		PublicAuth:   authenticator,
		RateLimitNow: func() time.Time { return now },
	})

	for requestNumber := 1; requestNumber <= 30; requestNumber++ {
		request := httptest.NewRequest(http.MethodGet, "/api/public/v1/stats/summary?site_id=1&period=30d", nil)
		request.RemoteAddr = "198.51.100.8:4321"
		request.Header.Set("Authorization", "Bearer invalid")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("setup request %d status=%d, want 401", requestNumber, response.Code)
		}
	}
	authenticator.calls = 0
	request := httptest.NewRequest(http.MethodGet, "/api/public/v1/stats/summary?site_id=1&period=30d", nil)
	request.RemoteAddr = "198.51.100.8:4321"
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("exhausted anonymous request status=%d, want 429", response.Code)
	}
	if authenticator.calls != 0 {
		t.Fatalf("Authenticate calls=%d, want 0 for exhausted request without Authorization", authenticator.calls)
	}
}

type orgSites map[int32]sites.Site

func (o orgSites) ByPublicID(string) (sites.Site, bool) { return sites.Site{}, false }
func (o orgSites) Site(id int32) (sites.Site, bool)     { s, ok := o[id]; return s, ok }
func (o orgSites) All() []sites.Site {
	result := make([]sites.Site, 0, len(o))
	for _, site := range o {
		result = append(result, site)
	}
	return result
}

func TestAuthorizationScopeSiteAndCSRF(t *testing.T) {
	// Sites 1 and 2 belong to org 7; principals are scoped to org 7.
	registry := orgSites{1: {ID: 1, OrgID: 7}, 2: {ID: 2, OrgID: 7}}
	private := principalAuth{principal: api.Principal{OrgID: 7, Scopes: []string{"stats:read"}}}
	router := identifiedRouter(api.Deps{
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
		noScopeRouter := identifiedRouter(api.Deps{
			Config:      config.Config{EventPath: "/api/public/v1/events", TrackerPath: "/js/script.js"},
			Query:       queryStub{},
			PublicAuth:  principalAuth{principal: api.Principal{OrgID: 7, Scopes: []string{"sites:write"}}},
			Sites:       registry,
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
		request.AddCookie(&http.Cookie{Name: "supermetry_csrf", Value: "present"})
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
	router := identifiedRouter(api.Deps{
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
				request.AddCookie(&http.Cookie{Name: "supermetry_csrf", Value: "token"})
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
	router := identifiedRouter(api.Deps{
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
	registry := routerScriptSites{"abc123def456": {PublicID: "abc123def456", HeatmapsEnabled: true}}
	router := newTestRouterWithSites(registry)
	tests := []struct {
		name   string
		method string
		path   string
		want   int
	}{
		{name: "event path", method: http.MethodPost, path: "/api/public/v1/events", want: http.StatusAccepted},
		{name: "tracker", method: http.MethodGet, path: "/js/script.js", want: http.StatusOK},
		{name: "full tracker", method: http.MethodGet, path: "/js/script.full.js", want: http.StatusOK},
		{name: "site tracker", method: http.MethodGet, path: "/js/abc123def456.js", want: http.StatusOK},
		{name: "site tracker traversal", method: http.MethodGet, path: "/js/..%2fabc123def456.js", want: http.StatusNotFound},
		{name: "health", method: http.MethodGet, path: "/healthz", want: http.StatusOK},
		{name: "ready", method: http.MethodGet, path: "/readyz", want: http.StatusOK},
		{name: "metrics", method: http.MethodGet, path: "/metrics", want: http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(tt.method, tt.path, nil)
			if tt.path == "/metrics" {
				request.RemoteAddr = "127.0.0.1:1234"
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != tt.want {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

type routerScriptSites map[string]sites.Site

func (r routerScriptSites) ByPublicID(id string) (sites.Site, bool) {
	site, ok := r[id]
	return site, ok
}
func (r routerScriptSites) Site(id int32) (sites.Site, bool) { return sites.Site{}, false }
func (r routerScriptSites) All() []sites.Site                { return nil }

func TestConfigDefaultsToPublicEventPath(t *testing.T) {
	values := map[string]string{
		"SUPERMETRY_DATABASE_URL": "postgres://example.test/supermetry",
		"SUPERMETRY_BASE_URL":     "https://example.test",
		"SUPERMETRY_SECRET_KEY":   "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
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
	return newTestRouterWithSites(nil)
}

func newTestRouterWithSites(registry sites.Registry) http.Handler {
	return identifiedRouter(api.Deps{
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
		Sites:       registry,
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
