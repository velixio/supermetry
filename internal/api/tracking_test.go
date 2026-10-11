package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"supermetry/internal/api"
	"supermetry/internal/config"
	"supermetry/internal/privacy"
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
		{"historic URL mixed with legacy dimensions", "POST", "/query", `{"site_id":1,"metrics":["visitors"],"dimensions":["page","country"]}`, 403},
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
			wantCalls := 0
			if tc.want == http.StatusOK || tc.name == "CSV historical URL" {
				wantCalls = 1
			}
			// Page-shaped CSV reaches the service, then fails absent provenance.
			if calls != wantCalls {
				t.Fatalf("query calls=%d want=%d", calls, wantCalls)
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
		{"POST", "/api/private/query", `{"site_id":1,"metrics":["visitors"],"dimensions":["page","country"]}`},
	} {
		t.Run(tc.path, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			r.Header.Set("Authorization", "Bearer test-key")
			r.Header.Set("X-CSRF-Token", "test-csrf")
			r.AddCookie(&http.Cookie{Name: "supermetry_csrf", Value: "test-csrf"})
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
		{"traffic grid", `{"site_id":1,"metrics":["visitors","pageviews"],"dimensions":["weekday","hour_of_day"]}`, 200},
		{"country filter", `{"site_id":1,"metrics":["visitors"],"filters":[{"dimension":"country","operator":"is","values":["US"]}]}`, 200},
		{"traffic filter", `{"site_id":1,"metrics":["visitors"],"filters":[{"dimension":"hour_of_day","operator":"is","values":["23"]}]}`, 200},
		{"invalid traffic filter", `{"site_id":1,"metrics":["visitors"],"filters":[{"dimension":"weekday","operator":"is","values":["7"]}]}`, 403},
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

func TestAnonymousResponseKeepsCanonicalTrafficDimensions(t *testing.T) {
	input := api.QueryResponse{
		Columns: []string{"weekday", "hour_of_day", "visitors", "pageviews"},
		Rows:    []map[string]any{{"weekday": "0", "hour_of_day": "23", "visitors": float64(2), "pageviews": float64(4)}},
		Totals:  map[string]float64{"visitors": 2, "pageviews": 4},
		Meta:    api.QueryMeta{SourcesUsed: []string{"hot"}},
	}
	got := api.SanitizeAnonymousResponse(input, api.QueryRequest{Dimensions: &[]string{"weekday", "hour_of_day"}, Metrics: []api.Metric{api.Visitors, api.Pageviews}})
	if len(got.Rows) != 1 || got.Rows[0]["weekday"] != "0" || got.Rows[0]["hour_of_day"] != "23" {
		t.Fatalf("rows=%v", got.Rows)
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

func TestAnonymousVerifiedPathQueryShape(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		allowed    bool
	}{
		{"page counts", `{"metrics":["visitors","visits","pageviews"],"dimensions":["page"]}`, true},
		{"path comparison", `{"metrics":["visitors"],"dimensions":["path"],"compare":"previous_period","cursor":"Mg"}`, true},
		{"extra dimension", `{"metrics":["visitors"],"dimensions":["page","country"]}`, false},
		{"extra metric", `{"metrics":["visitors","bounce_rate"],"dimensions":["page"]}`, false},
		{"empty metrics", `{"metrics":[],"dimensions":["page"]}`, false},
		{"canonical filter still forbidden", `{"metrics":["visitors"],"dimensions":["page"],"filters":[{"dimension":"country","operator":"is","values":["US"]}]}`, false},
		{"non numeric cursor", `{"metrics":["visitors"],"dimensions":["page"],"cursor":"L3NlY3JldA"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var request api.QueryRequest
			if err := json.Unmarshal([]byte(tc.body), &request); err != nil {
				t.Fatal(err)
			}
			if api.IsVerifiedPathQuery(request) != tc.allowed {
				t.Fatalf("shape allowed=%v", tc.allowed)
			}
			if got := api.ValidateAnonymousQuery(request); (got == nil) != tc.allowed {
				t.Fatalf("allowed=%v err=%v", tc.allowed, got)
			}
		})
	}
}

func TestAnonymousVerifiedPathAlternateService(t *testing.T) {
	paths, err := privacy.NewPathPolicy([]string{"/public"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*api.QueryResponse)
		want   int
	}{
		{"verified public labels", func(*api.QueryResponse) {}, http.StatusOK},
		{"missing provenance", func(r *api.QueryResponse) { r.Meta.Privacy = nil }, http.StatusForbidden},
		{"wrong view", func(r *api.QueryResponse) { r.Meta.Privacy.View = "legacy" }, http.StatusForbidden},
		{"wrong coverage", func(r *api.QueryResponse) { r.Meta.Privacy.Coverage = "all" }, http.StatusForbidden},
		{"old policy", func(r *api.QueryResponse) { r.Meta.Privacy.PolicyId = "private-policy" }, http.StatusForbidden},
		{"old sanitizer", func(r *api.QueryResponse) { r.Meta.Privacy.PathSanitizerVersion++ }, http.StatusForbidden},
		{"unverified included", func(r *api.QueryResponse) { r.Meta.Privacy.UnverifiedExcluded = false }, http.StatusForbidden},
		{"import coverage", func(r *api.QueryResponse) { r.Meta.Privacy.ImportsExcluded = false }, http.StatusForbidden},
		{"imports included", func(r *api.QueryResponse) { r.Meta.ImportedIncluded = true }, http.StatusForbidden},
		{"legacy source", func(r *api.QueryResponse) { r.Meta.SourcesUsed = []string{"hot"} }, http.StatusForbidden},
		{"removed path", func(r *api.QueryResponse) { r.Rows[0]["page"] = "/removed-private" }, http.StatusForbidden},
		{"comparison unverified label", func(r *api.QueryResponse) { r.Comparison.Rows[0]["page"] = "/private" }, http.StatusForbidden},
		{"non string label", func(r *api.QueryResponse) { r.Rows[0]["page"] = 42 }, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := queryFunc{query: func(api.QueryRequest) (api.QueryResponse, error) {
				response := api.QueryResponse{
					Columns:    []string{"page", "visitors", "private"},
					Rows:       []map[string]any{{"page": "/public", "visitors": 3, "private": "secret"}},
					Totals:     map[string]float64{"visitors": 3, "private": 1},
					Meta:       api.QueryMeta{SourcesUsed: []string{"verified_hot", "verified_day"}, Privacy: &api.QueryPrivacy{View: "verified_paths", Coverage: "verified_only", PathSanitizerVersion: int(privacy.PathSanitizerVersion), PolicyId: paths.Fingerprint(), UnverifiedExcluded: true, ImportsExcluded: true}},
					Comparison: &api.ComparisonResult{Rows: []map[string]any{{"page": privacy.RedactedPath, "visitors": 2}}, Totals: map[string]float64{"visitors": 2}},
				}
				tc.change(&response)
				return response, nil
			}}
			router := api.NewRouter(api.Deps{Config: config.Config{AdminAPIKey: "test-key", TrackingPaths: paths}, Query: service})
			r := httptest.NewRequest("POST", "/api/public/v1/query", strings.NewReader(`{"site_id":1,"metrics":["visitors"],"dimensions":["page"],"compare":"previous_period"}`))
			r.Header.Set("Authorization", "Bearer test-key")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			if w.Code != tc.want || strings.Contains(w.Body.String(), "private") || strings.Contains(w.Body.String(), "secret") {
				t.Fatalf("status=%d want=%d body=%s", w.Code, tc.want, w.Body.String())
			}
			if tc.want == http.StatusOK {
				var result api.QueryResponse
				if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if result.Rows[0]["page"] != "/public" || result.Totals["visitors"] != 3 || result.Comparison.Rows[0]["page"] != privacy.RedactedPath || result.Meta.Privacy == nil {
					t.Fatalf("response=%s", w.Body.String())
				}
			}
		})
	}
}
