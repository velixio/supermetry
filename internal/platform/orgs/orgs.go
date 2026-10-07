// Package orgs manages organizations and memberships.
package orgs

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"panel/internal/platform/audit"
	"panel/internal/platform/authz"
	"panel/internal/platform/store"
)

type Role = authz.Role

const (
	RoleOwner  = authz.RoleOwner
	RoleAdmin  = authz.RoleAdmin
	RoleEditor = authz.RoleEditor
	RoleViewer = authz.RoleViewer
)

var (
	ErrForbidden   = errors.New("forbidden")
	ErrLastOwner   = errors.New("organization must have at least one owner")
	ErrNotFound    = errors.New("membership not found")
	ErrInvalidRole = errors.New("invalid organization role")
)

type Membership struct {
	OrgID   int64
	UserID  int64
	OrgName string
	OrgSlug string
	Role    Role
	SiteIDs []int32
}

type Orgs struct {
	store *store.Store
}

func New(data *store.Store) *Orgs {
	return &Orgs{store: data}
}

func (o *Orgs) Create(ctx context.Context, tx pgx.Tx, name string, ownerID int64) (int64, error) {
	return store.CreateOrg(ctx, tx, name, ownerID)
}

func (o *Orgs) SetRole(ctx context.Context, actor authz.Principal, userID int64, role Role, siteIDs []int32) error {
	if !authz.Can(actor, authz.ActionMembersManage, 0) {
		return ErrForbidden
	}
	if !validRole(role) {
		return ErrInvalidRole
	}
	if role == RoleOwner || role == RoleAdmin {
		siteIDs = nil
	}

	return o.store.Tx(ctx, func(tx pgx.Tx) error {
		if err := lockOrg(ctx, tx, actor.OrgID); err != nil {
			return err
		}
		actorRole, err := memberManagerRole(ctx, tx, actor.OrgID, actor.UserID)
		if err != nil {
			return err
		}
		currentRole, err := membershipRole(ctx, tx, actor.OrgID, userID)
		if err != nil {
			return err
		}
		if actorRole == RoleAdmin && (currentRole == RoleOwner || role == RoleOwner) {
			return ErrForbidden
		}
		if currentRole == RoleOwner && role != RoleOwner {
			last, err := isLastOwner(ctx, tx, actor.OrgID)
			if err != nil {
				return err
			}
			if last {
				return ErrLastOwner
			}
		}
		if _, err := tx.Exec(ctx, `
			update memberships set role = $3, site_ids = $4
			where org_id = $1 and user_id = $2`, actor.OrgID, userID, role, siteIDs); err != nil {
			return fmt.Errorf("update membership role: %w", err)
		}
		return audit.Write(ctx, tx, actor, "membership.role_changed", "membership", userID, map[string]any{
			"old_role": currentRole,
			"new_role": role,
			"site_ids": siteIDs,
		})
	})
}

func (o *Orgs) Remove(ctx context.Context, actor authz.Principal, userID int64) error {
	if !authz.Can(actor, authz.ActionMembersManage, 0) {
		return ErrForbidden
	}
	return o.store.Tx(ctx, func(tx pgx.Tx) error {
		if err := lockOrg(ctx, tx, actor.OrgID); err != nil {
			return err
		}
		actorRole, err := memberManagerRole(ctx, tx, actor.OrgID, actor.UserID)
		if err != nil {
			return err
		}
		role, err := membershipRole(ctx, tx, actor.OrgID, userID)
		if err != nil {
			return err
		}
		if actorRole == RoleAdmin && role == RoleOwner {
			return ErrForbidden
		}
		if role == RoleOwner {
			last, err := isLastOwner(ctx, tx, actor.OrgID)
			if err != nil {
				return err
			}
			if last {
				return ErrLastOwner
			}
		}
		if _, err := tx.Exec(ctx, `delete from memberships where org_id = $1 and user_id = $2`, actor.OrgID, userID); err != nil {
			return fmt.Errorf("delete membership: %w", err)
		}
		// Stop email reports for the removed member's sites in the same transaction.
		if _, err := tx.Exec(ctx, `delete from report_subscriptions rs using sites s
			where rs.site_id = s.id and s.org_id = $1 and rs.user_id = $2`, actor.OrgID, userID); err != nil {
			return fmt.Errorf("delete report subscriptions: %w", err)
		}
		return audit.Write(ctx, tx, actor, "membership.removed", "membership", userID, map[string]any{"role": role})
	})
}

func (o *Orgs) MembershipsFor(ctx context.Context, userID int64) ([]Membership, error) {
	stored, err := store.MembershipsFor(ctx, o.store, userID)
	if err != nil {
		return nil, err
	}
	memberships := make([]Membership, 0, len(stored))
	for _, membership := range stored {
		memberships = append(memberships, Membership{
			OrgID: membership.OrgID, UserID: membership.UserID,
			OrgName: membership.OrgName, OrgSlug: membership.OrgSlug,
			Role: Role(membership.Role), SiteIDs: membership.SiteIDs,
		})
	}
	return memberships, nil
}

func validRole(role Role) bool {
	switch role {
	case RoleOwner, RoleAdmin, RoleEditor, RoleViewer:
		return true
	default:
		return false
	}
}

func lockOrg(ctx context.Context, tx pgx.Tx, orgID int64) error {
	var id int64
	if err := tx.QueryRow(ctx, `select id from orgs where id = $1 and deleted_at is null for update`, orgID).Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("lock organization: %w", err)
	}
	return nil
}

func membershipRole(ctx context.Context, tx pgx.Tx, orgID, userID int64) (Role, error) {
	var role Role
	if err := tx.QueryRow(ctx, `select role from memberships where org_id = $1 and user_id = $2 for update`, orgID, userID).Scan(&role); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("query membership: %w", err)
	}
	return role, nil
}

func memberManagerRole(ctx context.Context, tx pgx.Tx, orgID, userID int64) (Role, error) {
	role, err := membershipRole(ctx, tx, orgID, userID)
	if errors.Is(err, ErrNotFound) {
		return "", ErrForbidden
	}
	if err != nil {
		return "", err
	}
	if role != RoleOwner && role != RoleAdmin {
		return "", ErrForbidden
	}
	return role, nil
}

func isLastOwner(ctx context.Context, tx pgx.Tx, orgID int64) (bool, error) {
	var owners int
	if err := tx.QueryRow(ctx, `select count(*) from memberships where org_id = $1 and role = 'owner'`, orgID).Scan(&owners); err != nil {
		return false, fmt.Errorf("count organization owners: %w", err)
	}
	return owners == 1, nil
}
