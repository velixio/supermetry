package auth

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/platform/audit"
	"supermetry/internal/platform/authz"
	platformcrypto "supermetry/internal/platform/crypto"
	"supermetry/internal/platform/store"
)

// ImpersonationLifetime is how long an admin may stay signed in as a user.
// Activity never extends it.
const ImpersonationLifetime = time.Hour

// ErrImpersonationForbidden is returned when the target may not be signed in
// as: another instance admin, a disabled account, or an actor that is no
// longer an instance admin.
var ErrImpersonationForbidden = errors.New("this account cannot be signed in as")

// ErrImpersonationResetRequired is returned when the target must reset their
// password first: the authenticator would reject the session on first use.
var ErrImpersonationResetRequired = errors.New("user must reset their password first")

// CreateImpersonationSession opens a session for targetUserID on behalf of the
// instance admin adminUserID. The session ends at now+1h whatever the activity,
// records the admin in impersonated_by, and is audited for the instance and for
// every organization the target belongs to, in the same transaction. A missing
// target or the hidden system user returns store.ErrNotFound.
func (a *Auth) CreateImpersonationSession(ctx context.Context, adminUserID, targetUserID int64, now time.Time) (string, error) {
	plain, hash := platformcrypto.NewToken("")
	metadata, _ := ctx.Value(sessionMetadataKey{}).(SessionMetadata)
	hardExpiresAt := now.Add(ImpersonationLifetime)
	err := a.store.Tx(ctx, func(tx pgx.Tx) error {
		actorAllowed, err := store.InstanceAdminActive(ctx, tx, adminUserID)
		if err != nil {
			return err
		}
		if !actorAllowed {
			return ErrImpersonationForbidden
		}
		var isAdmin, isSystem, disabled, resetRequired bool
		err = tx.QueryRow(ctx, `select is_admin,is_system,disabled_at is not null,password_reset_required from users where id=$1 for share`, targetUserID).Scan(&isAdmin, &isSystem, &disabled, &resetRequired)
		if errors.Is(err, pgx.ErrNoRows) {
			return store.ErrNotFound
		}
		if err != nil {
			return err
		}
		switch {
		case isSystem:
			return store.ErrNotFound
		case isAdmin || disabled:
			return ErrImpersonationForbidden
		case resetRequired:
			return ErrImpersonationResetRequired
		}
		if err := store.CreateSession(ctx, tx, store.UserSession{
			IDHash:      hash,
			UserID:      targetUserID,
			CreatedAt:   now,
			ExpiresAt:   hardExpiresAt,
			DeviceLabel: "Signed in by an administrator",
			IPAddress:   metadata.IPAddress, CountryCode: metadata.CountryCode, Browser: metadata.Browser,
			ImpersonatedBy: adminUserID,
			HardExpiresAt:  &hardExpiresAt,
		}); err != nil {
			return err
		}
		const action = "instance.user.impersonated"
		details := map[string]any{"expires_at": hardExpiresAt.UTC().Format(time.RFC3339)}
		if err := store.WriteInstanceAudit(ctx, tx, store.InstanceAuditEntry{
			ActorUserID: adminUserID, Action: action, TargetType: "user", TargetID: targetUserID, Metadata: details,
		}); err != nil {
			return err
		}
		orgIDs, err := targetOrgIDs(ctx, tx, targetUserID)
		if err != nil {
			return err
		}
		// Org owners see that an administrator signed in as their member.
		for _, orgID := range orgIDs {
			if err := audit.Write(ctx, tx, authz.Principal{OrgID: orgID, UserID: adminUserID}, action, "user", targetUserID, details); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return plain, nil
}

func targetOrgIDs(ctx context.Context, tx pgx.Tx, userID int64) ([]int64, error) {
	rows, err := tx.Query(ctx, `select org_id from memberships where user_id=$1 order by org_id`, userID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}
