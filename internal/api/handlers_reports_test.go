package api_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"panel/internal/api"
	"panel/internal/config"
	"panel/internal/ingest"
)

type reportsQueryStub struct {
	journeyErr   error
	retentionErr error
}

func (reportsQueryStub) Query(context.Context, api.QueryRequest) (api.QueryResponse, error) {
	return api.QueryResponse{}, nil
}

func (reportsQueryStub) Funnel(context.Context, api.FunnelRequest) (api.FunnelResponse, error) {
	return api.FunnelResponse{}, nil
}

func (s reportsQueryStub) Journey(_ context.Context, request api.JourneyRequest) (api.JourneyResponse, error) {
	if s.journeyErr != nil {
		return api.JourneyResponse{}, s.journeyErr
	}
	if request.Filters != nil && len(*request.Filters) > 0 && !(*request.Filters)[0].Operator.Valid() {
		return api.JourneyResponse{}, errors.New(`invalid filter "country"`)
	}
	return api.JourneyResponse{
		Nodes: []api.JourneyNode{{Step: 1, Path: "/", Visitors: 3}},
		Links: []api.JourneyLink{{FromStep: 1, FromPath: "/", ToPath: "exit", Visitors: 2}},
	}, nil
}

func (s reportsQueryStub) Retention(context.Context, api.RetentionRequest) (api.RetentionResponse, error) {
	if s.retentionErr != nil {
		return api.RetentionResponse{}, s.retentionErr
	}
	return api.RetentionResponse{
		IdentityWindow: api.DailyIdentityWindow,
		RetentionMode:  api.WithinDaySessionReturn,
		Cohorts: []api.RetentionCohort{{
			Start:    openapi_types.Date{Time: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)},
			Size:     3,
			Retained: []int64{3, 2},
		}},
	}, nil
}

func TestJourneyReport(t *testing.T) {
	t.Run("returns sankey response", func(t *testing.T) {
		response := reportRequest(t, reportsQueryStub{}, "/api/public/v1/journeys/query", `{"site_id":1,"date_range":{"from":"2026-10-01","to":"2026-10-02"},"steps":3,"limit":5}`)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"to_path":"exit"`) {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	})

	tests := []struct {
		name string
		body string
	}{
		{name: "steps below minimum", body: `{"site_id":1,"date_range":{"from":"2026-10-01","to":"2026-10-02"},"steps":1,"limit":5}`},
		{name: "steps above maximum", body: `{"site_id":1,"date_range":{"from":"2026-10-01","to":"2026-10-02"},"steps":6,"limit":5}`},
		{name: "zero limit", body: `{"site_id":1,"date_range":{"from":"2026-10-01","to":"2026-10-02"},"steps":3,"limit":0}`},
		{name: "limit above maximum", body: `{"site_id":1,"date_range":{"from":"2026-10-01","to":"2026-10-02"},"steps":3,"limit":11}`},
		{name: "both path anchors", body: `{"site_id":1,"date_range":{"from":"2026-10-01","to":"2026-10-02"},"start_path":"/","end_path":"/done","steps":3,"limit":5}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := reportRequest(t, reportsQueryStub{}, "/api/public/v1/journeys/query", tt.body)
			assertRecorderProblem(t, response, http.StatusBadRequest)
		})
	}

	t.Run("raw retention error is a bad request", func(t *testing.T) {
		response := reportRequest(t, reportsQueryStub{journeyErr: errors.New("journey date range must be within the raw retention window")}, "/api/public/v1/journeys/query", `{"site_id":1,"date_range":{"from":"2026-01-01","to":"2026-10-02"},"steps":3,"limit":5}`)
		assertRecorderProblem(t, response, http.StatusBadRequest)
	})

	t.Run("malformed filter error is a bad request", func(t *testing.T) {
		response := reportRequest(t, reportsQueryStub{}, "/api/public/v1/journeys/query", `{"site_id":1,"date_range":{"from":"2026-10-01","to":"2026-10-02"},"filters":[{"dimension":"country","operator":"bogus","values":["IN"]}],"steps":3,"limit":5}`)
		assertRecorderProblem(t, response, http.StatusBadRequest)
	})
}

func TestRetentionReport(t *testing.T) {
	t.Run("returns documented identity semantics", func(t *testing.T) {
		response := reportRequest(t, reportsQueryStub{}, "/api/public/v1/retention/query", `{"site_id":1,"date_range":{"from":"2026-10-01","to":"2026-10-02"},"interval":"day"}`)
		body := response.Body.String()
		if response.Code != http.StatusOK || !strings.Contains(body, `"identity_window":"day"`) || !strings.Contains(body, `"retention_mode":"within_day_session_return"`) {
			t.Fatalf("status=%d body=%s", response.Code, body)
		}
	})

	for _, interval := range []string{"hour", ""} {
		name := interval
		if name == "" {
			name = "missing"
		}
		t.Run("rejects "+name+" interval", func(t *testing.T) {
			body := `{"site_id":1,"date_range":{"from":"2026-10-01","to":"2026-10-02"},"interval":"` + interval + `"}`
			response := reportRequest(t, reportsQueryStub{}, "/api/public/v1/retention/query", body)
			assertRecorderProblem(t, response, http.StatusBadRequest)
		})
	}
}

func TestReportsAuthorization(t *testing.T) {
	t.Run("public journey cannot read another site", func(t *testing.T) {
		router := api.NewRouter(api.Deps{
			Config:      config.Config{},
			Query:       reportsQueryStub{},
			PublicAuth:  principalAuth{principal: api.Principal{OrgID: 7, Scopes: []string{"stats:read"}, SiteIDs: []int32{2}}},
			Sites:       orgSites{1: {ID: 1, OrgID: 7}},
			RateLimiter: ingest.NewTokenBuckets(0, 30),
		})
		request := httptest.NewRequest(http.MethodPost, "/api/public/v1/journeys/query", strings.NewReader(`{"site_id":1,"date_range":{"from":"2026-10-01","to":"2026-10-02"},"steps":3,"limit":5}`))
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		assertRecorderProblem(t, response, http.StatusNotFound)
	})

	t.Run("foreign site is 404 even when the body is invalid", func(t *testing.T) {
		for _, tc := range []struct{ path, body string }{
			{"/api/public/v1/journeys/query", `{"site_id":1,"date_range":{"from":"2026-10-01","to":"2026-10-02"},"steps":99,"limit":5}`},
			{"/api/public/v1/retention/query", `{"site_id":1,"date_range":{"from":"2026-10-01","to":"2026-10-02"},"interval":"year"}`},
		} {
			router := api.NewRouter(api.Deps{
				Config:      config.Config{},
				Query:       reportsQueryStub{},
				PublicAuth:  principalAuth{principal: api.Principal{OrgID: 7, Scopes: []string{"stats:read"}, SiteIDs: []int32{2}}},
				Sites:       orgSites{1: {ID: 1, OrgID: 7}},
				RateLimiter: ingest.NewTokenBuckets(0, 30),
			})
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body)))
			assertRecorderProblem(t, response, http.StatusNotFound)
		}
	})

	t.Run("missing stats:read scope is forbidden", func(t *testing.T) {
		router := api.NewRouter(api.Deps{
			Config:      config.Config{},
			Query:       reportsQueryStub{},
			PublicAuth:  principalAuth{principal: api.Principal{OrgID: 7, Scopes: []string{"sites:write"}, SiteIDs: []int32{1}}},
			Sites:       orgSites{1: {ID: 1, OrgID: 7}},
			RateLimiter: ingest.NewTokenBuckets(0, 30),
		})
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/public/v1/retention/query", strings.NewReader(`{"site_id":1,"date_range":{"from":"2026-10-01","to":"2026-10-02"},"interval":"day"}`)))
		assertRecorderProblem(t, response, http.StatusForbidden)
	})

	t.Run("private retention requires CSRF", func(t *testing.T) {
		router := api.NewRouter(api.Deps{
			Config:      config.Config{},
			Query:       reportsQueryStub{},
			PrivateAuth: principalAuth{principal: api.Principal{OrgID: 7, Scopes: []string{"stats:read"}}},
			Sites:       orgSites{1: {ID: 1, OrgID: 7}},
			RateLimiter: ingest.NewTokenBuckets(0, 30),
		})
		request := httptest.NewRequest(http.MethodPost, "/api/private/retention/query", strings.NewReader(`{"site_id":1,"date_range":{"from":"2026-10-01","to":"2026-10-02"},"interval":"day"}`))
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		assertRecorderProblem(t, response, http.StatusForbidden)
	})
}

func reportRequest(t *testing.T, query reportsQueryStub, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	router := api.NewRouter(api.Deps{
		Config:      config.Config{},
		Query:       query,
		PublicAuth:  principalAuth{principal: api.Principal{OrgID: 7, Scopes: []string{"stats:read"}, SiteIDs: []int32{1}}},
		Sites:       orgSites{1: {ID: 1, OrgID: 7}},
		RateLimiter: ingest.NewTokenBuckets(0, 30),
	})
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}
