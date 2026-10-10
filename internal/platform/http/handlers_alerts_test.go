package platformhttp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"supermetry/internal/alerts"
	"supermetry/internal/platform/authz"
	platformhttp "supermetry/internal/platform/http"
)

type fakeAlertsService struct {
	channels    []alerts.Channel
	alerts      []alerts.Alert
	channelCall int
	alertCall   int
	channelID   int64
	alertID     int64
	siteID      int32
	channelIn   alerts.ChannelInput
	alertIn     alerts.AlertInput
	err         error
}

func (f *fakeAlertsService) ListChannels(context.Context, authz.Principal) ([]alerts.Channel, error) {
	f.channelCall++
	return f.channels, nil
}

func (f *fakeAlertsService) CreateChannel(_ context.Context, _ authz.Principal, input alerts.ChannelInput) (alerts.Channel, error) {
	f.channelCall++
	f.channelIn = input
	return alerts.Channel{ID: 7, OrgID: 1, Kind: input.Kind, Name: input.Name}, f.err
}

func (f *fakeAlertsService) UpdateChannel(_ context.Context, _ authz.Principal, id int64, input alerts.ChannelInput) (alerts.Channel, error) {
	f.channelCall++
	f.channelID, f.channelIn = id, input
	return alerts.Channel{ID: id, OrgID: 1, Kind: input.Kind, Name: input.Name}, nil
}

func (f *fakeAlertsService) DeleteChannel(context.Context, authz.Principal, int64) error {
	f.channelCall++
	return nil
}

func (f *fakeAlertsService) TestChannel(context.Context, authz.Principal, int64) error {
	f.channelCall++
	return f.err
}

func (f *fakeAlertsService) ListAlerts(context.Context, authz.Principal, int32) ([]alerts.Alert, error) {
	f.alertCall++
	return f.alerts, nil
}

func (f *fakeAlertsService) CreateAlert(_ context.Context, _ authz.Principal, siteID int32, input alerts.AlertInput) (alerts.Alert, error) {
	f.alertCall++
	f.siteID, f.alertIn = siteID, input
	return alerts.Alert{ID: 9, SiteID: siteID, Kind: input.Kind, Threshold: input.Threshold, GoalID: input.GoalID, ChannelIDs: input.ChannelIDs, Enabled: true}, nil
}

func (f *fakeAlertsService) UpdateAlert(_ context.Context, _ authz.Principal, _ int32, id int64, input alerts.AlertInput) (alerts.Alert, error) {
	f.alertCall++
	f.alertID, f.alertIn = id, input
	return alerts.Alert{ID: id, SiteID: 10, Kind: input.Kind, Threshold: input.Threshold, GoalID: input.GoalID, ChannelIDs: input.ChannelIDs, Enabled: true}, nil
}

func (f *fakeAlertsService) DeleteAlert(context.Context, authz.Principal, int32, int64) error {
	f.alertCall++
	return nil
}

func TestAlertRoutesEnforceRoleBoundaries(t *testing.T) {
	actors := actorAuthenticator{
		"owner":  {UserID: 1, OrgID: 1, Role: authz.RoleOwner},
		"admin":  {UserID: 2, OrgID: 1, Role: authz.RoleAdmin},
		"editor": {UserID: 3, OrgID: 1, Role: authz.RoleEditor, SiteIDs: []int32{10}},
		"viewer": {UserID: 4, OrgID: 1, Role: authz.RoleViewer, SiteIDs: []int32{10}},
		"other":  {UserID: 5, OrgID: 2, Role: authz.RoleOwner},
	}
	service := &fakeAlertsService{}
	handler := platformhttp.New(platformhttp.Deps{
		Authenticator: actors,
		Sites: siteRegistry{
			10: {ID: 10, OrgID: 1, PublicID: "siteaaaaaaaa", TZ: time.UTC},
			20: {ID: 20, OrgID: 2, PublicID: "sitebbbbbbbb", TZ: time.UTC},
		},
		Alerts: service,
	})

	tests := []struct {
		name   string
		actor  string
		path   string
		status int
	}{
		{name: "owner lists channels", actor: "owner", path: "/api/private/orgs/1/channels", status: http.StatusOK},
		{name: "admin lists channels", actor: "admin", path: "/api/private/orgs/1/channels", status: http.StatusOK},
		{name: "editor lists sanitized channels", actor: "editor", path: "/api/private/orgs/1/channels", status: http.StatusOK},
		{name: "viewer cannot list channels", actor: "viewer", path: "/api/private/orgs/1/channels", status: http.StatusForbidden},
		{name: "cross-org channels are hidden", actor: "other", path: "/api/private/orgs/1/channels", status: http.StatusNotFound},
		{name: "owner lists alerts", actor: "owner", path: "/api/private/sites/10/alerts", status: http.StatusOK},
		{name: "admin lists alerts", actor: "admin", path: "/api/private/sites/10/alerts", status: http.StatusOK},
		{name: "editor lists alerts", actor: "editor", path: "/api/private/sites/10/alerts", status: http.StatusOK},
		{name: "viewer cannot list alerts", actor: "viewer", path: "/api/private/sites/10/alerts", status: http.StatusForbidden},
		{name: "cross-org alerts are hidden", actor: "owner", path: "/api/private/sites/20/alerts", status: http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, tt.path, nil)
			request.Header.Set("X-Test-Actor", tt.actor)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != tt.status {
				t.Fatalf("status=%d body=%s, want %d", response.Code, response.Body.String(), tt.status)
			}
		})
	}
}

func TestChannelResponsesDoNotExposeDeliverySecrets(t *testing.T) {
	service := &fakeAlertsService{channels: []alerts.Channel{{
		ID: 1, OrgID: 1, Kind: "webhook", Name: "Deploys",
		Config: alerts.ChannelConfig{Recipients: []string{"ops@example.test"}, Configured: true},
	}}}
	handler := platformhttp.New(platformhttp.Deps{
		Authenticator: actorAuthenticator{"admin": {UserID: 2, OrgID: 1, Role: authz.RoleAdmin}},
		Alerts:        service,
	})
	request := httptest.NewRequest(http.MethodGet, "/api/private/orgs/1/channels", nil)
	request.Header.Set("X-Test-Actor", "admin")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if strings.Contains(body, "url") || strings.Contains(body, "secret") || !strings.Contains(body, `"configured":true`) {
		t.Fatalf("unsafe channel response: %s", body)
	}
}

func TestChannelMutationsValidateInputBeforeService(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "unknown field", body: `{"kind":"email","name":"Ops","config":{"recipients":["ops@example.test"]},"extra":true}`},
		{name: "missing name", body: `{"kind":"email","config":{"recipients":["ops@example.test"]}}`},
		{name: "unknown kind", body: `{"kind":"sms","name":"Ops","config":{"recipients":["ops@example.test"]}}`},
		{name: "email needs recipients", body: `{"kind":"email","name":"Ops","config":{}}`},
		{name: "webhook needs URL", body: `{"kind":"webhook","name":"Hook","config":{}}`},
		{name: "telegram needs token", body: `{"kind":"telegram","name":"Ops","config":{"chat_id":"-100123"}}`},
		{name: "telegram needs chat", body: `{"kind":"telegram","name":"Ops","config":{"bot_token":"123456:fake-token-value-for-tests"}}`},
		{name: "telegram rejects invalid token", body: `{"kind":"telegram","name":"Ops","config":{"bot_token":"not-a-token","chat_id":"-100123"}}`},
		{name: "telegram rejects invalid chat", body: `{"kind":"telegram","name":"Ops","config":{"bot_token":"123456:fake-token-value-for-tests","chat_id":"https://evil.test"}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeAlertsService{}
			handler := platformhttp.New(platformhttp.Deps{
				Authenticator: actorAuthenticator{"admin": {UserID: 2, OrgID: 1, Role: authz.RoleAdmin}},
				Alerts:        service,
			})
			response := requestAs(t, handler, "admin", http.MethodPost, "/api/private/orgs/1/channels", tt.body)
			if response.Code != http.StatusBadRequest || service.channelCall != 0 {
				t.Fatalf("status=%d calls=%d body=%s", response.Code, service.channelCall, response.Body.String())
			}
		})
	}
}

func TestChannelCRUDDelegatesValidatedRequests(t *testing.T) {
	service := &fakeAlertsService{}
	handler := platformhttp.New(platformhttp.Deps{
		Authenticator: actorAuthenticator{"admin": {UserID: 2, OrgID: 1, Role: authz.RoleAdmin}},
		Alerts:        service,
	})
	created := requestAs(t, handler, "admin", http.MethodPost, "/api/private/orgs/1/channels", `{"kind":"webhook","name":"Deploys","config":{"url":"https://hooks.example.test/supermetry","secret":"shared"}}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	if service.channelIn.URL != "https://hooks.example.test/supermetry" || service.channelIn.Secret != "shared" {
		t.Fatalf("create input=%+v", service.channelIn)
	}
	if response := requestAs(t, handler, "admin", http.MethodPatch, "/api/private/orgs/1/channels/7", `{"kind":"email","name":"Ops","config":{"recipients":["ops@example.test"]}}`); response.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", response.Code, response.Body.String())
	}
	if response := requestAs(t, handler, "admin", http.MethodPost, "/api/private/orgs/1/channels/7/test", ""); response.Code != http.StatusNoContent {
		t.Fatalf("test status=%d body=%s", response.Code, response.Body.String())
	}
	if response := requestAs(t, handler, "admin", http.MethodDelete, "/api/private/orgs/1/channels/7", ""); response.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestTelegramChannelCRUDAcceptsWriteOnlyTokenAndDisabledState(t *testing.T) {
	service := &fakeAlertsService{}
	handler := platformhttp.New(platformhttp.Deps{
		Authenticator: actorAuthenticator{"admin": {UserID: 2, OrgID: 1, Role: authz.RoleAdmin}},
		Alerts:        service,
	})
	created := requestAs(t, handler, "admin", http.MethodPost, "/api/private/orgs/1/channels", `{"kind":"telegram","name":"Ops","enabled":false,"config":{"bot_token":"123456:fake-token-value-for-tests","chat_id":"-1001234567890"}}`)
	if created.Code != http.StatusCreated || service.channelIn.BotToken == "" || service.channelIn.ChatID != "-1001234567890" || service.channelIn.Enabled == nil || *service.channelIn.Enabled {
		t.Fatalf("status=%d input=%+v body=%s", created.Code, service.channelIn, created.Body.String())
	}
	updated := requestAs(t, handler, "admin", http.MethodPatch, "/api/private/orgs/1/channels/7", `{"kind":"telegram","name":"Ops","enabled":true,"config":{"bot_token":"","chat_id":"@supermetry_alerts"}}`)
	if updated.Code != http.StatusOK || service.channelIn.BotToken != "" || service.channelIn.ChatID != "@supermetry_alerts" || service.channelIn.Enabled == nil || !*service.channelIn.Enabled {
		t.Fatalf("status=%d input=%+v body=%s", updated.Code, service.channelIn, updated.Body.String())
	}
}

func TestTelegramChannelAuthorizationAndTenantIsolation(t *testing.T) {
	service := &fakeAlertsService{}
	handler := platformhttp.New(platformhttp.Deps{
		Authenticator: actorAuthenticator{
			"admin":  {UserID: 2, OrgID: 1, Role: authz.RoleAdmin},
			"editor": {UserID: 3, OrgID: 1, Role: authz.RoleEditor},
			"other":  {UserID: 4, OrgID: 2, Role: authz.RoleOwner},
		},
		Alerts: service,
	})
	body := `{"kind":"telegram","name":"Ops","config":{"bot_token":"123456:fake-token-value-for-tests","chat_id":"-1001234567890"}}`
	tests := []struct {
		name, actor, method, path, body string
		status                          int
	}{
		{name: "admin creates", actor: "admin", method: http.MethodPost, path: "/api/private/orgs/1/channels", body: body, status: http.StatusCreated},
		{name: "editor cannot create", actor: "editor", method: http.MethodPost, path: "/api/private/orgs/1/channels", body: body, status: http.StatusForbidden},
		{name: "other org cannot create", actor: "other", method: http.MethodPost, path: "/api/private/orgs/1/channels", body: body, status: http.StatusNotFound},
		{name: "other org cannot test", actor: "other", method: http.MethodPost, path: "/api/private/orgs/1/channels/7/test", status: http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := service.channelCall
			response := requestAs(t, handler, tt.actor, tt.method, tt.path, tt.body)
			if response.Code != tt.status {
				t.Fatalf("status=%d body=%s, want %d", response.Code, response.Body.String(), tt.status)
			}
			if tt.status != http.StatusCreated && service.channelCall != before {
				t.Fatalf("unauthorized request reached service: calls %d -> %d", before, service.channelCall)
			}
		})
	}
}

func TestChannelTestFailureReturnsUsefulSanitizedError(t *testing.T) {
	service := &fakeAlertsService{err: alerts.ErrTestDelivery}
	handler := platformhttp.New(platformhttp.Deps{
		Authenticator: actorAuthenticator{"admin": {UserID: 2, OrgID: 1, Role: authz.RoleAdmin}},
		Alerts:        service,
	})
	response := requestAs(t, handler, "admin", http.MethodPost, "/api/private/orgs/1/channels/7/test", "")
	if response.Code != http.StatusBadGateway || !strings.Contains(response.Body.String(), "verify the channel credentials") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestChannelServiceValidationErrorIsBadRequest(t *testing.T) {
	service := &fakeAlertsService{err: alerts.ErrInvalid}
	handler := platformhttp.New(platformhttp.Deps{
		Authenticator: actorAuthenticator{"admin": {UserID: 2, OrgID: 1, Role: authz.RoleAdmin}},
		Alerts:        service,
	})
	response := requestAs(t, handler, "admin", http.MethodPost, "/api/private/orgs/1/channels", `{"kind":"email","name":"Ops","config":{"recipients":["ops@example.test"]}}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s, want %d", response.Code, response.Body.String(), http.StatusBadRequest)
	}
}

func TestAlertMutationsValidateInputBeforeService(t *testing.T) {
	goalID := int64(3)
	tests := []struct {
		name string
		body string
	}{
		{name: "unknown field", body: `{"kind":"traffic_spike","threshold":2,"channel_ids":[1],"extra":true}`},
		{name: "unknown kind", body: `{"kind":"unknown","threshold":2,"channel_ids":[1]}`},
		{name: "threshold must be positive", body: `{"kind":"traffic_spike","threshold":0,"channel_ids":[1]}`},
		{name: "channels required", body: `{"kind":"traffic_spike","threshold":2,"channel_ids":[]}`},
		{name: "goal alert requires goal", body: `{"kind":"goal_reached","threshold":2,"channel_ids":[1]}`},
		{name: "traffic alert rejects goal", body: fmt.Sprintf(`{"kind":"traffic_drop","threshold":2,"goal_id":%d,"channel_ids":[1]}`, goalID)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeAlertsService{}
			handler := platformhttp.New(platformhttp.Deps{
				Authenticator: actorAuthenticator{"editor": {UserID: 3, OrgID: 1, Role: authz.RoleEditor, SiteIDs: []int32{10}}},
				Sites:         siteRegistry{10: {ID: 10, OrgID: 1, PublicID: "siteaaaaaaaa", TZ: time.UTC}},
				Alerts:        service,
			})
			response := requestAs(t, handler, "editor", http.MethodPost, "/api/private/sites/10/alerts", tt.body)
			if response.Code != http.StatusBadRequest || service.alertCall != 0 {
				t.Fatalf("status=%d calls=%d body=%s", response.Code, service.alertCall, response.Body.String())
			}
		})
	}
}

func TestDisabledAlertHTTPValidation(t *testing.T) {
	service := &fakeAlertsService{}
	handler := platformhttp.New(platformhttp.Deps{
		Authenticator: actorAuthenticator{"editor": {UserID: 3, OrgID: 1, Role: authz.RoleEditor, SiteIDs: []int32{10}}},
		Sites:         siteRegistry{10: {ID: 10, OrgID: 1, PublicID: "siteaaaaaaaa", TZ: time.UTC}},
		Alerts:        service,
	})

	created := requestAs(t, handler, "editor", http.MethodPost, "/api/private/sites/10/alerts", `{"kind":"traffic_spike","threshold":2,"enabled":false}`)
	if created.Code < 200 || created.Code >= 300 {
		t.Fatalf("disabled create status=%d body=%s", created.Code, created.Body.String())
	}
	if service.alertIn.Enabled == nil || *service.alertIn.Enabled || len(service.alertIn.ChannelIDs) != 0 {
		t.Fatalf("disabled create input=%+v, want disabled without channels", service.alertIn)
	}

	before := service.alertCall
	enabled := requestAs(t, handler, "editor", http.MethodPatch, "/api/private/sites/10/alerts/9", `{"kind":"traffic_spike","threshold":2,"enabled":true}`)
	if enabled.Code != http.StatusBadRequest || service.alertCall != before {
		t.Fatalf("enable without channels status=%d calls=%d->%d body=%s", enabled.Code, before, service.alertCall, enabled.Body.String())
	}

	updated := requestAs(t, handler, "editor", http.MethodPatch, "/api/private/sites/10/alerts/9", `{"kind":"traffic_spike","threshold":3,"enabled":false}`)
	if updated.Code < 200 || updated.Code >= 300 || service.alertIn.Threshold != 3 {
		t.Fatalf("disabled threshold update status=%d input=%+v body=%s", updated.Code, service.alertIn, updated.Body.String())
	}
}

func TestAlertCRUDDelegatesValidatedRequests(t *testing.T) {
	service := &fakeAlertsService{}
	handler := platformhttp.New(platformhttp.Deps{
		Authenticator: actorAuthenticator{"editor": {UserID: 3, OrgID: 1, Role: authz.RoleEditor, SiteIDs: []int32{10}}},
		Sites:         siteRegistry{10: {ID: 10, OrgID: 1, PublicID: "siteaaaaaaaa", TZ: time.UTC}},
		Alerts:        service,
	})
	created := requestAs(t, handler, "editor", http.MethodPost, "/api/private/sites/10/alerts", `{"kind":"goal_reached","threshold":2.5,"goal_id":3,"channel_ids":[1]}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	var alert alerts.Alert
	if err := json.NewDecoder(created.Body).Decode(&alert); err != nil || alert.ID != 9 || alert.SiteID != 10 {
		t.Fatalf("created alert=%+v error=%v", alert, err)
	}
	if service.siteID != 10 || service.alertIn.Kind != alerts.AlertGoalReached || service.alertIn.GoalID == nil || *service.alertIn.GoalID != 3 || service.alertIn.Threshold != 2.5 {
		t.Fatalf("create input site=%d input=%+v", service.siteID, service.alertIn)
	}
	if response := requestAs(t, handler, "editor", http.MethodPatch, "/api/private/sites/10/alerts/9", `{"kind":"traffic_drop","threshold":3,"channel_ids":[1],"enabled":false}`); response.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", response.Code, response.Body.String())
	}
	if service.alertID != 9 || service.alertIn.Enabled == nil || *service.alertIn.Enabled {
		t.Fatalf("update id=%d input=%+v", service.alertID, service.alertIn)
	}
	if response := requestAs(t, handler, "editor", http.MethodDelete, "/api/private/sites/10/alerts/9", ""); response.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", response.Code, response.Body.String())
	}
}

func requestAs(t *testing.T, handler http.Handler, actor, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("X-Test-Actor", actor)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CSRF-Token", "token")
	request.AddCookie(&http.Cookie{Name: "supermetry_csrf", Value: "token"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
