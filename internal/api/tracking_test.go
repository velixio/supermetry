package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"panel/internal/api"
	"panel/internal/config"
)

func TestAnonymousHistoryRoutesAndQueries(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, body string
		want                     int
	}{
		{"summary", "GET", "/stats/summary?site_id=1&period=7d", "", 200},
		{"raw sessions", "GET", "/sites/1/sessions", "", 403},
		{"session timeline", "GET", "/sessions/123?site_id=1", "", 403},
		{"raw events", "GET", "/sites/1/events", "", 403},
		{"property names", "GET", "/sites/1/properties", "", 403},
		{"property values", "GET", "/sites/1/properties/email/values", "", 403},
		{"suggestions", "GET", "/sites/1/suggestions", "", 403},
		{"realtime detail", "GET", "/realtime?site_id=1", "", 403},
		{"heatmap", "POST", "/heatmaps/query", `{}`, 403},
		{"journey", "POST", "/journeys/query", `{}`, 403},
		{"funnel", "POST", "/funnels/query", `{}`, 403},
		{"retention", "POST", "/retention/query", `{}`, 403},
		{"historic URL", "POST", "/query", `{"site_id":1,"metrics":["visitors"],"dimensions":["page"]}`, 403},
		{"identity inference", "POST", "/query", `{"site_id":1,"metrics":["visitors"],"filters":[{"dimension":"prop:email","operator":"is","values":["private"]}]}`, 403},
		{"CSV historical URL", "GET", "/stats/breakdown?site_id=1&period=7d&dimension=page&format=csv", "", 403},
		{"count", "POST", "/query", `{"site_id":1,"metrics":["visitors"]}`, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			service := queryFunc{query: func(api.QueryRequest) (api.QueryResponse, error) { calls++; return emptyQueryResponse(), nil }}
			router := api.NewRouter(api.Deps{Config: config.Config{AdminAPIKey: "test-key"}, Query: service})
			request := httptest.NewRequest(tc.method, "/api/public/v1"+tc.path, strings.NewReader(tc.body))
			request.Header.Set("Authorization", "Bearer test-key")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != tc.want {
				t.Fatalf("status=%d want=%d body=%s", response.Code, tc.want, response.Body.String())
			}
			if tc.want == http.StatusForbidden && calls != 0 {
				t.Fatalf("restricted request reached query service: %d", calls)
			}
		})
	}
}

func TestAnonymousPrivateRequestsCannotBypassPolicy(t *testing.T) {
	router := api.NewRouter(api.Deps{PrivateAuth: api.StaticKey("test-key"), Query: queryFunc{query: func(api.QueryRequest) (api.QueryResponse, error) {
		t.Fatal("restricted private request reached service")
		return api.QueryResponse{}, nil
	}}})
	for _, tc := range []struct{ method, path, body string }{
		{"GET", "/api/private/sites/1/sessions", ""},
		{"GET", "/api/private/realtime?site_id=1", ""},
		{"POST", "/api/private/query", `{"site_id":1,"metrics":["visitors"],"dimensions":["page"]}`},
	} {
		t.Run(tc.path, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			r.Header.Set("Authorization", "Bearer test-key")
			r.Header.Set("X-CSRF-Token", "test-csrf")
			r.AddCookie(&http.Cookie{Name: "panel_csrf", Value: "test-csrf"})
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "anonymous tracking policy") {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

func TestAnonymousSafeDimensionsAndCanonicalFilters(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       int
	}{
		{"country", `{"site_id":1,"metrics":["visitors"],"dimensions":["country"]}`, 200},
		{"device", `{"site_id":1,"metrics":["visitors"],"dimensions":["device"]}`, 200},
		{"country filter", `{"site_id":1,"metrics":["visitors"],"filters":[{"dimension":"country","operator":"is","values":["US"]}]}`, 200},
		{"hostile filter", `{"site_id":1,"metrics":["visitors"],"filters":[{"dimension":"browser","operator":"is","values":["private"]}]}`, 403},
		{"probe filter", `{"site_id":1,"metrics":["visitors"],"filters":[{"dimension":"browser","operator":"contains","values":["Chrome"]}]}`, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router := api.NewRouter(api.Deps{Config: config.Config{AdminAPIKey: "test-key"}, Query: queryFunc{query: func(api.QueryRequest) (api.QueryResponse, error) {
				cursor := "cHJpdmF0ZQ"
				return api.QueryResponse{Rows: []map[string]any{{"country": "private", "device": "private", "browser": "private", "profile_id": "private", "visitors": 2}}, NextCursor: &cursor}, nil
			}}})
			r := httptest.NewRequest("POST", "/api/public/v1/query", strings.NewReader(tc.body))
			r.Header.Set("Authorization", "Bearer test-key")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			if w.Code != tc.want || strings.Contains(w.Body.String(), "private") {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

func TestAnonymousResponseSanitizesComparisonsAndCursors(t *testing.T) {
	privateCursor := "cHJpdmF0ZQ"
	input := api.QueryResponse{Columns: []string{"country", "private"}, Rows: []map[string]any{{"country": "private", "visitors": float64(2), "private": "secret"}}, Totals: map[string]float64{"visitors": 2, "private": 1}, Meta: api.QueryMeta{SourcesUsed: []string{"hot", "private"}}, NextCursor: &privateCursor, Comparison: &api.ComparisonResult{Rows: []map[string]any{{"country": "private", "visitors": float64(1)}}, Totals: map[string]float64{"visitors": 1, "private": 1}}}
	got := api.SanitizeAnonymousResponse(input, api.QueryRequest{Dimensions: &[]string{"country"}, Metrics: []api.Metric{api.Visitors}})
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "private") || got.NextCursor != nil || got.Rows[0]["country"] != "" || got.Comparison.Rows[0]["country"] != "" {
		t.Fatalf("response=%s", encoded)
	}
	if got.Totals["visitors"] != 2 || got.Comparison.Totals["visitors"] != 1 {
		t.Fatalf("counts=%s", encoded)
	}
}
