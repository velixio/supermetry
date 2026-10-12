package auth

import (
	"context"
	"errors"
	"fmt"
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
	link := a.baseURL + "/password-reset?token=" + url.QueryEscape(plain)
	if !a.mail.Enabled() {
		return link, nil
	}
	message := mail.Message{
		To:      user.Email,
		Subject: "Reset your Supermetry password",
		Text:    "Reset your password using this one-time link:\n\n" + link,
	}
	if err := a.mail.Send(ctx, message); err != nil {
		return "", fmt.Errorf("send password reset: %w", err)
	}
	return "", nil
}

func (a *Auth) FinishReset(ctx context.Context, token, newPassword string) error {
	if newPassword == "" {
		return ErrInvalid
	}
	if err := platformcrypto.ValidateNewPassword(newPassword); err != nil {
		return err
	}
	tokenHash := platformcrypto.HashToken(token)
	now := a.clock.Now()
	var candidateUserID int64
	err := a.store.Pool.QueryRow(ctx, `
		select user_id from password_resets
		where token_hash=$1 and used_at is null and expires_at>$2`, tokenHash, now).Scan(&candidateUserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrInvalid
	}
	if err != nil {
		return err
	}
	passwordHash, err := platformcrypto.HashPassword(newPassword)
	if err != nil {
		return err
	}
	return a.store.Tx(ctx, func(tx pgx.Tx) error {
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
		_, err = tx.Exec(ctx, `delete from user_sessions where user_id=$1`, userID)
		return err
	})
}
