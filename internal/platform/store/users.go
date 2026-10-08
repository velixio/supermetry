package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// User is the authentication data needed for a platform user. PasswordHash,
// TOTPSecretEnc, and TOTPRecovery must never be logged or returned by APIs.
type User struct {
	ID            int64
	Email         string
	Name          string
	PasswordHash  string
	TOTPSecretEnc []byte
	TOTPEnabled   bool
	TOTPRecovery  [][]byte
	CreatedAt     time.Time
	LastLoginAt   *time.Time
}

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
		       totp_recovery,created_at,last_login_at
		from users where lower(email)=lower($1) and deleted_at is null`, email))
}

func (s *Store) UserByID(ctx context.Context, id int64) (User, error) {
	return scanUser(s.Pool.QueryRow(ctx, `
		select id,email,name,password_hash,totp_secret_enc,totp_enabled,
		       totp_recovery,created_at,last_login_at
		from users where id=$1 and deleted_at is null`, id))
}

func scanUser(row pgx.Row) (User, error) {
	var user User
	err := row.Scan(
		&user.ID,
		&user.Email,
		&user.Name,
		&user.PasswordHash,
		&user.TOTPSecretEnc,
		&user.TOTPEnabled,
		&user.TOTPRecovery,
		&user.CreatedAt,
		&user.LastLoginAt,
	)
	return user, err
}

func (s *Store) SetTOTPEnrollment(ctx context.Context, userID int64, encryptedSecret []byte, recovery [][]byte) error {
	_, err := s.Pool.Exec(ctx, `
		update users
		set totp_secret_enc=$2,totp_recovery=$3,totp_enabled=false
		where id=$1`, userID, encryptedSecret, recovery)
	return err
}

func (s *Store) EnableTOTP(ctx context.Context, userID int64) (bool, error) {
	tag, err := s.Pool.Exec(ctx, `
		update users set totp_enabled=true
		where id=$1 and totp_secret_enc is not null`, userID)
	return tag.RowsAffected() == 1, err
}

func (s *Store) ConsumeRecoveryCode(ctx context.Context, userID int64, hash []byte) (bool, error) {
	tag, err := s.Pool.Exec(ctx, `
		update users
		set totp_recovery=array_remove(totp_recovery,$2::bytea)
		where id=$1 and $2::bytea=any(totp_recovery)`, userID, hash)
	return tag.RowsAffected() == 1, err
}
