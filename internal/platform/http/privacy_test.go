package platformhttp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"supermetry/internal/config"
	"supermetry/internal/platform/authz"
	"supermetry/internal/privacy"
	coresites "supermetry/internal/sites"
)

func ptr[T any](v T) *T { return &v }

func TestReplayPolicyRejectsEscalationAllowsTightening(t *testing.T) {
	t.Parallel()
	env := config.ReplayEnv{Enabled: true, Mask: ptr(true), SampleRate: ptr(20), Retention: ptr(30), QuotaBytes: ptr(int64(1000))}
	for _, tc := range []struct {
		name   string
		in     coresites.ReplaySiteInput
		reject bool
	}{
		{"mask weakening", coresites.ReplaySiteInput{Mask: ptr(false)}, true},
		{"sample escalation", coresites.ReplaySiteInput{SampleRate: ptr(21)}, true},
		{"retention escalation", coresites.ReplaySiteInput{RetentionDays: ptr(31)}, true},
		{"quota escalation", coresites.ReplaySiteInput{QuotaBytes: ptr(int64(1001))}, true},
		{"strict values", coresites.ReplaySiteInput{Enabled: true, Mask: ptr(true), SampleRate: ptr(10), RetentionDays: ptr(7), QuotaBytes: ptr(int64(500))}, false},
		{"unset", coresites.ReplaySiteInput{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateReplayPolicy(env, tc.in); (err != nil) != tc.reject {
				t.Fatalf("error=%v reject=%v", err, tc.reject)
			}
		})
	}
}

func TestAnonymousRejectsForgedProfileOptIn(t *testing.T) {
	owner := authz.Principal{UserID: 1, OrgID: 7, Role: authz.RoleOwner}
	route := New(Deps{Authenticator: importTestAuth{owner}, Sites: importTestSites{1: {ID: 1, OrgID: 7}}})
	request := httptest.NewRequest(http.MethodPatch, "/api/private/sites/1", strings.NewReader(`{"profiles_enabled":true}`))
	request.Header.Set("X-CSRF-Token", "csrf")
	request.AddCookie(&http.Cookie{Name: "supermetry_csrf", Value: "csrf"})
	response := httptest.NewRecorder()
	route.ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestTrackingPolicyKeepsConfiguredAndEffectiveDistinct(t *testing.T) {
	h := handler{deps: Deps{TrackingMode: privacy.Anonymous}}
	result := h.trackingPolicy(true)
	if !result.ConfiguredProfilesEnabled || result.EffectiveMode != "anonymous" || len(result.Restrictions) != 1 || result.Restrictions[0] != "instance_anonymous" {
		t.Fatalf("policy=%+v", result)
	}
}

func TestAnonymousBlocksImportsAndExportsBeforeStorage(t *testing.T) {
	owner := authz.Principal{UserID: 1, OrgID: 7, Role: authz.RoleOwner}
	route := New(Deps{Authenticator: importTestAuth{owner}, Sites: importTestSites{1: {ID: 1, OrgID: 7}}})
	for _, tc := range []struct{ name, method, path string }{
		{"import", http.MethodPost, "/api/private/sites/1/imports"},
		{"export", http.MethodGet, "/api/private/sites/1/export"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(tc.method, tc.path, nil)
			request.Header.Set("X-CSRF-Token", "csrf")
			request.AddCookie(&http.Cookie{Name: "supermetry_csrf", Value: "csrf"})
			response := httptest.NewRecorder()
			route.ServeHTTP(response, request)
			if response.Code != http.StatusForbidden || response.Header().Get("Content-Disposition") != "" {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

type privacyTestSites struct{ importTestSites }

func (s privacyTestSites) All() []coresites.Site {
	result := make([]coresites.Site, 0, len(s.importTestSites))
	for _, site := range s.importTestSites {
		result = append(result, site)
	}
	return result
}

func TestSiteResponseReportsConfiguredAndEffectivePrivacy(t *testing.T) {
	owner := authz.Principal{UserID: 1, OrgID: 7, Role: authz.RoleOwner}
	route := New(Deps{Authenticator: importTestAuth{owner}, Sites: privacyTestSites{importTestSites{1: {ID: 1, OrgID: 7, ProfilesEnabled: true}}}})
	for _, path := range []string{"/api/private/sites/1", "/api/private/sites"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		route.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", path, response.Code, response.Body.String())
		}
		for _, want := range []string{`"instance_mode":"anonymous"`, `"configured_profiles_enabled":true`, `"effective_mode":"anonymous"`, `"restrictions":["instance_anonymous"]`} {
			if !strings.Contains(response.Body.String(), want) {
				t.Fatalf("%s missing %s in %s", path, want, response.Body.String())
			}
		}
	}
}
