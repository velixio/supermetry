package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

type Invitation struct {
	ID           int64
	OrgID        int64
	Email        string
	Role         string
	SiteIDs      []int32
	Capabilities []string
	BillingAdmin bool
	TokenHash    []byte
	InvitedBy    int64
	ExpiresAt    time.Time
	AcceptedAt   *time.Time
	RevokedAt    *time.Time
}

func InvitationSitesBelongToOrg(ctx context.Context, tx pgx.Tx, orgID int64, siteIDs []int32) (bool, error) {
	var valid bool
	err := tx.QueryRow(ctx, `
		select count(*) = cardinality($2::integer[])
		from sites
		where org_id=$1 and deleted_at is null and id=any($2::integer[])`, orgID, siteIDs).Scan(&valid)
	return valid, err
}

func CreateInvitation(ctx context.Context, tx pgx.Tx, invitation Invitation) (int64, error) {
	var id int64
	err := tx.QueryRow(ctx, `
		insert into invitations (org_id,email,role,site_ids,token_hash,invited_by,expires_at,capabilities,billing_admin)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		returning id`,
		invitation.OrgID, invitation.Email, invitation.Role, invitation.SiteIDs,
		invitation.TokenHash, invitation.InvitedBy, invitation.ExpiresAt,
		invitation.Capabilities, invitation.BillingAdmin,
	).Scan(&id)
	return id, err
}

func ConsumeInvitation(ctx context.Context, tx pgx.Tx, tokenHash []byte, now time.Time) (Invitation, error) {
	var invitation Invitation
	err := tx.QueryRow(ctx, `
		update invitations
		set accepted_at=$2
		where token_hash=$1 and accepted_at is null and revoked_at is null and expires_at>$2
		returning id,org_id,email,role,site_ids,token_hash,invited_by,expires_at,accepted_at,revoked_at,capabilities,billing_admin`,
		tokenHash, now,
	).Scan(
		&invitation.ID, &invitation.OrgID, &invitation.Email, &invitation.Role,
		&invitation.SiteIDs, &invitation.TokenHash, &invitation.InvitedBy,
		&invitation.ExpiresAt, &invitation.AcceptedAt, &invitation.RevokedAt,
		&invitation.Capabilities, &invitation.BillingAdmin,
	)
	return invitation, err
}

func RevokeInvitation(ctx context.Context, tx pgx.Tx, orgID, invitationID int64, now time.Time) error {
	var id int64
	return tx.QueryRow(ctx, `
		update invitations
		set revoked_at=$3
		where org_id=$1 and id=$2 and accepted_at is null and revoked_at is null
		returning id`, orgID, invitationID, now).Scan(&id)
}

func InvitationUserEmail(ctx context.Context, db DBTX, userID int64) (string, error) {
	var email string
	err := db.QueryRow(ctx, `select email from users where id=$1`, userID).Scan(&email)
	return email, err
}

func CreateInvitationMembership(ctx context.Context, tx pgx.Tx, invitation Invitation, userID int64) error {
	_, err := tx.Exec(ctx, `
		insert into memberships(org_id,user_id,role,site_ids,capabilities,billing_admin)
		values ($1,$2,$3,$4,$5,$6)`,
		invitation.OrgID, userID, invitation.Role, invitation.SiteIDs, invitation.Capabilities, invitation.BillingAdmin)
	return err
}
