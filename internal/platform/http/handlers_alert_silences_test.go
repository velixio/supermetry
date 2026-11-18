package platformhttp_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"supermetry/internal/alerts"
	"supermetry/internal/platform/authz"
	platformhttp "supermetry/internal/platform/http"
)

type fakeSilenceService struct {
	*fakeAlertsService
	created int
	input   alerts.SilenceInput
}

func (f *fakeSilenceService) ListSilences(context.Context, authz.Principal, int32) ([]alerts.Silence, error) {
	return []alerts.Silence{}, nil
}

func (f *fakeSilenceService) CreateSilence(_ context.Context, _ authz.Principal, siteID int32, in alerts.SilenceInput) (alerts.Silence, error) {
	f.created++
	f.input = in
	return alerts.Silence{ID: 3, SiteID: siteID, Kind: in.Kind}, nil
}

func (f *fakeSilenceService) DeleteSilence(context.Context, authz.Principal, int32, int64) error {
	return nil
}

func TestAlertSilenceRoutesEnforceRoleBoundaries(t *testing.T) {
	actors := actorAuthenticator{
		"owner":  {UserID: 1, OrgID: 1, Role: authz.RoleOwner},
		"admin":  {UserID: 2, OrgID: 1, Role: authz.RoleAdmin},
		"editor": {UserID: 3, OrgID: 1, Role: authz.RoleEditor, SiteIDs: []int32{10}},
		"viewer": {UserID: 4, OrgID: 1, Role: authz.RoleViewer, SiteIDs: []int32{10}},
	}
	service := &fakeSilenceService{fakeAlertsService: &fakeAlertsService{}}
	handler := platformhttp.New(platformhttp.Deps{
		Authenticator: actors,
		Sites: siteRegistry{
			10: {ID: 10, OrgID: 1, PublicID: "siteaaaaaaaa", TZ: time.UTC},
			20: {ID: 20, OrgID: 2, PublicID: "sitebbbbbbbb", TZ: time.UTC},
		},
		Alerts: service,
	})
	body := `{"kind":"silence","ends_at":"2031-01-01T00:00:00Z"}`
	tests := []struct {
		name, actor, method, path, body string
		status                          int
	}{
		{"owner lists", "owner", http.MethodGet, "/api/private/sites/10/alert-silences", "", http.StatusOK},
		{"editor lists", "editor", http.MethodGet, "/api/private/sites/10/alert-silences", "", http.StatusOK},
		{"viewer cannot list", "viewer", http.MethodGet, "/api/private/sites/10/alert-silences", "", http.StatusForbidden},
		{"other org list is hidden", "owner", http.MethodGet, "/api/private/sites/20/alert-silences", "", http.StatusNotFound},
		{"admin creates", "admin", http.MethodPost, "/api/private/sites/10/alert-silences", body, http.StatusCreated},
		{"viewer cannot create", "viewer", http.MethodPost, "/api/private/sites/10/alert-silences", body, http.StatusForbidden},
		{"other org create is hidden", "owner", http.MethodPost, "/api/private/sites/20/alert-silences", body, http.StatusNotFound},
		{"unknown field is rejected", "owner", http.MethodPost, "/api/private/sites/10/alert-silences", `{"kind":"silence","x":1}`, http.StatusBadRequest},
		{"editor deletes", "editor", http.MethodDelete, "/api/private/sites/10/alert-silences/3", "", http.StatusNoContent},
		{"viewer cannot delete", "viewer", http.MethodDelete, "/api/private/sites/10/alert-silences/3", "", http.StatusForbidden},
		{"other org delete is hidden", "owner", http.MethodDelete, "/api/private/sites/20/alert-silences/3", "", http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := requestAs(t, handler, tt.actor, tt.method, tt.path, tt.body)
			if response.Code != tt.status {
				t.Fatalf("status=%d body=%s, want %d", response.Code, response.Body.String(), tt.status)
			}
		})
	}
	if service.created != 1 {
		t.Fatalf("created = %d, want only the admin's request to reach the service", service.created)
	}
}

func TestAlertSilenceRoutesNeedSilenceService(t *testing.T) {
	handler := platformhttp.New(platformhttp.Deps{
		Authenticator: actorAuthenticator{"owner": {UserID: 1, OrgID: 1, Role: authz.RoleOwner}},
		Sites:         siteRegistry{10: {ID: 10, OrgID: 1, PublicID: "siteaaaaaaaa", TZ: time.UTC}},
		Alerts:        &fakeAlertsService{},
	})
	request := httptest.NewRequest(http.MethodGet, "/api/private/sites/10/alert-silences", nil)
	request.Header.Set("X-Test-Actor", "owner")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d, want 500 (unavailable)", response.Code)
	}
}
