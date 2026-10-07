package auth

import (
	"context"
	"encoding/base32"
	"time"

	platformcrypto "panel/internal/platform/crypto"
)

const sessionIdleTimeout = 14 * 24 * time.Hour

var sessionIDEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

type Session struct {
	UserID      int64
	ExpiresAt   time.Time
	DeviceLabel string
}

type SessionInfo struct {
	ID          string
	CreatedAt   time.Time
	LastSeenAt  time.Time
	ExpiresAt   time.Time
	DeviceLabel string
}

func (a *Auth) Session(ctx context.Context, cookieValue string) (Session, bool) {
	hash := platformcrypto.HashToken(cookieValue)
	record, err := a.store.SessionByHash(ctx, hash)
	if err != nil {
		return Session{}, false
	}
	now := a.clock.Now()
	if !now.Before(record.ExpiresAt) || now.Sub(record.LastSeenAt) >= sessionIdleTimeout {
		_ = a.store.DeleteSession(ctx, hash)
		return Session{}, false
	}
	if now.Sub(record.LastSeenAt) >= time.Minute {
		if err := a.store.TouchSession(ctx, hash, now); err != nil {
			return Session{}, false
		}
	}
	return Session{UserID: record.UserID, ExpiresAt: record.ExpiresAt, DeviceLabel: record.DeviceLabel}, true
}

func (a *Auth) Logout(ctx context.Context, cookieValue string) error {
	return a.store.DeleteSession(ctx, platformcrypto.HashToken(cookieValue))
}

func (a *Auth) Sessions(ctx context.Context, userID int64) ([]SessionInfo, error) {
	records, err := a.store.SessionsForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	sessions := make([]SessionInfo, 0, len(records))
	now := a.clock.Now()
	for _, record := range records {
		if !now.Before(record.ExpiresAt) || now.Sub(record.LastSeenAt) >= sessionIdleTimeout {
			_ = a.store.DeleteSession(ctx, record.IDHash)
			continue
		}
		sessions = append(sessions, SessionInfo{
			ID:          sessionIDEncoding.EncodeToString(record.IDHash),
			CreatedAt:   record.CreatedAt,
			LastSeenAt:  record.LastSeenAt,
			ExpiresAt:   record.ExpiresAt,
			DeviceLabel: record.DeviceLabel,
		})
	}
	return sessions, nil
}

func (a *Auth) Revoke(ctx context.Context, userID int64, sessionID string) error {
	hash, err := sessionIDEncoding.DecodeString(sessionID)
	if err != nil || len(hash) != 32 {
		return ErrInvalid
	}
	return a.store.RevokeSession(ctx, userID, hash)
}
