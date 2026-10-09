// Package audit writes append-only records for platform mutations.
package audit

import (
	"context"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/platform/authz"
	"supermetry/internal/platform/store"
)

func Write(ctx context.Context, tx pgx.Tx, principal authz.Principal, action, targetType string, targetID int64, metadata map[string]any) error {
	return store.WriteAudit(ctx, tx, store.AuditEntry{
		OrgID:       principal.OrgID,
		ActorUserID: principal.UserID,
		ActorKeyID:  principal.KeyID,
		Action:      action,
		TargetType:  targetType,
		TargetID:    targetID,
		Metadata:    metadata,
	})
}
