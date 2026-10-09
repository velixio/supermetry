package replay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const migrationBatchSize = 256

type migrationCheckpoint struct {
	LastKey       string          `json:"last_key,omitempty"`
	PendingDelete string          `json:"pending_delete,omitempty"`
	Complete      bool            `json:"complete"`
	From          json.RawMessage `json:"from,omitempty"`
	To            json.RawMessage `json:"to,omitempty"`
}

func RunMigration(ctx context.Context, pool *pgxpool.Pool, from, to BlobStore) (result error) {
	if pool == nil || from == nil || to == nil {
		return errors.New("replay migration requires a pool and two stores")
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire replay migration connection: %w", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `select pg_advisory_lock(hashtext('supermetry:replay-migration'))`); err != nil {
		return fmt.Errorf("lock replay migration: %w", err)
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, unlockErr := conn.Exec(unlockCtx, `select pg_advisory_unlock(hashtext('supermetry:replay-migration'))`)
		if unlockErr != nil {
			conn.Hijack().Close(unlockCtx)
			result = errors.Join(result, fmt.Errorf("unlock replay migration: %w", unlockErr))
		}
	}()

	checkpoint, err := loadMigrationCheckpoint(ctx, conn)
	if err != nil {
		return err
	}
	if checkpoint.PendingDelete != "" {
		if _, err := from.Delete(ctx, checkpoint.PendingDelete); err != nil {
			return fmt.Errorf("resume replay source deletion %q: %w", checkpoint.PendingDelete, err)
		}
		checkpoint.PendingDelete = ""
		if err := saveMigrationCheckpoint(ctx, conn, checkpoint); err != nil {
			return err
		}
	}
	if checkpoint.Complete {
		return nil
	}

	for {
		rows, err := conn.Query(ctx, `
select distinct blob_key
from replay_sessions
where blob_key > $1
order by blob_key
limit $2`, checkpoint.LastKey, migrationBatchSize)
		if err != nil {
			return fmt.Errorf("list replay blobs for migration: %w", err)
		}
		keys := make([]string, 0, migrationBatchSize)
		for rows.Next() {
			var key string
			if err := rows.Scan(&key); err != nil {
				rows.Close()
				return fmt.Errorf("scan replay migration key: %w", err)
			}
			keys = append(keys, key)
		}
		rowsErr := rows.Err()
		rows.Close()
		if rowsErr != nil {
			return fmt.Errorf("iterate replay migration keys: %w", rowsErr)
		}
		if len(keys) == 0 {
			checkpoint.Complete = true
			return saveMigrationCheckpoint(ctx, conn, checkpoint)
		}

		for _, key := range keys {
			reader, err := from.Open(ctx, key)
			if err != nil {
				return fmt.Errorf("open replay source %q: %w", key, err)
			}
			_, putErr := to.PutIfAbsent(ctx, key, reader)
			closeErr := reader.Close()
			if putErr != nil {
				return fmt.Errorf("copy replay blob %q: %w", key, putErr)
			}
			if closeErr != nil {
				return fmt.Errorf("close replay source %q: %w", key, closeErr)
			}

			checkpoint.LastKey = key
			checkpoint.PendingDelete = key
			if err := saveMigrationCheckpoint(ctx, conn, checkpoint); err != nil {
				return err
			}
			if _, err := from.Delete(ctx, key); err != nil {
				return fmt.Errorf("delete replay source %q: %w", key, err)
			}
			checkpoint.PendingDelete = ""
			if err := saveMigrationCheckpoint(ctx, conn, checkpoint); err != nil {
				return err
			}
		}
	}
}

func loadMigrationCheckpoint(ctx context.Context, conn *pgxpool.Conn) (migrationCheckpoint, error) {
	var raw []byte
	if err := conn.QueryRow(ctx, `
select coalesce(settings->'replay_migration', '{}'::jsonb)
from instance_settings
where id`).Scan(&raw); err != nil {
		return migrationCheckpoint{}, fmt.Errorf("load replay migration checkpoint: %w", err)
	}
	var checkpoint migrationCheckpoint
	if err := json.Unmarshal(raw, &checkpoint); err != nil {
		return migrationCheckpoint{}, fmt.Errorf("decode replay migration checkpoint: %w", err)
	}
	return checkpoint, nil
}

func saveMigrationCheckpoint(ctx context.Context, conn *pgxpool.Conn, checkpoint migrationCheckpoint) error {
	raw, err := json.Marshal(checkpoint)
	if err != nil {
		return fmt.Errorf("encode replay migration checkpoint: %w", err)
	}
	command, err := conn.Exec(ctx, `
update instance_settings
set settings = jsonb_set(settings, '{replay_migration}', $1::jsonb, true)
where id`, raw)
	if err != nil {
		return fmt.Errorf("save replay migration checkpoint: %w", err)
	}
	if command.RowsAffected() != 1 {
		return errors.New("instance settings row is missing")
	}
	return nil
}
