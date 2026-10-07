package authn_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"panel/internal/api"
	"panel/internal/db"
	"panel/internal/platform/auth"
	"panel/internal/platform/authn"
	"panel/internal/platform/authz"
	platformcrypto "panel/internal/platform/crypto"
	"panel/internal/platform/keys"
	"panel/internal/platform/mail"
	"panel/internal/platform/orgs"
	"panel/internal/platform/shares"
	"panel/internal/platform/store"
	"panel/internal/testpg"
)

type disabledMail struct{}

func (disabledMail) Send(context.Context, mail.Message) error { return nil }
func (disabledMail) Enabled() bool                            { return false }

type testEnv struct {
	authenticator *authn.Authenticator
	key           string
	session       string
	viewerSession string
	share         string
	orgID         int64
	otherOrgID    int64
	siteID        int32
	otherSiteID   int32
	pool          *pgxpool.Pool
}

func TestAuthenticator_Authenticate(t *testing.T) {
	env := newTestEnv(t)

	tests := []struct {
		name    string
		path    string
		headers map[string]string
		cookies []*http.Cookie
		want    api.Principal
		wantOK  bool
	}{
		{
			name: "public API key",
			path: "/api/public/v1/stats/summary",
			headers: map[string]string{
				"Authorization": "Bearer " + env.key,
			},
			want:   api.Principal{OrgID: env.orgID, Scopes: []string{"stats:read"}, SiteIDs: []int32{env.siteID}},
			wantOK: true,
		},
		{
			name: "bearer scheme is case insensitive",
			path: "/api/public/v1/stats/summary",
			headers: map[string]string{
				"Authorization": "bearer " + env.key,
			},
			want:   api.Principal{OrgID: env.orgID, Scopes: []string{"stats:read"}, SiteIDs: []int32{env.siteID}},
			wantOK: true,
		},
		{
			name:    "private session uses only membership",
			path:    "/api/private/stats/summary",
			cookies: []*http.Cookie{{Name: auth.CookieName, Value: env.session}},
			want:    api.Principal{OrgID: env.orgID, Scopes: []string{"*"}},
			wantOK:  true,
		},
		{
			name:    "restricted viewer session",
			path:    "/api/private/stats/summary",
			cookies: []*http.Cookie{{Name: auth.CookieName, Value: env.viewerSession}},
			want:    api.Principal{OrgID: env.orgID, Scopes: []string{"stats:read"}, SiteIDs: []int32{env.siteID}},
			wantOK:  true,
		},
		{
			name: "private session selects membership header",
			path: "/api/private/stats/summary",
			headers: map[string]string{
				"X-Panel-Org": integer(env.orgID),
			},
			cookies: []*http.Cookie{{Name: auth.CookieName, Value: env.session}},
			want:    api.Principal{OrgID: env.orgID, Scopes: []string{"*"}},
			wantOK:  true,
		},
		{
			name:   "shared dashboard path",
			path:   "/api/private/share/" + env.share + "/stats/summary",
			want:   api.Principal{OrgID: env.orgID, SiteIDs: []int32{env.siteID}, Scopes: []string{"stats:read"}},
			wantOK: true,
		},
		{
			name: "session rejects foreign organization header",
			path: "/api/private/stats/summary",
			headers: map[string]string{
				"X-Panel-Org": integer(env.otherOrgID),
			},
			cookies: []*http.Cookie{{Name: auth.CookieName, Value: env.session}},
		},
		{
			name: "private API rejects key",
			path: "/api/private/stats/summary",
			headers: map[string]string{
				"Authorization": "Bearer " + env.key,
			},
		},
		{
			name:    "public API rejects session",
			path:    "/api/public/v1/stats/summary",
			cookies: []*http.Cookie{{Name: auth.CookieName, Value: env.session}},
		},
		{
			name: "unrecognized path rejects credentials",
			path: "/healthz",
			headers: map[string]string{
				"Authorization": "Bearer " + env.key,
			},
			cookies: []*http.Cookie{{Name: auth.CookieName, Value: env.session}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, tt.path, nil)
			for name, value := range tt.headers {
				request.Header.Set(name, value)
			}
			for _, cookie := range tt.cookies {
				request.AddCookie(cookie)
			}

			got, ok := env.authenticator.Authenticate(request)
			if ok != tt.wantOK || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Authenticate() = (%+v, %t), want (%+v, %t)", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestAuthenticator_ViewerCannotReadAdminHealth(t *testing.T) {
	env := newTestEnv(t)
	router := api.NewRouter(api.Deps{PrivateAuth: env.authenticator})
	request := httptest.NewRequest(http.MethodGet, "/api/private/health", nil)
	request.AddCookie(&http.Cookie{Name: auth.CookieName, Value: env.viewerSession})
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("viewer health status = %d, want %d; body=%s", response.Code, http.StatusForbidden, response.Body.String())
	}
}

func TestAuthenticator_SessionWithoutOrgRejectsMultipleMemberships(t *testing.T) {
	env := newTestEnv(t)
	if _, err := env.pool.Exec(context.Background(), `
		insert into memberships(org_id, user_id, role)
		select $1, user_id, 'viewer' from memberships where org_id=$2`, env.otherOrgID, env.orgID); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/private/stats/summary", nil)
	request.AddCookie(&http.Cookie{Name: auth.CookieName, Value: env.session})
	if got, ok := env.authenticator.Authenticate(request); ok {
		t.Fatalf("Authenticate() = (%+v, true), want no principal for ambiguous membership", got)
	}
}

func newTestEnv(t *testing.T) testEnv {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	data := &store.Store{Pool: pool}

	var userID, viewerID, orgID, otherOrgID int64
	if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash) values ('user@example.test','User','hash') returning id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash) values ('viewer@example.test','Viewer','hash') returning id`).Scan(&viewerID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Acme','acme') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Other','other') returning id`).Scan(&otherOrgID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into memberships(org_id,user_id,role) values ($1,$2,'owner')`, orgID, userID); err != nil {
		t.Fatal(err)
	}

	var siteID, otherSiteID int32
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class) values ($1,'acmesite0001','Acme','UTC',30) returning id`, orgID).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class) values ($1,'othersite001','Other','UTC',30) returning id`, otherOrgID).Scan(&otherSiteID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into memberships(org_id,user_id,role,site_ids) values ($1,$2,'viewer',$3)`, orgID, viewerID, []int32{siteID}); err != nil {
		t.Fatal(err)
	}

	box, err := platformcrypto.NewSecretBox([]byte("0123456789abcdef0123456789abcdef"), "panel/totp/v1")
	if err != nil {
		t.Fatal(err)
	}
	authService := auth.New(data, disabledMail{}, box, "https://panel.example")
	session := "session-token"
	now := time.Now()
	if err := store.CreateSession(ctx, pool, store.UserSession{
		IDHash: platformcrypto.HashToken(session), UserID: userID,
		CreatedAt: now, ExpiresAt: now.Add(time.Hour), DeviceLabel: "Test browser",
	}); err != nil {
		t.Fatal(err)
	}
	viewerSession := "viewer-session-token"
	if err := store.CreateSession(ctx, pool, store.UserSession{
		IDHash: platformcrypto.HashToken(viewerSession), UserID: viewerID,
		CreatedAt: now, ExpiresAt: now.Add(time.Hour), DeviceLabel: "Viewer browser",
	}); err != nil {
		t.Fatal(err)
	}

	keyService := keys.New(data)
	t.Cleanup(keyService.Close)
	owner := authz.Principal{UserID: userID, OrgID: orgID, Role: authz.RoleOwner}
	key, _, err := keyService.Create(ctx, owner, "Test", []string{"stats:read"}, []int32{siteID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	shareService := shares.New(data)
	share, _, err := shareService.Create(ctx, owner, siteID, "")
	if err != nil {
		t.Fatal(err)
	}

	env := testEnv{
		authenticator: authn.New(authService, keyService, shareService, orgs.New(data)),
		key:           key, session: session, viewerSession: viewerSession, share: share,
		orgID: orgID, otherOrgID: otherOrgID, siteID: siteID, otherSiteID: otherSiteID,
		pool: pool,
	}
	return env
}

func integer(value int64) string {
	return strconv.FormatInt(value, 10)
}
