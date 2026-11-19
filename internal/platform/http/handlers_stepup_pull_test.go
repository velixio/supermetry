package platformhttp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"supermetry/internal/platform/authz"
)

func TestStepUpGuardsPullImportAndGA4Connect(t *testing.T) {
	stale := false
	stub, queue := &pullStub{}, &pullQueue{}
	handler := pullHandlerStepUp(authz.RoleOwner, stub, queue, &importTestStore{}, toggleStepUp{stale: &stale})
	pullBody := `{"method":"plausible_api","remote_site_id":"example.com","api_key":"k1","from":"2026-01-01","to":"2026-01-02"}`
	calls := []struct{ name, path, body string }{
		{"pull import", "/api/private/sites/7/imports/pull", pullBody},
		{"ga4 connect", "/api/private/sites/7/imports/ga4/connect", ""},
	}
	for _, c := range calls {
		*stub, queue.jobs = pullStub{}, nil
		stale = true
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, pullRequestFor("POST", c.path, c.body))
		if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), `"code":"reauth_required"`) {
			t.Errorf("%s stale = %d %s", c.name, response.Code, response.Body)
		}
		if stub.created.Secret != "" || len(queue.jobs) != 0 {
			t.Errorf("%s: a stale request must not store credentials or queue work", c.name)
		}
		stale = false
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, pullRequestFor("POST", c.path, c.body))
		if response.Code != http.StatusAccepted && response.Code != http.StatusOK {
			t.Errorf("%s fresh = %d %s", c.name, response.Code, response.Body)
		}
	}
}
