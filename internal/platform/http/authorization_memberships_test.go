package platformhttp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"panel/internal/db"
	platformauth "panel/internal/platform/auth"
	"panel/internal/platform/authn"
	"panel/internal/platform/authz"
	platformcrypto "panel/internal/platform/crypto"
	platformhttp "panel/internal/platform/http"
	"panel/internal/platform/invites"
	"panel/internal/platform/mail"
	"panel/internal/platform/orgs"
	"panel/internal/platform/store"
	"panel/internal/platform/users"
	"panel/internal/testpg"
)

type membershipHTTPFixture struct {
	handler       http.Handler
	authenticator *platformhttp.ServiceAuthenticator
	analyticsAuth *authn.Authenticator
	data          *store.Store
	orgID         int64
	siteID        int32
	users         map[string]int64
}

func newMembershipHTTPFixture(t *testing.T) membershipHTTPFixture {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	data := &store.Store{Pool: pool}
	f := membershipHTTPFixture{data: data, users: map[string]int64{}}
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Access','access') returning id`).Scan(&f.orgID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class) values ($1,'accesshttp01','Site','UTC',30) returning id`, f.orgID).Scan(&f.siteID); err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"owner", "admin", "viewer"} {
		var id int64
		if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash) values ($1,$2,'hash') returning id`, role+"@example.test", role).Scan(&id); err != nil {
			t.Fatal(err)
		}
		f.users[role] = id
		if _, err := pool.Exec(ctx, `insert into memberships(org_id,user_id,role) values ($1,$2,$3)`, f.orgID, id, role); err != nil {
			t.Fatal(err)
		}
		now := time.Now()
		if err := store.CreateSession(ctx, pool, store.UserSession{IDHash: platformcrypto.HashToken(role + "-session"), UserID: id, CreatedAt: now, ExpiresAt: now.Add(time.Hour), DeviceLabel: "Test"}); err != nil {
			t.Fatal(err)
		}
	}
	box, err := platformcrypto.NewSecretBox([]byte("0123456789abcdef0123456789abcdef"), "panel/totp/v1")
	if err != nil {
		t.Fatal(err)
	}
	a := platformauth.New(data, &mail.Fake{}, box, "https://panel.example")
	o := orgs.New(data)
	f.authenticator = platformhttp.NewAuthenticator(a, nil, nil, o)
	f.analyticsAuth = authn.New(a, nil, nil, o)
	f.handler = platformhttp.New(platformhttp.Deps{Auth: a, Store: data, Orgs: o, Invites: invites.New(data, users.New(data), &mail.Fake{}, "https://panel.example"), Authenticator: f.authenticator})
	return f
}

func (f membershipHTTPFixture) request(method, path, actor, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-CSRF-Token", "token")
	r.AddCookie(&http.Cookie{Name: "panel_csrf", Value: "token"})
	r.AddCookie(&http.Cookie{Name: platformauth.CookieName, Value: actor + "-session"})
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	return w
}

func (f membershipHTTPFixture) memberPath(role string) string {
	return fmt.Sprintf("/api/private/orgs/%d/members/%d", f.orgID, f.users[role])
}

func TestMembershipHTTPPreservesOmittedAccessAndExplicitSiteScopes(t *testing.T) {
	f := newMembershipHTTPFixture(t)
	path := f.memberPath("viewer")
	initial := fmt.Sprintf(`{"site_ids":[%d],"capabilities":["profiles.read","data.export"],"billing_admin":true}`, f.siteID)
	if w := f.request(http.MethodPatch, path, "owner", initial); w.Code != http.StatusNoContent {
		t.Fatalf("initialize: %d %s", w.Code, w.Body)
	}
	if w := f.request(http.MethodPatch, path, "admin", `{"role":"editor"}`); w.Code != http.StatusNoContent {
		t.Fatalf("role-only update: %d %s", w.Code, w.Body)
	}
	var got struct {
		Role         string
		SiteIDs      []int32
		Capabilities []string
		Billing      bool
	}
	if err := f.data.Pool.QueryRow(context.Background(), `select role,site_ids,capabilities,billing_admin from memberships where org_id=$1 and user_id=$2`, f.orgID, f.users["viewer"]).Scan(&got.Role, &got.SiteIDs, &got.Capabilities, &got.Billing); err != nil {
		t.Fatal(err)
	}
	if got.Role != "editor" || !reflect.DeepEqual(got.SiteIDs, []int32{f.siteID}) || !reflect.DeepEqual(got.Capabilities, []string{"profiles.read", "data.export"}) || !got.Billing {
		t.Fatalf("omitted access was changed: %+v", got)
	}
	for _, tc := range []struct {
		body    string
		wantNil bool
	}{{`{"site_ids":[]}`, false}, {`{"role":"viewer"}`, false}, {`{"site_ids":null}`, true}} {
		if w := f.request(http.MethodPatch, path, "owner", tc.body); w.Code != http.StatusNoContent {
			t.Fatalf("%s: %d %s", tc.body, w.Code, w.Body)
		}
		if err := f.data.Pool.QueryRow(context.Background(), `select site_ids from memberships where org_id=$1 and user_id=$2`, f.orgID, f.users["viewer"]).Scan(&got.SiteIDs); err != nil {
			t.Fatal(err)
		}
		if (got.SiteIDs == nil) != tc.wantNil {
			t.Fatalf("%s: sites=%v nil=%t", tc.body, got.SiteIDs, got.SiteIDs == nil)
		}
	}
}

func TestMembershipHTTPRejectsEscalationAndKeepsLastOwner(t *testing.T) {
	f := newMembershipHTTPFixture(t)
	for _, tc := range []struct {
		actor, target, body string
		status              int
	}{
		{"admin", "admin", `{"billing_admin":true}`, http.StatusForbidden},
		{"admin", "owner", `{"capabilities":[]}`, http.StatusForbidden},
		{"owner", "owner", `{"role":"viewer"}`, http.StatusBadRequest},
		{"owner", "viewer", `{"capabilities":["billing.manage"]}`, http.StatusBadRequest},
		{"owner", "viewer", `{"site_ids":[999999]}`, http.StatusBadRequest},
		{"viewer", "viewer", `{"capabilities":["replay.read"]}`, http.StatusForbidden},
	} {
		w := f.request(http.MethodPatch, f.memberPath(tc.target), tc.actor, tc.body)
		if w.Code != tc.status {
			t.Fatalf("%s -> %s %s: status=%d want=%d body=%s", tc.actor, tc.target, tc.body, w.Code, tc.status, w.Body)
		}
	}
	w := f.request(http.MethodPatch, fmt.Sprintf("/api/private/orgs/%d/members/%d", f.orgID+1, f.users["viewer"]), "owner", `{"role":"admin"}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-org mutation: %d %s", w.Code, w.Body)
	}
}

func TestMembershipHTTPBillingOnlyMeAndListSerialization(t *testing.T) {
	f := newMembershipHTTPFixture(t)
	w := f.request(http.MethodPatch, f.memberPath("viewer"), "owner", `{"site_ids":[],"billing_admin":true}`)
	if w.Code != http.StatusNoContent {
		t.Fatalf("billing setup: %d %s", w.Code, w.Body)
	}
	w = f.request(http.MethodGet, "/api/private/me", "viewer", "")
	if w.Code != http.StatusOK {
		t.Fatalf("me: %d %s", w.Code, w.Body)
	}
	var me struct {
		Memberships []struct {
			SiteIDs      json.RawMessage `json:"site_ids"`
			Sites        []any           `json:"sites"`
			Capabilities []string        `json:"capabilities"`
			Effective    []string        `json:"effective_capabilities"`
			Billing      bool            `json:"billing_admin"`
		} `json:"memberships"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &me); err != nil {
		t.Fatal(err)
	}
	if len(me.Memberships) != 1 {
		t.Fatalf("memberships=%+v", me)
	}
	m := me.Memberships[0]
	if string(m.SiteIDs) != "[]" || len(m.Sites) != 0 || m.Capabilities == nil || !m.Billing || !reflect.DeepEqual(m.Effective, []string{"billing.manage"}) {
		t.Fatalf("billing-only response=%s", w.Body)
	}
	w = f.request(http.MethodGet, fmt.Sprintf("/api/private/orgs/%d/members", f.orgID), "owner", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"site_ids":null`) || !strings.Contains(w.Body.String(), `"site_ids":[]`) || !strings.Contains(w.Body.String(), `"billing_admin":true`) {
		t.Fatalf("member serialization: %d %s", w.Code, w.Body)
	}
}

func TestMembershipSessionAdaptersReloadRevokedGrants(t *testing.T) {
	f := newMembershipHTTPFixture(t)
	path := f.memberPath("viewer")
	if w := f.request(http.MethodPatch, path, "owner", `{"capabilities":["profiles.read","replay.read","data.export"]}`); w.Code != http.StatusNoContent {
		t.Fatal(w.Body.String())
	}
	r := httptest.NewRequest(http.MethodGet, "/api/private/stats/summary", nil)
	r.AddCookie(&http.Cookie{Name: platformauth.CookieName, Value: "viewer-session"})
	p, ok := f.authenticator.Authenticate(r)
	if !ok || !authz.Can(p, authz.ActionProfilesRead, f.siteID) {
		t.Fatalf("platform grant missing: %+v %t", p, ok)
	}
	ap, ok := f.analyticsAuth.Authenticate(r)
	if !ok || !slices.Contains(ap.Scopes, "profiles:read") || slices.Contains(ap.Scopes, "*") {
		t.Fatalf("analytics grant missing: %+v %t", ap, ok)
	}
	if w := f.request(http.MethodPatch, path, "owner", `{"capabilities":[]}`); w.Code != http.StatusNoContent {
		t.Fatal(w.Body.String())
	}
	p, ok = f.authenticator.Authenticate(r)
	if !ok || authz.Can(p, authz.ActionProfilesRead, f.siteID) || authz.Can(p, authz.ActionReplayRead, f.siteID) {
		t.Fatalf("platform cached revoked grant: %+v %t", p, ok)
	}
	ap, ok = f.analyticsAuth.Authenticate(r)
	if !ok || slices.Contains(ap.Scopes, "profiles:read") || slices.Contains(ap.Scopes, "data:export") {
		t.Fatalf("analytics cached revoked grant: %+v %t", ap, ok)
	}
	if w := f.request(http.MethodDelete, path, "owner", ""); w.Code != http.StatusNoContent {
		t.Fatal(w.Body.String())
	}
	if _, ok := f.authenticator.Authenticate(r); ok {
		t.Fatal("removed member retained platform session access")
	}
	if _, ok := f.analyticsAuth.Authenticate(r); ok {
		t.Fatal("removed member retained analytics session access")
	}
}

func TestInvitationHTTPGrantSerialization(t *testing.T) {
	f := newMembershipHTTPFixture(t)
	path := fmt.Sprintf("/api/private/orgs/%d/invitations", f.orgID)
	body := `{"email":"billing@example.test","role":"viewer","site_ids":[],"capabilities":["replay.read"],"billing_admin":true}`
	if w := f.request(http.MethodPost, path, "admin", body); w.Code != http.StatusForbidden {
		t.Fatalf("admin billing invite: %d %s", w.Code, w.Body)
	}
	if w := f.request(http.MethodPost, path, "owner", body); w.Code != http.StatusCreated {
		t.Fatalf("owner billing invite: %d %s", w.Code, w.Body)
	}
	w := f.request(http.MethodGet, path, "owner", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"site_ids":[]`) || !strings.Contains(w.Body.String(), `"capabilities":["replay.read"]`) || !strings.Contains(w.Body.String(), `"billing_admin":true`) {
		t.Fatalf("invitation list: %d %s", w.Code, w.Body)
	}
}
