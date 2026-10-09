package auth_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"panel/internal/platform/auth"
	platformcrypto "panel/internal/platform/crypto"
)

func TestSessionMetadataAndPassiveActivity(t *testing.T) {
	env := newAuth(t)
	userID := createUser(t, env, "session@example.test", "test-password-long")
	metadata := auth.SessionMetadata{IPAddress: "2001:db8::42", CountryCode: "DE", Browser: "Firefox"}
	token, err := env.a.Login(auth.WithSessionMetadata(context.Background(), metadata), "session@example.test", "test-password-long", "", "Laptop")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := env.a.Sessions(ctx, userID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("sessions=%v err=%v", rows, err)
	}
	got := rows[0]
	if got.IPAddress != metadata.IPAddress || got.CountryCode != "DE" || got.Browser != "Firefox" || !got.CreatedAt.Equal(env.clock.Now()) || !got.LastSeenAt.Equal(got.CreatedAt) {
		t.Fatalf("metadata=%+v", got)
	}
	env.clock.Advance(2 * time.Minute)
	for _, path := range []string{"/api/private/me/sessions", "/api/private/realtime", "/api/private/realtime/summary", "/api/private/health", "/api/private/query", "/api/private/sites/1/imports", "/api/private/sites/1"} {
		r := httptest.NewRequest("GET", path, nil)
		if path == "/api/private/query" {
			r.Method = "POST"
		}
		if _, ok := env.a.SessionForRequest(r, token); !ok {
			t.Fatal("passive request rejected")
		}
	}
	stored, err := env.store.SessionByHash(ctx, platformcrypto.HashToken(token))
	if err != nil || !stored.LastSeenAt.Equal(got.LastSeenAt) {
		t.Fatalf("poll changed last active: %+v %v", stored, err)
	}
	if _, ok := env.a.SessionForRequest(httptest.NewRequest("PATCH", "/api/private/me", nil), token); !ok {
		t.Fatal("active request rejected")
	}
	stored, _ = env.store.SessionByHash(ctx, platformcrypto.HashToken(token))
	if !stored.LastSeenAt.Equal(env.clock.Now()) {
		t.Fatalf("last active=%v", stored.LastSeenAt)
	}
	env.clock.Advance(30 * time.Second)
	env.a.SessionForRequest(httptest.NewRequest("GET", "/api/private/me", nil), token)
	latest, _ := env.store.SessionByHash(ctx, platformcrypto.HashToken(token))
	if !latest.LastSeenAt.Equal(stored.LastSeenAt) {
		t.Fatal("activity not throttled")
	}
	env.clock.Advance(14 * 24 * time.Hour)
	if _, ok := env.a.SessionForRequest(httptest.NewRequest("GET", "/api/private/me/sessions", nil), token); ok {
		t.Fatal("passive activity revived expired session")
	}
}
