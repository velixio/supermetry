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

	"panel/internal/platform/audit"
	"panel/internal/platform/authz"
	platformcrypto "panel/internal/platform/crypto"
	"panel/internal/platform/mail"
	"panel/internal/platform/store"
	"panel/internal/platform/users"
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

func (i *Invites) Create(ctx context.Context, principal authz.Principal, email string, role authz.Role, siteIDs []int32) (link string, err error) {
	if !authz.Can(principal, authz.ActionMembersManage, 0) || principal.UserID == 0 {
		return "", ErrForbidden
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
	if role == authz.RoleAdmin {
		siteIDs = nil
	} else {
		siteIDs = cloneSiteIDs(siteIDs)
	}

	plain, tokenHash := platformcrypto.NewToken("")
	now := i.clock.Now()
	var invitationID int64
	err = i.store.Tx(ctx, func(tx pgx.Tx) error {
		if len(siteIDs) > 0 {
			valid, validateErr := store.InvitationSitesBelongToOrg(ctx, tx, principal.OrgID, siteIDs)
			if validateErr != nil {
				return validateErr
			}
			if !valid {
				return ErrInvalid
			}
		}
		var createErr error
		invitationID, createErr = store.CreateInvitation(ctx, tx, store.Invitation{
			OrgID: principal.OrgID, Email: email, Role: string(role), SiteIDs: siteIDs,
			TokenHash: tokenHash, InvitedBy: principal.UserID, ExpiresAt: now.Add(invitationLifetime),
		})
		if createErr != nil {
			return createErr
		}
		return audit.Write(ctx, tx, principal, "invitation.created", "invitation", invitationID, map[string]any{
			"email": email, "role": role, "site_ids": siteIDs,
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
		Subject: "You're invited to Panel",
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
		invitation, err := store.ConsumeInvitation(ctx, tx, tokenHash, now)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalid
		}
		if err != nil {
			return err
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
