// Package orgs manages organizations and memberships.
package orgs

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/platform/audit"
	"supermetry/internal/platform/authz"
	"supermetry/internal/platform/store"
)

type Role = authz.Role

const (
	RoleOwner  = authz.RoleOwner
	RoleAdmin  = authz.RoleAdmin
	RoleEditor = authz.RoleEditor
	RoleViewer = authz.RoleViewer
)

var (
	ErrForbidden     = errors.New("forbidden")
	ErrLastOwner     = errors.New("organization must have at least one owner")
	ErrNotFound      = errors.New("membership not found")
	ErrInvalidRole   = errors.New("invalid organization role")
	ErrInvalidAccess = errors.New("invalid membership access")
)

type Membership struct {
	OrgID        int64
	UserID       int64
	OrgName      string
	OrgSlug      string
	Role         Role
	SiteIDs      []int32
	Capabilities []string
	BillingAdmin bool
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

// AccessUpdate distinguishes omitted values from explicit grants. A pointer to a
// nil SiteIDs slice means all sites; a pointer to an empty slice means no sites.
type AccessUpdate struct {
	Role         *Role
	SiteIDs      *[]int32
	Capabilities *[]string
	BillingAdmin *bool
}

// SetRole preserves additive and billing grants for existing callers.
func (o *Orgs) SetRole(ctx context.Context, actor authz.Principal, userID int64, role Role, siteIDs []int32) error {
	return o.UpdateAccess(ctx, actor, userID, AccessUpdate{Role: &role, SiteIDs: &siteIDs})
}

func (o *Orgs) UpdateAccess(ctx context.Context, actor authz.Principal, userID int64, update AccessUpdate) error {
	if !authz.Can(actor, authz.ActionMembersManage, 0) || actor.UserID == 0 {
		return ErrForbidden
	}
	if update.Role != nil && !validRole(*update.Role) {
		return ErrInvalidRole
	}
	if update.Capabilities != nil && !authz.ValidCapabilities(*update.Capabilities) {
		return ErrInvalidAccess
	}
	return o.store.Tx(ctx, func(tx pgx.Tx) error {
		if err := lockOrg(ctx, tx, actor.OrgID); err != nil {
			return err
		}
		actorRole, err := memberManagerRole(ctx, tx, actor.OrgID, actor.UserID)
		if err != nil {
			return err
		}
		current, err := membershipAccess(ctx, tx, actor.OrgID, userID)
		if err != nil {
			return err
		}
		next := current
		if update.Role != nil {
			next.Role = *update.Role
		}
		if update.SiteIDs != nil {
			// Validate supplied IDs even when the selected preset is unrestricted.
			if len(*update.SiteIDs) > 0 {
				valid, err := store.InvitationSitesBelongToOrg(ctx, tx, actor.OrgID, *update.SiteIDs)
				if err != nil {
					return err
				}
				if !valid {
					return ErrInvalidAccess
				}
			}
			next.SiteIDs = *update.SiteIDs
		}
		if next.Role == RoleOwner || next.Role == RoleAdmin {
			next.SiteIDs = nil
		}
		if update.Capabilities != nil {
			next.Capabilities = *update.Capabilities
		}
		if next.Capabilities == nil {
			next.Capabilities = []string{}
		}
		if update.BillingAdmin != nil {
			next.BillingAdmin = *update.BillingAdmin
		}
		if actorRole == RoleAdmin && (current.Role == RoleOwner || next.Role == RoleOwner || current.BillingAdmin != next.BillingAdmin) {
			return ErrForbidden
		}
		if current.Role == RoleOwner && next.Role != RoleOwner {
			last, err := isLastOwner(ctx, tx, actor.OrgID)
			if err != nil {
				return err
			}
			if last {
				return ErrLastOwner
			}
		}
		if _, err := tx.Exec(ctx, `update memberships set role=$3,site_ids=$4,capabilities=$5,billing_admin=$6 where org_id=$1 and user_id=$2`, actor.OrgID, userID, next.Role, next.SiteIDs, next.Capabilities, next.BillingAdmin); err != nil {
			return fmt.Errorf("update membership access: %w", err)
		}
		return audit.Write(ctx, tx, actor, "membership.role_changed", "membership", userID, map[string]any{
			"old_role": current.Role, "new_role": next.Role, "site_ids": next.SiteIDs,
			"old_site_ids": current.SiteIDs, "capabilities": next.Capabilities, "old_capabilities": current.Capabilities,
			"billing_admin": next.BillingAdmin, "old_billing_admin": current.BillingAdmin,
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
		member, err := membershipAccess(ctx, tx, actor.OrgID, userID)
		if err != nil {
			return err
		}
		role := member.Role
		if actorRole == RoleAdmin && (role == RoleOwner || member.BillingAdmin) {
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
		return audit.Write(ctx, tx, actor, "membership.removed", "membership", userID, map[string]any{
			"role": role, "site_ids": member.SiteIDs, "capabilities": member.Capabilities, "billing_admin": member.BillingAdmin,
		})
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
			Capabilities: membership.Capabilities, BillingAdmin: membership.BillingAdmin,
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

func membershipAccess(ctx context.Context, tx pgx.Tx, orgID, userID int64) (Membership, error) {
	member := Membership{OrgID: orgID, UserID: userID}
	err := tx.QueryRow(ctx, `select role,site_ids,capabilities,billing_admin from memberships where org_id=$1 and user_id=$2 for update`, orgID, userID).Scan(&member.Role, &member.SiteIDs, &member.Capabilities, &member.BillingAdmin)
	if errors.Is(err, pgx.ErrNoRows) {
		return Membership{}, ErrNotFound
	}
	if err != nil {
		return Membership{}, fmt.Errorf("query membership access: %w", err)
	}
	return member, nil
}
