package auth_test

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"supermetry/internal/platform/auth"
)

func enrollTestTOTP(t *testing.T, env accountEnv, email string) (int64, string, []string) {
	t.Helper()
	_, err := env.a.Signup(auth.WithClientKey(ctx, "signup-"+email), email, "Owner", "pw-123456789", "Acme", "device")
	if err != nil {
		t.Fatal(err)
	}
	user, err := env.store.UserByEmail(ctx, email)
	if err != nil {
		t.Fatal(err)
	}
	setupURL, recovery, err := env.a.EnrollTOTP(ctx, user.ID, "pw-123456789", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := env.a.ConfirmTOTP(ctx, user.ID, codeFor(t, setupURL, env.clock.Now())); err != nil {
		t.Fatal(err)
	}
	return user.ID, setupURL, recovery
}

func TestSecondFactorLocksAfterFiveFailuresAndIsAccountScoped(t *testing.T) {
	env := newAccountEnv(t)
	userID, setupURL, _ := enrollTestTOTP(t, env, "locked@example.test")
	_, otherURL, _ := enrollTestTOTP(t, env, "other@example.test")
	env.clock.Advance(30 * time.Second)

	for i := 0; i < 5; i++ {
		attemptCtx := auth.WithClientKey(ctx, fmt.Sprintf("locked-attempt-%d", i))
		if _, err := env.a.Login(attemptCtx, "locked@example.test", "pw-123456789", "wrong-code", "device"); !errors.Is(err, auth.ErrInvalid) {
			t.Fatalf("failed attempt %d error = %v, want ErrInvalid", i+1, err)
		}
	}
	correct := codeFor(t, setupURL, env.clock.Now())
	if _, err := env.a.Login(auth.WithClientKey(ctx, "locked-correct"), "locked@example.test", "pw-123456789", correct, "device"); !errors.Is(err, auth.ErrRateLimited) {
		t.Fatalf("correct code during lockout error = %v, want ErrRateLimited", err)
	}
	if _, err := env.a.Login(auth.WithClientKey(ctx, "other-correct"), "other@example.test", "pw-123456789", codeFor(t, otherURL, env.clock.Now()), "device"); err != nil {
		t.Fatalf("other account was affected by lockout: %v", err)
	}

	waitFor(t, func() (bool, error) {
		var count int
		err := env.store.Pool.QueryRow(ctx, `select count(*) from audit_log where target_id=$1 and action='auth.totp_locked'`, userID).Scan(&count)
		return count == 1, err
	})
	env.clock.Advance(15 * time.Minute)
	if _, err := env.a.Login(auth.WithClientKey(ctx, "after-lockout"), "locked@example.test", "pw-123456789", codeFor(t, setupURL, env.clock.Now()), "device"); err != nil {
		t.Fatalf("correct code after lockout = %v", err)
	}
}

func TestSecondFactorSuccessResetsFailures(t *testing.T) {
	env := newAccountEnv(t)
	_, setupURL, _ := enrollTestTOTP(t, env, "reset@example.test")
	env.clock.Advance(30 * time.Second)

	for round := 0; round < 2; round++ {
		for i := 0; i < 4; i++ {
			attemptCtx := auth.WithClientKey(ctx, fmt.Sprintf("reset-%d-%d", round, i))
			if _, err := env.a.Login(attemptCtx, "reset@example.test", "pw-123456789", "wrong-code", "device"); !errors.Is(err, auth.ErrInvalid) {
				t.Fatalf("round %d failed attempt %d error = %v", round+1, i+1, err)
			}
		}
		if _, err := env.a.Login(auth.WithClientKey(ctx, fmt.Sprintf("reset-success-%d", round)), "reset@example.test", "pw-123456789", codeFor(t, setupURL, env.clock.Now()), "device"); err != nil {
			t.Fatalf("round %d success = %v", round+1, err)
		}
		env.clock.Advance(30 * time.Second)
	}
}

func TestRecoveryCodesShareSecondFactorLimiter(t *testing.T) {
	env := newAccountEnv(t)
	_, _, recovery := enrollTestTOTP(t, env, "recovery-limit@example.test")
	env.clock.Advance(30 * time.Second)

	for i := 0; i < 5; i++ {
		attemptCtx := auth.WithClientKey(ctx, fmt.Sprintf("recovery-failure-%d", i))
		if _, err := env.a.Login(attemptCtx, "recovery-limit@example.test", "pw-123456789", "wrong-recovery", "device"); !errors.Is(err, auth.ErrInvalid) {
			t.Fatalf("failed attempt %d error = %v, want ErrInvalid", i+1, err)
		}
	}
	if _, err := env.a.Login(auth.WithClientKey(ctx, "recovery-locked"), "recovery-limit@example.test", "pw-123456789", recovery[0], "device"); !errors.Is(err, auth.ErrRateLimited) {
		t.Fatalf("recovery code during lockout error = %v, want ErrRateLimited", err)
	}
	env.clock.Advance(15 * time.Minute)
	if _, err := env.a.Login(auth.WithClientKey(ctx, "recovery-after-lockout"), "recovery-limit@example.test", "pw-123456789", recovery[0], "device"); err != nil {
		t.Fatalf("recovery code after lockout = %v", err)
	}
}

func TestTOTPEnrollmentConfirmationHasSeparateAllowance(t *testing.T) {
	env := newAccountEnv(t)
	_, err := env.a.Signup(auth.WithClientKey(ctx, "enrollment-limit"), "enrollment-limit@example.test", "Owner", "pw-123456789", "Acme", "device")
	if err != nil {
		t.Fatal(err)
	}
	user, err := env.store.UserByEmail(ctx, "enrollment-limit@example.test")
	if err != nil {
		t.Fatal(err)
	}
	setupURL, _, err := env.a.EnrollTOTP(ctx, user.ID, "pw-123456789", "")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if err := env.a.ConfirmTOTP(ctx, user.ID, "wrong-code"); !errors.Is(err, auth.ErrInvalid) {
			t.Fatalf("failed confirmation %d error = %v, want ErrInvalid", i+1, err)
		}
	}
	if err := env.a.ConfirmTOTP(ctx, user.ID, codeFor(t, setupURL, env.clock.Now())); !errors.Is(err, auth.ErrRateLimited) {
		t.Fatalf("confirmation during enrollment lockout error = %v, want ErrRateLimited", err)
	}
	env.clock.Advance(5 * time.Minute)
	if err := env.a.ConfirmTOTP(ctx, user.ID, codeFor(t, setupURL, env.clock.Now())); err != nil {
		t.Fatalf("confirmation after enrollment lockout = %v", err)
	}
}

func TestTOTPEnrollmentPreservesActiveFactorUntilPendingSecretIsConfirmed(t *testing.T) {
	env := newAccountEnv(t)
	_, err := env.a.Signup(auth.WithClientKey(ctx, "totp-enrollment"), "owner@example.test", "Owner", "pw-123456789", "Acme", "device")
	if err != nil {
		t.Fatal(err)
	}
	user, err := env.store.UserByEmail(ctx, "owner@example.test")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := env.a.EnrollTOTP(ctx, user.ID, "wrong", ""); !errors.Is(err, auth.ErrInvalid) {
		t.Fatalf("wrong password = %v", err)
	}
	firstURL, firstRecovery, err := env.a.EnrollTOTP(ctx, user.ID, "pw-123456789", "")
	if err != nil {
		t.Fatal(err)
	}
	pending, err := env.store.UserByID(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pending.TOTPEnabled || len(pending.TOTPSecretEnc) != 0 || len(pending.TOTPRecovery) != 0 || len(pending.TOTPPendingSecretEnc) == 0 || len(firstRecovery) != 10 {
		t.Fatalf("initial pending enrollment changed active factor: %+v recovery=%d", pending, len(firstRecovery))
	}
	if err := env.a.ConfirmTOTP(ctx, user.ID, codeFor(t, firstURL, env.clock.Now())); err != nil {
		t.Fatal(err)
	}
	active, err := env.store.UserByID(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	oldSecret := append([]byte(nil), active.TOTPSecretEnc...)
	oldRecovery := append([][]byte(nil), active.TOTPRecovery...)

	env.clock.Advance(30 * time.Second)
	if _, _, err := env.a.EnrollTOTP(ctx, user.ID, "pw-123456789", ""); !errors.Is(err, auth.ErrTOTPRequired) {
		t.Fatalf("missing current factor = %v", err)
	}
	currentCode := codeFor(t, firstURL, env.clock.Now())
	secondURL, _, err := env.a.EnrollTOTP(ctx, user.ID, "pw-123456789", currentCode)
	if err != nil {
		t.Fatal(err)
	}
	stillActive, err := env.store.UserByID(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !stillActive.TOTPEnabled || !bytes.Equal(stillActive.TOTPSecretEnc, oldSecret) || !equalByteSlices(stillActive.TOTPRecovery, oldRecovery) || len(stillActive.TOTPPendingSecretEnc) == 0 {
		t.Fatalf("replacement enrollment changed active factor before confirmation: %+v", stillActive)
	}
	if err := env.a.ConfirmTOTP(ctx, user.ID, codeFor(t, secondURL, env.clock.Now())); err != nil {
		t.Fatal(err)
	}
	replaced, err := env.store.UserByID(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !replaced.TOTPEnabled || bytes.Equal(replaced.TOTPSecretEnc, oldSecret) || len(replaced.TOTPPendingSecretEnc) != 0 {
		t.Fatalf("pending enrollment was not promoted: %+v", replaced)
	}
	assertTOTPAudit(t, env, user.ID, "2fa.enabled,2fa.enabled")
}

func TestTOTPReplayDisableAndRecoveryRegeneration(t *testing.T) {
	env := newAccountEnv(t)
	_, err := env.a.Signup(auth.WithClientKey(ctx, "totp-actions"), "owner@example.test", "Owner", "pw-123456789", "Acme", "device")
	if err != nil {
		t.Fatal(err)
	}
	user, err := env.store.UserByEmail(ctx, "owner@example.test")
	if err != nil {
		t.Fatal(err)
	}
	setupURL, oldRecovery, err := env.a.EnrollTOTP(ctx, user.ID, "pw-123456789", "")
	if err != nil {
		t.Fatal(err)
	}
	setupCode := codeFor(t, setupURL, env.clock.Now())
	if err := env.a.ConfirmTOTP(ctx, user.ID, setupCode); err != nil {
		t.Fatal(err)
	}
	if _, err := env.a.Login(ctx, user.Email, "pw-123456789", setupCode, "replay"); !errors.Is(err, auth.ErrInvalid) {
		t.Fatalf("confirmed TOTP replay = %v", err)
	}

	env.clock.Advance(30 * time.Second)
	code := codeFor(t, setupURL, env.clock.Now())
	if _, err := env.a.RegenerateTOTPRecoveryCodes(ctx, user.ID, "wrong", code); !errors.Is(err, auth.ErrInvalid) {
		t.Fatalf("regenerate with wrong password = %v", err)
	}
	newRecovery, err := env.a.RegenerateTOTPRecoveryCodes(ctx, user.ID, "pw-123456789", code)
	if err != nil {
		t.Fatal(err)
	}
	if len(newRecovery) != 10 || newRecovery[0] == oldRecovery[0] {
		t.Fatalf("new recovery codes = %v", newRecovery)
	}
	if _, err := env.a.Login(ctx, user.Email, "pw-123456789", oldRecovery[0], "old recovery"); !errors.Is(err, auth.ErrInvalid) {
		t.Fatalf("old recovery code = %v", err)
	}
	if _, err := env.a.Login(ctx, user.Email, "pw-123456789", newRecovery[0], "new recovery"); err != nil {
		t.Fatalf("new recovery code = %v", err)
	}

	env.clock.Advance(30 * time.Second)
	if err := env.a.DisableTOTP(ctx, user.ID, "pw-123456789", ""); !errors.Is(err, auth.ErrTOTPRequired) {
		t.Fatalf("disable without factor = %v", err)
	}
	if err := env.a.DisableTOTP(ctx, user.ID, "pw-123456789", codeFor(t, setupURL, env.clock.Now())); err != nil {
		t.Fatal(err)
	}
	disabled, err := env.store.UserByID(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if disabled.TOTPEnabled || len(disabled.TOTPSecretEnc) != 0 || len(disabled.TOTPRecovery) != 0 || disabled.TOTPLastStep != nil {
		t.Fatalf("factor remains after disable: %+v", disabled)
	}
	assertTOTPAudit(t, env, user.ID, "2fa.enabled,2fa.recovery_regenerated,2fa.disabled")
}

func equalByteSlices(left, right [][]byte) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if !bytes.Equal(left[i], right[i]) {
			return false
		}
	}
	return true
}

func assertTOTPAudit(t *testing.T, env accountEnv, userID int64, want string) {
	t.Helper()
	rows, err := env.store.Pool.Query(ctx, `select action from audit_log where actor_user_id=$1 and action like '2fa.%' order by id`, userID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var actions []string
	for rows.Next() {
		var action string
		if err := rows.Scan(&action); err != nil {
			t.Fatal(err)
		}
		actions = append(actions, action)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(actions, ","); got != want {
		t.Fatalf("audit actions = %q, want %q", got, want)
	}
}
