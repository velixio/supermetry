// Package audit writes append-only records for platform mutations.
package audit

import (
	"context"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/platform/authz"
	"supermetry/internal/platform/store"
)

// Write records an organization audit entry for principal. When the principal
// is a session an instance admin opened as another user, the entry's metadata
// also carries impersonated_by (the admin's user id).
func Write(ctx context.Context, tx pgx.Tx, principal authz.Principal, action, targetType string, targetID int64, metadata map[string]any) error {
	if principal.ImpersonatedBy != 0 {
		withAdmin := make(map[string]any, len(metadata)+1)
		for key, value := range metadata {
			withAdmin[key] = value
		}
		withAdmin["impersonated_by"] = principal.ImpersonatedBy
		metadata = withAdmin
	}
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
