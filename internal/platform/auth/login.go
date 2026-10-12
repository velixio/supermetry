// Package auth provides password, TOTP, reset, setup, and session
// authentication for platform users.
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	platformcrypto "supermetry/internal/platform/crypto"
	"supermetry/internal/platform/mail"
	"supermetry/internal/platform/store"
	"supermetry/internal/platform/users"
)

var (
	ErrInvalid      = errors.New("invalid credentials")
	ErrTOTPRequired = errors.New("TOTP code required")
	ErrRateLimited  = errors.New("too many authentication attempts")
	ErrSMTPRequired = errors.New("email change requires SMTP")
	ErrLastOwner    = errors.New("account is the last owner of an organization")
)

const CookieName = "supermetry_session"

const loginClientHashPurpose = "supermetry/auth-login-client/v1"

type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

type Option func(*Auth)

func WithClock(clock Clock) Option {
	return func(a *Auth) { a.clock = clock }
}

// WithAuditSecret configures the server secret used to pseudonymize client
// identifiers in failed-login audit entries.
func WithAuditSecret(secret []byte) Option {
	return func(a *Auth) { a.auditSecret = append([]byte(nil), secret...) }
}

// WithLoginAttemptCapacity overrides the in-memory login budget. It is
// intended for isolated test harnesses; production callers should use the
// security defaults.
func WithLoginAttemptCapacity(capacity int) Option {
	return func(a *Auth) { a.limits.setLoginCapacity(capacity) }
}

type Auth struct {
	store            *store.Store
	users            *users.Users
	mail             mail.Sender
	box              *platformcrypto.SecretBox
	baseURL          string
	clock            Clock
	limits           *loginLimiter
	totpLimits       *secondFactorLimiter
	enrollmentLimits *secondFactorLimiter

	auditMu                sync.Mutex
	auditWG                sync.WaitGroup
	auditClosed            bool
	auditSecret            []byte
	writeLoginFailureAudit func(context.Context, int64, string) error
	writeTOTPLockoutAudit  func(context.Context, int64) error
}

var (
	dummyOnce sync.Once
	dummyPHC  string
)

func New(s *store.Store, sender mail.Sender, box *platformcrypto.SecretBox, baseURL string, options ...Option) *Auth {
	dummyOnce.Do(func() {
		var err error
		dummyPHC, err = platformcrypto.HashPassword("supermetry-dummy-password")
		if err != nil {
			panic("initialize password verifier: " + err.Error())
		}
	})
	a := &Auth{
		store:            s,
		users:            users.New(s),
		mail:             sender,
		box:              box,
		baseURL:          strings.TrimRight(baseURL, "/"),
		clock:            realClock{},
		limits:           newLoginLimiter(),
		totpLimits:       newSecondFactorLimiter(secondFactorMaxFailures, secondFactorWindow, secondFactorLockout),
		enrollmentLimits: newSecondFactorLimiter(secondFactorMaxFailures, secondFactorWindow, enrollmentLockout),
	}
	for _, option := range options {
		option(a)
	}
	if len(a.auditSecret) == 0 {
		a.auditSecret = make([]byte, 32)
		if _, err := rand.Read(a.auditSecret); err != nil {
			panic("initialize login audit secret: " + err.Error())
		}
	}
	a.writeLoginFailureAudit = func(ctx context.Context, userID int64, clientHash string) error {
		return a.store.Tx(ctx, func(tx pgx.Tx) error {
			return auditUserOrgsWithActor(ctx, tx, userID, nil, "auth.login_failed", map[string]any{
				"client_hash": clientHash,
			})
		})
	}
	a.writeTOTPLockoutAudit = func(ctx context.Context, userID int64) error {
		return a.store.Tx(ctx, func(tx pgx.Tx) error {
			return auditUserOrgsWithActor(ctx, tx, userID, nil, "auth.totp_locked", nil)
		})
	}
	return a
}

func (a *Auth) Login(ctx context.Context, email, password, totpCode, deviceLabel string) (string, error) {
	now := a.clock.Now()
	normalizedEmail := users.NormalizeEmail(email)
	if !a.limits.allow(normalizedEmail, clientKey(ctx), now) {
		return "", ErrRateLimited
	}

	user, err := a.store.UserByEmail(ctx, normalizedEmail)
	if errors.Is(err, pgx.ErrNoRows) {
		_, _ = platformcrypto.VerifyPassword(dummyPHC, password)
		return "", ErrInvalid
	}
	if err != nil {
		return "", err
	}
	valid, err := platformcrypto.VerifyPassword(user.PasswordHash, password)
	if err != nil || !valid {
		a.auditLoginFailure(user.ID, clientKey(ctx))
		return "", ErrInvalid
	}
	if user.TOTPEnabled {
		if totpCode == "" {
			return "", ErrTOTPRequired
		}
		valid, err := a.validSecondFactor(ctx, user, totpCode, now)
		if err != nil {
			return "", err
		}
		if !valid {
			a.auditLoginFailure(user.ID, clientKey(ctx))
			return "", ErrInvalid
		}
	}

	var plain string
	err = a.store.Tx(ctx, func(tx pgx.Tx) error {
		var createErr error
		plain, createErr = createSession(ctx, tx, user.ID, deviceLabel, now)
		if createErr != nil {
			return createErr
		}
		return writeUserAudits(ctx, tx, user.ID, "auth.login")
	})
	if err != nil {
		return "", err
	}
	return plain, nil
}

func (a *Auth) auditLoginFailure(userID int64, client string) {
	clientHash := hashLoginClient(a.auditSecret, client)
	a.auditAsync("write failed-login audit", func(ctx context.Context) error {
		return a.writeLoginFailureAudit(ctx, userID, clientHash)
	})
}

func (a *Auth) auditTOTPLockout(userID int64) {
	a.auditAsync("write TOTP-lockout audit", func(ctx context.Context) error {
		return a.writeTOTPLockoutAudit(ctx, userID)
	})
}

func (a *Auth) auditAsync(logMessage string, write func(context.Context) error) {
	a.auditMu.Lock()
	if a.auditClosed {
		a.auditMu.Unlock()
		return
	}
	a.auditWG.Add(1)
	a.auditMu.Unlock()
	go func() {
		defer a.auditWG.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := write(ctx)
		if err != nil {
			slog.ErrorContext(ctx, logMessage, "error", err)
		}
	}()
}

func hashLoginClient(secret []byte, client string) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(loginClientHashPurpose + "\x00" + client))
	return hex.EncodeToString(mac.Sum(nil))
}

// Shutdown stops accepting new asynchronous audit writes and waits until all
// in-flight writes finish or ctx expires.
func (a *Auth) Shutdown(ctx context.Context) error {
	a.auditMu.Lock()
	a.auditClosed = true
	a.auditMu.Unlock()
	done := make(chan struct{})
	go func() {
		a.auditWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func createSession(ctx context.Context, db store.DBTX, userID int64, deviceLabel string, now time.Time) (string, error) {
	plain, hash := platformcrypto.NewToken("")
	metadata, _ := ctx.Value(sessionMetadataKey{}).(SessionMetadata)
	if err := store.CreateSession(ctx, db, store.UserSession{
		IDHash:      hash,
		UserID:      userID,
		CreatedAt:   now,
		ExpiresAt:   now.Add(90 * 24 * time.Hour),
		DeviceLabel: sanitizeDeviceLabel(deviceLabel),
		IPAddress:   metadata.IPAddress, CountryCode: metadata.CountryCode, Browser: metadata.Browser,
	}); err != nil {
		return "", err
	}
	if _, err := db.Exec(ctx, `update users set last_login_at=$2 where id=$1`, userID, now); err != nil {
		return "", err
	}
	return plain, nil
}

func sanitizeDeviceLabel(label string) string {
	label = strings.ToValidUTF8(strings.TrimSpace(label), "")
	if len(label) > 200 {
		label = label[:200]
		for !utf8.ValidString(label) {
			label = label[:len(label)-1]
		}
	}
	return label
}
