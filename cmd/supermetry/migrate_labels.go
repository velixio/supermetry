package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/config"
	"supermetry/internal/db"
	platformcrypto "supermetry/internal/platform/crypto"
	"supermetry/internal/privacy"
)

// These are the labels used before the Supermetry rename. They are the only
// place the old name remains; the migration below is the only reader.
const (
	legacyTOTPInfo       = "panel/totp/v1"
	legacyAlertsInfo     = "panel/alerts/v1"
	legacyReplayS3Info   = "panel/replay-s3/v1"
	legacyPathPolicyBase = "panel/path-sanitizer/v1"
)

type labelMigrationReport struct {
	TOTP, Channels, ReplayS3 int
	PathRows                 int64
}

// migrateLabelsCommand re-encrypts stored secrets and rewrites stored path
// policy IDs from the pre-rename labels to the current ones. Run it once, with
// the same SUPERMETRY_SECRET_KEY and SUPERMETRY_TRACKING_PUBLIC_PATHS as before,
// before starting the new release. It is idempotent.
func migrateLabelsCommand(ctx context.Context, args []string, out io.Writer, getenv func(string) string) error {
	if len(args) > 0 {
		return errors.New("migrate-labels takes no arguments")
	}
	cfg, err := config.Load(getenv)
	if err != nil {
		return err
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		return err
	}
	report, err := migrateLabels(ctx, pool, cfg.SecretKey, cfg.TrackingPaths)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "re-encrypted %d TOTP secret(s), %d alert channel(s), %d replay S3 secret(s); rewrote %d path-policy row(s)\n",
		report.TOTP, report.Channels, report.ReplayS3, report.PathRows)
	return nil
}

func migrateLabels(ctx context.Context, pool *pgxpool.Pool, masterKey []byte, policy privacy.PathPolicy) (labelMigrationReport, error) {
	var report labelMigrationReport
	totp, err := newLabelBoxes(masterKey, legacyTOTPInfo, "supermetry/totp/v1")
	if err != nil {
		return report, err
	}
	alerts, err := newLabelBoxes(masterKey, legacyAlertsInfo, "supermetry/alerts/v1")
	if err != nil {
		return report, err
	}
	replayS3, err := newLabelBoxes(masterKey, legacyReplayS3Info, "supermetry/replay-s3/v1")
	if err != nil {
		return report, err
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return report, err
	}
	defer tx.Rollback(ctx)

	if report.TOTP, err = migrateTOTP(ctx, tx, totp); err != nil {
		return report, fmt.Errorf("TOTP secrets: %w", err)
	}
	if report.Channels, err = migrateChannels(ctx, tx, alerts); err != nil {
		return report, fmt.Errorf("alert channels: %w", err)
	}
	if report.ReplayS3, err = migrateReplayS3(ctx, tx, replayS3); err != nil {
		return report, fmt.Errorf("replay S3 secret: %w", err)
	}
	if report.PathRows, err = migratePathPolicy(ctx, tx, policy); err != nil {
		return report, fmt.Errorf("path policy IDs: %w", err)
	}
	return report, tx.Commit(ctx)
}

type labelBoxes struct{ old, current *platformcrypto.SecretBox }

func newLabelBoxes(masterKey []byte, oldInfo, newInfo string) (labelBoxes, error) {
	old, err := platformcrypto.NewSecretBox(masterKey, oldInfo)
	if err != nil {
		return labelBoxes{}, err
	}
	current, err := platformcrypto.NewSecretBox(masterKey, newInfo)
	if err != nil {
		return labelBoxes{}, err
	}
	return labelBoxes{old: old, current: current}, nil
}

// reseal returns the value sealed under the current label. changed is false when
// the value already opens under it. A value that opens under neither label means
// the wrong secret key, which aborts the whole migration.
func (b labelBoxes) reseal(sealed []byte) (out []byte, changed bool, err error) {
	if _, err := b.current.Open(sealed); err == nil {
		return sealed, false, nil
	}
	plain, err := b.old.Open(sealed)
	if err != nil {
		return nil, false, errors.New("value opens under neither the old nor the new label; is SUPERMETRY_SECRET_KEY the one used before the rename?")
	}
	return b.current.Seal(plain), true, nil
}

func (b labelBoxes) resealBase64(value string) (string, bool, error) {
	if value == "" {
		return value, false, nil
	}
	sealed, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return "", false, errors.New("stored value is not base64")
	}
	out, changed, err := b.reseal(sealed)
	if err != nil || !changed {
		return value, false, err
	}
	return base64.StdEncoding.EncodeToString(out), true, nil
}

func migrateTOTP(ctx context.Context, tx pgx.Tx, boxes labelBoxes) (int, error) {
	rows, err := tx.Query(ctx, `select id, totp_secret_enc from users where totp_secret_enc is not null`)
	if err != nil {
		return 0, err
	}
	type update struct {
		id     int64
		sealed []byte
	}
	var updates []update
	for rows.Next() {
		var id int64
		var sealed []byte
		if err := rows.Scan(&id, &sealed); err != nil {
			rows.Close()
			return 0, err
		}
		out, changed, err := boxes.reseal(sealed)
		if err != nil {
			rows.Close()
			return 0, fmt.Errorf("user %d: %w", id, err)
		}
		if changed {
			updates = append(updates, update{id, out})
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, u := range updates {
		if _, err := tx.Exec(ctx, `update users set totp_secret_enc=$2 where id=$1`, u.id, u.sealed); err != nil {
			return 0, err
		}
	}
	return len(updates), nil
}

func migrateChannels(ctx context.Context, tx pgx.Tx, boxes labelBoxes) (int, error) {
	rows, err := tx.Query(ctx, `select id, config from notification_channels`)
	if err != nil {
		return 0, err
	}
	type update struct {
		id     int64
		config []byte
	}
	var updates []update
	for rows.Next() {
		var id int64
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			rows.Close()
			return 0, err
		}
		var config map[string]json.RawMessage
		if err := json.Unmarshal(raw, &config); err != nil {
			rows.Close()
			return 0, fmt.Errorf("channel %d: %w", id, err)
		}
		changedAny := false
		for _, field := range []string{"url", "secret", "bot_token"} {
			value, ok := config[field]
			if !ok {
				continue
			}
			var text string
			if err := json.Unmarshal(value, &text); err != nil {
				rows.Close()
				return 0, fmt.Errorf("channel %d %s: %w", id, field, err)
			}
			next, changed, err := boxes.resealBase64(text)
			if err != nil {
				rows.Close()
				return 0, fmt.Errorf("channel %d %s: %w", id, field, err)
			}
			if changed {
				encoded, _ := json.Marshal(next)
				config[field] = encoded
				changedAny = true
			}
		}
		if changedAny {
			encoded, err := json.Marshal(config)
			if err != nil {
				rows.Close()
				return 0, err
			}
			updates = append(updates, update{id, encoded})
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, u := range updates {
		if _, err := tx.Exec(ctx, `update notification_channels set config=$2 where id=$1`, u.id, u.config); err != nil {
			return 0, err
		}
	}
	return len(updates), nil
}

func migrateReplayS3(ctx context.Context, tx pgx.Tx, boxes labelBoxes) (int, error) {
	var raw []byte
	if err := tx.QueryRow(ctx, `select settings from instance_settings`).Scan(&raw); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil
		}
		return 0, err
	}
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(raw, &settings); err != nil {
		return 0, err
	}
	// The storage document is stored under the "storage" key by the settings
	// layer; locate any s3.secret_key inside it without depending on its other
	// fields.
	changedAny := false
	for key, value := range settings {
		var doc map[string]json.RawMessage
		if json.Unmarshal(value, &doc) != nil {
			continue
		}
		var s3 map[string]json.RawMessage
		if json.Unmarshal(doc["s3"], &s3) != nil {
			continue
		}
		var secret string
		if json.Unmarshal(s3["secret_key"], &secret) != nil {
			continue
		}
		next, changed, err := boxes.resealBase64(secret)
		if err != nil {
			return 0, err
		}
		if !changed {
			continue
		}
		s3["secret_key"], _ = json.Marshal(next)
		doc["s3"], _ = json.Marshal(s3)
		settings[key], _ = json.Marshal(doc)
		changedAny = true
	}
	if !changedAny {
		return 0, nil
	}
	encoded, err := json.Marshal(settings)
	if err != nil {
		return 0, err
	}
	_, err = tx.Exec(ctx, `update instance_settings set settings=$1, updated_at=now()`, encoded)
	return 1, err
}

// migratePathPolicy maps the configured policy's old fingerprint to the current
// one. Rows written under a policy that is no longer configured cannot be
// mapped and are left as they are.
func migratePathPolicy(ctx context.Context, tx pgx.Tx, policy privacy.PathPolicy) (int64, error) {
	old := policy.FingerprintForLabel(legacyPathPolicyBase)
	current := policy.Fingerprint()
	if old == current {
		return 0, nil
	}
	var total int64
	for _, table := range []string{"events", "verified_page_sessions", "verified_page_day"} {
		tag, err := tx.Exec(ctx, `update `+table+` set path_policy_id=$2 where path_policy_id=$1`, old, current)
		if err != nil {
			return 0, fmt.Errorf("%s: %w", table, err)
		}
		total += tag.RowsAffected()
	}
	return total, nil
}
