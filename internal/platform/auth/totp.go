package auth

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	platformcrypto "supermetry/internal/platform/crypto"
	"supermetry/internal/platform/store"
)

const recoveryCodeCount = 10

func (a *Auth) EnrollTOTP(ctx context.Context, userID int64, password, code string) (string, []string, error) {
	if err := a.VerifySensitiveAction(ctx, userID, password, code); err != nil {
		return "", nil, err
	}
	user, err := a.store.UserByID(ctx, userID)
	if err != nil {
		return "", nil, err
	}
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      "Supermetry",
		AccountName: user.Email,
		Period:      30,
		SecretSize:  20,
		Digits:      otp.DigitsSix,
		Algorithm:   otp.AlgorithmSHA1,
	})
	if err != nil {
		return "", nil, fmt.Errorf("generate TOTP secret: %w", err)
	}

	recovery := make([]string, recoveryCodeCount)
	hashes := make([][]byte, recoveryCodeCount)
	for i := range recovery {
		recovery[i], hashes[i] = platformcrypto.NewToken("")
	}
	if err := a.store.SetPendingTOTPEnrollment(ctx, userID, a.box.Seal([]byte(key.Secret())), hashes); err != nil {
		return "", nil, err
	}
	return key.URL(), recovery, nil
}

func (a *Auth) ConfirmTOTP(ctx context.Context, userID int64, code string) error {
	user, err := a.store.UserByID(ctx, userID)
	if err != nil {
		return ErrInvalid
	}
	step, valid, err := a.validTOTPStep(user.TOTPPendingSecretEnc, code, a.clock.Now())
	if err != nil || !valid {
		return ErrInvalid
	}
	var enabled bool
	err = a.store.Tx(ctx, func(tx pgx.Tx) error {
		var activateErr error
		enabled, activateErr = store.ActivatePendingTOTP(ctx, tx, userID, step)
		if activateErr != nil || !enabled {
			return activateErr
		}
		return auditUserOrgs(ctx, tx, userID, "2fa.enabled")
	})
	if err != nil {
		return err
	}
	if !enabled {
		return ErrInvalid
	}
	return nil
}

func (a *Auth) validSecondFactor(ctx context.Context, user store.User, code string, now time.Time) (bool, error) {
	step, valid, err := a.validTOTPStep(user.TOTPSecretEnc, code, now)
	if err == nil && valid {
		return store.AcceptTOTPStep(ctx, a.store.Pool, user.ID, step)
	}

	hash := platformcrypto.HashToken(code)
	for _, candidate := range user.TOTPRecovery {
		if subtle.ConstantTimeCompare(hash, candidate) == 1 {
			return a.store.ConsumeRecoveryCode(ctx, user.ID, hash)
		}
	}
	return false, nil
}

// VerifySensitiveAction re-authenticates a user for a destructive or
// credential-changing operation. A successful TOTP is consumed, so callers
// cannot replay the same time step for another sensitive action.
func (a *Auth) VerifySensitiveAction(ctx context.Context, userID int64, password, code string) error {
	if !a.allowAccountCheck(ctx, userID) {
		return ErrRateLimited
	}
	user, err := a.store.UserByID(ctx, userID)
	if err != nil {
		return ErrInvalid
	}
	valid, err := platformcrypto.VerifyPassword(user.PasswordHash, password)
	if err != nil || !valid {
		return ErrInvalid
	}
	if !user.TOTPEnabled {
		return nil
	}
	if code == "" {
		return ErrTOTPRequired
	}
	valid, err = a.validSecondFactor(ctx, user, code, a.clock.Now())
	if err != nil {
		return err
	}
	if !valid {
		return ErrInvalid
	}
	return nil
}

func (a *Auth) DisableTOTP(ctx context.Context, userID int64, password, code string) error {
	if err := a.VerifySensitiveAction(ctx, userID, password, code); err != nil {
		return err
	}
	return a.store.Tx(ctx, func(tx pgx.Tx) error {
		disabled, err := store.DisableTOTP(ctx, tx, userID)
		if err != nil {
			return err
		}
		if !disabled {
			return ErrInvalid
		}
		return auditUserOrgs(ctx, tx, userID, "2fa.disabled")
	})
}

func (a *Auth) RegenerateTOTPRecoveryCodes(ctx context.Context, userID int64, password, code string) ([]string, error) {
	if err := a.VerifySensitiveAction(ctx, userID, password, code); err != nil {
		return nil, err
	}
	recovery := make([]string, recoveryCodeCount)
	hashes := make([][]byte, recoveryCodeCount)
	for i := range recovery {
		recovery[i], hashes[i] = platformcrypto.NewToken("")
	}
	err := a.store.Tx(ctx, func(tx pgx.Tx) error {
		replaced, err := store.ReplaceTOTPRecovery(ctx, tx, userID, hashes)
		if err != nil {
			return err
		}
		if !replaced {
			return ErrInvalid
		}
		return auditUserOrgs(ctx, tx, userID, "2fa.recovery_regenerated")
	})
	if err != nil {
		return nil, err
	}
	return recovery, nil
}

func (a *Auth) validTOTPStep(encryptedSecret []byte, code string, now time.Time) (int64, bool, error) {
	if len(encryptedSecret) == 0 {
		return 0, false, errors.New("TOTP is not enrolled")
	}
	secret, err := a.box.Open(encryptedSecret)
	if err != nil {
		return 0, false, err
	}
	for _, offset := range []int64{-1, 0, 1} {
		candidate := now.Add(time.Duration(offset) * 30 * time.Second)
		valid, err := totp.ValidateCustom(code, string(secret), candidate, totp.ValidateOpts{
			Period: 30, Skew: 0, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1,
		})
		if err != nil {
			return 0, false, err
		}
		if valid {
			return candidate.Unix() / 30, true, nil
		}
	}
	return 0, false, nil
}

func (a *Auth) validTOTP(encryptedSecret []byte, code string, now time.Time) (bool, error) {
	_, valid, err := a.validTOTPStep(encryptedSecret, code, now)
	return valid, err
}
