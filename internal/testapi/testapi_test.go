package testapi_test

import (
	"net/http"
	"testing"

	"supermetry/internal/testapi"
)

func TestEnvExpandsPlaceholdersAndDecodesProblems(t *testing.T) {
	env := testapi.New(t)
	org := env.Org(t)
	site := env.Site(t, org)
	key := env.APIKey(t, org, "stats:read")

	response := env.Do(t, testapi.Req{
		Name:   "malformed session id",
		Method: http.MethodGet,
		Path:   "/api/public/v1/sessions/{id}?site_id={site}",
		APIKey: key,
		Values: map[string]string{"site": site.String(), "id": "bad"},
	})
	if response.Status != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Status, response.Body)
	}
	if response.Problem == nil || response.Problem.Type == "" || response.Problem.Status != http.StatusBadRequest {
		t.Fatalf("problem=%+v", response.Problem)
	}
}
