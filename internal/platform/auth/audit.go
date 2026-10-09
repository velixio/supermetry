package auth

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
)

func writeUserAudits(ctx context.Context, tx pgx.Tx, userID int64, action string) error {
	return auditUserOrgs(ctx, tx, userID, action)
}

func auditUserOrgsWithActor(ctx context.Context, tx pgx.Tx, userID int64, actorUserID *int64, action string, metadata map[string]any) error {
	if metadata == nil {
		metadata = map[string]any{}
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		insert into audit_log (org_id, actor_user_id, action, target_type, target_id, metadata)
		select org_id, $2, $3, 'user', $1, $4::jsonb
		from memberships where user_id=$1`, userID, actorUserID, action, encoded)
	return err
}
