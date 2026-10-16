package auth

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"

	platformcrypto "supermetry/internal/platform/crypto"
	"supermetry/internal/platform/store"
)

func (a *Auth) SetupRequired(ctx context.Context) (bool, error) {
	var required bool
	err := a.store.Pool.QueryRow(ctx, `select not exists(select 1 from users where not is_system)`).Scan(&required)
	return required, err
}

func (a *Auth) SetupURL(ctx context.Context) (string, bool) {
	var usersExist bool
	if err := a.store.Pool.QueryRow(ctx, `select exists(select 1 from users where not is_system)`).Scan(&usersExist); err != nil || usersExist {
		return "", false
	}
	plain, hash := platformcrypto.NewToken("")
	tag, err := a.store.Pool.Exec(ctx, `
		insert into setup_state(singleton,setup_token_hash)
		values (true,$1)
		on conflict (singleton) do update set setup_token_hash=excluded.setup_token_hash
		where setup_state.completed_at is null`, hash)
	if err != nil || tag.RowsAffected() != 1 {
		return "", false
	}
	return a.baseURL + "/setup?token=" + url.QueryEscape(plain), true
}

func (a *Auth) CompleteSetup(ctx context.Context, token, email, name, password, orgName string) (int64, int64, error) {
	orgName = strings.TrimSpace(orgName)
	normalizedEmail := strings.ToLower(strings.TrimSpace(email))
	name = strings.TrimSpace(name)
	if orgName == "" || normalizedEmail == "" || name == "" || password == "" {
		return 0, 0, ErrInvalid
	}
	if err := platformcrypto.ValidateNewPassword(password); err != nil {
		return 0, 0, err
	}
	var preliminaryHash []byte
	var preliminaryCompleted bool
	err := a.store.Pool.QueryRow(ctx, `
		select setup_token_hash,completed_at is not null
		from setup_state where singleton=true`).Scan(&preliminaryHash, &preliminaryCompleted)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, 0, ErrInvalid
	}
	if err != nil {
		return 0, 0, err
	}
	if preliminaryCompleted || !equalTokenHash(preliminaryHash, token) {
		return 0, 0, ErrInvalid
	}
	passwordHash, err := platformcrypto.HashPassword(password)
	if err != nil {
		return 0, 0, err
	}

	var userID, orgID int64
	err = a.store.Tx(ctx, func(tx pgx.Tx) error {
		var storedHash []byte
		var completed bool
		err := tx.QueryRow(ctx, `
			select setup_token_hash,completed_at is not null
			from setup_state where singleton=true for update`).Scan(&storedHash, &completed)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalid
		}
		if err != nil {
			return err
		}
		if completed || !equalTokenHash(storedHash, token) {
			return ErrInvalid
		}
		var exists bool
		if err := tx.QueryRow(ctx, `select exists(select 1 from users where not is_system)`).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return ErrInvalid
		}
		if err := tx.QueryRow(ctx, `
			insert into users(email,name,password_hash,created_at,is_admin)
			values ($1,$2,$3,$4,true) returning id`,
			normalizedEmail, name, passwordHash, a.clock.Now()).Scan(&userID); err != nil {
			return err
		}
		orgID, err = store.CreateOrg(ctx, tx, orgName, userID)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			update setup_state set completed_at=$1,setup_token_hash=null
			where singleton=true`, a.clock.Now())
		return err
	})
	if err != nil {
		return 0, 0, err
	}
	return userID, orgID, nil
}

func equalTokenHash(stored []byte, plain string) bool {
	want := platformcrypto.HashToken(plain)
	if len(stored) != len(want) {
		return false
	}
	var different byte
	for i := range stored {
		different |= stored[i] ^ want[i]
	}
	return different == 0
}
