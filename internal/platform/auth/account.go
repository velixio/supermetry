package auth

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	platformcrypto "supermetry/internal/platform/crypto"
	"supermetry/internal/platform/mail"
	"supermetry/internal/platform/users"
)

const emailChangeLifetime = time.Hour

// allowAccountCheck rate-limits password re-checks on account mutations per
// user and per client, so a stolen session cannot be used to guess the password.
func (a *Auth) allowAccountCheck(ctx context.Context, userID int64) bool {
	return a.limits.allow(fmt.Sprintf("user:%d", userID), clientKey(ctx), a.clock.Now())
}

// auditUserOrgs records an account event in the audit log of every
// organization the user belongs to.
func auditUserOrgs(ctx context.Context, tx pgx.Tx, userID int64, action string) error {
	return auditUserOrgsWithActor(ctx, tx, userID, &userID, action, nil)
}

func (a *Auth) UpdateName(ctx context.Context, userID int64, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return ErrInvalid
	}
	tag, err := a.store.Pool.Exec(ctx, `update users set name=$2 where id=$1 and deleted_at is null`, userID, name)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrInvalid
	}
	return nil
}

func (a *Auth) StartEmailChange(ctx context.Context, userID int64, newEmail, password string) error {
	if a.mail == nil || !a.mail.Enabled() {
		return ErrSMTPRequired
	}
	if !a.allowAccountCheck(ctx, userID) {
		return ErrRateLimited
	}
	newEmail = users.NormalizeEmail(newEmail)
	if newEmail == "" {
		return ErrInvalid
	}
	user, err := a.store.UserByID(ctx, userID)
	if err != nil {
		return ErrInvalid
	}
	valid, err := platformcrypto.VerifyPassword(user.PasswordHash, password)
	if err != nil || !valid || strings.EqualFold(user.Email, newEmail) {
		return ErrInvalid
	}
	var exists bool
	if err := a.store.Pool.QueryRow(ctx, `select exists(select 1 from users where lower(email)=lower($1) and deleted_at is null)`, newEmail).Scan(&exists); err != nil {
		return err
	}
	if exists {
		// Look like success so the endpoint does not reveal registered emails.
		return nil
	}
	plain, hash := platformcrypto.NewToken("")
	if _, err := a.store.Pool.Exec(ctx, `
		insert into email_changes(token_hash,user_id,new_email,expires_at)
		values ($1,$2,$3,$4)`, hash, userID, newEmail, a.clock.Now().Add(emailChangeLifetime)); err != nil {
		return err
	}
	link := a.baseURL + "/api/private/auth/email-change/confirm?token=" + url.QueryEscape(plain)
	if err := a.mail.Send(ctx, mail.Message{
		To: newEmail, Subject: "Confirm your new Supermetry email",
		Text: "Confirm your new email address using this one-time link:\n\n" + link,
	}); err != nil {
		_, _ = a.store.Pool.Exec(ctx, `delete from email_changes where token_hash=$1`, hash)
		return fmt.Errorf("send email change confirmation: %w", err)
	}
	return nil
}

// ConfirmEmailChange applies a pending email change. The token must belong to
// userID, the account of the session confirming it.
func (a *Auth) ConfirmEmailChange(ctx context.Context, userID int64, token string) error {
	if userID == 0 || token == "" {
		return ErrInvalid
	}
	now := a.clock.Now()
	hash := platformcrypto.HashToken(token)
	var oldEmail, newEmail string
	err := a.store.Tx(ctx, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
			update email_changes set used_at=$2
			where token_hash=$1 and user_id=$3 and used_at is null and expires_at>$2
			returning new_email`, hash, now, userID).Scan(&newEmail)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalid
		}
		if err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `select email from users where id=$1 and deleted_at is null for update`, userID).Scan(&oldEmail); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrInvalid
			}
			return err
		}
		if _, err := tx.Exec(ctx, `update users set email=$2 where id=$1 and deleted_at is null`, userID, newEmail); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return ErrInvalid
			}
			return err
		}
		// Links mailed to the old address, and other pending changes, die here.
		if _, err := tx.Exec(ctx, `delete from password_resets where user_id=$1`, userID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `delete from email_changes where user_id=$1 and used_at is null`, userID); err != nil {
			return err
		}
		return auditUserOrgs(ctx, tx, userID, "account.email_changed")
	})
	if err != nil {
		return err
	}
	if a.mail != nil && a.mail.Enabled() {
		// The change is committed; a failed notice must not report failure.
		_ = a.mail.Send(ctx, mail.Message{
			To: oldEmail, Subject: "Your Supermetry email was changed",
			Text: "Your Supermetry account email address was changed to " + newEmail + ". If you did not make this change, contact your administrator.",
		})
	}
	return nil
}

func (a *Auth) ChangePassword(ctx context.Context, userID int64, currentPassword, newPassword, currentSession string) error {
	if newPassword == "" || currentSession == "" {
		return ErrInvalid
	}
	if err := platformcrypto.ValidateNewPassword(newPassword); err != nil {
		return err
	}
	if !a.allowAccountCheck(ctx, userID) {
		return ErrRateLimited
	}
	user, err := a.store.UserByID(ctx, userID)
	if err != nil {
		return ErrInvalid
	}
	valid, err := platformcrypto.VerifyPassword(user.PasswordHash, currentPassword)
	if err != nil || !valid {
		return ErrInvalid
	}
	if currentPassword == newPassword {
		return ErrInvalid
	}
	passwordHash, err := platformcrypto.HashPassword(newPassword)
	if err != nil {
		return err
	}
	currentHash := platformcrypto.HashToken(currentSession)
	return a.store.Tx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `update users set password_hash=$2 where id=$1 and deleted_at is null`, userID, passwordHash)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrInvalid
		}
		if _, err := tx.Exec(ctx, `delete from user_sessions where user_id=$1 and id_hash<>$2`, userID, currentHash); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `delete from password_resets where user_id=$1`, userID); err != nil {
			return err
		}
		return auditUserOrgs(ctx, tx, userID, "account.password_changed")
	})
}

// DeleteAccount tombstones the user. API keys and shared links the user
// created belong to the organization, so they keep working; org admins can
// revoke them.
func (a *Auth) DeleteAccount(ctx context.Context, userID int64, password, totpCode string) error {
	if !a.allowAccountCheck(ctx, userID) {
		return ErrRateLimited
	}
	user, err := a.store.UserByID(ctx, userID)
	if err != nil {
		return ErrInvalid
	}
	valid, err := platformcrypto.VerifyPassword(user.PasswordHash, password)
	if err != nil || !valid {
		return ErrInvalid
	}
	if user.TOTPEnabled {
		if strings.TrimSpace(totpCode) == "" {
			return ErrTOTPRequired
		}
		valid, err := a.validSecondFactor(ctx, user, totpCode, a.clock.Now())
		if err != nil {
			return err
		}
		if !valid {
			return ErrInvalid
		}
	}
	tombstoneSecret, _ := platformcrypto.NewToken("")
	tombstonePassword, err := platformcrypto.HashPassword(tombstoneSecret)
	if err != nil {
		return err
	}
	now := a.clock.Now()
	return a.store.Tx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `select m.org_id from memberships m join orgs o on o.id=m.org_id where m.user_id=$1 and m.role='owner' and o.deleted_at is null order by m.org_id`, userID)
		if err != nil {
			return err
		}
		var owned []int64
		for rows.Next() {
			var orgID int64
			if err := rows.Scan(&orgID); err != nil {
				rows.Close()
				return err
			}
			owned = append(owned, orgID)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		for _, orgID := range owned {
			var lockedID int64
			if err := tx.QueryRow(ctx, `select id from orgs where id=$1 for update`, orgID).Scan(&lockedID); err != nil {
				return err
			}
			var owners int
			if err := tx.QueryRow(ctx, `select count(*) from memberships where org_id=$1 and role='owner'`, orgID).Scan(&owners); err != nil {
				return err
			}
			if owners <= 1 {
				return ErrLastOwner
			}
		}
		if err := auditUserOrgs(ctx, tx, userID, "account.deleted"); err != nil {
			return err
		}
		statements := []string{
			`delete from report_subscriptions where user_id=$1`,
			`delete from password_resets where user_id=$1`,
			`delete from email_changes where user_id=$1`,
			`delete from user_sessions where user_id=$1`,
			`delete from memberships where user_id=$1`,
		}
		for _, statement := range statements {
			if _, err := tx.Exec(ctx, statement, userID); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `
			update users set email=$2,name='Deleted user',password_hash=$3,
				totp_secret_enc=null,totp_enabled=false,totp_recovery='{}',deleted_at=$4
			where id=$1 and deleted_at is null`, userID, fmt.Sprintf("deleted-%d@deleted.invalid", userID), tombstonePassword, now)
		return err
	})
}
