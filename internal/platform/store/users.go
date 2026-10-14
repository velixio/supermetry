package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// User is the authentication data needed for a platform user. PasswordHash,
// TOTPSecretEnc, and TOTPRecovery must never be logged or returned by APIs.
type User struct {
	ID                      int64
	Email                   string
	Name                    string
	PasswordHash            string
	TOTPSecretEnc           []byte
	TOTPEnabled             bool
	TOTPRecovery            [][]byte
	TOTPPendingSecretEnc    []byte
	TOTPPendingRecovery     [][]byte
	TOTPLastStep            *int64
	CreatedAt               time.Time
	LastLoginAt             *time.Time
	IsAdmin                 bool
	DisabledAt              *time.Time
	PasswordResetRequired   bool
	TwoFactorGraceStartedAt *time.Time
	Preferences             UserPreferences
}

type UserPreferences struct {
	LandingPage string `json:"landing_page,omitempty"`
	SitesView   string `json:"sites_view,omitempty"`
	LastSiteID  *int32 `json:"last_site_id,omitempty"`
	// Locale is the interface language; empty follows the browser.
	Locale string `json:"locale,omitempty"`
}

// SupportedLocales lists the English, Spanish, German and Bengali interface
// languages a user may choose. en-XA is a pseudo-locale used to test layouts.
var SupportedLocales = map[string]bool{"": true, "en": true, "es": true, "de": true, "bn": true, "en-XA": true}

type DBTX interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func CreateUser(ctx context.Context, db DBTX, email, name, passwordHash string, now time.Time) (int64, error) {
	var id int64
	err := db.QueryRow(ctx, `
		insert into users(email,name,password_hash,created_at)
		values ($1,$2,$3,$4)
		returning id`, email, name, passwordHash, now).Scan(&id)
	return id, err
}

func (s *Store) UserByEmail(ctx context.Context, email string) (User, error) {
	return scanUser(s.Pool.QueryRow(ctx, `
		select id,email,name,password_hash,totp_secret_enc,totp_enabled,
		       totp_recovery,totp_pending_secret_enc,totp_pending_recovery,
		       totp_last_step,created_at,last_login_at,is_admin,disabled_at,password_reset_required,two_factor_grace_started_at,preferences
		from users where lower(email)=lower($1) and deleted_at is null`, email))
}

func (s *Store) UserByID(ctx context.Context, id int64) (User, error) {
	return scanUser(s.Pool.QueryRow(ctx, `
		select id,email,name,password_hash,totp_secret_enc,totp_enabled,
		       totp_recovery,totp_pending_secret_enc,totp_pending_recovery,
		       totp_last_step,created_at,last_login_at,is_admin,disabled_at,password_reset_required,two_factor_grace_started_at,preferences
		from users where id=$1 and deleted_at is null`, id))
}

func scanUser(row pgx.Row) (User, error) {
	var user User
	var preferences []byte
	err := row.Scan(
		&user.ID,
		&user.Email,
		&user.Name,
		&user.PasswordHash,
		&user.TOTPSecretEnc,
		&user.TOTPEnabled,
		&user.TOTPRecovery,
		&user.TOTPPendingSecretEnc,
		&user.TOTPPendingRecovery,
		&user.TOTPLastStep,
		&user.CreatedAt,
		&user.LastLoginAt,
		&user.IsAdmin,
		&user.DisabledAt,
		&user.PasswordResetRequired,
		&user.TwoFactorGraceStartedAt,
		&preferences,
	)
	if err == nil {
		err = json.Unmarshal(preferences, &user.Preferences)
	}
	return user, err
}

func (s *Store) UpdateUserProfile(ctx context.Context, userID int64, name *string, preferences *UserPreferences) error {
	var encoded []byte
	var err error
	if preferences != nil {
		encoded, err = json.Marshal(preferences)
		if err != nil {
			return err
		}
	}
	tag, err := s.Pool.Exec(ctx, `
		update users
		set name=coalesce($2,name), preferences=coalesce($3::jsonb,preferences)
		where id=$1 and deleted_at is null`, userID, name, encoded)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return pgx.ErrNoRows
	}
	return nil
}

func (s *Store) SetPendingTOTPEnrollment(ctx context.Context, userID int64, encryptedSecret []byte, recovery [][]byte) error {
	_, err := s.Pool.Exec(ctx, `
		update users
		set totp_pending_secret_enc=$2,totp_pending_recovery=$3
		where id=$1 and deleted_at is null`, userID, encryptedSecret, recovery)
	return err
}

// ActivatePendingTOTP promotes the pending enrollment only if it is still the
// one whose secret validated the code, so a re-enrollment in between cannot be
// activated with a code for the earlier secret.
func ActivatePendingTOTP(ctx context.Context, db DBTX, userID, step int64, pendingSecretEnc []byte) (bool, error) {
	tag, err := db.Exec(ctx, `
		update users
		set totp_secret_enc=totp_pending_secret_enc,
		    totp_recovery=totp_pending_recovery,
		    totp_enabled=true,
		    totp_pending_secret_enc=null,
		    totp_pending_recovery='{}',
		    totp_last_step=$2
		where id=$1 and deleted_at is null and totp_pending_secret_enc=$3`, userID, step, pendingSecretEnc)
	return tag.RowsAffected() == 1, err
}

func AcceptTOTPStep(ctx context.Context, db DBTX, userID, step int64) (bool, error) {
	tag, err := db.Exec(ctx, `
		update users set totp_last_step=$2
		where id=$1 and deleted_at is null and totp_enabled
		  and (totp_last_step is null or totp_last_step<$2)`, userID, step)
	return tag.RowsAffected() == 1, err
}

func DisableTOTP(ctx context.Context, db DBTX, userID int64) (bool, error) {
	tag, err := db.Exec(ctx, `
		update users
		set totp_secret_enc=null,
		    totp_recovery='{}',
		    totp_enabled=false,
		    totp_pending_secret_enc=null,
		    totp_pending_recovery='{}',
		    totp_last_step=null
		where id=$1 and deleted_at is null and totp_enabled`, userID)
	return tag.RowsAffected() == 1, err
}

func ReplaceTOTPRecovery(ctx context.Context, db DBTX, userID int64, recovery [][]byte) (bool, error) {
	tag, err := db.Exec(ctx, `
		update users set totp_recovery=$2
		where id=$1 and deleted_at is null and totp_enabled`, userID, recovery)
	return tag.RowsAffected() == 1, err
}

func (s *Store) ConsumeRecoveryCode(ctx context.Context, userID int64, hash []byte) (bool, error) {
	tag, err := s.Pool.Exec(ctx, `
		update users
		set totp_recovery=array_remove(totp_recovery,$2::bytea)
		where id=$1 and deleted_at is null and totp_enabled and $2::bytea=any(totp_recovery)`, userID, hash)
	return tag.RowsAffected() == 1, err
}
