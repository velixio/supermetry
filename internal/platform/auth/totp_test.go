package auth_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"supermetry/internal/platform/auth"
)

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
