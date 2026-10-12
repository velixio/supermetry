package auth_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"supermetry/internal/platform/admin"
	"supermetry/internal/platform/auth"
	platformcrypto "supermetry/internal/platform/crypto"
)

func TestLoginAndSessionRefreshEnforceAccountStateAndTwoFactorPolicy(t *testing.T) {
	env := newAuth(t)
	actorID := createUser(t, env, "admin@example.test", "pw-123456789")
	memberID := createUser(t, env, "member@example.test", "pw-123456789")
	if _, err := env.store.Pool.Exec(ctx, `update users set is_admin=true,totp_secret_enc='x',totp_enabled=true where id=$1`, actorID); err != nil {
		t.Fatal(err)
	}
	adminService := admin.New(env.store)
	if err := adminService.UpdateTwoFactorPolicy(ctx, actorID, admin.TwoFactorPolicy{Scope: admin.ScopeEveryone, GraceDays: 0}); err != nil {
		t.Fatal(err)
	}
	box, err := platformcrypto.NewSecretBox(testSecretKey, "supermetry/totp/v1")
	if err != nil {
		t.Fatal(err)
	}
	service := auth.New(env.store, disabledMail{}, box, "https://supermetry.example", auth.WithClock(env.clock), auth.WithPolicySource(adminService))

	token, err := service.Login(ctx, "member@example.test", "pw-123456789", "", "browser")
	if err != nil {
		t.Fatal(err)
	}
	normal := httptest.NewRequest(http.MethodGet, "/api/private/sites", nil)
	if _, ok := service.SessionForRequest(normal, token); ok {
		t.Fatal("unenrolled policy user received a normal session")
	}
	enrollment := httptest.NewRequest(http.MethodPost, "/api/private/me/totp", nil)
	if _, ok := service.SessionForRequest(enrollment, token); !ok {
		t.Fatal("unenrolled policy user could not reach the enrollment recovery path")
	}
	if err := adminService.SetExemption(ctx, actorID, memberID, "temporary recovery", nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := service.SessionForRequest(normal, token); !ok {
		t.Fatal("exempt user remained blocked on session refresh")
	}

	disabled := true
	if err := adminService.UpdateUser(ctx, actorID, memberID, admin.UserPatch{Disabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	if _, ok := service.SessionForRequest(enrollment, token); ok {
		t.Fatal("disabled user retained a session")
	}
	if _, err := service.Login(ctx, "member@example.test", "pw-123456789", "", "browser"); !errors.Is(err, auth.ErrInvalid) {
		t.Fatalf("disabled login error = %v, want ErrInvalid", err)
	}
}

func TestForcedPasswordResetBlocksLogin(t *testing.T) {
	env := newAuth(t)
	userID := createUser(t, env, "reset-required@example.test", "pw-123456789")
	if _, err := env.store.Pool.Exec(ctx, `update users set password_reset_required=true where id=$1`, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := env.a.Login(ctx, "reset-required@example.test", "pw-123456789", "", "browser"); !errors.Is(err, auth.ErrPasswordResetRequired) {
		t.Fatalf("login error = %v, want ErrPasswordResetRequired", err)
	}
}
