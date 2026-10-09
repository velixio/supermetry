package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"panel/internal/api"
	"panel/internal/ingest"
)

func TestSensitiveSessionMetadataAndReplayFilter(t *testing.T) {
	external := "identified-person"
	for _, scopes := range [][]string{{"stats:read"}, {"stats:read", "profiles:read"}, {"stats:read", "replay:read"}, {"stats:read", "profiles:read", "replay:read"}} {
		t.Run(strings.Join(scopes, ","), func(t *testing.T) {
			profile, replay := strings.Contains(strings.Join(scopes, ","), "profiles:read"), strings.Contains(strings.Join(scopes, ","), "replay:read")
			principal := api.Principal{OrgID: 7, Scopes: scopes}
			for _, path := range []string{"/api/public/v1/sites/1/sessions?from=2026-10-01&to=2026-10-08", "/api/private/sessions/301?site_id=1"} {
				service := &sessionExplorerStub{list: api.SessionList{Data: []api.SessionSummary{{Id: "301", ExternalId: &external, HasReplay: true}}}, detail: api.SessionDetail{Session: api.SessionSummary{Id: "301", ExternalId: &external, HasReplay: true}}}
				response := sessionRequest(t, service, principal, http.MethodGet, path)
				if response.Code != 200 || strings.Contains(response.Body.String(), external) != profile || strings.Contains(response.Body.String(), `"has_replay":true`) != replay {
					t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
				}
			}
			service := &sessionExplorerStub{}
			response := sessionRequest(t, service, principal, http.MethodGet, "/api/public/v1/sites/1/sessions?from=2026-10-01&to=2026-10-08&has_replay=false")
			want := http.StatusForbidden
			if replay {
				want = http.StatusOK
			}
			if response.Code != want {
				t.Fatalf("filter status=%d want=%d body=%s", response.Code, want, response.Body.String())
			}
			if !replay && service.listCalls != 0 {
				t.Fatal("replay filter reached data service without grant")
			}
		})
	}
}

type sensitiveEventsStub struct{ eventsExploreStub }

func (*sensitiveEventsStub) Events(context.Context, api.EventExploreRequest) (api.EventExploreList, error) {
	return api.EventExploreList{Data: []api.EventExploreItem{{SessionId: "301", HasReplay: true}}}, nil
}

func TestSensitiveEventReplayMetadata(t *testing.T) {
	for _, grant := range []bool{false, true} {
		scopes := []string{"stats:read"}
		if grant {
			scopes = append(scopes, "replay:read")
		}
		router := identifiedRouter(api.Deps{Query: &sensitiveEventsStub{}, PublicAuth: principalAuth{api.Principal{OrgID: 7, Scopes: scopes}}, Sites: orgSites{1: {ID: 1, OrgID: 7}}, RateLimiter: ingest.NewTokenBuckets(0, 30)})
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest("GET", "/api/public/v1/sites/1/events?from=2026-10-01&to=2026-10-08", nil))
		if response.Code != 200 || strings.Contains(response.Body.String(), `"has_replay":true`) != grant {
			t.Fatalf("grant=%v status=%d body=%s", grant, response.Code, response.Body.String())
		}
	}
}

func TestCSVExportRequiresSeparateScope(t *testing.T) {
	for _, endpoint := range []string{"/stats/summary?site_id=1&period=7d", "/stats/timeseries?site_id=1&period=7d", "/stats/breakdown?site_id=1&period=7d&dimension=page", "/query?unused=1"} {
		for _, variant := range []string{"header", "format", "stream-format", "stream-header"} {
			for _, grant := range []bool{false, true} {
				name := endpoint + "/" + variant
				if grant {
					name += "/granted"
				}
				t.Run(name, func(t *testing.T) {
					scopes := []string{"stats:read"}
					if grant {
						scopes = append(scopes, "data:export")
					}
					principal := api.Principal{OrgID: 7, Scopes: scopes}
					router := identifiedRouter(api.Deps{Query: queryStub{}, PublicAuth: principalAuth{principal}, PrivateAuth: principalAuth{principal}, Sites: orgSites{1: {ID: 1, OrgID: 7}}, RateLimiter: ingest.NewTokenBuckets(0, 30)})
					path := "/api/public/v1" + endpoint
					if strings.Contains(variant, "format") {
						path += "&format=csv"
					}
					if strings.Contains(variant, "stream") {
						path += "&limit=all"
					}
					method, body := "GET", ""
					if strings.Contains(endpoint, "query?") {
						method, body = "POST", `{"site_id":1,"period":"7d","metrics":["visitors"]}`
					}
					request := httptest.NewRequest(method, path, strings.NewReader(body))
					if strings.Contains(variant, "header") {
						request.Header.Set("Accept", "application/json, text/csv; charset=utf-8")
					}
					response := httptest.NewRecorder()
					router.ServeHTTP(response, request)
					want := http.StatusForbidden
					if grant {
						want = http.StatusOK
					}
					if response.Code != want {
						t.Fatalf("status=%d want=%d body=%s", response.Code, want, response.Body.String())
					}
				})
			}
		}
	}
}

func TestTenantVisibilityPrecedesMissingSensitiveScope(t *testing.T) {
	for _, p := range []api.Principal{{OrgID: 8}, {OrgID: 7, SiteIDs: []int32{}}, {OrgID: 7, SiteIDs: []int32{2}}} {
		response := sessionRequest(t, &sessionExplorerStub{}, p, http.MethodGet, "/api/public/v1/sites/1/sessions?from=2026-10-01&to=2026-10-08&has_replay=true")
		if response.Code != http.StatusNotFound {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	}
}

func TestProfileRetentionRequiresProfileRead(t *testing.T) {
	for _, grant := range []bool{false, true} {
		scopes := []string{"stats:read"}
		if grant {
			scopes = append(scopes, "profiles:read")
		}
		router := identifiedRouter(api.Deps{Query: queryStub{}, PublicAuth: principalAuth{api.Principal{OrgID: 7, Scopes: scopes}}, Sites: orgSites{1: {ID: 1, OrgID: 7}}, RateLimiter: ingest.NewTokenBuckets(0, 30)})
		request := httptest.NewRequest("POST", "/api/public/v1/retention/query", strings.NewReader(`{"site_id":1,"date_range":{"from":"2026-10-01","to":"2026-10-08"},"interval":"day","identity":"profile"}`))
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		want := http.StatusForbidden
		if grant {
			want = http.StatusOK
		}
		if response.Code != want {
			t.Fatalf("grant=%v status=%d body=%s", grant, response.Code, response.Body.String())
		}
	}
}
