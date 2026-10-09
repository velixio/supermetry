package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"panel/internal/api"
	"panel/internal/config"
	"panel/internal/ingest"
)

type sessionExplorerStub struct {
	list       api.SessionList
	detail     api.SessionDetail
	err        error
	listCalls  int
	detailCall int
}

func (s *sessionExplorerStub) Sessions(_ context.Context, _ int32, _ api.ListSessionsParams, _ []api.Filter) (api.SessionList, error) {
	s.listCalls++
	return s.list, s.err
}

func (s *sessionExplorerStub) Session(_ context.Context, _ int32, _ int64) (api.SessionDetail, error) {
	s.detailCall++
	return s.detail, s.err
}

func TestSessionsEndpoints(t *testing.T) {
	started := time.Date(2026, 10, 8, 8, 0, 0, 0, time.UTC)
	t.Run("public list returns session page", func(t *testing.T) {
		service := &sessionExplorerStub{list: api.SessionList{Data: []api.SessionSummary{{Id: "301", StartedAt: started}}}}
		response := sessionRequest(t, service, api.Principal{OrgID: 7, Scopes: []string{"stats:read"}, SiteIDs: []int32{1}}, http.MethodGet,
			"/api/public/v1/sites/1/sessions?from=2026-10-01&to=2026-10-08&filter=country:is:US&limit=25")
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"id":"301"`) || service.listCalls != 1 {
			t.Fatalf("status=%d body=%s calls=%d", response.Code, response.Body.String(), service.listCalls)
		}
	})

	t.Run("private detail returns timeline", func(t *testing.T) {
		service := &sessionExplorerStub{detail: api.SessionDetail{
			Session:  api.SessionSummary{Id: "301", StartedAt: started},
			Timeline: []api.SessionTimelineItem{{Ts: started, Kind: api.SessionPageview, Name: "pageview", Page: "/"}},
		}}
		response := privateSessionRequest(t, service, api.Principal{OrgID: 7, Scopes: []string{"stats:read"}, SiteIDs: []int32{1}},
			"/api/private/sessions/301?site_id=1")
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"kind":"pageview"`) || service.detailCall != 1 {
			t.Fatalf("status=%d body=%s calls=%d", response.Code, response.Body.String(), service.detailCall)
		}
	})

	t.Run("closed timeline returns retention problem", func(t *testing.T) {
		service := &sessionExplorerStub{err: api.ErrSessionRawExpired}
		response := sessionRequest(t, service, api.Principal{OrgID: 7, Scopes: []string{"stats:read"}, SiteIDs: []int32{1}}, http.MethodGet,
			"/api/public/v1/sessions/201?site_id=1")
		assertRecorderProblem(t, response, http.StatusBadRequest)
	})

	t.Run("unknown session is not found", func(t *testing.T) {
		service := &sessionExplorerStub{err: api.ErrSessionNotFound}
		response := sessionRequest(t, service, api.Principal{OrgID: 7, Scopes: []string{"stats:read"}, SiteIDs: []int32{1}}, http.MethodGet,
			"/api/public/v1/sessions/999?site_id=1")
		assertRecorderProblem(t, response, http.StatusNotFound)
	})
}

func TestSessionsValidationAndAuthorization(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{name: "missing from", path: "/api/public/v1/sites/1/sessions?to=2026-10-08"},
		{name: "invalid date", path: "/api/public/v1/sites/1/sessions?from=2026-02-30&to=2026-10-08"},
		{name: "reversed range", path: "/api/public/v1/sites/1/sessions?from=2026-10-08&to=2026-10-01"},
		{name: "invalid filter", path: "/api/public/v1/sites/1/sessions?from=2026-10-01&to=2026-10-08&filter=country"},
		{name: "limit too large", path: "/api/public/v1/sites/1/sessions?from=2026-10-01&to=2026-10-08&limit=1001"},
		{name: "invalid session", path: "/api/public/v1/sessions/nope?site_id=1"},
		{name: "missing site query", path: "/api/public/v1/sessions/301"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &sessionExplorerStub{}
			response := sessionRequest(t, service, api.Principal{OrgID: 7, Scopes: []string{"stats:read"}, SiteIDs: []int32{1}}, http.MethodGet, tt.path)
			assertRecorderProblem(t, response, http.StatusBadRequest)
			if service.listCalls != 0 {
				t.Fatalf("service called for invalid request: %+v", service)
			}
		})
	}

	t.Run("missing scope is forbidden", func(t *testing.T) {
		service := &sessionExplorerStub{}
		response := sessionRequest(t, service, api.Principal{OrgID: 7, Scopes: []string{"sites:write"}, SiteIDs: []int32{1}}, http.MethodGet,
			"/api/public/v1/sites/1/sessions?from=2026-10-01&to=2026-10-08")
		assertRecorderProblem(t, response, http.StatusForbidden)
		if service.listCalls != 0 {
			t.Fatal("service called without stats:read")
		}
	})

	t.Run("foreign site is hidden before validation", func(t *testing.T) {
		service := &sessionExplorerStub{}
		response := sessionRequest(t, service, api.Principal{OrgID: 7, Scopes: []string{"stats:read"}, SiteIDs: []int32{2}}, http.MethodGet,
			"/api/public/v1/sites/1/sessions?from=bad&to=bad")
		assertRecorderProblem(t, response, http.StatusNotFound)
		if service.listCalls != 0 {
			t.Fatal("service called for foreign site")
		}
	})
}

func sessionRequest(t *testing.T, service *sessionExplorerStub, principal api.Principal, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	router := identifiedRouter(api.Deps{
		Config:         config.Config{},
		SessionExplore: service,
		PublicAuth:     principalAuth{principal: principal},
		PrivateAuth:    principalAuth{principal: principal},
		Sites:          orgSites{1: {ID: 1, OrgID: 7}},
		RateLimiter:    ingest.NewTokenBuckets(0, 30),
	})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(method, path, nil))
	return response
}

func privateSessionRequest(t *testing.T, service *sessionExplorerStub, principal api.Principal, path string) *httptest.ResponseRecorder {
	t.Helper()
	return sessionRequest(t, service, principal, http.MethodGet, path)
}
