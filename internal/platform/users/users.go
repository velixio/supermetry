// Package users manages platform user accounts.
package users

import (
	"context"
	"errors"
	"strings"
	"time"

	"panel/internal/platform/crypto"
	"panel/internal/platform/store"
)

var ErrInvalid = errors.New("invalid user")

type Users struct {
	store *store.Store
}

func New(s *store.Store) *Users {
	return &Users{store: s}
}

func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func (u *Users) Create(ctx context.Context, email, name, password string) (int64, error) {
	return u.CreateWithDB(ctx, u.store.Pool, email, name, password, time.Now().UTC())
}

func (u *Users) CreateWithDB(ctx context.Context, db store.DBTX, email, name, password string, now time.Time) (int64, error) {
	email = NormalizeEmail(email)
	name = strings.TrimSpace(name)
	if email == "" || name == "" || len(password) < 12 {
		return 0, ErrInvalid
	}
	hash, err := crypto.HashPassword(password)
	if err != nil {
		return 0, err
	}
	return store.CreateUser(ctx, db, email, name, hash, now)
}
