package auth

import (
	"context"
	"errors"
	"time"

	platformcrypto "supermetry/internal/platform/crypto"
)

// StepUpWindow is how long a sign-in or re-authentication keeps sensitive
// actions (creating or rolling an API key, changing roles, exporting data,
// deleting a site or organization) available without asking again.
const StepUpWindow = 10 * time.Minute

// ErrReauthRequired means the session has not proved the user's identity
// within StepUpWindow.
var ErrReauthRequired = errors.New("recent authentication required")

// Reauthenticate checks the password (and the second factor, when enabled) of
// the session's own user and stamps the session. Every user has a password
// and the only second factor is TOTP, so no passwordless fallback exists.
func (a *Auth) Reauthenticate(ctx context.Context, userID int64, cookieValue, password, code string) error {
	if cookieValue == "" {
		return ErrInvalid
	}
	if err := a.VerifySensitiveAction(ctx, userID, password, code); err != nil {
		return err
	}
	hash := platformcrypto.HashToken(cookieValue)
	return a.store.StampSessionReauth(ctx, hash, a.clock.Now())
}

// RequireRecentAuth returns ErrReauthRequired unless the session signed in or
// re-authenticated within StepUpWindow.
func (a *Auth) RequireRecentAuth(ctx context.Context, cookieValue string) error {
	if cookieValue == "" {
		return ErrReauthRequired
	}
	at, err := a.store.SessionReauthAt(ctx, platformcrypto.HashToken(cookieValue))
	if err != nil {
		return ErrReauthRequired
	}
	if a.clock.Now().Sub(at) >= StepUpWindow {
		return ErrReauthRequired
	}
	return nil
}
