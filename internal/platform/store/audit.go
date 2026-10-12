package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
)

type AuditEntry struct {
	OrgID       int64
	ActorUserID int64
	ActorKeyID  int64
	Action      string
	TargetType  string
	TargetID    int64
	Metadata    map[string]any
}

type InstanceAuditEntry struct {
	ActorUserID int64
	Action      string
	TargetType  string
	TargetID    int64
	Metadata    map[string]any
}

func WriteAudit(ctx context.Context, tx pgx.Tx, entry AuditEntry) error {
	metadata := entry.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("encode audit metadata: %w", err)
	}
	_, err = tx.Exec(ctx, `
		insert into audit_log (
			org_id, actor_user_id, actor_key_id, action, target_type, target_id, metadata
		) values ($1, nullif($2, 0), nullif($3, 0), $4, $5, $6, $7)`,
		entry.OrgID, entry.ActorUserID, entry.ActorKeyID, entry.Action,
		entry.TargetType, entry.TargetID, encoded)
	if err != nil {
		return fmt.Errorf("insert audit entry: %w", err)
	}
	return nil
}

func WriteInstanceAudit(ctx context.Context, tx pgx.Tx, entry InstanceAuditEntry) error {
	metadata := entry.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("encode instance audit metadata: %w", err)
	}
	_, err = tx.Exec(ctx, `
		insert into audit_log(org_id,actor_user_id,action,target_type,target_id,metadata)
		values (null,nullif($1,0),$2,$3,$4,$5)`, entry.ActorUserID, entry.Action, entry.TargetType, entry.TargetID, encoded)
	if err != nil {
		return fmt.Errorf("insert instance audit entry: %w", err)
	}
	return nil
}
