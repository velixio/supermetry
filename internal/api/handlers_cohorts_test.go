package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"supermetry/internal/api"
	"supermetry/internal/config"
	"supermetry/internal/ingest"
)

type cohortStub struct {
	evaluation api.CohortEvaluation
	power      api.PowerUsers
	err        error
	calls      int
	cohortID   int64
	definition *api.CohortDefinition
	sample     int
	allowed    bool
	metric     string
	minimum    int
}

func (s *cohortStub) EvaluateCohort(_ context.Context, _ int32, cohortID int64, definition *api.CohortDefinition, sampleLimit int, includeSample bool) (api.CohortEvaluation, error) {
	s.calls++
	s.cohortID, s.definition, s.sample, s.allowed = cohortID, definition, sampleLimit, includeSample
	return s.evaluation, s.err
}

func (s *cohortStub) PowerUsers(_ context.Context, _ int32, _, _ time.Time, metric string, minimum, _ int) (api.PowerUsers, error) {
	s.calls++
	s.metric, s.minimum = metric, minimum
	return s.power, s.err
}

func cohortRequest(t *testing.T, service *cohortStub, principal api.Principal, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	router := identifiedRouter(api.Deps{
		Config:      config.Config{},
		Cohorts:     service,
		PrivateAuth: principalAuth{principal: principal},
		PublicAuth:  principalAuth{principal: principal},
		Sites:       orgSites{1: {ID: 1, OrgID: 7}},
		RateLimiter: ingest.NewTokenBuckets(0, 30),
	})
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CSRF-Token", "token")
	request.AddCookie(&http.Cookie{Name: "supermetry_csrf", Value: "token"})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

const evaluatePath = "/api/private/sites/1/cohorts/evaluate"

func TestEvaluateCohortEndpointGatesSamplesBehindProfileScope(t *testing.T) {
	stub := &cohortStub{evaluation: api.CohortEvaluation{Size: 3, SampleWithheld: true}}
	reader := api.Principal{OrgID: 7, Scopes: []string{"stats:read"}, SiteIDs: []int32{1}}
	response := cohortRequest(t, stub, reader, http.MethodPost, evaluatePath, `{"cohort_id":5,"sample_limit":10}`)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"sample":[]`) || !strings.Contains(response.Body.String(), `"sample_withheld":true`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if stub.cohortID != 5 || stub.sample != 10 || stub.allowed {
		t.Fatalf("service saw cohort %d sample %d includeSample %v; a stats-only caller must not be allowed samples", stub.cohortID, stub.sample, stub.allowed)
	}
	person := api.Principal{OrgID: 7, Scopes: []string{"stats:read", "profiles:read"}, SiteIDs: []int32{1}}
	cohortRequest(t, stub, person, http.MethodPost, evaluatePath, `{"definition":{"match":"all","window_days":7,"conditions":[{"type":"event","name":"x"}]}}`)
	if !stub.allowed || stub.definition == nil || stub.definition.WindowDays != 7 {
		t.Fatalf("profiles:read caller: allowed=%v definition=%+v", stub.allowed, stub.definition)
	}
}

func TestEvaluateCohortEndpointAuthorizationAndErrors(t *testing.T) {
	stub := &cohortStub{}
	good := api.Principal{OrgID: 7, Scopes: []string{"stats:read"}, SiteIDs: []int32{1}}
	body := `{"cohort_id":1}`
	for name, tt := range map[string]struct {
		principal api.Principal
		path      string
		body      string
		status    int
	}{
		"missing scope": {api.Principal{OrgID: 7, Scopes: []string{"events:write"}, SiteIDs: []int32{1}}, evaluatePath, body, http.StatusForbidden},
		"foreign site":  {api.Principal{OrgID: 7, Scopes: []string{"stats:read"}, SiteIDs: []int32{2}}, evaluatePath, body, http.StatusNotFound},
		"bad site":      {good, "/api/private/sites/zero/cohorts/evaluate", body, http.StatusBadRequest},
		"bad body":      {good, evaluatePath, `{"cohort_id":`, http.StatusBadRequest},
		"unknown field": {good, evaluatePath, `{"cohort_id":1,"sql":"1"}`, http.StatusBadRequest},
	} {
		if response := cohortRequest(t, stub, tt.principal, http.MethodPost, tt.path, tt.body); response.Code != tt.status {
			t.Fatalf("%s: status=%d body=%s", name, response.Code, response.Body.String())
		}
	}
	if stub.calls != 0 {
		t.Fatal("rejected requests must not reach the service")
	}
	for name, tt := range map[string]struct {
		err    error
		status int
	}{
		"anonymous mode":  {api.ErrCohortsUnavailable, http.StatusForbidden},
		"missing cohort":  {api.ErrCohortNotFound, http.StatusNotFound},
		"invalid request": {api.ErrCohortInput, http.StatusBadRequest},
		"timeout":         {context.DeadlineExceeded, http.StatusServiceUnavailable},
	} {
		response := cohortRequest(t, &cohortStub{err: tt.err}, good, http.MethodPost, evaluatePath, body)
		if response.Code != tt.status {
			t.Fatalf("%s: status=%d body=%s", name, response.Code, response.Body.String())
		}
	}
	anonymous := cohortRequest(t, &cohortStub{err: api.ErrCohortsUnavailable}, good, http.MethodPost, evaluatePath, body)
	if !strings.Contains(anonymous.Body.String(), "anonymous mode keeps no person-level history") {
		t.Fatalf("the policy explanation must reach the client: %s", anonymous.Body.String())
	}
}

func TestPowerUsersEndpointNeedsProfileScope(t *testing.T) {
	stub := &cohortStub{power: api.PowerUsers{Metric: "events", Min: 3, Qualifying: 1, Data: []api.PowerUser{{Id: 4, ExternalId: "u-4", Sessions: 2, Events: 5}}}}
	path := "/api/private/sites/1/power-users?from=2026-10-01&to=2026-10-05&metric=events&min=3"
	reader := api.Principal{OrgID: 7, Scopes: []string{"stats:read"}, SiteIDs: []int32{1}}
	assertRecorderProblem(t, cohortRequest(t, stub, reader, http.MethodGet, path, ""), http.StatusForbidden)
	if stub.calls != 0 {
		t.Fatal("power users must not be queried without profiles:read")
	}
	allowed := api.Principal{OrgID: 7, Scopes: []string{"stats:read", "profiles:read"}, SiteIDs: []int32{1}}
	response := cohortRequest(t, stub, allowed, http.MethodGet, path, "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"external_id":"u-4"`) || stub.metric != "events" || stub.minimum != 3 {
		t.Fatalf("status=%d body=%s metric=%q min=%d", response.Code, response.Body.String(), stub.metric, stub.minimum)
	}
	for name, bad := range map[string]string{
		"bad date":    "/api/private/sites/1/power-users?from=nope&to=2026-10-05",
		"bad min":     "/api/private/sites/1/power-users?from=2026-10-01&to=2026-10-05&min=lots",
		"foreign":     "/api/private/sites/2/power-users?from=2026-10-01&to=2026-10-05",
		"bad service": path,
	} {
		principal := allowed
		wantStatus := http.StatusBadRequest
		if name == "foreign" {
			wantStatus = http.StatusNotFound
		}
		service := stub
		if name == "bad service" {
			service = &cohortStub{err: api.ErrCohortsUnavailable}
			wantStatus = http.StatusForbidden
		}
		if got := cohortRequest(t, service, principal, http.MethodGet, bad, ""); got.Code != wantStatus {
			t.Fatalf("%s: status=%d body=%s", name, got.Code, got.Body.String())
		}
	}
}
