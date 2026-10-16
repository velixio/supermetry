// Package admin owns instance-wide administration and security policy.
package admin

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/platform/store"
)

var (
	ErrForbidden    = errors.New("instance administration required")
	ErrInvalid      = errors.New("invalid admin request")
	ErrNotFound     = errors.New("admin resource not found")
	ErrLastAdmin    = errors.New("instance must retain an enabled administrator")
	ErrWouldLockOut = errors.New("two-factor policy would lock out every instance administrator")
)

type TwoFactorScope string

const (
	ScopeOff            TwoFactorScope = "off"
	ScopeInstanceAdmins TwoFactorScope = "instance_admins"
	ScopeOwnersAdmins   TwoFactorScope = "owners_admins"
	ScopeEveryone       TwoFactorScope = "everyone"
)

type TwoFactorPolicy struct {
	Scope     TwoFactorScope `json:"scope"`
	GraceDays int            `json:"grace_days"`
}

type TwoFactorStatus struct {
	Required      bool       `json:"required"`
	SetupRequired bool       `json:"setup_required"`
	GraceExpired  bool       `json:"grace_expired"`
	GraceUntil    *time.Time `json:"grace_until,omitempty"`
	Exempt        bool       `json:"exempt"`
}

type UserPatch struct {
	Disabled              *bool
	PasswordResetRequired *bool
	InstanceAdmin         *bool
}

type Service struct{ store *store.Store }

func New(data *store.Store) *Service { return &Service{store: data} }

func (s *Service) IsInstanceAdmin(ctx context.Context, userID int64) (bool, error) {
	var admin, enabled bool
	err := s.store.Pool.QueryRow(ctx, `select is_admin,disabled_at is null from users where id=$1 and deleted_at is null`, userID).Scan(&admin, &enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return admin && enabled, err
}

func (s *Service) UpdateUser(ctx context.Context, actorID, userID int64, patch UserPatch) error {
	if patch.Disabled == nil && patch.PasswordResetRequired == nil && patch.InstanceAdmin == nil {
		return ErrInvalid
	}
	return s.store.Tx(ctx, func(tx pgx.Tx) error {
		if err := requireAdmin(ctx, tx, actorID); err != nil {
			return err
		}
		var currentAdmin bool
		var disabledAt *time.Time
		var resetRequired bool
		if err := tx.QueryRow(ctx, `select is_admin,disabled_at,password_reset_required from users where id=$1 and deleted_at is null and not is_system for update`, userID).Scan(&currentAdmin, &disabledAt, &resetRequired); errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		nextAdmin := currentAdmin
		if patch.InstanceAdmin != nil {
			nextAdmin = *patch.InstanceAdmin
		}
		nextDisabled := disabledAt != nil
		if patch.Disabled != nil {
			nextDisabled = *patch.Disabled
		}
		if currentAdmin && (!nextAdmin || nextDisabled) {
			var others int
			if err := tx.QueryRow(ctx, `select count(*) from users where is_admin and disabled_at is null and deleted_at is null and id<>$1`, userID).Scan(&others); err != nil {
				return err
			}
			if others == 0 {
				return ErrLastAdmin
			}
		}
		nextReset := resetRequired
		if patch.PasswordResetRequired != nil {
			nextReset = *patch.PasswordResetRequired
		}
		_, err := tx.Exec(ctx, `
			update users set
				is_admin=$2,
				disabled_at=case when $3 then coalesce(disabled_at,now()) else null end,
				password_reset_required=$4,
				two_factor_grace_started_at=case when is_admin is distinct from $2 then null else two_factor_grace_started_at end
			where id=$1`, userID, nextAdmin, nextDisabled, nextReset)
		if err != nil {
			return err
		}
		if nextDisabled || nextReset {
			if _, err := tx.Exec(ctx, `delete from user_sessions where user_id=$1`, userID); err != nil {
				return err
			}
		}
		return store.WriteInstanceAudit(ctx, tx, store.InstanceAuditEntry{
			ActorUserID: actorID, Action: "instance.user.updated", TargetType: "user", TargetID: userID,
			Metadata: map[string]any{"is_admin": nextAdmin, "disabled": nextDisabled, "password_reset_required": nextReset},
		})
	})
}

func (s *Service) TwoFactorPolicy(ctx context.Context) (TwoFactorPolicy, error) {
	var policy TwoFactorPolicy
	err := s.store.Pool.QueryRow(ctx, `select two_factor_scope,two_factor_grace_days from instance_settings where id`).Scan(&policy.Scope, &policy.GraceDays)
	return policy, err
}

func (s *Service) UpdateTwoFactorPolicy(ctx context.Context, actorID int64, policy TwoFactorPolicy) error {
	if !validScope(policy.Scope) || policy.GraceDays < 0 || policy.GraceDays > 30 {
		return ErrInvalid
	}
	return s.store.Tx(ctx, func(tx pgx.Tx) error {
		if err := requireAdmin(ctx, tx, actorID); err != nil {
			return err
		}
		if policy.GraceDays == 0 && (policy.Scope == ScopeInstanceAdmins || policy.Scope == ScopeOwnersAdmins || policy.Scope == ScopeEveryone) {
			var recoverable int
			if err := tx.QueryRow(ctx, `
				select count(*) from users u
				where u.is_admin and u.disabled_at is null and u.deleted_at is null
				  and (u.totp_enabled or exists(
					select 1 from two_factor_exemptions e where e.user_id=u.id and (e.expires_at is null or e.expires_at>now())
				  ))`).Scan(&recoverable); err != nil {
				return err
			}
			if recoverable == 0 {
				return ErrWouldLockOut
			}
		}
		if _, err := tx.Exec(ctx, `update instance_settings set two_factor_scope=$1,two_factor_grace_days=$2,policy_changed_at=now(),updated_at=now() where id`, policy.Scope, policy.GraceDays); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `update users set two_factor_grace_started_at=null`); err != nil {
			return err
		}
		return store.WriteInstanceAudit(ctx, tx, store.InstanceAuditEntry{ActorUserID: actorID, Action: "instance.two_factor_policy.updated", TargetType: "instance", TargetID: 1, Metadata: map[string]any{"scope": policy.Scope, "grace_days": policy.GraceDays}})
	})
}

func (s *Service) TwoFactorStatus(ctx context.Context, userID int64, now time.Time) (TwoFactorStatus, error) {
	policy, err := s.TwoFactorPolicy(ctx)
	if err != nil {
		return TwoFactorStatus{}, err
	}
	var isAdmin, enabled bool
	var graceStarted *time.Time
	var elevated bool
	err = s.store.Pool.QueryRow(ctx, `
		select u.is_admin,u.totp_enabled,u.two_factor_grace_started_at,
		       exists(select 1 from memberships m where m.user_id=u.id and m.role in ('owner','admin'))
		from users u where u.id=$1 and u.deleted_at is null`, userID).Scan(&isAdmin, &enabled, &graceStarted, &elevated)
	if errors.Is(err, pgx.ErrNoRows) {
		return TwoFactorStatus{}, ErrNotFound
	}
	if err != nil {
		return TwoFactorStatus{}, err
	}
	required := policy.Scope == ScopeEveryone || policy.Scope == ScopeInstanceAdmins && isAdmin || policy.Scope == ScopeOwnersAdmins && (isAdmin || elevated)
	status := TwoFactorStatus{Required: required}
	if !required || enabled {
		return status, nil
	}
	if err := s.store.Pool.QueryRow(ctx, `select exists(select 1 from two_factor_exemptions where user_id=$1 and (expires_at is null or expires_at>$2))`, userID, now).Scan(&status.Exempt); err != nil {
		return TwoFactorStatus{}, err
	}
	if status.Exempt {
		return status, nil
	}
	if graceStarted == nil {
		if err := s.store.Pool.QueryRow(ctx, `update users set two_factor_grace_started_at=$2 where id=$1 and two_factor_grace_started_at is null returning two_factor_grace_started_at`, userID, now).Scan(&graceStarted); err != nil {
			return TwoFactorStatus{}, err
		}
	}
	deadline := graceStarted.Add(time.Duration(policy.GraceDays) * 24 * time.Hour)
	status.SetupRequired = true
	status.GraceUntil = &deadline
	status.GraceExpired = !now.Before(deadline)
	return status, nil
}

// RequiresTwoFactorSetup is the narrow authentication-facing policy contract.
func (s *Service) RequiresTwoFactorSetup(ctx context.Context, userID int64, now time.Time) (bool, bool, error) {
	status, err := s.TwoFactorStatus(ctx, userID, now)
	return status.SetupRequired, status.GraceExpired, err
}

func (s *Service) SetExemption(ctx context.Context, actorID, userID int64, reason string, expiresAt *time.Time) error {
	if len(reason) < 1 || len(reason) > 500 || expiresAt != nil && !expiresAt.After(time.Now()) {
		return ErrInvalid
	}
	return s.store.Tx(ctx, func(tx pgx.Tx) error {
		if err := requireAdmin(ctx, tx, actorID); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `
			insert into two_factor_exemptions(user_id,granted_by,reason,expires_at)
			select id,$2,$3,$4 from users where id=$1 and deleted_at is null and not is_system
			on conflict(user_id) do update set granted_by=excluded.granted_by,reason=excluded.reason,expires_at=excluded.expires_at,created_at=now()`, userID, actorID, reason, expiresAt)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrNotFound
		}
		return store.WriteInstanceAudit(ctx, tx, store.InstanceAuditEntry{ActorUserID: actorID, Action: "instance.two_factor_exemption.updated", TargetType: "user", TargetID: userID, Metadata: map[string]any{"reason": reason, "expires_at": expiresAt}})
	})
}

func (s *Service) DeleteExemption(ctx context.Context, actorID, userID int64) error {
	return s.store.Tx(ctx, func(tx pgx.Tx) error {
		if err := requireAdmin(ctx, tx, actorID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `delete from two_factor_exemptions where user_id=$1`, userID); err != nil {
			return err
		}
		return store.WriteInstanceAudit(ctx, tx, store.InstanceAuditEntry{ActorUserID: actorID, Action: "instance.two_factor_exemption.deleted", TargetType: "user", TargetID: userID})
	})
}

// requireAdmin accepts enabled instance admins and the admin API key's system
// user (is_system), which is disabled so it can never sign in but must still be
// able to act through the key on the admin routes.
func requireAdmin(ctx context.Context, tx pgx.Tx, userID int64) error {
	var allowed bool
	if err := tx.QueryRow(ctx, `select coalesce((select is_admin and (disabled_at is null or is_system) from users where id=$1 and deleted_at is null),false)`, userID).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return ErrForbidden
	}
	return nil
}

func validScope(scope TwoFactorScope) bool {
	switch scope {
	case ScopeOff, ScopeInstanceAdmins, ScopeOwnersAdmins, ScopeEveryone:
		return true
	default:
		return false
	}
}

func (p TwoFactorPolicy) String() string { return fmt.Sprintf("%s/%dd", p.Scope, p.GraceDays) }
