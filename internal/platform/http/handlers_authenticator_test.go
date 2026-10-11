package platformhttp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	platformauth "supermetry/internal/platform/auth"
	platformcrypto "supermetry/internal/platform/crypto"
	"supermetry/internal/platform/store"
)

func TestAccountSessionIndependentOfOrganization(t *testing.T) {
	for _, count := range []int{0, 1, 2} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			f := newMembershipHTTPFixture(t)
			ctx := context.Background()
			if count == 0 {
				if _, err := f.data.Pool.Exec(ctx, `delete from memberships where user_id=$1`, f.users["viewer"]); err != nil {
					t.Fatal(err)
				}
			}
			if count == 2 {
				var id int64
				if err := f.data.Pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Other','other') returning id`).Scan(&id); err != nil {
					t.Fatal(err)
				}
				if _, err := f.data.Pool.Exec(ctx, `insert into memberships(org_id,user_id,role) values ($1,$2,'viewer')`, id, f.users["viewer"]); err != nil {
					t.Fatal(err)
				}
			}
			for _, header := range []string{"", "999999", "invalid"} {
				r := httptest.NewRequest(http.MethodGet, "/api/private/me", nil)
				r.AddCookie(&http.Cookie{Name: platformauth.CookieName, Value: "viewer-session"})
				r.Header.Set("X-Supermetry-Org", header)
				p, ok := f.authenticator.Authenticate(r)
				if !ok || p.UserID != f.users["viewer"] || p.OrgID != 0 || p.Role != "" || p.SiteIDs == nil || len(p.SiteIDs) != 0 || len(p.Capabilities) != 0 || p.BillingAdmin {
					t.Fatalf("account principal: %+v, %t", p, ok)
				}
				w := httptest.NewRecorder()
				f.handler.ServeHTTP(w, r)
				if w.Code != http.StatusOK {
					t.Fatalf("me: %d %s", w.Code, w.Body)
				}
				var body struct {
					Memberships []json.RawMessage `json:"memberships"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if len(body.Memberships) != count {
					t.Fatalf("memberships=%d want=%d", len(body.Memberships), count)
				}
			}
			for _, path := range []string{"/api/private/orgs", "/api/private/me/sessions"} {
				if w := f.request(http.MethodGet, path, "viewer", ""); w.Code != http.StatusOK {
					t.Fatalf("%s: %d %s", path, w.Code, w.Body)
				}
			}
			if w := f.request(http.MethodPatch, "/api/private/me", "viewer", `{"name":"Updated"}`); w.Code != http.StatusOK {
				t.Fatalf("update account: %d %s", w.Code, w.Body)
			}
			r := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/private/orgs/%d", f.orgID), nil)
			r.AddCookie(&http.Cookie{Name: platformauth.CookieName, Value: "viewer-session"})
			if _, ok := f.authenticator.Authenticate(r); ok != (count == 1) {
				t.Fatalf("tenant without selection accepted=%t count=%d", ok, count)
			}
			r.Header.Set("X-Supermetry-Org", "999999")
			if _, ok := f.authenticator.Authenticate(r); ok {
				t.Fatal("foreign tenant authenticated")
			}
			r.Header.Set("X-Supermetry-Org", fmt.Sprint(f.orgID))
			if _, ok := f.authenticator.Authenticate(r); ok != (count > 0) {
				t.Fatalf("selected tenant accepted=%t count=%d", ok, count)
			}
			if w := f.request(http.MethodGet, "/api/private/me", "unknown", ""); w.Code != http.StatusUnauthorized {
				t.Fatalf("missing session: %d", w.Code)
			}
		})
	}
}

func TestCurrentUserIncludesBuildVersion(t *testing.T) {
	f := newMembershipHTTPFixture(t)
	response := f.request(http.MethodGet, "/api/private/me", "owner", "")
	if response.Code != http.StatusOK {
		t.Fatalf("me status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Version != "1.4.2" {
		t.Fatalf("version=%q, want 1.4.2", body.Version)
	}
}

func TestLogoutCurrentSessionAndCSRF(t *testing.T) {
	f := newMembershipHTTPFixture(t)
	ctx := context.Background()
	// Account logout must remain available after the last membership is removed.
	if _, err := f.data.Pool.Exec(ctx, `delete from memberships where user_id=$1`, f.users["viewer"]); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := store.CreateSession(ctx, f.data.Pool, store.UserSession{IDHash: platformcrypto.HashToken("other-session"), UserID: f.users["viewer"], CreatedAt: now, ExpiresAt: now.Add(time.Hour), DeviceLabel: "Other"}); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/private/auth/logout", nil)
	r.AddCookie(&http.Cookie{Name: platformauth.CookieName, Value: "viewer-session"})
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden || len(w.Result().Cookies()) != 0 {
		t.Fatalf("missing csrf: %d", w.Code)
	}
	if _, err := f.data.SessionByHash(ctx, platformcrypto.HashToken("viewer-session")); err != nil {
		t.Fatalf("CSRF rejection revoked session: %v", err)
	}
	w = f.request(http.MethodPost, "/api/private/auth/logout", "viewer", "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("logout: %d %s", w.Code, w.Body)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 2 {
		t.Fatalf("cookies: %+v", cookies)
	}
	for _, cookie := range cookies {
		if cookie.MaxAge != -1 || cookie.Value != "" || cookie.Path != "/" || !cookie.Expires.Before(now) {
			t.Fatalf("cookie not cleared: %+v", cookie)
		}
		if cookie.Name == platformauth.CookieName && !cookie.HttpOnly {
			t.Fatal("session cookie must be HttpOnly")
		}
	}
	if _, err := f.data.SessionByHash(ctx, platformcrypto.HashToken("viewer-session")); err == nil {
		t.Fatal("current session persists")
	}
	if _, err := f.data.SessionByHash(ctx, platformcrypto.HashToken("other-session")); err != nil {
		t.Fatalf("other session revoked: %v", err)
	}
	if w := f.request(http.MethodGet, "/api/private/me", "viewer", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("revoked session: %d", w.Code)
	}
	if w := f.request(http.MethodPost, "/api/private/auth/logout", "viewer", ""); w.Code != http.StatusNoContent {
		t.Fatalf("repeated logout: %d", w.Code)
	}
	if _, err := f.data.Pool.Exec(ctx, `update user_sessions set expires_at=$1 where id_hash=$2`, now.Add(-time.Hour), platformcrypto.HashToken("other-session")); err != nil {
		t.Fatal(err)
	}
	w = f.request(http.MethodPost, "/api/private/auth/logout", "other", "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("expired session: %d %s", w.Code, w.Body)
	}
}

func TestLogoutDatabaseFailureDoesNotConfirmSuccess(t *testing.T) {
	f := newMembershipHTTPFixture(t)
	// A closed real PostgreSQL pool makes the DELETE fail deterministically.
	// It must not be confused with an expired or missing session.
	f.data.Pool.Close()
	w := f.request(http.MethodPost, "/api/private/auth/logout", "viewer", "")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("database failure: %d %s", w.Code, w.Body)
	}
	if len(w.Result().Cookies()) != 0 {
		t.Fatal("failed deletion cleared auth cookies")
	}
}

func TestLogoutWithoutCookieAndWithMultipleOrganizations(t *testing.T) {
	f := newMembershipHTTPFixture(t)
	ctx := context.Background()
	var id int64
	if err := f.data.Pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Second','second') returning id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := f.data.Pool.Exec(ctx, `insert into memberships(org_id,user_id,role) values ($1,$2,'viewer')`, id, f.users["viewer"]); err != nil {
		t.Fatal(err)
	}
	if w := f.request(http.MethodPost, "/api/private/auth/logout", "viewer", ""); w.Code != http.StatusNoContent {
		t.Fatalf("multiple org logout: %d %s", w.Code, w.Body)
	}
	if _, err := f.data.SessionByHash(ctx, platformcrypto.HashToken("viewer-session")); err == nil {
		t.Fatal("session persists")
	}
	r := httptest.NewRequest(http.MethodPost, "/api/private/auth/logout", nil)
	r.Header.Set("X-CSRF-Token", "token")
	r.AddCookie(&http.Cookie{Name: "supermetry_csrf", Value: "token"})
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent || len(w.Result().Cookies()) != 2 {
		t.Fatalf("absent cookie logout: %d %s", w.Code, w.Body)
	}
}
