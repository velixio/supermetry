package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"panel/internal/api"
	"panel/internal/config"
	"panel/internal/db"
	"panel/internal/ingest"
	"panel/internal/sites"
)

type eventsExploreStub struct {
	queryStub
	eventsRequests []api.EventExploreRequest
}

func (s *eventsExploreStub) Events(_ context.Context, request api.EventExploreRequest) (api.EventExploreList, error) {
	s.eventsRequests = append(s.eventsRequests, request)
	return api.EventExploreList{Data: []api.EventExploreItem{{
		Ts: time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC), Kind: api.ExploreEvent,
		Name: "signup", Page: "/pricing", Country: "US", Device: "desktop", Browser: "Chrome", Source: "Google",
	}}}, nil
}

func (*eventsExploreStub) Properties(context.Context, api.PropertyExploreRequest) ([]api.PropertyKeyCount, error) {
	return []api.PropertyKeyCount{{Key: "plan", Events: 3}}, nil
}

func (*eventsExploreStub) PropertyValues(context.Context, api.PropertyValueExploreRequest) ([]api.PropertyValueCount, error) {
	return []api.PropertyValueCount{{Value: "pro", Events: 2}}, nil
}

func (*eventsExploreStub) Suggestions(context.Context, api.SuggestionExploreRequest) ([]api.Suggestion, error) {
	return []api.Suggestion{{Value: "/pricing", Count: 4}}, nil
}

func TestEventsExploreRoutesReturnAllResources(t *testing.T) {
	service := &eventsExploreStub{}
	router := newEventsExploreRouter(service, api.Principal{OrgID: 7, Scopes: []string{"stats:read"}, SiteIDs: []int32{1}})
	tests := []struct {
		name, path, contains string
	}{
		{name: "events", path: "/api/public/v1/sites/1/events?from=2026-10-01&to=2026-10-08&name=signup&filter=country:is:US&limit=25", contains: `"name":"signup"`},
		{name: "properties", path: "/api/public/v1/sites/1/properties?from=2026-10-01&to=2026-10-08&event=signup", contains: `"key":"plan"`},
		{name: "property values", path: "/api/public/v1/sites/1/properties/plan/values?from=2026-10-01&to=2026-10-08&event=signup", contains: `"value":"pro"`},
		{name: "suggestions", path: "/api/public/v1/sites/1/suggestions?from=2026-10-01&to=2026-10-08&dimension=page&q=pri", contains: `"value":"/pricing"`},
		{name: "private events", path: "/api/private/sites/1/events?from=2026-10-01&to=2026-10-08", contains: `"name":"signup"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, tt.path, nil)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusOK || !json.Valid(response.Body.Bytes()) || !strings.Contains(response.Body.String(), tt.contains) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
	request := service.eventsRequests[0]
	if request.Name != "signup" || request.Limit != 25 || len(request.Filters) != 1 || request.Filters[0].Dimension != "country" {
		t.Fatalf("events request = %+v", request)
	}
}

func TestEventsExploreRoutesValidateAndAuthorize(t *testing.T) {
	tests := []struct {
		name      string
		principal api.Principal
		path      string
		want      int
	}{
		{name: "missing scope", principal: api.Principal{OrgID: 7, SiteIDs: []int32{1}}, path: "/api/public/v1/sites/1/events?from=2026-10-01&to=2026-10-08", want: http.StatusForbidden},
		{name: "foreign site", principal: api.Principal{OrgID: 7, Scopes: []string{"stats:read"}, SiteIDs: []int32{1}}, path: "/api/public/v1/sites/2/events?from=2026-10-01&to=2026-10-08", want: http.StatusNotFound},
		{name: "properties missing scope", principal: api.Principal{OrgID: 7, SiteIDs: []int32{1}}, path: "/api/public/v1/sites/1/properties?from=2026-10-01&to=2026-10-08", want: http.StatusForbidden},
		{name: "properties foreign site", principal: api.Principal{OrgID: 7, Scopes: []string{"stats:read"}, SiteIDs: []int32{1}}, path: "/api/public/v1/sites/2/properties?from=2026-10-01&to=2026-10-08", want: http.StatusNotFound},
		{name: "property values missing scope", principal: api.Principal{OrgID: 7, SiteIDs: []int32{1}}, path: "/api/public/v1/sites/1/properties/plan/values?from=2026-10-01&to=2026-10-08", want: http.StatusForbidden},
		{name: "property values foreign site", principal: api.Principal{OrgID: 7, Scopes: []string{"stats:read"}, SiteIDs: []int32{1}}, path: "/api/public/v1/sites/2/properties/plan/values?from=2026-10-01&to=2026-10-08", want: http.StatusNotFound},
		{name: "suggestions missing scope", principal: api.Principal{OrgID: 7, SiteIDs: []int32{1}}, path: "/api/public/v1/sites/1/suggestions?from=2026-10-01&to=2026-10-08&dimension=page", want: http.StatusForbidden},
		{name: "suggestions foreign site", principal: api.Principal{OrgID: 7, Scopes: []string{"stats:read"}, SiteIDs: []int32{1}}, path: "/api/public/v1/sites/2/suggestions?from=2026-10-01&to=2026-10-08&dimension=page", want: http.StatusNotFound},
		{name: "invalid date", principal: api.Principal{OrgID: 7, Scopes: []string{"stats:read"}, SiteIDs: []int32{1}}, path: "/api/public/v1/sites/1/events?from=bad&to=2026-10-08", want: http.StatusBadRequest},
		{name: "reverse range", principal: api.Principal{OrgID: 7, Scopes: []string{"stats:read"}, SiteIDs: []int32{1}}, path: "/api/public/v1/sites/1/properties?from=2026-10-09&to=2026-10-08", want: http.StatusBadRequest},
		{name: "invalid filter", principal: api.Principal{OrgID: 7, Scopes: []string{"stats:read"}, SiteIDs: []int32{1}}, path: "/api/public/v1/sites/1/events?from=2026-10-01&to=2026-10-08&filter=broken", want: http.StatusBadRequest},
		{name: "event name too long", principal: api.Principal{OrgID: 7, Scopes: []string{"stats:read"}, SiteIDs: []int32{1}}, path: "/api/public/v1/sites/1/events?from=2026-10-01&to=2026-10-08&name=" + strings.Repeat("x", 2001), want: http.StatusBadRequest},
		{name: "property event too long", principal: api.Principal{OrgID: 7, Scopes: []string{"stats:read"}, SiteIDs: []int32{1}}, path: "/api/public/v1/sites/1/properties?from=2026-10-01&to=2026-10-08&event=" + strings.Repeat("x", 2001), want: http.StatusBadRequest},
		{name: "property key too long", principal: api.Principal{OrgID: 7, Scopes: []string{"stats:read"}, SiteIDs: []int32{1}}, path: "/api/public/v1/sites/1/properties/" + strings.Repeat("x", 2001) + "/values?from=2026-10-01&to=2026-10-08", want: http.StatusBadRequest},
		{name: "missing dimension", principal: api.Principal{OrgID: 7, Scopes: []string{"stats:read"}, SiteIDs: []int32{1}}, path: "/api/public/v1/sites/1/suggestions?from=2026-10-01&to=2026-10-08", want: http.StatusBadRequest},
		{name: "foreign site before validation", principal: api.Principal{OrgID: 7, Scopes: []string{"stats:read"}, SiteIDs: []int32{1}}, path: "/api/public/v1/sites/2/suggestions?from=bad", want: http.StatusNotFound},
		{name: "empty property suggestion key", principal: api.Principal{OrgID: 7, Scopes: []string{"stats:read"}, SiteIDs: []int32{1}}, path: "/api/public/v1/sites/1/suggestions?from=2026-10-01&to=2026-10-08&dimension=prop:", want: http.StatusBadRequest},
		{name: "empty property filter key", principal: api.Principal{OrgID: 7, Scopes: []string{"stats:read"}, SiteIDs: []int32{1}}, path: "/api/public/v1/sites/1/events?from=2026-10-01&to=2026-10-08&filter=prop::is:x", want: http.StatusBadRequest},
		{name: "unknown suggestion dimension", principal: api.Principal{OrgID: 7, Scopes: []string{"stats:read"}, SiteIDs: []int32{1}}, path: "/api/public/v1/sites/1/suggestions?from=2026-10-01&to=2026-10-08&dimension=visitor_id", want: http.StatusBadRequest},
		{name: "suggestion query too long", principal: api.Principal{OrgID: 7, Scopes: []string{"stats:read"}, SiteIDs: []int32{1}}, path: "/api/public/v1/sites/1/suggestions?from=2026-10-01&to=2026-10-08&dimension=page&q=" + strings.Repeat("x", 2001), want: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := newEventsExploreRouter(&eventsExploreStub{}, tt.principal)
			request := httptest.NewRequest(http.MethodGet, tt.path, nil)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != tt.want || response.Header().Get("Content-Type") != "application/problem+json" {
				t.Fatalf("status=%d content-type=%q body=%s, want %d", response.Code, response.Header().Get("Content-Type"), response.Body.String(), tt.want)
			}
		})
	}
}

func newEventsExploreRouter(service *eventsExploreStub, principal api.Principal) http.Handler {
	registry := orgSites{
		1: sites.Site{ID: 1, OrgID: 7, TZ: time.UTC, Retention: db.Ret30},
		2: sites.Site{ID: 2, OrgID: 8, TZ: time.UTC, Retention: db.Ret30},
	}
	return identifiedRouter(api.Deps{
		Config: config.Config{}, Query: service,
		PublicAuth: principalAuth{principal: principal}, PrivateAuth: principalAuth{principal: principal},
		Sites: registry, RateLimiter: ingest.NewTokenBuckets(0, 30),
	})
}
