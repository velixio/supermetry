// Package invites creates, accepts, and revokes organization invitations.
package invites

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"supermetry/internal/platform/audit"
	"supermetry/internal/platform/authz"
	platformcrypto "supermetry/internal/platform/crypto"
	"supermetry/internal/platform/mail"
	"supermetry/internal/platform/store"
	"supermetry/internal/platform/users"
)

const invitationLifetime = 7 * 24 * time.Hour

var (
	ErrForbidden = errors.New("forbidden")
	ErrInvalid   = errors.New("invalid invitation")
	ErrNotFound  = errors.New("invitation not found")
)

type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

type Option func(*Invites)

func WithClock(clock Clock) Option {
	return func(invites *Invites) { invites.clock = clock }
}

type Invites struct {
	store   *store.Store
	users   *users.Users
	mail    mail.Sender
	baseURL string
	clock   Clock
}

func New(data *store.Store, accounts *users.Users, sender mail.Sender, baseURL string, options ...Option) *Invites {
	service := &Invites{
		store: data, users: accounts, mail: sender,
		baseURL: strings.TrimRight(baseURL, "/"), clock: realClock{},
	}
	for _, option := range options {
		option(service)
	}
	return service
}

// Access contains the additive project grants and the independent Billing Admin
// assignment. Create accepts one optional value for backwards compatibility.
type Access struct {
	Capabilities []string
	BillingAdmin bool
}

func (i *Invites) Create(ctx context.Context, principal authz.Principal, email string, role authz.Role, siteIDs []int32, options ...Access) (link string, err error) {
	if !authz.Can(principal, authz.ActionMembersManage, 0) || principal.UserID == 0 {
		return "", ErrForbidden
	}
	if len(options) > 1 {
		return "", ErrInvalid
	}
	access := Access{Capabilities: []string{}}
	if len(options) == 1 {
		access = options[0]
	}
	if !authz.ValidCapabilities(access.Capabilities) {
		return "", ErrInvalid
	}
	if access.Capabilities == nil {
		access.Capabilities = []string{}
	}
	if role == authz.RoleOwner {
		return "", ErrForbidden
	}
	if !validInvitationRole(role) {
		return "", ErrInvalid
	}
	email = users.NormalizeEmail(email)
	if email == "" {
		return "", ErrInvalid
	}
	siteIDs = cloneSiteIDs(siteIDs)

	plain, tokenHash := platformcrypto.NewToken("")
	now := i.clock.Now()
	var invitationID int64
	err = i.store.Tx(ctx, func(tx pgx.Tx) error {
		actorRole, err := invitationManager(ctx, tx, principal.OrgID, principal.UserID)
		if err != nil {
			return err
		}
		if access.BillingAdmin && actorRole != authz.RoleOwner {
			return ErrForbidden
		}
		if len(siteIDs) > 0 {
			valid, validateErr := store.InvitationSitesBelongToOrg(ctx, tx, principal.OrgID, siteIDs)
			if validateErr != nil {
				return validateErr
			}
			if !valid {
				return ErrInvalid
			}
		}
		if role == authz.RoleAdmin {
			siteIDs = nil
		}
		var createErr error
		invitationID, createErr = store.CreateInvitation(ctx, tx, store.Invitation{
			OrgID: principal.OrgID, Email: email, Role: string(role), SiteIDs: siteIDs,
			TokenHash: tokenHash, InvitedBy: principal.UserID, ExpiresAt: now.Add(invitationLifetime),
			Capabilities: access.Capabilities, BillingAdmin: access.BillingAdmin,
		})
		if createErr != nil {
			return createErr
		}
		return audit.Write(ctx, tx, principal, "invitation.created", "invitation", invitationID, map[string]any{
			"email": email, "role": role, "site_ids": siteIDs,
			"capabilities": access.Capabilities, "billing_admin": access.BillingAdmin,
		})
	})
	if err != nil {
		return "", err
	}

	link = i.baseURL + "/invite?token=" + url.QueryEscape(plain)
	if !i.mail.Enabled() {
		return link, nil
	}
	message := mail.Message{
		To:      email,
		Subject: "You're invited to Supermetry",
		Text:    "Accept your invitation using this one-time link:\n\n" + link,
	}
	if err := i.mail.Send(ctx, message); err != nil {
		return "", fmt.Errorf("send invitation: %w", err)
	}
	return link, nil
}

func (i *Invites) Accept(ctx context.Context, token string, userID int64, name, password string) (int64, error) {
	if token == "" {
		return 0, ErrInvalid
	}
	tokenHash := platformcrypto.HashToken(token)
	now := i.clock.Now()
	err := i.store.Tx(ctx, func(tx pgx.Tx) error {
		// Every invite/member mutation locks the organization first. Looking up its
		// ID does not consume the token or expose invitation details to callers.
		var orgID int64
		if err := tx.QueryRow(ctx, `select org_id from invitations where token_hash=$1`, tokenHash).Scan(&orgID); errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalid
		} else if err != nil {
			return err
		}
		if err := lockInvitationOrg(ctx, tx, orgID); err != nil {
			return err
		}
		invitation, err := store.ConsumeInvitation(ctx, tx, tokenHash, now)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalid
		}
		if err != nil {
			return err
		}

		role, err := currentInvitationManager(ctx, tx, invitation.OrgID, invitation.InvitedBy)
		if errors.Is(err, ErrForbidden) {
			return ErrInvalid
		}
		if err != nil {
			return err
		}
		if !validInvitationRole(authz.Role(invitation.Role)) || !authz.ValidCapabilities(invitation.Capabilities) || (invitation.BillingAdmin && role != authz.RoleOwner) {
			return ErrInvalid
		}
		if len(invitation.SiteIDs) > 0 {
			valid, err := store.InvitationSitesBelongToOrg(ctx, tx, invitation.OrgID, invitation.SiteIDs)
			if err != nil {
				return err
			}
			if !valid {
				return ErrInvalid
			}
		}
		if userID == 0 {
			userID, err = i.users.CreateWithDB(ctx, tx, invitation.Email, name, password, now)
			if err != nil {
				return err
			}
		} else {
			var existingEmail string
			existingEmail, err = store.InvitationUserEmail(ctx, tx, userID)
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrInvalid
			}
			if err != nil {
				return err
			}
			if users.NormalizeEmail(existingEmail) != invitation.Email {
				return ErrInvalid
			}
		}
		if err := store.CreateInvitationMembership(ctx, tx, invitation, userID); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return ErrInvalid
			}
			return err
		}
		return audit.Write(ctx, tx, authz.Principal{UserID: userID, OrgID: invitation.OrgID}, "invitation.accepted", "invitation", invitation.ID, map[string]any{
			"role": invitation.Role, "site_ids": invitation.SiteIDs,
			"capabilities": invitation.Capabilities, "billing_admin": invitation.BillingAdmin,
		})
	})
	if err != nil {
		return 0, err
	}
	return userID, nil
}

func (i *Invites) Revoke(ctx context.Context, principal authz.Principal, id int64) error {
	if !authz.Can(principal, authz.ActionMembersManage, 0) || principal.UserID == 0 {
		return ErrForbidden
	}
	return i.store.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := invitationManager(ctx, tx, principal.OrgID, principal.UserID); err != nil {
			return err
		}
		if err := store.RevokeInvitation(ctx, tx, principal.OrgID, id, i.clock.Now()); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		return audit.Write(ctx, tx, principal, "invitation.revoked", "invitation", id, nil)
	})
}

func validInvitationRole(role authz.Role) bool {
	switch role {
	case authz.RoleAdmin, authz.RoleEditor, authz.RoleViewer:
		return true
	default:
		return false
	}
}

func cloneSiteIDs(siteIDs []int32) []int32 {
	if siteIDs == nil {
		return nil
	}
	out := make([]int32, len(siteIDs)) // keeps an empty restriction empty (no sites), unlike append on nil
	copy(out, siteIDs)
	return out
}

func invitationManager(ctx context.Context, tx pgx.Tx, orgID, userID int64) (authz.Role, error) {
	if err := lockInvitationOrg(ctx, tx, orgID); err != nil {
		return "", err
	}
	return currentInvitationManager(ctx, tx, orgID, userID)
}

func lockInvitationOrg(ctx context.Context, tx pgx.Tx, orgID int64) error {
	var id int64
	err := tx.QueryRow(ctx, `select id from orgs where id=$1 and deleted_at is null for update`, orgID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrInvalid
	}
	return err
}

func currentInvitationManager(ctx context.Context, tx pgx.Tx, orgID, userID int64) (authz.Role, error) {
	var role authz.Role
	err := tx.QueryRow(ctx, `select role from memberships where org_id=$1 and user_id=$2 for update`, orgID, userID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrForbidden
	}
	if err != nil {
		return "", err
	}
	if role != authz.RoleOwner && role != authz.RoleAdmin {
		return "", ErrForbidden
	}
	return role, nil
}
