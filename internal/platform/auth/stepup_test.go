package auth_test

import (
	"errors"
	"testing"
	"time"

	"supermetry/internal/platform/auth"
)

func TestStepUpWindowAndReauthenticate(t *testing.T) {
	env := newAuth(t)
	userID := createUser(t, env, "step@example.test", "pw-123456789")
	cookie, err := env.a.Login(ctx, "step@example.test", "pw-123456789", "", "x")
	if err != nil {
		t.Fatal(err)
	}
	if err := env.a.RequireRecentAuth(ctx, cookie); err != nil {
		t.Fatalf("a fresh sign-in counts as recent: %v", err)
	}
	if err := env.a.RequireRecentAuth(ctx, ""); !errors.Is(err, auth.ErrReauthRequired) {
		t.Fatalf("no session: %v", err)
	}
	env.clock.Advance(auth.StepUpWindow - time.Second)
	if err := env.a.RequireRecentAuth(ctx, cookie); err != nil {
		t.Fatalf("inside the window: %v", err)
	}
	env.clock.Advance(2 * time.Second)
	if err := env.a.RequireRecentAuth(ctx, cookie); !errors.Is(err, auth.ErrReauthRequired) {
		t.Fatalf("stale session: %v", err)
	}
	if err := env.a.Reauthenticate(ctx, userID, cookie, "wrong-password-1", ""); !errors.Is(err, auth.ErrInvalid) {
		t.Fatalf("wrong password: %v", err)
	}
	if err := env.a.RequireRecentAuth(ctx, cookie); !errors.Is(err, auth.ErrReauthRequired) {
		t.Fatal("a failed re-authentication must not stamp the session")
	}
	if err := env.a.Reauthenticate(ctx, userID, cookie, "pw-123456789", ""); err != nil {
		t.Fatal(err)
	}
	if err := env.a.RequireRecentAuth(ctx, cookie); err != nil {
		t.Fatalf("after re-authentication: %v", err)
	}
	env.clock.Advance(auth.StepUpWindow + time.Second)
	if err := env.a.RequireRecentAuth(ctx, cookie); !errors.Is(err, auth.ErrReauthRequired) {
		t.Fatal("the stamp must expire")
	}
	// A different session of the same user is not affected by the stamp.
	other, err := env.a.Login(ctx, "step@example.test", "pw-123456789", "", "y")
	if err != nil {
		t.Fatal(err)
	}
	env.clock.Advance(auth.StepUpWindow + time.Second)
	if err := env.a.Reauthenticate(ctx, userID, other, "pw-123456789", ""); err != nil {
		t.Fatal(err)
	}
	if err := env.a.RequireRecentAuth(ctx, cookie); !errors.Is(err, auth.ErrReauthRequired) {
		t.Fatal("re-authentication is per session")
	}
}

func TestReauthenticateNeedsSecondFactor(t *testing.T) {
	env := newAuth(t)
	userID := createUser(t, env, "tf@example.test", "pw-123456789")
	cookie, err := env.a.Login(ctx, "tf@example.test", "pw-123456789", "", "x")
	if err != nil {
		t.Fatal(err)
	}
	setupURL, _, err := env.a.EnrollTOTP(ctx, userID, "pw-123456789", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := env.a.ConfirmTOTP(ctx, userID, codeFor(t, setupURL, env.clock.Now())); err != nil {
		t.Fatal(err)
	}
	env.clock.Advance(auth.StepUpWindow + time.Minute)
	if err := env.a.Reauthenticate(ctx, userID, cookie, "pw-123456789", ""); !errors.Is(err, auth.ErrTOTPRequired) {
		t.Fatalf("missing code: %v", err)
	}
	if err := env.a.Reauthenticate(ctx, userID, cookie, "pw-123456789", "000000"); !errors.Is(err, auth.ErrInvalid) {
		t.Fatalf("wrong code: %v", err)
	}
	if err := env.a.RequireRecentAuth(ctx, cookie); !errors.Is(err, auth.ErrReauthRequired) {
		t.Fatal("not stamped without a valid code")
	}
	if err := env.a.Reauthenticate(ctx, userID, cookie, "pw-123456789", codeFor(t, setupURL, env.clock.Now())); err != nil {
		t.Fatal(err)
	}
	if err := env.a.RequireRecentAuth(ctx, cookie); err != nil {
		t.Fatal(err)
	}
}
