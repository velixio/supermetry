package platformhttp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"supermetry/internal/platform/authz"
	"supermetry/internal/privacy"
)

func TestSensitiveReadCapabilitiesBeforeStorage(t *testing.T) {
	for _, route := range []struct {
		path  string
		grant string
	}{
		{"/api/private/sites/1/profiles", "profiles.read"},
		{"/api/private/sites/1/replays?from=2026-10-01&to=2026-10-09", "replay.read"},
		{"/api/private/sites/1/replays/123/data", "replay.read"},
		{"/api/private/sites/1/replays/css/" + strings.Repeat("a", 64), "replay.read"},
	} {
		for _, role := range []authz.Role{authz.RoleEditor, authz.RoleViewer} {
			for _, grant := range []bool{false, true} {
				principal := authz.Principal{OrgID: 7, Role: role}
				if grant {
					principal.Capabilities = []string{route.grant}
				}
				h := New(Deps{TrackingMode: privacy.Identified, Authenticator: importTestAuth{principal}, Sites: importTestSites{1: {ID: 1, OrgID: 7}}})
				response := httptest.NewRecorder()
				h.ServeHTTP(response, httptest.NewRequest("GET", route.path, nil))
				want := http.StatusForbidden
				if grant {
					want = http.StatusInternalServerError
				}
				if response.Code != want {
					t.Fatalf("role=%s path=%s grant=%v status=%d want=%d body=%s", role, route.path, grant, response.Code, want, response.Body.String())
				}
			}
		}
	}
}

func TestSensitiveReadsHideTenantAndRestrictedSites(t *testing.T) {
	for _, principal := range []authz.Principal{{OrgID: 8, Role: authz.RoleViewer}, {OrgID: 7, Role: authz.RoleViewer, SiteIDs: []int32{}}, {OrgID: 7, Role: authz.RoleViewer, SiteIDs: []int32{2}, Capabilities: []string{"profiles.read", "replay.read"}}} {
		for _, path := range []string{"/api/private/sites/1/profiles", "/api/private/sites/1/replays", "/api/private/sites/1/replays/123/data", "/api/private/sites/1/replays/css/" + strings.Repeat("a", 64)} {
			h := New(Deps{TrackingMode: privacy.Identified, Authenticator: importTestAuth{principal}, Sites: importTestSites{1: {ID: 1, OrgID: 7}}})
			response := httptest.NewRecorder()
			h.ServeHTTP(response, httptest.NewRequest("GET", path, nil))
			if response.Code != http.StatusNotFound {
				t.Fatalf("path=%s status=%d body=%s", path, response.Code, response.Body.String())
			}
		}
	}
}

func TestExportCapabilityIsIndependentOfEditing(t *testing.T) {
	for _, role := range []authz.Role{authz.RoleEditor, authz.RoleViewer} {
		for _, grant := range []bool{false, true} {
			principal := authz.Principal{OrgID: 7, Role: role}
			if grant {
				principal.Capabilities = []string{"data.export"}
			}
			h := New(Deps{TrackingMode: privacy.Identified, Authenticator: importTestAuth{principal}, Sites: importTestSites{1: {ID: 1, OrgID: 7}}, Exporter: importTestExporter{}})
			response := httptest.NewRecorder()
			h.ServeHTTP(response, httptest.NewRequest("GET", "/api/private/sites/1/export?from=2026-10-01&to=2026-10-09", nil))
			want := http.StatusForbidden
			if grant {
				want = http.StatusOK
			}
			if response.Code != want {
				t.Fatalf("role=%s grant=%v status=%d want=%d body=%s", role, grant, response.Code, want, response.Body.String())
			}
		}
	}
}

func TestCombinedSensitiveExportNeedsEveryGrant(t *testing.T) {
	for _, capabilities := range [][]string{{}, {"data.export"}, {"data.export", "profiles.read"}, {"data.export", "replay.read"}, {"profiles.read", "replay.read"}, {"data.export", "profiles.read", "replay.read"}} {
		for _, variant := range []string{"header", "format", "stream"} {
			request := httptest.NewRequest("GET", "/api/private/sites/1/export", nil)
			switch variant {
			case "header":
				request.Header.Set("Accept", "text/csv")
			case "format":
				request.URL.RawQuery = "format=csv"
			case "stream":
				request.URL.RawQuery = "limit=all"
			}
			response := httptest.NewRecorder()
			principal := authz.Principal{OrgID: 7, Role: authz.RoleViewer, Capabilities: capabilities}
			ok := authorizeSensitiveExport(response, request, principal, 1, authz.ActionProfilesRead, authz.ActionReplayRead)
			if ok != (len(capabilities) == 3) {
				t.Fatalf("grants=%v variant=%s allowed=%v", capabilities, variant, ok)
			}
			if !ok && response.Code != 403 {
				t.Fatalf("status=%d", response.Code)
			}
		}
	}
}
