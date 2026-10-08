package authn_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"panel/internal/api"
	"panel/internal/config"
	"panel/internal/ingest"
	"panel/internal/sites"
)

func TestAuthenticator_KeyCannotReadAnotherOrganizationsSite(t *testing.T) {
	env := newTestEnv(t)
	router := api.NewRouter(api.Deps{
		Config:      config.Config{},
		Query:       queryStub{},
		PublicAuth:  env.authenticator,
		Sites:       siteRegistry{env.otherSiteID: {ID: env.otherSiteID, OrgID: env.otherOrgID}},
		RateLimiter: ingest.NewTokenBuckets(0, 30),
	})
	request := httptest.NewRequest(http.MethodGet, "/api/public/v1/stats/summary?site_id="+integer(int64(env.otherSiteID))+"&period=7d", nil)
	request.Header.Set("Authorization", "Bearer "+env.key)
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("cross-org request status = %d, want %d; body=%s", response.Code, http.StatusNotFound, response.Body.String())
	}
}

type queryStub struct{}

func (queryStub) Query(context.Context, api.QueryRequest) (api.QueryResponse, error) {
	return api.QueryResponse{}, nil
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

type siteRegistry map[int32]sites.Site

func (s siteRegistry) ByPublicID(string) (sites.Site, bool) { return sites.Site{}, false }
func (s siteRegistry) Site(id int32) (sites.Site, bool)     { site, ok := s[id]; return site, ok }
func (s siteRegistry) All() []sites.Site                    { return nil }
