package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

type UserSession struct {
	IDHash      []byte
	UserID      int64
	CreatedAt   time.Time
	LastSeenAt  time.Time
	ExpiresAt   time.Time
	DeviceLabel string
	IPAddress   string
	CountryCode string
	Browser     string
	// ImpersonatedBy is the instance admin who opened this session as the
	// user; zero for an ordinary session.
	ImpersonatedBy int64
	// HardExpiresAt, when set, ends the session regardless of activity.
	HardExpiresAt *time.Time
}

func CreateSession(ctx context.Context, db DBTX, session UserSession) error {
	_, err := db.Exec(ctx, `
		insert into user_sessions(id_hash,user_id,created_at,last_seen_at,expires_at,device_label,ip_address,country_code,browser,impersonated_by,hard_expires_at)
		values ($1,$2,$3,$3,$4,$5,nullif($6,'')::inet,nullif($7,''),nullif($8,''),nullif($9,0),$10)`,
		session.IDHash,
		session.UserID,
		session.CreatedAt,
		session.ExpiresAt,
		session.DeviceLabel,
		session.IPAddress, session.CountryCode, session.Browser,
		session.ImpersonatedBy, session.HardExpiresAt,
	)
	return err
}

func (s *Store) SessionByHash(ctx context.Context, hash []byte) (UserSession, error) {
	return scanSession(s.Pool.QueryRow(ctx, `
		select id_hash,user_id,created_at,last_seen_at,expires_at,device_label,
		coalesce(host(ip_address),''),coalesce(country_code,''),coalesce(browser,''),
		coalesce(impersonated_by,0),hard_expires_at
		from user_sessions where id_hash=$1`, hash))
}

func scanSession(row pgx.Row) (UserSession, error) {
	var session UserSession
	err := row.Scan(
		&session.IDHash,
		&session.UserID,
		&session.CreatedAt,
		&session.LastSeenAt,
		&session.ExpiresAt,
		&session.DeviceLabel,
		&session.IPAddress, &session.CountryCode, &session.Browser,
		&session.ImpersonatedBy, &session.HardExpiresAt,
	)
	return session, err
}

func (s *Store) TouchSession(ctx context.Context, hash []byte, now time.Time) error {
	_, err := s.Pool.Exec(ctx, `
		update user_sessions set last_seen_at=$2
		where id_hash=$1 and last_seen_at <= $2::timestamptz - interval '1 minute'`, hash, now)
	return err
}

func (s *Store) DeleteSession(ctx context.Context, hash []byte) error {
	_, err := s.Pool.Exec(ctx, `delete from user_sessions where id_hash=$1`, hash)
	return err
}

func (s *Store) SessionsForUser(ctx context.Context, userID int64) ([]UserSession, error) {
	rows, err := s.Pool.Query(ctx, `
		select id_hash,user_id,created_at,last_seen_at,expires_at,device_label,
		coalesce(host(ip_address),''),coalesce(country_code,''),coalesce(browser,''),
		coalesce(impersonated_by,0),hard_expires_at
		from user_sessions where user_id=$1
		order by last_seen_at desc,created_at desc,id_hash`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	sessions := make([]UserSession, 0)
	for rows.Next() {
		session, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}
	return sessions, rows.Err()
}

func (s *Store) RevokeSession(ctx context.Context, userID int64, hash []byte) error {
	_, err := s.Pool.Exec(ctx, `delete from user_sessions where user_id=$1 and id_hash=$2`, userID, hash)
	return err
}

// RevokeOtherSessions removes every session belonging to userID except the
// session identified by currentHash. The database owns the exclusion so a
// caller cannot choose which session remains active.
func (s *Store) RevokeOtherSessions(ctx context.Context, userID int64, currentHash []byte) error {
	_, err := s.Pool.Exec(ctx, `delete from user_sessions where user_id=$1 and id_hash<>$2`, userID, currentHash)
	return err
}

// ImpersonatorActive reports whether userID may still hold an impersonation
// session: an enabled instance admin, or the hidden admin API key system user.
func (s *Store) ImpersonatorActive(ctx context.Context, userID int64) (bool, error) {
	var active bool
	err := s.Pool.QueryRow(ctx, `
		select is_admin and (disabled_at is null or is_system) from users where id=$1`, userID).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return active, err
}
