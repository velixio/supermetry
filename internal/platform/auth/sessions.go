package auth

import (
	"context"
	"encoding/base32"
	"net/http"
	"strings"
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
	IPAddress   string
	CountryCode string
	Browser     string
	Current     bool
	ID          string
	CreatedAt   time.Time
	LastSeenAt  time.Time
	ExpiresAt   time.Time
	DeviceLabel string
}

func (a *Auth) Session(ctx context.Context, cookieValue string) (Session, bool) {
	return a.session(ctx, cookieValue, true)
}

// SessionForRequest excludes known read/poll endpoints from activity updates.
// Authentication, expiry and revocation checks still run on every request.
func (a *Auth) SessionForRequest(r *http.Request, cookieValue string) (Session, bool) {
	return a.session(r.Context(), cookieValue, sessionRequestIsActive(r))
}

func sessionRequestIsActive(r *http.Request) bool {
	path := r.URL.Path
	// Query uses POST, but is read-only and polled.
	if path == "/api/private/query" {
		return false
	}
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		switch path {
		case "/api/private/me/sessions", "/api/private/realtime", "/api/private/realtime/summary", "/api/private/health":
			return false
		}
		// Installation status and import progress are polled by the dashboard.
		parts := strings.Split(strings.Trim(path, "/"), "/")
		if len(parts) >= 4 && parts[0] == "api" && parts[1] == "private" && parts[2] == "sites" {
			if len(parts) == 4 || (len(parts) == 5 && parts[4] == "imports") {
				return false
			}
		}
	}
	return true
}

func (a *Auth) session(ctx context.Context, cookieValue string, activity bool) (Session, bool) {
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
	if activity && now.Sub(record.LastSeenAt) >= time.Minute {
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
			IPAddress:   record.IPAddress, CountryCode: record.CountryCode, Browser: record.Browser,
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

// IsCurrentSession compares the public revocation identifier, never the token.
func IsCurrentSession(id, cookieValue string) bool {
	return cookieValue != "" && id == sessionIDEncoding.EncodeToString(platformcrypto.HashToken(cookieValue))
}
