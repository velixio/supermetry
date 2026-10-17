package platformhttp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"supermetry/internal/db"
	"supermetry/internal/platform/authz"
	platformhttp "supermetry/internal/platform/http"
	"supermetry/internal/platform/invites"
	"supermetry/internal/platform/mail"
	"supermetry/internal/platform/orgs"
	"supermetry/internal/platform/sitesvc"
	"supermetry/internal/platform/store"
	"supermetry/internal/platform/users"
	"supermetry/internal/testpg"
)

func TestInvitationPreviewAndLeaveOrganization(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var ownerID, viewerID, orgID int64
	if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash) values ('owner@example.test','Olive Owner','hash') returning id`).Scan(&ownerID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash) values ('viewer@example.test','Vic','hash') returning id`).Scan(&viewerID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Acme Analytics','acme-preview') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into memberships(org_id,user_id,role) values ($1,$2,'owner'),($1,$3,'viewer')`, orgID, ownerID, viewerID); err != nil {
		t.Fatal(err)
	}
	data := &store.Store{Pool: pool}
	registry := sitesvc.NewRegistry(data, 0)
	t.Cleanup(registry.Close)
	invitations := invites.New(data, users.New(data), &mail.Fake{}, "https://supermetry.example")
	owner := authz.Principal{UserID: ownerID, OrgID: orgID, Role: authz.RoleOwner}
	viewer := authz.Principal{UserID: viewerID, OrgID: orgID, Role: authz.RoleViewer}
	handler := platformhttp.New(platformhttp.Deps{
		Store: data, Orgs: orgs.New(data, registry), Invites: invitations,
		Authenticator: actorAuthenticator{"owner": owner, "viewer": viewer},
	})
	serve := func(request *http.Request) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	link, err := invitations.Create(ctx, owner, "new@example.test", authz.RoleEditor, nil)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	token := parsed.Query().Get("token")
	preview := serve(httptest.NewRequest(http.MethodGet, "/api/private/invitations/"+url.PathEscape(token), nil))
	if preview.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", preview.Code, preview.Body)
	}
	var body struct {
		OrgName       string `json:"org_name"`
		Email         string `json:"email"`
		Role          string `json:"role"`
		InvitedBy     string `json:"invited_by"`
		AccountExists bool   `json:"account_exists"`
	}
	if err := json.Unmarshal(preview.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.OrgName != "Acme Analytics" || body.Email != "new@example.test" || body.Role != "editor" || body.InvitedBy != "Olive Owner" || body.AccountExists {
		t.Fatalf("preview = %+v", body)
	}
	if got := preview.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
	if missing := serve(httptest.NewRequest(http.MethodGet, "/api/private/invitations/unknown-token", nil)); missing.Code != http.StatusNotFound {
		t.Fatalf("unknown token status=%d", missing.Code)
	}

	leavePath := fmt.Sprintf("/api/private/orgs/%d/membership", orgID)
	if response := serve(authenticatedRequestAs("owner", http.MethodDelete, leavePath)); response.Code != http.StatusConflict {
		t.Fatalf("last owner leave status=%d body=%s", response.Code, response.Body)
	}
	if response := serve(authenticatedRequestAs("viewer", http.MethodDelete, leavePath)); response.Code != http.StatusNoContent {
		t.Fatalf("viewer leave status=%d body=%s", response.Code, response.Body)
	}
	var members int
	if err := pool.QueryRow(ctx, `select count(*) from memberships where org_id=$1`, orgID).Scan(&members); err != nil || members != 1 {
		t.Fatalf("members after leave = %d, %v", members, err)
	}
}

func authenticatedRequestAs(actor, method, path string) *http.Request {
	request := authenticatedRequest(method, path, "")
	request.Header.Set("X-Test-Actor", actor)
	return request
}
