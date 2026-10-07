// Package auth provides password, TOTP, reset, setup, and session
// authentication for platform users.
package auth

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	platformcrypto "panel/internal/platform/crypto"
	"panel/internal/platform/mail"
	"panel/internal/platform/store"
	"panel/internal/platform/users"
)

var (
	ErrInvalid      = errors.New("invalid credentials")
	ErrTOTPRequired = errors.New("TOTP code required")
	ErrRateLimited  = errors.New("too many authentication attempts")
)

const CookieName = "panel_session"

type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

type Option func(*Auth)

func WithClock(clock Clock) Option {
	return func(a *Auth) { a.clock = clock }
}

type Auth struct {
	store   *store.Store
	users   *users.Users
	mail    mail.Sender
	box     *platformcrypto.SecretBox
	baseURL string
	clock   Clock
	limits  *loginLimiter
}

var (
	dummyOnce sync.Once
	dummyPHC  string
)

func New(s *store.Store, sender mail.Sender, box *platformcrypto.SecretBox, baseURL string, options ...Option) *Auth {
	dummyOnce.Do(func() {
		var err error
		dummyPHC, err = platformcrypto.HashPassword("panel-dummy-password")
		if err != nil {
			panic("initialize password verifier: " + err.Error())
		}
	})
	a := &Auth{
		store:   s,
		users:   users.New(s),
		mail:    sender,
		box:     box,
		baseURL: strings.TrimRight(baseURL, "/"),
		clock:   realClock{},
		limits:  newLoginLimiter(),
	}
	for _, option := range options {
		option(a)
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
			return "", ErrInvalid
		}
	}

	plain, hash := platformcrypto.NewToken("")
	err = a.store.Tx(ctx, func(tx pgx.Tx) error {
		if err := store.CreateSession(ctx, tx, store.UserSession{
			IDHash:      hash,
			UserID:      user.ID,
			CreatedAt:   now,
			ExpiresAt:   now.Add(90 * 24 * time.Hour),
			DeviceLabel: sanitizeDeviceLabel(deviceLabel),
		}); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `update users set last_login_at=$2 where id=$1`, user.ID, now)
		return err
	})
	if err != nil {
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
