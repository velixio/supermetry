package platformhttp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
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
