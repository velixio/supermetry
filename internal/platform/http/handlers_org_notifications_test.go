package platformhttp_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"supermetry/internal/db"
	"supermetry/internal/notifications"
	"supermetry/internal/platform/authz"
	platformcrypto "supermetry/internal/platform/crypto"
	platformhttp "supermetry/internal/platform/http"
	"supermetry/internal/testpg"
)

const notifToken = "123456789:AAEhBOweik6ad9r_QXMENQjcrTu-oSYZ0abc"

type fakeOrgNotifications struct {
	calls []string
	input notifications.TelegramInput
	err   error
}

func (f *fakeOrgNotifications) List(context.Context, int64) ([]notifications.Channel, error) {
	f.calls = append(f.calls, "list")
	return []notifications.Channel{{ID: 1, Kind: "telegram", TokenSet: true, ChatID: "-1"}}, f.err
}

func (f *fakeOrgNotifications) SaveTelegram(_ context.Context, _ authz.Principal, in notifications.TelegramInput) (notifications.Channel, error) {
	f.calls = append(f.calls, "save")
	f.input = in
	return notifications.Channel{ID: 1, Kind: "telegram", TokenSet: true}, f.err
}

func (f *fakeOrgNotifications) DeleteTelegram(context.Context, authz.Principal) error {
	f.calls = append(f.calls, "delete")
	return f.err
}

func (f *fakeOrgNotifications) TestTelegram(context.Context, authz.Principal) error {
	f.calls = append(f.calls, "test")
	return f.err
}

func TestOrgNotificationRoutesEnforceRoles(t *testing.T) {
	actors := actorAuthenticator{
		"owner":  {UserID: 1, OrgID: 1, Role: authz.RoleOwner},
		"admin":  {UserID: 2, OrgID: 1, Role: authz.RoleAdmin},
		"editor": {UserID: 3, OrgID: 1, Role: authz.RoleEditor, SiteIDs: []int32{10}},
		"viewer": {UserID: 4, OrgID: 1, Role: authz.RoleViewer, SiteIDs: []int32{10}},
		"other":  {UserID: 5, OrgID: 2, Role: authz.RoleOwner},
	}
	routes := []struct {
		method, path, body string
		ok                 int
	}{
		{http.MethodGet, "/api/private/orgs/1/notifications", "", http.StatusOK},
		{http.MethodPut, "/api/private/orgs/1/notifications/telegram", `{"token":"` + notifToken + `","chat_id":"-1","enabled":true,"events":{"star_milestone":true}}`, http.StatusOK},
		{http.MethodPost, "/api/private/orgs/1/notifications/telegram/test", "", http.StatusOK},
		{http.MethodDelete, "/api/private/orgs/1/notifications/telegram", "", http.StatusNoContent},
	}
	want := map[string]int{"owner": 0, "admin": 0, "editor": http.StatusForbidden, "viewer": http.StatusForbidden, "other": http.StatusNotFound}
	for actor, status := range want {
		for _, route := range routes {
			t.Run(actor+" "+route.method+" "+route.path, func(t *testing.T) {
				service := &fakeOrgNotifications{}
				handler := platformhttp.New(platformhttp.Deps{Authenticator: actors, OrgNotifications: service})
				response := requestAs(t, handler, actor, route.method, route.path, route.body)
				expect := status
				if expect == 0 {
					expect = route.ok
				}
				if response.Code != expect {
					t.Fatalf("status=%d body=%s, want %d", response.Code, response.Body.String(), expect)
				}
				if status != 0 && len(service.calls) != 0 {
					t.Fatalf("service reached despite %d: %v", status, service.calls)
				}
				if status == 0 && len(service.calls) != 1 {
					t.Fatalf("calls = %v", service.calls)
				}
			})
		}
	}
}

func TestOrgNotificationSaveDecodesAndRejectsBadBodies(t *testing.T) {
	service := &fakeOrgNotifications{}
	handler := platformhttp.New(platformhttp.Deps{
		Authenticator:    actorAuthenticator{"admin": {UserID: 2, OrgID: 1, Role: authz.RoleAdmin}},
		OrgNotifications: service,
	})
	path := "/api/private/orgs/1/notifications/telegram"
	if r := requestAs(t, handler, "admin", http.MethodPut, path, `{"chat_id":"@chan_name","enabled":false,"events":{"star_spike":true}}`); r.Code != http.StatusOK {
		t.Fatalf("status=%d %s", r.Code, r.Body.String())
	}
	if service.input.Token != nil || service.input.ChatID != "@chan_name" || service.input.Enabled || !service.input.Events[notifications.StarSpike] {
		t.Fatalf("input = %+v", service.input)
	}
	if r := requestAs(t, handler, "admin", http.MethodPut, path, `{"chat_id":"1","bogus":1}`); r.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status=%d", r.Code)
	}
	tests := map[string]struct {
		err    error
		method string
		path   string
		status int
	}{
		"invalid":   {notifications.ErrInvalid, http.MethodPut, path, http.StatusBadRequest},
		"not found": {notifications.ErrNotFound, http.MethodDelete, path, http.StatusNotFound},
		"too soon":  {notifications.ErrTooSoon, http.MethodPost, path + "/test", http.StatusTooManyRequests},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			failing := &fakeOrgNotifications{err: tt.err}
			h := platformhttp.New(platformhttp.Deps{
				Authenticator:    actorAuthenticator{"admin": {UserID: 2, OrgID: 1, Role: authz.RoleAdmin}},
				OrgNotifications: failing,
			})
			if r := requestAs(t, h, "admin", tt.method, tt.path, `{"chat_id":"-1"}`); r.Code != tt.status {
				t.Fatalf("status=%d %s, want %d", r.Code, r.Body.String(), tt.status)
			}
		})
	}
	failing := &fakeOrgNotifications{err: &notifications.DeliveryError{Message: "chat not found"}}
	h := platformhttp.New(platformhttp.Deps{
		Authenticator:    actorAuthenticator{"admin": {UserID: 2, OrgID: 1, Role: authz.RoleAdmin}},
		OrgNotifications: failing,
	})
	r := requestAs(t, h, "admin", http.MethodPost, path+"/test", "")
	if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), `"ok":false`) || !strings.Contains(r.Body.String(), "chat not found") {
		t.Fatalf("delivery failure: status=%d %s", r.Code, r.Body.String())
	}
}

type failingSender struct{ err error }

func (f failingSender) SendMessage(context.Context, string, string, string) error { return f.err }

func TestOrgNotificationRealServiceNeverEchoesToken(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var orgID, userID int64
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('O','o') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash) values ('u@example.test','U','h') returning id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	box, err := platformcrypto.NewSecretBox([]byte("01234567890123456789012345678901"), "supermetry/org-notifications/v1")
	if err != nil {
		t.Fatal(err)
	}
	service, err := notifications.NewService(pool, box, notifications.Options{Sender: failingSender{errors.New("rejected " + notifToken)}})
	if err != nil {
		t.Fatal(err)
	}
	handler := platformhttp.New(platformhttp.Deps{
		Authenticator:    actorAuthenticator{"owner": {UserID: userID, OrgID: orgID, Role: authz.RoleOwner}},
		OrgNotifications: service,
	})
	base := "/api/private/orgs/" + itoa64(orgID) + "/notifications"
	bodies := []string{}
	save := requestAs(t, handler, "owner", http.MethodPut, base+"/telegram", `{"token":"`+notifToken+`","chat_id":"-100","enabled":true,"events":{"star_milestone":true}}`)
	bodies = append(bodies, save.Body.String())
	list := requestAs(t, handler, "owner", http.MethodGet, base, "")
	bodies = append(bodies, list.Body.String())
	test := requestAs(t, handler, "owner", http.MethodPost, base+"/telegram/test", "")
	bodies = append(bodies, test.Body.String())
	bad := requestAs(t, handler, "owner", http.MethodPut, base+"/telegram", `{"token":"`+notifToken+`x y","chat_id":"-100"}`)
	bodies = append(bodies, bad.Body.String())
	if save.Code != http.StatusOK || list.Code != http.StatusOK || test.Code != http.StatusOK || bad.Code != http.StatusBadRequest {
		t.Fatalf("statuses save=%d list=%d test=%d bad=%d", save.Code, list.Code, test.Code, bad.Code)
	}
	for _, body := range bodies {
		if strings.Contains(body, "AAEhBOweik6ad9r") {
			t.Fatalf("response leaks the token: %s", body)
		}
	}
	if !strings.Contains(list.Body.String(), `"token_set":true`) || !strings.Contains(list.Body.String(), `"event_kinds"`) || !strings.Contains(test.Body.String(), `"ok":false`) {
		t.Fatalf("list=%s test=%s", list.Body.String(), test.Body.String())
	}
}
