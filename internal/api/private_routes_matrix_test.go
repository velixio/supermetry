package api_test

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"supermetry/internal/testapi"
)

type privateMatrixRoute struct {
	name   string
	method string
	path   string
	body   string
}

// TestPrivateRoutesMatrixAuthentication covers the session-authenticated half
// of the API as a single inventory-shaped table. Concrete placeholder values
// ensure each row reaches its registered pattern before authentication fails.
func TestPrivateRoutesMatrixAuthentication(t *testing.T) {
	env := testapi.New(t)
	routes := privateMatrixRoutes()
	for _, route := range routes {
		t.Run(route.name, func(t *testing.T) {
			headers := make(http.Header)
			if route.method != http.MethodGet && route.method != http.MethodHead && route.method != http.MethodOptions {
				headers.Set("X-CSRF-Token", "matrix")
				headers.Set("Cookie", "supermetry_csrf=matrix")
			}
			response := env.Do(t, testapi.Req{
				Name: t.Name(), Method: route.method, Path: route.path,
				Body: bodyOrNil(route.body), Headers: headers,
			})
			if response.Status != http.StatusUnauthorized {
				t.Fatalf("status=%d body=%s, want 401", response.Status, response.Body)
			}
			assertPrivateProblem(t, response, http.StatusUnauthorized)
		})
	}
}

func TestPrivateRoutesMatrixWritesRequireCSRF(t *testing.T) {
	env := testapi.New(t)
	org := env.Org(t)
	session := env.Session(t, org.Owner)
	for _, route := range privateMatrixRoutes() {
		if route.method == http.MethodGet || route.method == http.MethodHead || route.method == http.MethodOptions || strings.HasPrefix(route.path, "/api/private/share/") {
			continue
		}
		t.Run(route.name, func(t *testing.T) {
			response := env.Do(t, testapi.Req{
				Name: t.Name(), Method: route.method, Path: route.path,
				Body: bodyOrNil(route.body), Cookie: session,
			})
			if response.Status != http.StatusForbidden {
				t.Fatalf("status=%d body=%s, want 403", response.Status, response.Body)
			}
			assertPrivateProblem(t, response, http.StatusForbidden)

			invalid := env.Do(t, testapi.Req{
				Name: t.Name() + "/invalid", Method: route.method, Path: route.path,
				Body: bodyOrNil(route.body), Cookie: session,
				Headers: http.Header{
					"X-CSRF-Token": []string{"header"},
					"Cookie":       []string{session.String() + "; supermetry_csrf=cookie"},
				},
			})
			if invalid.Status != http.StatusForbidden {
				t.Fatalf("invalid CSRF status=%d body=%s, want 403", invalid.Status, invalid.Body)
			}
			assertPrivateProblem(t, invalid, http.StatusForbidden)
		})
	}
}

func TestPrivateResourcesMatrixRoleSiteAndObjectBoundaries(t *testing.T) {
	env := testapi.New(t)
	org := env.Org(t)
	allowedSite := env.Site(t, org)
	otherSite := env.Site(t, org)
	viewer := env.Member(t, org, "viewer", []int32{allowedSite.ID})
	editor := env.Member(t, org, "editor", []int32{allowedSite.ID})
	viewerSession := env.Session(t, viewer)
	editorSession := env.Session(t, editor)
	ownerSession := env.Session(t, org.Owner)
	foreignOrg := env.Org(t)
	foreignSession := env.Session(t, foreignOrg.Owner)

	for _, test := range []struct {
		name   string
		method string
		path   string
		body   string
		cookie *http.Cookie
		want   int
	}{
		{name: "viewer reads allowed site", method: http.MethodGet, path: fmt.Sprintf("/api/private/sites/%d", allowedSite.ID), cookie: viewerSession, want: http.StatusOK},
		{name: "viewer cannot read unassigned site", method: http.MethodGet, path: fmt.Sprintf("/api/private/sites/%d", otherSite.ID), cookie: viewerSession, want: http.StatusNotFound},
		{name: "editor cannot read unassigned site", method: http.MethodGet, path: fmt.Sprintf("/api/private/sites/%d", otherSite.ID), cookie: editorSession, want: http.StatusNotFound},
		{name: "foreign owner cannot read site", method: http.MethodGet, path: fmt.Sprintf("/api/private/sites/%d", allowedSite.ID), cookie: foreignSession, want: http.StatusNotFound},
		{name: "viewer cannot create site", method: http.MethodPost, path: "/api/private/sites", body: `{"name":"Denied","hosts":["denied.example.test"],"timezone":"UTC","retention_days":30}`, cookie: viewerSession, want: http.StatusForbidden},
		{name: "viewer cannot update site", method: http.MethodPatch, path: fmt.Sprintf("/api/private/sites/%d", allowedSite.ID), body: `{"name":"Denied"}`, cookie: viewerSession, want: http.StatusForbidden},
		{name: "viewer cannot delete site", method: http.MethodDelete, path: fmt.Sprintf("/api/private/sites/%d", allowedSite.ID), cookie: viewerSession, want: http.StatusForbidden},
		{name: "viewer cannot create goal", method: http.MethodPost, path: fmt.Sprintf("/api/private/sites/%d/goals", allowedSite.ID), body: `{"kind":"page","match":"exact","value":"/done","name":"Denied"}`, cookie: viewerSession, want: http.StatusForbidden},
		{name: "viewer cannot create funnel", method: http.MethodPost, path: fmt.Sprintf("/api/private/sites/%d/funnels", allowedSite.ID), body: `{"name":"Denied","steps":[]}`, cookie: viewerSession, want: http.StatusForbidden},
		{name: "viewer cannot create annotation", method: http.MethodPost, path: fmt.Sprintf("/api/private/sites/%d/annotations", allowedSite.ID), body: `{"day":"2026-10-12","text":"Denied"}`, cookie: viewerSession, want: http.StatusForbidden},
		{name: "viewer cannot create segment", method: http.MethodPost, path: fmt.Sprintf("/api/private/sites/%d/segments", allowedSite.ID), body: `{"name":"Denied","filters":[]}`, cookie: viewerSession, want: http.StatusForbidden},
		{name: "viewer cannot create import", method: http.MethodPost, path: fmt.Sprintf("/api/private/sites/%d/imports", allowedSite.ID), body: `{}`, cookie: viewerSession, want: http.StatusForbidden},
		{name: "editor cannot update unassigned site", method: http.MethodPatch, path: fmt.Sprintf("/api/private/sites/%d", otherSite.ID), body: `{"name":"Denied"}`, cookie: editorSession, want: http.StatusNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := testapi.Req{Name: t.Name(), Method: test.method, Path: test.path, Body: bodyOrNil(test.body), Cookie: test.cookie}
			if test.method != http.MethodGet {
				request.Cookie = nil
				request.Headers = testapi.SessionHeaders(test.cookie)
			}
			response := env.Do(t, request)
			if response.Status != test.want {
				t.Fatalf("status=%d body=%s, want %d", response.Status, response.Body, test.want)
			}
			if test.want >= 400 {
				assertPrivateProblem(t, response, test.want)
			}
		})
	}

	objects := []struct {
		name       string
		collection string
		body       string
		item       string
		updateBody string
	}{
		{name: "goal", collection: fmt.Sprintf("/api/private/sites/%d/goals", allowedSite.ID), body: `{"kind":"page","match":"exact","value":"/done","name":"Done"}`, item: "/api/private/goals/%d", updateBody: `{"kind":"page","match":"exact","value":"/done","name":"Changed"}`},
		{name: "funnel", collection: fmt.Sprintf("/api/private/sites/%d/funnels", allowedSite.ID), body: `{"name":"Signup","steps":[{"type":"page","match":"exact","value":"/"},{"type":"page","match":"exact","value":"/done"}]}`, item: "/api/private/funnels/%d", updateBody: `{"name":"Changed","steps":[{"type":"page","match":"exact","value":"/"},{"type":"page","match":"exact","value":"/done"}]}`},
		{name: "annotation", collection: fmt.Sprintf("/api/private/sites/%d/annotations", allowedSite.ID), body: `{"day":"2026-10-12","text":"Release"}`, item: "/api/private/annotations/%d", updateBody: `{"text":"Changed"}`},
		{name: "segment", collection: fmt.Sprintf("/api/private/sites/%d/segments", allowedSite.ID), body: `{"name":"Paid","filters":[{"dimension":"source","operator":"contains","values":["ads"]}]}`, item: "/api/private/segments/%d", updateBody: `{"name":"Changed","filters":[{"dimension":"source","operator":"contains","values":["ads"]}]}`},
	}
	for _, object := range objects {
		t.Run(object.name, func(t *testing.T) {
			created := env.Do(t, testapi.Req{Name: t.Name() + "/create", Method: http.MethodPost, Path: object.collection, Body: object.body, Headers: testapi.SessionHeaders(ownerSession)})
			if created.Status != http.StatusCreated {
				t.Fatalf("create status=%d body=%s", created.Status, created.Body)
			}
			id := matrixJSONID(t, created.JSON)
			path := fmt.Sprintf(object.item, id)
			for _, mutation := range []struct {
				name   string
				method string
				body   string
			}{
				{name: "foreign update", method: http.MethodPatch, body: object.updateBody},
				{name: "foreign delete", method: http.MethodDelete},
			} {
				t.Run(mutation.name, func(t *testing.T) {
					response := env.Do(t, testapi.Req{Name: t.Name(), Method: mutation.method, Path: path, Body: bodyOrNil(mutation.body), Headers: testapi.SessionHeaders(foreignSession)})
					if response.Status != http.StatusForbidden && response.Status != http.StatusNotFound {
						t.Fatalf("status=%d body=%s, want 403/404", response.Status, response.Body)
					}
					assertPrivateProblem(t, response, response.Status)
				})
			}
		})
	}
}

func matrixJSONID(t *testing.T, value any) int64 {
	t.Helper()
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("response JSON=%T, want object", value)
	}
	number, ok := object["id"].(float64)
	if !ok || number < 1 {
		t.Fatalf("response id=%v", object["id"])
	}
	id, err := strconv.ParseInt(strconv.FormatFloat(number, 'f', 0, 64), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func privateMatrixRoutes() []privateMatrixRoute {
	return []privateMatrixRoute{
		{name: "sites list", method: http.MethodGet, path: "/api/private/sites"},
		{name: "sites create", method: http.MethodPost, path: "/api/private/sites", body: `{}`},
		{name: "site get", method: http.MethodGet, path: "/api/private/sites/1"},
		{name: "site snippet", method: http.MethodGet, path: "/api/private/sites/1/snippet"},
		{name: "site icon refetch", method: http.MethodPost, path: "/api/private/sites/1/icon/refetch"},
		{name: "site update", method: http.MethodPatch, path: "/api/private/sites/1", body: `{}`},
		{name: "site delete", method: http.MethodDelete, path: "/api/private/sites/1"},
		{name: "site restore", method: http.MethodPost, path: "/api/private/sites/1/restore"},
		{name: "purges list", method: http.MethodGet, path: "/api/private/sites/1/purge-requests"},
		{name: "purges create", method: http.MethodPost, path: "/api/private/sites/1/purge-requests", body: `{}`},
		{name: "goals list", method: http.MethodGet, path: "/api/private/sites/1/goals"},
		{name: "goals create", method: http.MethodPost, path: "/api/private/sites/1/goals", body: `{}`},
		{name: "goal update", method: http.MethodPatch, path: "/api/private/goals/1", body: `{}`},
		{name: "goal delete", method: http.MethodDelete, path: "/api/private/goals/1"},
		{name: "annotations list", method: http.MethodGet, path: "/api/private/sites/1/annotations"},
		{name: "annotations create", method: http.MethodPost, path: "/api/private/sites/1/annotations", body: `{}`},
		{name: "annotation update", method: http.MethodPatch, path: "/api/private/annotations/1", body: `{}`},
		{name: "annotation delete", method: http.MethodDelete, path: "/api/private/annotations/1"},
		{name: "funnels list", method: http.MethodGet, path: "/api/private/sites/1/funnels"},
		{name: "funnels create", method: http.MethodPost, path: "/api/private/sites/1/funnels", body: `{}`},
		{name: "funnel update", method: http.MethodPatch, path: "/api/private/funnels/1", body: `{}`},
		{name: "funnel delete", method: http.MethodDelete, path: "/api/private/funnels/1"},
		{name: "segments list", method: http.MethodGet, path: "/api/private/sites/1/segments"},
		{name: "segments create", method: http.MethodPost, path: "/api/private/sites/1/segments", body: `{}`},
		{name: "segment update", method: http.MethodPatch, path: "/api/private/segments/1", body: `{}`},
		{name: "segment delete", method: http.MethodDelete, path: "/api/private/segments/1"},
		{name: "imports create", method: http.MethodPost, path: "/api/private/sites/1/imports", body: `{}`},
		{name: "imports list", method: http.MethodGet, path: "/api/private/sites/1/imports"},
		{name: "import delete", method: http.MethodDelete, path: "/api/private/imports/1"},
		{name: "site export", method: http.MethodGet, path: "/api/private/sites/1/export?from=2026-10-01&to=2026-10-02"},
		{name: "profiles list", method: http.MethodGet, path: "/api/private/sites/1/profiles"},
		{name: "profile get", method: http.MethodGet, path: "/api/private/profiles/1"},
		{name: "profile erase", method: http.MethodDelete, path: "/api/private/profiles/1"},
		{name: "erasure get", method: http.MethodGet, path: "/api/private/erasures/1"},
		{name: "erasure retry", method: http.MethodPost, path: "/api/private/erasures/1/retry"},
		{name: "replays list", method: http.MethodGet, path: "/api/private/sites/1/replays?from=2026-10-01&to=2026-10-02"},
		{name: "replay asset", method: http.MethodGet, path: "/api/private/sites/1/replays/1/data"},
		{name: "replay settings get", method: http.MethodGet, path: "/api/private/sites/1/replay-settings"},
		{name: "replay settings update", method: http.MethodPut, path: "/api/private/sites/1/replay-settings", body: `{}`},
		{name: "demo heartbeat", method: http.MethodPost, path: "/api/private/sites/1/demo/heartbeat"},
		{name: "shares create", method: http.MethodPost, path: "/api/private/sites/1/shares", body: `{}`},
		{name: "shares list", method: http.MethodGet, path: "/api/private/sites/1/shares"},
		{name: "share update", method: http.MethodPatch, path: "/api/private/sites/1/shares/1", body: `{}`},
		{name: "share delete", method: http.MethodDelete, path: "/api/private/sites/1/shares/1"},
		{name: "reports create", method: http.MethodPost, path: "/api/private/sites/1/reports", body: `{}`},
		{name: "reports list", method: http.MethodGet, path: "/api/private/sites/1/reports"},
		{name: "report delete", method: http.MethodDelete, path: "/api/private/sites/1/reports/1"},
		{name: "alerts list", method: http.MethodGet, path: "/api/private/sites/1/alerts"},
		{name: "alerts create", method: http.MethodPost, path: "/api/private/sites/1/alerts", body: `{}`},
		{name: "alert update", method: http.MethodPatch, path: "/api/private/sites/1/alerts/1", body: `{}`},
		{name: "alert delete", method: http.MethodDelete, path: "/api/private/sites/1/alerts/1"},
		{name: "notification history", method: http.MethodGet, path: "/api/private/sites/1/notification-history"},
		{name: "notification retry", method: http.MethodPost, path: "/api/private/sites/1/notification-history/1/retry"},
		{name: "notification templates", method: http.MethodGet, path: "/api/private/orgs/1/notification-templates"},
		{name: "notification template save", method: http.MethodPut, path: "/api/private/orgs/1/notification-templates/email", body: `{}`},
		{name: "notification template reset", method: http.MethodDelete, path: "/api/private/orgs/1/notification-templates/email"},
		{name: "notification template preview", method: http.MethodPost, path: "/api/private/orgs/1/notification-templates/email/preview", body: `{}`},
		{name: "notification settings", method: http.MethodPut, path: "/api/private/orgs/1/notification-settings", body: `{}`},
		{name: "share stats", method: http.MethodGet, path: "/api/private/share/missing/stats/summary?period=7d"},
		{name: "share query", method: http.MethodPost, path: "/api/private/share/missing/query", body: `{}`},
		{name: "events", method: http.MethodGet, path: "/api/private/sites/1/events?from=2026-10-01&to=2026-10-02"},
		{name: "properties", method: http.MethodGet, path: "/api/private/sites/1/properties?from=2026-10-01&to=2026-10-02"},
		{name: "property values", method: http.MethodGet, path: "/api/private/sites/1/properties/plan/values?from=2026-10-01&to=2026-10-02"},
		{name: "suggestions", method: http.MethodGet, path: "/api/private/sites/1/suggestions?from=2026-10-01&to=2026-10-02&dimension=page"},
		{name: "sessions list", method: http.MethodGet, path: "/api/private/sites/1/sessions?from=2026-10-01&to=2026-10-02"},
		{name: "session get", method: http.MethodGet, path: "/api/private/sessions/1?site_id=1"},
		{name: "stats summary", method: http.MethodGet, path: "/api/private/stats/summary?site_id=1&period=7d"},
		{name: "stats timeseries", method: http.MethodGet, path: "/api/private/stats/timeseries?site_id=1&period=7d"},
		{name: "stats breakdown", method: http.MethodGet, path: "/api/private/stats/breakdown?site_id=1&period=7d&dimension=page"},
		{name: "query", method: http.MethodPost, path: "/api/private/query", body: `{}`},
		{name: "heatmaps query", method: http.MethodPost, path: "/api/private/heatmaps/query", body: `{}`},
		{name: "vitals query", method: http.MethodPost, path: "/api/private/vitals/query", body: `{}`},
		{name: "funnels query", method: http.MethodPost, path: "/api/private/funnels/query", body: `{}`},
		{name: "journeys query", method: http.MethodPost, path: "/api/private/journeys/query", body: `{}`},
		{name: "retention query", method: http.MethodPost, path: "/api/private/retention/query", body: `{}`},
		{name: "realtime", method: http.MethodGet, path: "/api/private/realtime?site_id=1"},
		{name: "realtime summary", method: http.MethodGet, path: "/api/private/realtime/summary?site_id=1"},
		{name: "health", method: http.MethodGet, path: "/api/private/health"},
	}
}

func assertPrivateProblem(t *testing.T, response testapi.Resp, want int) {
	t.Helper()
	if response.Problem == nil || response.Headers.Get("Content-Type") != "application/problem+json" {
		t.Fatalf("status=%d content-type=%q body=%s, want problem+json", response.Status, response.Headers.Get("Content-Type"), response.Body)
	}
	if response.Problem.Status != want {
		t.Fatalf("problem status=%d, want %d", response.Problem.Status, want)
	}
}
