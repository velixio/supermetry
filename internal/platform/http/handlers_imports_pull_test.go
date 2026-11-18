package platformhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"supermetry/internal/imports"
	"supermetry/internal/platform/authz"
	"supermetry/internal/privacy"
)

type pullStub struct {
	created   imports.PullRequest
	previewed imports.PullRequest
	err       error
	userID    int64
	orgID     int64
	callback  struct {
		site int32
		err  error
	}
}

func (s *pullStub) Capabilities() imports.Capabilities {
	return imports.Capabilities{
		PlausibleAPI: imports.SourceCapability{Available: true},
		MatomoAPI:    imports.SourceCapability{Available: true},
		GA4API:       imports.SourceCapability{Reason: "Google Analytics import is not configured"},
	}
}
func (s *pullStub) Create(_ context.Context, siteID int32, orgID, userID int64, request imports.PullRequest) (imports.Import, error) {
	s.created, s.userID, s.orgID = request, userID, orgID
	if s.err != nil {
		return imports.Import{}, s.err
	}
	return imports.Import{ID: 11, SiteID: siteID, Source: request.Source(), Status: imports.StatusPending, Method: string(request.Method),
		Progress: &imports.Progress{TotalDays: 2}, CreatedAt: time.Now()}, nil
}
func (s *pullStub) Preview(_ context.Context, _ int32, _ int64, request imports.PullRequest) (imports.PullPreview, error) {
	s.previewed = request
	if s.err != nil {
		return imports.PullPreview{}, s.err
	}
	return imports.PullPreview{Summary: imports.Summary{Days: 2, Totals: imports.Totals{Visitors: 20}}, Overlap: imports.Overlap{ImportedDays: 1}}, nil
}
func (s *pullStub) StartGA4(context.Context, int64, int64, int32) (string, error) {
	return "https://accounts.example.test/auth?state=abc", s.err
}
func (s *pullStub) CallbackGA4(context.Context, int64, int64, string, string, string) (int32, error) {
	return s.callback.site, s.callback.err
}
func (s *pullStub) ListGA4Connections(context.Context, int64) ([]imports.GA4Connection, error) {
	return []imports.GA4Connection{{ID: 3}}, s.err
}
func (s *pullStub) DeleteGA4Connection(context.Context, int64, int64) error { return s.err }
func (s *pullStub) ListGA4Properties(context.Context, int64, int64) ([]imports.GA4Property, error) {
	return []imports.GA4Property{{Property: "properties/1", DisplayName: "Shop"}}, s.err
}

type pullQueue struct{ jobs []imports.Job }

func (q *pullQueue) Queue(job imports.Job) error { q.jobs = append(q.jobs, job); return nil }

func pullHandler(role authz.Role, stub *pullStub, queue *pullQueue, store *importTestStore) http.Handler {
	return pullHandlerStepUp(role, stub, queue, store, freshStepUp{})
}

func pullHandlerStepUp(role authz.Role, stub *pullStub, queue *pullQueue, store *importTestStore, stepUp StepUpChecker) http.Handler {
	return New(Deps{TrackingMode: privacy.Identified, StepUp: stepUp,
		Authenticator: importTestAuth{principal: authz.Principal{UserID: 1, OrgID: 4, Role: role}},
		Sites:         importTestSites{7: {ID: 7, OrgID: 4, TZ: time.UTC}},
		Imports:       store, ImportQueue: queue, ImportPulls: stub,
	})
}

func pullRequestFor(method, target, body string) *http.Request {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CSRF-Token", "token")
	request.AddCookie(&http.Cookie{Name: "supermetry_csrf", Value: "token"})
	return request
}

func TestCreatePullImportQueuesJobAndMapsSecrets(t *testing.T) {
	for _, tt := range []struct {
		role authz.Role
		want int
	}{{authz.RoleOwner, 202}, {authz.RoleAdmin, 202}, {authz.RoleEditor, 403}, {authz.RoleViewer, 403}} {
		stub, queue := &pullStub{}, &pullQueue{}
		handler := pullHandler(tt.role, stub, queue, &importTestStore{})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, pullRequestFor("POST", "/api/private/sites/7/imports/pull",
			`{"method":"plausible_api","remote_site_id":"example.com","api_key":"k1","token_auth":"ignored","from":"2026-01-01","to":"2026-01-02"}`))
		if response.Code != tt.want {
			t.Fatalf("%s: status = %d body=%s", tt.role, response.Code, response.Body.String())
		}
		if tt.want != 202 {
			continue
		}
		if stub.created.Secret != "k1" || stub.created.RemoteSiteID != "example.com" || stub.userID != 1 || stub.orgID != 4 {
			t.Fatalf("request = %+v", stub.created)
		}
		if len(queue.jobs) != 1 || !queue.jobs[0].Pull || queue.jobs[0].ImportID != 11 || queue.jobs[0].Path != "" {
			t.Fatalf("jobs = %+v", queue.jobs)
		}
		var got imports.Import
		if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil || got.Progress == nil || got.Progress.TotalDays != 2 || got.Method != "plausible_api" {
			t.Fatalf("body = %s", response.Body.String())
		}
		if strings.Contains(response.Body.String(), "k1") {
			t.Fatal("response echoes the API key")
		}
	}
	stub := &pullStub{}
	handler := pullHandler(authz.RoleOwner, stub, &pullQueue{}, &importTestStore{})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, pullRequestFor("POST", "/api/private/sites/7/imports/pull",
		`{"method":"matomo_api","base_url":"https://m.example","remote_site_id":"1","api_key":"ignored","token_auth":"t1","from":"2026-01-01","to":"2026-01-02"}`))
	if response.Code != 202 || stub.created.Secret != "t1" || stub.created.BaseURL != "https://m.example" {
		t.Fatalf("matomo: %d %+v", response.Code, stub.created)
	}
}

func TestPullErrorsMapToProblems(t *testing.T) {
	for _, tt := range []struct {
		err    error
		status int
		detail string
	}{
		{fmt.Errorf("%w: from must not be after to", imports.ErrPullInvalid), 400, "from must not be after to"},
		{imports.ErrGA4NotConfigured, 409, "SUPERMETRY_GA4_OAUTH_CLIENT_ID"},
		{imports.ErrGA4Connection, 404, "connection not found"},
		{&imports.RemoteError{Err: fmt.Errorf("%w: check the key", imports.ErrRemoteAuth)}, 422, "check the key"},
		{&imports.RemoteError{Err: errors.New("Plausible answered HTTP 500")}, 502, "HTTP 500"},
		{fmt.Errorf("wrapped: %w", context.DeadlineExceeded), 504, "shorter date range"},
	} {
		handler := pullHandler(authz.RoleOwner, &pullStub{err: tt.err}, &pullQueue{}, &importTestStore{})
		for _, target := range []string{"/api/private/sites/7/imports/pull", "/api/private/sites/7/imports/pull/preview"} {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, pullRequestFor("POST", target, `{"method":"plausible_api"}`))
			if response.Code != tt.status || !strings.Contains(response.Body.String(), tt.detail) || response.Header().Get("Content-Type") != "application/problem+json" {
				t.Fatalf("%v on %s: status=%d body=%s", tt.err, target, response.Code, response.Body.String())
			}
		}
	}
}

func TestPreviewPullImportReturnsSummaryAndOverlap(t *testing.T) {
	stub := &pullStub{}
	handler := pullHandler(authz.RoleAdmin, stub, &pullQueue{}, &importTestStore{})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, pullRequestFor("POST", "/api/private/sites/7/imports/pull/preview",
		`{"method":"matomo_api","base_url":"https://m.example","remote_site_id":"1","token_auth":"t1","from":"2026-01-01","to":"2026-01-02"}`))
	if response.Code != 200 {
		t.Fatalf("status = %d %s", response.Code, response.Body.String())
	}
	var got struct {
		Source  string `json:"source"`
		Days    int    `json:"days"`
		Overlap struct {
			ImportedDays int `json:"imported_days"`
		} `json:"overlap"`
		FileName string `json:"file_name"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil || got.Source != "matomo" || got.Days != 2 || got.Overlap.ImportedDays != 1 || got.FileName != "Matomo API: site 1" {
		t.Fatalf("body = %s err=%v", response.Body.String(), err)
	}
	if stub.previewed.Secret != "t1" {
		t.Fatalf("previewed = %+v", stub.previewed)
	}
}

func TestImportCapabilitiesReportsMissingGA4Configuration(t *testing.T) {
	handler := pullHandler(authz.RoleOwner, &pullStub{}, &pullQueue{}, &importTestStore{})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/api/private/sites/7/imports/capabilities", nil))
	var got map[string]struct {
		Available bool   `json:"available"`
		Reason    string `json:"reason"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &got) != nil {
		t.Fatalf("status = %d %s", response.Code, response.Body.String())
	}
	if !got["plausible_api"].Available || !got["matomo_api"].Available || got["ga4_api"].Available || got["ga4_api"].Reason == "" {
		t.Fatalf("capabilities = %+v", got)
	}
	viewer := pullHandler(authz.RoleViewer, &pullStub{}, &pullQueue{}, &importTestStore{})
	response = httptest.NewRecorder()
	viewer.ServeHTTP(response, httptest.NewRequest("GET", "/api/private/sites/7/imports/capabilities", nil))
	if response.Code != 403 {
		t.Fatalf("viewer status = %d", response.Code)
	}
}

func TestGA4CallbackRedirectsWithOutcome(t *testing.T) {
	for _, tt := range []struct {
		site int32
		err  error
		want string
	}{
		{7, nil, "ga4=connected"},
		{7, imports.ErrGA4Denied, "ga4=denied"},
		{7, errors.New("exchange failed"), "ga4=error"},
	} {
		stub := &pullStub{}
		stub.callback.site, stub.callback.err = tt.site, tt.err
		handler := pullHandler(authz.RoleOwner, stub, &pullQueue{}, &importTestStore{})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", "/api/private/imports/ga4/callback?code=c&state=s", nil))
		if response.Code != http.StatusSeeOther || !strings.Contains(response.Header().Get("Location"), tt.want) {
			t.Fatalf("%v: status=%d location=%q", tt.err, response.Code, response.Header().Get("Location"))
		}
	}
	stub := &pullStub{}
	stub.callback.err = imports.ErrGA4InvalidState
	handler := pullHandler(authz.RoleOwner, stub, &pullQueue{}, &importTestStore{})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/api/private/imports/ga4/callback?code=c&state=bad", nil))
	if response.Code != 400 {
		t.Fatalf("invalid state status = %d", response.Code)
	}
}

func TestGA4ConnectionEndpointsRequireOwnerOrAdmin(t *testing.T) {
	for _, tt := range []struct {
		role authz.Role
		want int
	}{{authz.RoleOwner, 200}, {authz.RoleEditor, 403}} {
		handler := pullHandler(tt.role, &pullStub{}, &pullQueue{}, &importTestStore{})
		for _, target := range []string{"/api/private/imports/ga4/connections", "/api/private/imports/ga4/connections/3/properties"} {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest("GET", target, nil))
			if response.Code != tt.want {
				t.Fatalf("%s %s: %d", tt.role, target, response.Code)
			}
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, pullRequestFor("POST", "/api/private/sites/7/imports/ga4/connect", ""))
		if tt.want == 200 && (response.Code != 200 || !strings.Contains(response.Body.String(), "authorization_url")) {
			t.Fatalf("connect: %d %s", response.Code, response.Body.String())
		}
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, pullRequestFor("DELETE", "/api/private/imports/ga4/connections/3", ""))
		wantDelete := 204
		if tt.want != 200 {
			wantDelete = 403
		}
		if response.Code != wantDelete {
			t.Fatalf("%s delete: %d", tt.role, response.Code)
		}
	}
}

func TestGetImportShowsProgressOnlyToSiteAdmins(t *testing.T) {
	store := &importTestStore{item: imports.Import{ID: 11, SiteID: 7, Source: imports.SourcePlausible, Status: imports.StatusRunning, Method: "plausible_api",
		Progress: &imports.Progress{TotalDays: 74, DoneDays: 31, Rows: 450}}}
	handler := pullHandler(authz.RoleOwner, &pullStub{}, &pullQueue{}, store)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/api/private/imports/11", nil))
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"done_days":31`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	other := New(Deps{TrackingMode: privacy.Identified, StepUp: freshStepUp{},
		Authenticator: importTestAuth{principal: authz.Principal{UserID: 1, OrgID: 99, Role: authz.RoleOwner}},
		Sites:         importTestSites{7: {ID: 7, OrgID: 4, TZ: time.UTC}}, Imports: store, ImportPulls: &pullStub{},
	})
	response = httptest.NewRecorder()
	other.ServeHTTP(response, httptest.NewRequest("GET", "/api/private/imports/11", nil))
	if response.Code != 404 {
		t.Fatalf("cross-org status = %d", response.Code)
	}
	editor := pullHandler(authz.RoleEditor, &pullStub{}, &pullQueue{}, store)
	response = httptest.NewRecorder()
	editor.ServeHTTP(response, httptest.NewRequest("GET", "/api/private/imports/11", nil))
	if response.Code != 403 {
		t.Fatalf("editor status = %d", response.Code)
	}
}
