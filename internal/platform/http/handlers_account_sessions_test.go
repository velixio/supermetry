package platformhttp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	platformauth "panel/internal/platform/auth"
	platformcrypto "panel/internal/platform/crypto"
	"panel/internal/platform/store"
)

func TestAccountSessionsMetadataIsOwnedCurrentAndRevocable(t *testing.T) {
	f := newMembershipHTTPFixture(t)
	ctx := context.Background()
	created := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Microsecond)
	if err := store.CreateSession(ctx, f.data.Pool, store.UserSession{IDHash: platformcrypto.HashToken("own-other"), UserID: f.users["viewer"], CreatedAt: created, ExpiresAt: created.Add(24 * time.Hour), DeviceLabel: "Synthetic laptop", IPAddress: "2001:db8::42", CountryCode: "DE", Browser: "Firefox"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.data.Pool.Exec(ctx, `update user_sessions set ip_address='198.51.100.99',browser='Foreign browser' where user_id=$1`, f.users["owner"]); err != nil {
		t.Fatal(err)
	}
	list := func(actor string) []platformauth.SessionInfo {
		t.Helper()
		w := f.request(http.MethodGet, "/api/private/me/sessions", actor, "")
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("session metadata may be cached")
		}
		if w.Code != 200 {
			t.Fatalf("list=%d %s", w.Code, w.Body)
		}
		for _, secret := range []string{"viewer-session", "own-other", "198.51.100.99", "Foreign browser", "IDHash", "CSRF"} {
			if strings.Contains(w.Body.String(), secret) {
				t.Fatalf("response leaked %s", secret)
			}
		}
		var result struct{ Data []platformauth.SessionInfo }
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result.Data
	}
	rows := list("viewer")
	if len(rows) != 2 {
		t.Fatalf("rows=%+v", rows)
	}
	var current, other platformauth.SessionInfo
	for _, row := range rows {
		if row.Current {
			current = row
		} else {
			other = row
		}
	}
	if current.ID == "" || current.IPAddress != "" || current.CountryCode != "" || current.Browser != "" {
		t.Fatalf("legacy/current=%+v", current)
	}
	if other.IPAddress != "2001:db8::42" || other.CountryCode != "DE" || other.Browser != "Firefox" || !other.CreatedAt.Equal(created) || !other.LastSeenAt.Equal(created) {
		t.Fatalf("other=%+v", other)
	}
	// Even an organization owner cannot revoke another person's session.
	if w := f.request(http.MethodDelete, "/api/private/me/sessions/"+other.ID, "owner", ""); w.Code != 204 {
		t.Fatalf("foreign revoke=%d", w.Code)
	}
	if len(list("viewer")) != 2 {
		t.Fatal("foreign revoke deleted session")
	}
	if w := f.request(http.MethodDelete, "/api/private/me/sessions/"+other.ID, "viewer", ""); w.Code != 204 {
		t.Fatalf("revoke=%d", w.Code)
	}
	if rows = list("viewer"); len(rows) != 1 || !rows[0].Current {
		t.Fatalf("current changed=%+v", rows)
	}
	if w := f.request(http.MethodDelete, fmt.Sprintf("/api/private/me/sessions/%s", current.ID), "viewer", ""); w.Code != 204 {
		t.Fatalf("current revoke=%d", w.Code)
	}
	if w := f.request(http.MethodGet, "/api/private/me/sessions", "viewer", ""); w.Code != 401 {
		t.Fatalf("revoked cookie authenticated=%d", w.Code)
	}
}

func TestRevokeOtherSessionsPreservesCurrentAndUserBoundaries(t *testing.T) {
	f := newMembershipHTTPFixture(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	for _, token := range []string{"viewer-other-one", "viewer-other-two"} {
		if err := store.CreateSession(ctx, f.data.Pool, store.UserSession{
			IDHash: platformcrypto.HashToken(token), UserID: f.users["viewer"], CreatedAt: now, ExpiresAt: now.Add(time.Hour), DeviceLabel: "Other device",
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.data.Pool.Exec(ctx, `insert into api_keys(org_id,name,prefix,hash,scopes,created_by) values ($1,'session boundary','sesskey1',$2,'{read}',$3)`, f.orgID, make([]byte, 32), f.users["viewer"]); err != nil {
		t.Fatal(err)
	}

	// The route ignores forged client exclusions and derives the survivor from
	// the authenticated cookie. It also leaves other users and API keys alone.
	w := f.request(http.MethodDelete, "/api/private/me/sessions", "viewer", `{"current_session":"viewer-other-one","user_id":0}`)
	if w.Code != http.StatusNoContent {
		t.Fatalf("bulk revoke=%d %s", w.Code, w.Body)
	}
	if w := f.request(http.MethodGet, "/api/private/me/sessions", "viewer", ""); w.Code != http.StatusOK || strings.Count(w.Body.String(), `"ID"`) != 1 || !strings.Contains(w.Body.String(), `"Current":true`) {
		t.Fatalf("current session changed: %d %s", w.Code, w.Body)
	}
	for _, token := range []string{"viewer-other-one", "viewer-other-two"} {
		r := httptest.NewRequest(http.MethodGet, "/api/private/me/sessions", nil)
		r.AddCookie(&http.Cookie{Name: platformauth.CookieName, Value: token})
		other := httptest.NewRecorder()
		f.handler.ServeHTTP(other, r)
		if other.Code != http.StatusUnauthorized {
			t.Fatalf("revoked session %q authenticated: %d %s", token, other.Code, other.Body)
		}
	}
	if w := f.request(http.MethodGet, "/api/private/me/sessions", "owner", ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"Current":true`) {
		t.Fatalf("other user changed: %d %s", w.Code, w.Body)
	}
	var keys int
	if err := f.data.Pool.QueryRow(ctx, `select count(*) from api_keys where created_by=$1 and revoked_at is null`, f.users["viewer"]).Scan(&keys); err != nil || keys != 1 {
		t.Fatalf("api keys changed: count=%d err=%v", keys, err)
	}

	// No remaining sessions is a successful idempotent operation.
	if w := f.request(http.MethodDelete, "/api/private/me/sessions", "viewer", ""); w.Code != http.StatusNoContent {
		t.Fatalf("repeated bulk revoke=%d %s", w.Code, w.Body)
	}

	// Concurrent requests remain idempotent and retain exactly the active
	// session because the exclusion and deletion are one SQL statement.
	for i := range 8 {
		if err := store.CreateSession(ctx, f.data.Pool, store.UserSession{
			IDHash: platformcrypto.HashToken(fmt.Sprintf("concurrent-other-%d", i)), UserID: f.users["viewer"], CreatedAt: now, ExpiresAt: now.Add(time.Hour), DeviceLabel: "Concurrent device",
		}); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	errs := make(chan string, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if w := f.request(http.MethodDelete, "/api/private/me/sessions", "viewer", ""); w.Code != http.StatusNoContent {
				errs <- fmt.Sprintf("%d %s", w.Code, w.Body)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent bulk revoke=%s", err)
	}
	var sessions int
	if err := f.data.Pool.QueryRow(ctx, `select count(*) from user_sessions where user_id=$1`, f.users["viewer"]).Scan(&sessions); err != nil || sessions != 1 {
		t.Fatalf("sessions=%d err=%v", sessions, err)
	}
}

func TestRevokeOtherSessionsRequiresCSRF(t *testing.T) {
	f := newMembershipHTTPFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := store.CreateSession(ctx, f.data.Pool, store.UserSession{IDHash: platformcrypto.HashToken("csrf-other"), UserID: f.users["viewer"], CreatedAt: now, ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodDelete, "/api/private/me/sessions", nil)
	r.AddCookie(&http.Cookie{Name: platformauth.CookieName, Value: "viewer-session"})
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF=%d %s", w.Code, w.Body)
	}
	var sessions int
	if err := f.data.Pool.QueryRow(ctx, `select count(*) from user_sessions where user_id=$1`, f.users["viewer"]).Scan(&sessions); err != nil || sessions != 2 {
		t.Fatalf("sessions=%d err=%v", sessions, err)
	}
}
