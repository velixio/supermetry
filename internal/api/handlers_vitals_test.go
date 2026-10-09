package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"panel/internal/api"
	"panel/internal/config"
	"panel/internal/ingest"
)

type vitalsQueryStub struct {
	queryStub
	err error
}

func (s vitalsQueryStub) Vitals(_ context.Context, _ api.VitalsRequest) (api.VitalsResponse, error) {
	if s.err != nil {
		return api.VitalsResponse{}, s.err
	}
	return api.VitalsResponse{Breakdown: []api.VitalsBreakdown{}, Timeseries: []api.VitalsTrendPoint{}}, nil
}

func TestVitalsRoutesAuthorizeSiteAndMapRetentionErrors(t *testing.T) {
	registry := orgSites{1: {ID: 1, OrgID: 7}, 2: {ID: 2, OrgID: 7}}
	newRouter := func(service vitalsQueryStub) http.Handler {
		return identifiedRouter(api.Deps{
			Config:      config.Config{},
			Query:       service,
			PublicAuth:  principalAuth{principal: api.Principal{OrgID: 7, Scopes: []string{"stats:read"}, SiteIDs: []int32{2}}},
			PrivateAuth: principalAuth{principal: api.Principal{OrgID: 7, Scopes: []string{"stats:read"}}},
			Sites:       registry,
			RateLimiter: ingest.NewTokenBuckets(0, 30),
		})
	}
	body := `{"site_id":2,"date_range":{"from":"2026-10-01","to":"2026-10-07"}}`

	t.Run("public route returns report", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodPost, "/api/public/v1/vitals/query", strings.NewReader(body))
		response := httptest.NewRecorder()
		newRouter(vitalsQueryStub{}).ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		if !strings.Contains(response.Body.String(), `"timeseries":[]`) {
			t.Fatalf("body=%s", response.Body.String())
		}
	})

	t.Run("public route hides an unauthorized site", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodPost, "/api/public/v1/vitals/query", strings.NewReader(strings.Replace(body, `"site_id":2`, `"site_id":1`, 1)))
		response := httptest.NewRecorder()
		newRouter(vitalsQueryStub{}).ServeHTTP(response, request)
		assertRecorderProblem(t, response, http.StatusNotFound)
	})

	t.Run("private route requires CSRF", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodPost, "/api/private/vitals/query", strings.NewReader(body))
		response := httptest.NewRecorder()
		newRouter(vitalsQueryStub{}).ServeHTTP(response, request)
		assertRecorderProblem(t, response, http.StatusForbidden)
	})

	t.Run("private route returns report with CSRF", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodPost, "/api/private/vitals/query", strings.NewReader(body))
		request.Header.Set("X-CSRF-Token", "token")
		request.AddCookie(&http.Cookie{Name: "panel_csrf", Value: "token"})
		response := httptest.NewRecorder()
		newRouter(vitalsQueryStub{}).ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	})

	t.Run("retention error is an explanatory problem", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodPost, "/api/public/v1/vitals/query", strings.NewReader(body))
		response := httptest.NewRecorder()
		newRouter(vitalsQueryStub{err: errors.New("Web Vitals are available only within the site's 30-day raw retention window")}).ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		var problem api.Problem
		if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
			t.Fatal(err)
		}
		if problem.Detail == nil || !strings.Contains(*problem.Detail, "raw retention") {
			t.Fatalf("problem = %+v", problem)
		}
	})
}
