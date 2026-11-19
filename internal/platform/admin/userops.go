package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/platform/store"
)

// AuditFilter narrows the instance audit log. Zero values match everything.
type AuditFilter struct {
	ActorUserID int64
	// Action matches exactly, or as a prefix when it ends in "*".
	Action string
	// OrgID matches entries about that organization (target_type organization).
	OrgID int64
	// From is inclusive, To exclusive.
	From, To *time.Time
	Cursor   string
	Limit    int
}

// ListAudit is the newest page of the instance audit log.
func (s *Service) ListAudit(ctx context.Context, limit int) ([]AuditEntry, error) {
	page, err := s.ListAuditPage(ctx, AuditFilter{Limit: limit})
	return page.Items, err
}

// ListAuditPage lists instance audit entries newest first with keyset paging on
// (created_at, id), served by audit_log_instance_created_at_idx.
func (s *Service) ListAuditPage(ctx context.Context, f AuditFilter) (Page[AuditEntry], error) {
	limit := normalizeLimit(f.Limit)
	action, prefix := f.Action, false
	if strings.HasSuffix(action, "*") {
		prefix = true
		action = strings.TrimSuffix(action, "*")
		action = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(action) + "%"
	}
	if len(f.Action) > 200 {
		return Page[AuditEntry]{}, ErrInvalid
	}
	args := []any{f.ActorUserID, action, prefix, f.OrgID, f.From, f.To}
	query := `
		select id,actor_user_id,action,target_type,target_id,metadata,created_at
		from audit_log
		where org_id is null
		  and ($1::bigint=0 or actor_user_id=$1)
		  and ($2::text='' or (not $3::boolean and action=$2) or ($3::boolean and action like $2))
		  and ($4::bigint=0 or (target_type in ('org','organization') and target_id=$4))
		  and ($5::timestamptz is null or created_at>=$5)
		  and ($6::timestamptz is null or created_at<$6)`
	if f.Cursor != "" {
		c, err := decodeCursor(f.Cursor)
		if err != nil || c.Sort != "created_at" || c.Order != "desc" {
			return Page[AuditEntry]{}, ErrInvalid
		}
		cursorAt, err := parseCursorTime(c.Value)
		if err != nil {
			return Page[AuditEntry]{}, ErrInvalid
		}
		args = append(args, cursorAt, c.ID)
		query += `
		  and (created_at,id) < ($7::timestamptz,$8::bigint)`
	}
	args = append(args, limit+1)
	query += fmt.Sprintf(`
		order by created_at desc,id desc limit $%d`, len(args))
	rows, err := s.store.Pool.Query(ctx, query, args...)
	if err != nil {
		return Page[AuditEntry]{}, err
	}
	defer rows.Close()
	items := make([]AuditEntry, 0)
	for rows.Next() {
		var item AuditEntry
		var metadata []byte
		if err := rows.Scan(&item.ID, &item.ActorUserID, &item.Action, &item.TargetType, &item.TargetID, &metadata, &item.CreatedAt); err != nil {
			return Page[AuditEntry]{}, err
		}
		if err := json.Unmarshal(metadata, &item.Metadata); err != nil {
			return Page[AuditEntry]{}, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return Page[AuditEntry]{}, err
	}
	page := Page[AuditEntry]{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		page.NextCursor = encodeCursor(cursorToken{Sort: "created_at", Order: "desc", Value: formatCursorTime(page.Items[limit-1].CreatedAt), ID: page.Items[limit-1].ID})
	}
	return page, nil
}

// UserDetail is a user with the live session count and 2FA exemption state.
type UserDetail struct {
	User
	ActiveSessions  int  `json:"active_sessions"`
	TwoFactorExempt bool `json:"two_factor_exempt"`
}

// UserDetail returns a visible user with memberships, last login, 2FA state and
// the number of unexpired sessions.
func (s *Service) UserDetail(ctx context.Context, id int64) (UserDetail, error) {
	user, err := s.User(ctx, id)
	if err != nil {
		return UserDetail{}, err
	}
	detail := UserDetail{User: user}
	err = s.store.Pool.QueryRow(ctx, `
		select (select count(*) from user_sessions where user_id=$1 and expires_at>now() and (hard_expires_at is null or hard_expires_at>now())),
		       exists(select 1 from two_factor_exemptions where user_id=$1 and (expires_at is null or expires_at>now()))`, id).Scan(&detail.ActiveSessions, &detail.TwoFactorExempt)
	return detail, err
}

// RevokeUserSessions signs the user out everywhere and audits it; it returns
// how many session rows were removed.
func (s *Service) RevokeUserSessions(ctx context.Context, actorID, userID int64) (int64, error) {
	var revoked int64
	err := s.store.Tx(ctx, func(tx pgx.Tx) error {
		if err := requireAdmin(ctx, tx, actorID); err != nil {
			return err
		}
		if err := requireVisibleUser(ctx, tx, userID); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `delete from user_sessions where user_id=$1`, userID)
		if err != nil {
			return err
		}
		revoked = tag.RowsAffected()
		return store.WriteInstanceAudit(ctx, tx, store.InstanceAuditEntry{
			ActorUserID: actorID, Action: "instance.user.sessions_revoked", TargetType: "user", TargetID: userID,
			Metadata: map[string]any{"revoked": revoked},
		})
	})
	return revoked, err
}

// PasswordReset is the outcome of an admin-started reset: Sent when the link
// was emailed, otherwise ResetURL carries the one-time link for the admin.
type PasswordReset struct {
	Sent     bool   `json:"sent"`
	ResetURL string `json:"reset_url,omitempty"`
}

// SetPasswordResetStarter wires the sender used by StartPasswordReset. Call it
// during startup, before serving requests.
func (s *Service) SetPasswordResetStarter(start ResetStarter) { s.passwordResetStarter = start }

// StartPasswordReset issues a password reset for the user: it emails the link
// when mail is configured and otherwise returns it. The action is audited
// without the link.
func (s *Service) StartPasswordReset(ctx context.Context, actorID, userID int64) (PasswordReset, error) {
	if s.passwordResetStarter == nil {
		return PasswordReset{}, fmt.Errorf("%w: password reset is not configured", ErrInvalid)
	}
	var email string
	// The audit row is committed before the link is issued, so a reset can never
	// be handed out without a record of who asked for it.
	if err := s.store.Tx(ctx, func(tx pgx.Tx) error {
		if err := requireAdmin(ctx, tx, actorID); err != nil {
			return err
		}
		if err := requireVisibleUser(ctx, tx, userID); err != nil {
			return err
		}
		var isAdmin bool
		if err := tx.QueryRow(ctx, `select email,is_admin from users where id=$1`, userID).Scan(&email, &isAdmin); err != nil {
			return err
		}
		if isAdmin {
			return ErrAdminTarget
		}
		return store.WriteInstanceAudit(ctx, tx, store.InstanceAuditEntry{
			ActorUserID: actorID, Action: "instance.user.password_reset_started", TargetType: "user", TargetID: userID,
			Metadata: map[string]any{},
		})
	}); err != nil {
		return PasswordReset{}, err
	}
	link, err := s.passwordResetStarter(ctx, email)
	if err != nil {
		return PasswordReset{}, err
	}
	return PasswordReset{Sent: link == "", ResetURL: link}, nil
}

func requireVisibleUser(ctx context.Context, tx pgx.Tx, userID int64) error {
	var ok bool
	if err := tx.QueryRow(ctx, `select exists(select 1 from users where id=$1 and deleted_at is null and not is_system)`, userID).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	return nil
}

// formatCursorTime and parseCursorTime carry a timestamptz cursor value in a
// session-independent form (UTC, RFC 3339 with microseconds).
func formatCursorTime(t time.Time) string {
	return t.UTC().Format(cursorTimeLayout)
}

func parseCursorTime(v string) (time.Time, error) {
	return time.Parse(cursorTimeLayout, v)
}

const cursorTimeLayout = "2006-01-02T15:04:05.000000Z"
