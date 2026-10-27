// Package orgs manages organizations and memberships.
package orgs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/platform/audit"
	"supermetry/internal/platform/authz"
	"supermetry/internal/platform/lifecycle"
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
	ErrInvalidName   = errors.New("invalid organization name")
	ErrNotRestorable = errors.New("organization is not restorable")
	ErrSiteLimit     = errors.New("site limit")
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
	store       *store.Store
	invalidator interface{ Invalidate() error }
	maxSites    int
}

// SetSiteLimit caps live non-demo sites after an organization restore. Zero
// means no limit.
func (o *Orgs) SetSiteLimit(max int) { o.maxSites = max }

func New(data *store.Store, invalidators ...interface{ Invalidate() error }) *Orgs {
	service := &Orgs{store: data}
	if len(invalidators) > 0 {
		service.invalidator = invalidators[0]
	}
	return service
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

// Leave removes the actor's own membership. Any member may leave; the last
// owner must hand over ownership or delete the organization instead.
func (o *Orgs) Leave(ctx context.Context, actor authz.Principal) error {
	if actor.UserID == 0 || actor.OrgID == 0 {
		return ErrForbidden
	}
	return o.store.Tx(ctx, func(tx pgx.Tx) error {
		if err := lockOrg(ctx, tx, actor.OrgID); err != nil {
			return err
		}
		member, err := membershipAccess(ctx, tx, actor.OrgID, actor.UserID)
		if err != nil {
			return err
		}
		if member.Role == RoleOwner {
			last, err := isLastOwner(ctx, tx, actor.OrgID)
			if err != nil {
				return err
			}
			if last {
				return ErrLastOwner
			}
		}
		if _, err := tx.Exec(ctx, `delete from memberships where org_id = $1 and user_id = $2`, actor.OrgID, actor.UserID); err != nil {
			return fmt.Errorf("delete membership: %w", err)
		}
		if _, err := tx.Exec(ctx, `delete from report_subscriptions rs using sites s
			where rs.site_id = s.id and s.org_id = $1 and rs.user_id = $2`, actor.OrgID, actor.UserID); err != nil {
			return fmt.Errorf("delete report subscriptions: %w", err)
		}
		return audit.Write(ctx, tx, actor, "membership.left", "membership", actor.UserID, map[string]any{
			"role": member.Role, "site_ids": member.SiteIDs, "capabilities": member.Capabilities, "billing_admin": member.BillingAdmin,
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

// Restore lets an owner bring back an organization they (or another owner)
// deleted within the grace period.
func (o *Orgs) Restore(ctx context.Context, userID int64, orgID int64, now time.Time) error {
	return o.restore(ctx, userID, orgID, now, false)
}

// AdminRestore restores any deleted, not yet erased organization that is still
// inside the grace period, whoever deleted it. It returns store.ErrNotFound for
// an unknown or erased organization, ErrNotRestorable outside the grace period
// or for a live organization, and ErrForbidden when the actor is not an active
// instance admin.
func (o *Orgs) AdminRestore(ctx context.Context, actorUserID int64, orgID int64, now time.Time) error {
	return o.restore(ctx, actorUserID, orgID, now, true)
}

func (o *Orgs) restore(ctx context.Context, userID int64, orgID int64, now time.Time, asAdmin bool) error {
	err := o.store.Tx(ctx, func(tx pgx.Tx) error {
		if asAdmin {
			if allowed, err := store.InstanceAdminActive(ctx, tx, userID); err != nil {
				return err
			} else if !allowed {
				return ErrForbidden
			}
		}
		var deletedAt *time.Time
		var erasedAt, eraseRequestedAt *time.Time
		var deletedByKind *string
		if err := tx.QueryRow(ctx, `select deleted_at,erased_at,deleted_by_kind,erase_requested_at from orgs where id=$1 for update`, orgID).Scan(&deletedAt, &erasedAt, &deletedByKind, &eraseRequestedAt); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				if asAdmin {
					return store.ErrNotFound
				}
				return ErrNotRestorable
			}
			return fmt.Errorf("lock organization for restore: %w", err)
		}
		if asAdmin {
			if erasedAt != nil && eraseRequestedAt == nil {
				return store.ErrNotFound
			}
		} else {
			if deletedByKind != nil && *deletedByKind == "instance_admin" {
				return ErrNotRestorable
			}
			var role Role
			if err := tx.QueryRow(ctx, `select role from memberships where org_id=$1 and user_id=$2 for update`, orgID, userID).Scan(&role); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return ErrForbidden
				}
				return fmt.Errorf("lock restoring owner membership: %w", err)
			}
			if role != RoleOwner {
				return ErrForbidden
			}
		}
		var scheduled bool
		if err := tx.QueryRow(ctx, `
			select coalesce(
			  (select (metadata->>'scheduled')::boolean from audit_log
			   where org_id=$1 and action='organization.deleted' and target_id=$1
			   order by id desc limit 1),
			  exists(select 1 from sites where org_id=$1 and deleted_with_org)
			)`, orgID).Scan(&scheduled); err != nil {
			return fmt.Errorf("check scheduled organization deletion: %w", err)
		}
		if deletedAt == nil || erasedAt != nil || eraseRequestedAt != nil || !scheduled || !lifecycle.Restorable(*deletedAt, false, now) {
			return ErrNotRestorable
		}
		rows, err := tx.Query(ctx, `
			select erasure_started_at from sites
			where org_id=$1 and deleted_with_org and not is_demo and erased_at is null
			order by id for update`, orgID)
		if err != nil {
			return fmt.Errorf("lock organization sites for restore: %w", err)
		}
		for rows.Next() {
			var erasureStartedAt *time.Time
			if err := rows.Scan(&erasureStartedAt); err != nil {
				rows.Close()
				return fmt.Errorf("scan organization site erasure claim: %w", err)
			}
			if erasureStartedAt != nil {
				rows.Close()
				return ErrNotRestorable
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return fmt.Errorf("iterate organization sites for restore: %w", err)
		}
		rows.Close()
		if o.maxSites > 0 {
			var restoredCount int
			if err := tx.QueryRow(ctx, `select count(*) from sites where org_id=$1 and not is_demo and erased_at is null and (deleted_at is null or deleted_with_org)`, orgID).Scan(&restoredCount); err != nil {
				return fmt.Errorf("count restored organization sites: %w", err)
			}
			if restoredCount > o.maxSites {
				return ErrSiteLimit
			}
		}
		if _, err := tx.Exec(ctx, `
			update orgs
			set deleted_at=null,deleted_by_kind=null,deleted_by_user_id=null,
			    deletion_notified_at=null,deletion_reminded_at=null
			where id=$1`, orgID); err != nil {
			return fmt.Errorf("restore organization: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			update sites
			set deleted_at=case when not is_demo and erased_at is null then null else deleted_at end,
			    deleted_with_org=false,deletion_notified_at=null,deletion_reminded_at=null
			where org_id=$1 and deleted_with_org`, orgID); err != nil {
			return fmt.Errorf("restore organization sites: %w", err)
		}
		if asAdmin {
			return store.WriteInstanceAudit(ctx, tx, store.InstanceAuditEntry{
				ActorUserID: userID, Action: "instance.organization.restored", TargetType: "organization", TargetID: orgID,
			})
		}
		return audit.Write(ctx, tx, authz.Principal{UserID: userID, OrgID: orgID, Role: authz.RoleOwner}, "organization.restored", "organization", orgID, nil)
	})
	if err != nil {
		return err
	}
	if o.invalidator != nil {
		if err := o.invalidator.Invalidate(); err != nil {
			slog.WarnContext(ctx, "refresh site registry after organization restore", "err", err)
		}
	}
	return nil
}

func (o *Orgs) Rename(ctx context.Context, principal authz.Principal, name string) error {
	if !authz.Can(principal, authz.ActionOrgUpdate, 0) || principal.UserID == 0 {
		return ErrForbidden
	}
	name = strings.TrimSpace(name)
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 100 {
		return ErrInvalidName
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return ErrInvalidName
		}
	}
	return o.store.Tx(ctx, func(tx pgx.Tx) error {
		var oldName string
		if err := tx.QueryRow(ctx, `select name from orgs where id=$1 and deleted_at is null for update`, principal.OrgID).Scan(&oldName); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("lock organization for rename: %w", err)
		}
		current, err := store.CurrentMemberForUpdate(ctx, tx, principal.OrgID, principal.UserID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrForbidden
			}
			return err
		}
		if !authz.Can(current, authz.ActionOrgUpdate, 0) {
			return ErrForbidden
		}
		if _, err := tx.Exec(ctx, `update orgs set name=$2 where id=$1`, principal.OrgID, name); err != nil {
			return fmt.Errorf("rename organization: %w", err)
		}
		return audit.Write(ctx, tx, principal, "organization.renamed", "organization", principal.OrgID, map[string]any{"from": oldName, "to": name})
	})
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
