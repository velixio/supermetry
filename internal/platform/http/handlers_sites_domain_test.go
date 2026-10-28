package platformhttp_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"supermetry/internal/db"
	"supermetry/internal/platform/authz"
	platformhttp "supermetry/internal/platform/http"
	"supermetry/internal/platform/sitesvc"
	"supermetry/internal/platform/store"
	"supermetry/internal/testpg"
)

func TestCreateSiteManagedRejectsSingleLabelHost(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var userID, orgID int64
	if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash) values ('owner@example.test','Owner','hash') returning id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Managed','managed-host') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into memberships(org_id,user_id,role) values ($1,$2,'owner')`, orgID, userID); err != nil {
		t.Fatal(err)
	}
	data := &store.Store{Pool: pool}
	registry := sitesvc.NewRegistry(data, 0)
	t.Cleanup(registry.Close)
	service := sitesvc.New(data, registry)
	service.SetRequireDomain(true)
	principal := authz.Principal{UserID: userID, OrgID: orgID, Role: authz.RoleOwner}
	handler := platformhttp.New(platformhttp.Deps{
		Authenticator: actorAuthenticator{"owner": principal}, Sites: registry,
		Mutations: platformhttp.Services{Sites: service}, Store: data,
	})
	post := func(host string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, authenticatedRequest(http.MethodPost, "/api/private/sites", `{"name":"S","hosts":["`+host+`"],"timezone":"UTC","retention_days":30,"revenue_currency":"USD"}`))
		return response
	}
	if response := post("afaridar"); response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `host \"afaridar\" is not a domain: use something like example.com`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	if response := post("example.com"); response.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
}
