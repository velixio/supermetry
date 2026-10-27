package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5"

	platformcrypto "supermetry/internal/platform/crypto"
	"supermetry/internal/platform/mail"
	"supermetry/internal/platform/users"
)

const resetLifetime = time.Hour

func (a *Auth) StartReset(ctx context.Context, email string) (string, error) {
	normalizedEmail := users.NormalizeEmail(email)
	if !a.limits.allowReset(normalizedEmail, clientKey(ctx), a.clock.Now()) {
		return "", ErrRateLimited
	}
	user, err := a.store.UserByEmail(ctx, normalizedEmail)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	plain, hash := platformcrypto.NewToken("")
	if _, err := a.store.Pool.Exec(ctx, `
		insert into password_resets(token_hash,user_id,expires_at)
		values ($1,$2,$3)`, hash, user.ID, a.clock.Now().Add(resetLifetime)); err != nil {
		return "", err
	}
	link := a.baseURL + "/reset/" + url.PathEscape(plain)
	if !a.mail.Enabled() {
		return link, nil
	}
	message := mail.PasswordReset(a.baseURL, link, user.Email, a.clock.Now(), resetLifetime).Message(user.Email)
	if err := a.mail.Send(ctx, message); err != nil {
		return "", fmt.Errorf("send password reset: %w", err)
	}
	return "", nil
}

// FinishReset sets a new password from a reset token and ends every session
// of the user.
func (a *Auth) FinishReset(ctx context.Context, token, newPassword string) error {
	_, err := a.finishReset(ctx, token, newPassword, false, "")
	return err
}

// FinishResetSignIn is FinishReset that also signs the user in: the one-time
// link proves the mailbox, so the new password need not be typed again. It
// returns the new session token. It returns "" with a nil error, after the
// password has been set, when the user must sign in the normal way: a second
// factor is enrolled, the account is disabled, or the instance's two-factor
// policy applies.
func (a *Auth) FinishResetSignIn(ctx context.Context, token, newPassword, deviceLabel string) (string, error) {
	return a.finishReset(ctx, token, newPassword, true, deviceLabel)
}

func (a *Auth) finishReset(ctx context.Context, token, newPassword string, signIn bool, deviceLabel string) (string, error) {
	if newPassword == "" {
		return "", ErrInvalid
	}
	if err := platformcrypto.ValidateNewPassword(newPassword); err != nil {
		return "", err
	}
	tokenHash := platformcrypto.HashToken(token)
	now := a.clock.Now()
	var candidateUserID int64
	err := a.store.Pool.QueryRow(ctx, `
		select user_id from password_resets
		where token_hash=$1 and used_at is null and expires_at>$2`, tokenHash, now).Scan(&candidateUserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrInvalid
	}
	if err != nil {
		return "", err
	}
	passwordHash, err := platformcrypto.HashPassword(newPassword)
	if err != nil {
		return "", err
	}
	var resetUserID int64
	err = a.store.Tx(ctx, func(tx pgx.Tx) error {
		var userID int64
		err := tx.QueryRow(ctx, `
			update password_resets
			set used_at=$2
			where token_hash=$1 and used_at is null and expires_at>$2
			returning user_id`, tokenHash, now).Scan(&userID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalid
		}
		if err != nil {
			return err
		}
		if userID != candidateUserID {
			return ErrInvalid
		}
		if _, err := tx.Exec(ctx, `update users set password_hash=$2,password_reset_required=false where id=$1`, userID, passwordHash); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `delete from user_sessions where user_id=$1`, userID); err != nil {
			return err
		}
		resetUserID = userID
		return nil
	})
	if err != nil {
		return "", err
	}
	if !signIn {
		return "", nil
	}
	return a.signInAfterReset(ctx, resetUserID, deviceLabel, now), nil
}

// signInAfterReset opens a session for a user whose password was just reset
// from an emailed link. It returns "" when the user must sign in the normal
// way: a second factor is enrolled, the account is disabled, or the instance's
// two-factor policy applies or cannot be read. The password is already set, so
// a failure here is logged and costs the user only the login form.
func (a *Auth) signInAfterReset(ctx context.Context, userID int64, deviceLabel string, now time.Time) string {
	var totpEnabled bool
	var disabledAt *time.Time
	if err := a.store.Pool.QueryRow(ctx, `select totp_enabled,disabled_at from users where id=$1`, userID).Scan(&totpEnabled, &disabledAt); err != nil {
		slog.WarnContext(ctx, "sign in after reset: read user", "error", err)
		return ""
	}
	if totpEnabled || disabledAt != nil {
		return ""
	}
	if a.policy != nil {
		setupRequired, _, err := a.policy.RequiresTwoFactorSetup(ctx, userID, now)
		if err != nil || setupRequired {
			return ""
		}
	}
	var session string
	err := a.store.Tx(ctx, func(tx pgx.Tx) error {
		var createErr error
		session, createErr = createSession(ctx, tx, userID, deviceLabel, now)
		if createErr != nil {
			return createErr
		}
		return writeUserAudits(ctx, tx, userID, "auth.login")
	})
	if err != nil {
		slog.WarnContext(ctx, "sign in after reset: open session", "error", err)
		return ""
	}
	return session
}
