package platformhttp_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"fmt"

	"supermetry/internal/db"
	platformauth "supermetry/internal/platform/auth"
	"supermetry/internal/platform/authz"
	platformcrypto "supermetry/internal/platform/crypto"
	platformhttp "supermetry/internal/platform/http"
	"supermetry/internal/platform/mail"
	"supermetry/internal/platform/sitesvc"
	"supermetry/internal/platform/store"
	"supermetry/internal/platform/users"
	"supermetry/internal/testpg"
)

func TestDataPurgeRequestNeedsPasswordAndValidFilters(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	data := &store.Store{Pool: pool}
	box, err := platformcrypto.NewSecretBox([]byte("0123456789abcdef0123456789abcdef"), "supermetry/totp/v1")
	if err != nil {
		t.Fatal(err)
	}
	authService := platformauth.New(data, &mail.Fake{}, box, "https://supermetry.example")
	userID, err := users.New(data).Create(ctx, "purge@example.test", "Purge", "pw-123456789")
	if err != nil {
		t.Fatal(err)
	}
	var orgID int64
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Purge','purge') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into memberships(org_id,user_id,role) values ($1,$2,'owner')`, orgID, userID); err != nil {
		t.Fatal(err)
	}
	registry := sitesvc.NewRegistry(data, 0)
	t.Cleanup(registry.Close)
	owner := authz.Principal{UserID: userID, OrgID: orgID, Role: authz.RoleOwner}
	site, err := sitesvc.New(data, registry).Create(ctx, owner, "Purge", []string{"purge.example.test"}, "UTC", 30, "USD")
	if err != nil {
		t.Fatal(err)
	}
	handler := platformhttp.New(platformhttp.Deps{
		Auth: authService, Store: data, Sites: registry,
		Authenticator: actorAuthenticator{"owner": owner, "viewer": {UserID: userID, OrgID: orgID, Role: authz.RoleViewer}},
	})
	path := fmt.Sprintf("/api/private/sites/%d/purge-requests", site.ID)
	send := func(actor, method, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Test-Actor", actor)
		request.Header.Set("X-CSRF-Token", "token")
		request.AddCookie(&http.Cookie{Name: "supermetry_csrf", Value: "token"})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	good := `{"from":"2026-10-01","to":"2026-10-05","source":"spam.test","password":"pw-123456789"}`

	if got := send("viewer", http.MethodPost, good); got.Code != http.StatusForbidden {
		t.Fatalf("viewer status=%d body=%s", got.Code, got.Body.String())
	}
	if got := send("owner", http.MethodPost, `{"from":"2026-10-01","to":"2026-10-05","source":"spam.test","password":"wrong-password"}`); got.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password status=%d body=%s", got.Code, got.Body.String())
	}
	if got := send("owner", http.MethodPost, `{"from":"2026-10-01","to":"2026-10-05","password":"pw-123456789"}`); got.Code != http.StatusBadRequest {
		t.Fatalf("no filter status=%d body=%s", got.Code, got.Body.String())
	}
	if got := send("owner", http.MethodPost, `{"from":"yesterday","to":"2026-10-05","source":"x","password":"pw-123456789"}`); got.Code != http.StatusBadRequest {
		t.Fatalf("bad date status=%d body=%s", got.Code, got.Body.String())
	}
	var count int
	if err := pool.QueryRow(ctx, `select count(*) from data_purge_requests`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rows after rejected requests = %d err=%v", count, err)
	}
	if got := send("owner", http.MethodPost, good); got.Code != http.StatusAccepted || !strings.Contains(got.Body.String(), `"status":"pending"`) {
		t.Fatalf("accepted status=%d body=%s", got.Code, got.Body.String())
	}
	if got := send("owner", http.MethodPost, good); got.Code != http.StatusConflict {
		t.Fatalf("second request status=%d body=%s", got.Code, got.Body.String())
	}
	list := send("owner", http.MethodGet, "")
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"source":"spam.test"`) {
		t.Fatalf("list status=%d body=%s", list.Code, list.Body.String())
	}
}
