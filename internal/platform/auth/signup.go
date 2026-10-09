package auth

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"supermetry/internal/platform/audit"
	"supermetry/internal/platform/authz"
	platformcrypto "supermetry/internal/platform/crypto"
	"supermetry/internal/platform/store"
	"supermetry/internal/platform/users"
)

func (a *Auth) Signup(ctx context.Context, email, name, password, orgName, deviceLabel string) (string, error) {
	now := a.clock.Now()
	if !a.limits.allowClient(clientKey(ctx), now) {
		return "", ErrRateLimited
	}

	email = users.NormalizeEmail(email)
	name = strings.TrimSpace(name)
	orgName = strings.TrimSpace(orgName)
	if email == "" || name == "" || password == "" || orgName == "" {
		return "", ErrInvalid
	}
	if err := platformcrypto.ValidateNewPassword(password); err != nil {
		return "", err
	}
	passwordHash, err := platformcrypto.HashPassword(password)
	if err != nil {
		return "", err
	}

	var token string
	err = a.store.Tx(ctx, func(tx pgx.Tx) error {
		userID, err := store.CreateUser(ctx, tx, email, name, passwordHash, now)
		if err != nil {
			return err
		}
		orgID, err := store.CreateOrg(ctx, tx, orgName, userID)
		if err != nil {
			return err
		}
		if err := audit.Write(ctx, tx, authz.Principal{UserID: userID, OrgID: orgID}, "account.created", "user", userID, nil); err != nil {
			return err
		}
		token, err = createSession(ctx, tx, userID, deviceLabel, now)
		return err
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return "", ErrInvalid
		}
		return "", err
	}
	return token, nil
}
