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

	"github.com/jackc/pgx/v5/pgxpool"

	"panel/internal/db"
	"panel/internal/platform/authz"
	platformhttp "panel/internal/platform/http"
	"panel/internal/platform/keys"
	"panel/internal/platform/store"
	"panel/internal/testpg"
)

func TestPublicAPIKeysRejectEscalation(t *testing.T) {
	env := newAPIKeysHTTPEnv(t)
	longerExpiry := env.expiresAt.Add(time.Second).Format(time.RFC3339Nano)
	expiresAt := env.expiresAt.Format(time.RFC3339Nano)
	tests := []struct {
		name string
		body string
	}{
		{name: "broader scope", body: fmt.Sprintf(`{"name":"bad","scopes":["sites:write"],"site_ids":[10],"expires_at":%q}`, expiresAt)},
		{name: "management scope", body: fmt.Sprintf(`{"name":"bad","scopes":["keys:manage"],"site_ids":[10],"expires_at":%q}`, expiresAt)},
		{name: "extra site", body: fmt.Sprintf(`{"name":"bad","scopes":["stats:read"],"site_ids":[10,20],"expires_at":%q}`, expiresAt)},
		{name: "all sites", body: fmt.Sprintf(`{"name":"bad","scopes":["stats:read"],"expires_at":%q}`, expiresAt)},
		{name: "longer expiry", body: fmt.Sprintf(`{"name":"bad","scopes":["stats:read"],"site_ids":[10],"expires_at":%q}`, longerExpiry)},
		{name: "no expiry", body: `{"name":"bad","scopes":["stats:read"],"site_ids":[10]}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := env.request(http.MethodPost, "/api/public/v1/api-keys", tt.body, env.managerSecret)
			if response.Code != http.StatusForbidden {
				t.Fatalf("status=%d body=%s, want 403", response.Code, response.Body.String())
			}
		})
	}

	response := env.request(http.MethodGet, "/api/public/v1/api-keys", "", env.statsSecret)
	if response.Code != http.StatusForbidden {
		t.Fatalf("key without keys:manage status=%d body=%s, want 403", response.Code, response.Body.String())
	}
}

func TestPublicAPIKeyLifecycle(t *testing.T) {
	env := newAPIKeysHTTPEnv(t)
	ctx := context.Background()
	if _, err := env.pool.Exec(ctx, `delete from audit_log`); err != nil {
		t.Fatal(err)
	}

	childExpiry := env.expiresAt.Add(-time.Minute).Format(time.RFC3339Nano)
	created := env.request(http.MethodPost, "/api/public/v1/api-keys", fmt.Sprintf(
		`{"name":"Reporting","scopes":["stats:read"],"site_ids":[10],"expires_at":%q}`, childExpiry,
	), env.managerSecret)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	var createBody struct {
		ID  int64  `json:"id"`
		Key string `json:"key"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &createBody); err != nil {
		t.Fatal(err)
	}
	if createBody.ID == 0 || !strings.HasPrefix(createBody.Key, "pnl_") {
		t.Fatalf("create response = %+v", createBody)
	}

	listed := env.request(http.MethodGet, "/api/public/v1/api-keys", "", env.managerSecret)
	if listed.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", listed.Code, listed.Body.String())
	}
	if strings.Contains(listed.Body.String(), createBody.Key) || strings.Contains(strings.ToLower(listed.Body.String()), "hash") || strings.Contains(strings.ToLower(listed.Body.String()), "prefix") {
		t.Fatalf("list leaked key material: %s", listed.Body.String())
	}
	var listBody struct {
		Data []map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(listed.Body.Bytes(), &listBody); err != nil {
		t.Fatal(err)
	}
	wantFields := map[string]bool{
		"id": true, "name": true, "scopes": true, "site_ids": true, "created_at": true,
		"expires_at": true, "last_used_at": true, "revoked": true,
	}
	for _, item := range listBody.Data {
		if string(item["id"]) == fmt.Sprint(env.foreignKeyID) {
			t.Fatalf("list exposed another organization's key: %s", listed.Body.String())
		}
		for field := range item {
			if !wantFields[field] {
				t.Fatalf("unexpected list field %q in %s", field, listed.Body.String())
			}
		}
		for field := range wantFields {
			if _, ok := item[field]; !ok {
				t.Fatalf("missing list field %q in %s", field, listed.Body.String())
			}
		}
	}

	foreign := env.request(http.MethodDelete, fmt.Sprintf("/api/public/v1/api-keys/%d", env.foreignKeyID), "", env.managerSecret)
	if foreign.Code != http.StatusNotFound {
		t.Fatalf("cross-org revoke status=%d body=%s, want 404", foreign.Code, foreign.Body.String())
	}

	revoked := env.request(http.MethodDelete, fmt.Sprintf("/api/public/v1/api-keys/%d", env.managerKeyID), "", env.managerSecret)
	if revoked.Code != http.StatusNoContent {
		t.Fatalf("self revoke status=%d body=%s", revoked.Code, revoked.Body.String())
	}
	usedAfterRevoke := env.request(http.MethodGet, "/api/public/v1/api-keys", "", env.managerSecret)
	if usedAfterRevoke.Code != http.StatusUnauthorized {
		t.Fatalf("revoked key status=%d body=%s, want 401", usedAfterRevoke.Code, usedAfterRevoke.Body.String())
	}

	rows, err := env.pool.Query(ctx, `select action, actor_key_id, actor_user_id from audit_log order by id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var actions []string
	for rows.Next() {
		var action string
		var actorKeyID, actorUserID *int64
		if err := rows.Scan(&action, &actorKeyID, &actorUserID); err != nil {
			t.Fatal(err)
		}
		if actorKeyID == nil || *actorKeyID != env.managerKeyID || actorUserID != nil {
			t.Fatalf("audit %q actor key=%v user=%v", action, actorKeyID, actorUserID)
		}
		actions = append(actions, action)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(actions, ","); got != "api_key.created,api_key.revoked" {
		t.Fatalf("audit actions=%q", got)
	}
}

type apiKeysHTTPEnv struct {
	handler       http.Handler
	pool          *pgxpool.Pool
	managerSecret string
	statsSecret   string
	managerKeyID  int64
	foreignKeyID  int64
	expiresAt     time.Time
}

func newAPIKeysHTTPEnv(t *testing.T) apiKeysHTTPEnv {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	createActor := func(email, slug string) authz.Principal {
		var userID, orgID int64
		if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash) values ($1,'Owner','hash') returning id`, email).Scan(&userID); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Org',$1) returning id`, slug).Scan(&orgID); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `insert into memberships(org_id,user_id,role) values($1,$2,'owner')`, orgID, userID); err != nil {
			t.Fatal(err)
		}
		return authz.Principal{UserID: userID, OrgID: orgID, Role: authz.RoleOwner}
	}
	owner := createActor("owner@example.test", "owner")
	otherOwner := createActor("other@example.test", "other")
	if _, err := pool.Exec(ctx, `insert into sites(id,org_id,public_id,name,timezone,retention_class) overriding system value values(10,$1,'keysite00010','Ten','UTC',30),(20,$1,'keysite00020','Twenty','UTC',30)`, owner.OrgID); err != nil {
		t.Fatal(err)
	}
	data := &store.Store{Pool: pool}
	keyService := keys.New(data)
	t.Cleanup(keyService.Close)
	expiresAt := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	managerSecret, manager, err := keyService.Create(ctx, owner, "Automation", []string{"keys:manage", "stats:read"}, []int32{10}, &expiresAt)
	if err != nil {
		t.Fatal(err)
	}
	statsSecret, _, err := keyService.Create(ctx, owner, "Stats", []string{"stats:read"}, []int32{10}, &expiresAt)
	if err != nil {
		t.Fatal(err)
	}
	_, foreign, err := keyService.Create(ctx, otherOwner, "Foreign", []string{"stats:read"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := platformhttp.New(platformhttp.Deps{
		Authenticator: platformhttp.NewAuthenticator(nil, keyService, nil, nil),
		Mutations:     platformhttp.Services{Keys: keyService},
		Keys:          keyService,
	})
	return apiKeysHTTPEnv{
		handler: handler, pool: pool, managerSecret: managerSecret, statsSecret: statsSecret,
		managerKeyID: manager.ID, foreignKeyID: foreign.ID, expiresAt: expiresAt,
	}
}

func (e apiKeysHTTPEnv) request(method, path, body, secret string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+secret)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	e.handler.ServeHTTP(response, request)
	return response
}
