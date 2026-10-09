package auth

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	platformcrypto "supermetry/internal/platform/crypto"
	"supermetry/internal/platform/store"
)

const recoveryCodeCount = 10

func (a *Auth) EnrollTOTP(ctx context.Context, userID int64) (string, []string, error) {
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
	if err := a.store.SetTOTPEnrollment(ctx, userID, a.box.Seal([]byte(key.Secret())), hashes); err != nil {
		return "", nil, err
	}
	return key.URL(), recovery, nil
}

func (a *Auth) ConfirmTOTP(ctx context.Context, userID int64, code string) error {
	user, err := a.store.UserByID(ctx, userID)
	if err != nil {
		return ErrInvalid
	}
	valid, err := a.validTOTP(user.TOTPSecretEnc, code, a.clock.Now())
	if err != nil || !valid {
		return ErrInvalid
	}
	enabled, err := a.store.EnableTOTP(ctx, userID)
	if err != nil {
		return err
	}
	if !enabled {
		return ErrInvalid
	}
	return nil
}

func (a *Auth) validSecondFactor(ctx context.Context, user store.User, code string, now time.Time) (bool, error) {
	valid, err := a.validTOTP(user.TOTPSecretEnc, code, now)
	if err == nil && valid {
		return true, nil
	}

	hash := platformcrypto.HashToken(code)
	for _, candidate := range user.TOTPRecovery {
		if subtle.ConstantTimeCompare(hash, candidate) == 1 {
			return a.store.ConsumeRecoveryCode(ctx, user.ID, hash)
		}
	}
	return false, nil
}

func (a *Auth) validTOTP(encryptedSecret []byte, code string, now time.Time) (bool, error) {
	if len(encryptedSecret) == 0 {
		return false, errors.New("TOTP is not enrolled")
	}
	secret, err := a.box.Open(encryptedSecret)
	if err != nil {
		return false, err
	}
	return totp.ValidateCustom(code, string(secret), now, totp.ValidateOpts{
		Period:    30,
		Skew:      1,
		Digits:    otp.DigitsSix,
		Algorithm: otp.AlgorithmSHA1,
	})
}
