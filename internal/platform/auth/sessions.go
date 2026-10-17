package auth

import (
	"context"
	"encoding/base32"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	platformcrypto "supermetry/internal/platform/crypto"
)

const sessionIdleTimeout = 14 * 24 * time.Hour

var sessionIDEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

type Session struct {
	UserID      int64
	ExpiresAt   time.Time
	DeviceLabel string
	// ImpersonatedBy is the instance admin signed in as UserID; zero for the
	// user's own session.
	ImpersonatedBy int64
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
	session, ok := a.session(r.Context(), cookieValue, sessionRequestIsActive(r))
	if !ok {
		return Session{}, false
	}
	if a.policy != nil {
		setupRequired, graceExpired, err := a.policy.RequiresTwoFactorSetup(r.Context(), session.UserID, a.clock.Now())
		if err != nil {
			return Session{}, false
		}
		if setupRequired && graceExpired && !twoFactorRecoveryRequest(r) {
			return Session{}, false
		}
	}
	return session, true
}

func twoFactorRecoveryRequest(r *http.Request) bool {
	if r.URL.Path == "/api/private/me" || r.URL.Path == "/api/private/auth/logout" {
		return true
	}
	return strings.HasPrefix(r.URL.Path, "/api/private/me/totp")
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
	// The hard expiry is independent of expires_at and of activity: nothing
	// extends it. An impersonation session must carry one.
	if (record.HardExpiresAt != nil && !now.Before(*record.HardExpiresAt)) || (record.ImpersonatedBy != 0 && record.HardExpiresAt == nil) {
		_ = a.store.DeleteSession(ctx, hash)
		return Session{}, false
	}
	if record.ImpersonatedBy != 0 {
		active, err := a.store.ImpersonatorActive(ctx, record.ImpersonatedBy)
		if err != nil {
			return Session{}, false
		}
		if !active {
			_ = a.store.DeleteSession(ctx, hash)
			return Session{}, false
		}
	}
	user, err := a.store.UserByID(ctx, record.UserID)
	if err != nil || user.DisabledAt != nil || user.PasswordResetRequired {
		_ = a.store.DeleteSession(ctx, hash)
		return Session{}, false
	}
	if activity && now.Sub(record.LastSeenAt) >= time.Minute {
		if err := a.store.TouchSession(ctx, hash, now); err != nil {
			return Session{}, false
		}
	}
	return Session{UserID: record.UserID, ExpiresAt: record.ExpiresAt, DeviceLabel: record.DeviceLabel, ImpersonatedBy: record.ImpersonatedBy}, true
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
		if !now.Before(record.ExpiresAt) || now.Sub(record.LastSeenAt) >= sessionIdleTimeout || (record.HardExpiresAt != nil && !now.Before(*record.HardExpiresAt)) {
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
	return a.store.Tx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `delete from user_sessions where user_id=$1 and id_hash=$2`, userID, hash)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		return writeUserAudits(ctx, tx, userID, "session.revoked")
	})
}

// RevokeOthers preserves the session named by the authenticated cookie and
// revokes every other session for the same user.
func (a *Auth) RevokeOthers(ctx context.Context, userID int64, cookieValue string) error {
	if cookieValue == "" {
		return ErrInvalid
	}
	return a.store.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `delete from user_sessions where user_id=$1 and id_hash<>$2`, userID, platformcrypto.HashToken(cookieValue)); err != nil {
			return err
		}
		return writeUserAudits(ctx, tx, userID, "sessions.revoked_all")
	})
}

// IsCurrentSession compares the public revocation identifier, never the token.
func IsCurrentSession(id, cookieValue string) bool {
	return cookieValue != "" && id == sessionIDEncoding.EncodeToString(platformcrypto.HashToken(cookieValue))
}
