package api_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"panel/internal/api"
	"panel/internal/config"
	"panel/internal/ingest"
)

type heatmapStub struct{ err error }

func (s heatmapStub) Query(_ context.Context, _ api.HeatmapRequest) (api.HeatmapResponse, error) {
	if s.err != nil {
		return api.HeatmapResponse{}, s.err
	}
	return api.HeatmapResponse{
		Cells:       []api.HeatmapCell{{Gx: 12, Gy: 34, Clicks: 5}},
		ScrollBands: []api.ScrollBand{},
		Totals:      api.HeatmapTotals{Clicks: 5, PageviewsSampled: 2},
	}, nil
}

func TestHeatmapRoutesAuthorizeSiteAndScope(t *testing.T) {
	registry := orgSites{1: {ID: 1, OrgID: 7}, 2: {ID: 2, OrgID: 7}}
	newRouter := func(service heatmapStub, scopes []string) http.Handler {
		return identifiedRouter(api.Deps{
			Config:      config.Config{},
			Query:       queryStub{},
			Heatmaps:    service,
			PublicAuth:  principalAuth{principal: api.Principal{OrgID: 7, Scopes: scopes, SiteIDs: []int32{2}}},
			PrivateAuth: principalAuth{principal: api.Principal{OrgID: 7, Scopes: scopes}},
			Sites:       registry,
			RateLimiter: ingest.NewTokenBuckets(0, 30),
		})
	}
	body := `{"site_id":2,"date_range":{"from":"2026-10-01","to":"2026-10-07"},"path":"/docs","device":"desktop"}`

	tests := []struct {
		name    string
		path    string
		body    string
		scopes  []string
		csrf    bool
		want    int
		service heatmapStub
	}{
		{name: "public", path: "/api/public/v1/heatmaps/query", body: body, scopes: []string{"stats:read"}, want: http.StatusOK},
		{name: "foreign site is hidden", path: "/api/public/v1/heatmaps/query", body: strings.Replace(body, `"site_id":2`, `"site_id":1`, 1), scopes: []string{"stats:read"}, want: http.StatusNotFound},
		{name: "scope is required", path: "/api/public/v1/heatmaps/query", body: body, scopes: []string{"sites:write"}, want: http.StatusForbidden},
		{name: "private requires csrf", path: "/api/private/heatmaps/query", body: body, scopes: []string{"stats:read"}, want: http.StatusForbidden},
		{name: "private", path: "/api/private/heatmaps/query", body: body, scopes: []string{"stats:read"}, csrf: true, want: http.StatusOK},
		{name: "validation error", path: "/api/public/v1/heatmaps/query", body: body, scopes: []string{"stats:read"}, want: http.StatusBadRequest, service: heatmapStub{err: errors.New("heatmap path is required")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, tt.path, strings.NewReader(tt.body))
			if tt.csrf {
				request.Header.Set("X-CSRF-Token", "token")
				request.AddCookie(&http.Cookie{Name: "panel_csrf", Value: "token"})
			}
			response := httptest.NewRecorder()
			newRouter(tt.service, tt.scopes).ServeHTTP(response, request)
			if response.Code != tt.want {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}
