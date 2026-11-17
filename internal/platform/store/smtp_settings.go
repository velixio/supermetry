package store

import (
	"context"
	"fmt"
)

// SMTPSettings keeps the instance's SMTP settings document in
// instance_settings.settings under the "smtp" key. It implements the store
// the mail package reads and writes.
type SMTPSettings struct{ Data *Store }

// LoadSMTP returns the stored document, or nil when none is saved.
func (s SMTPSettings) LoadSMTP(ctx context.Context) ([]byte, error) {
	var raw []byte
	err := s.Data.Pool.QueryRow(ctx, `select settings->'smtp' from instance_settings where id=true`).Scan(&raw)
	if err != nil {
		return nil, fmt.Errorf("load instance SMTP settings: %w", err)
	}
	if string(raw) == "null" {
		return nil, nil
	}
	return raw, nil
}

// SaveSMTP replaces the stored document; nil removes it.
func (s SMTPSettings) SaveSMTP(ctx context.Context, raw []byte) error {
	var err error
	if raw == nil {
		_, err = s.Data.Pool.Exec(ctx, `update instance_settings set settings=settings-'smtp', updated_at=now() where id=true`)
	} else {
		_, err = s.Data.Pool.Exec(ctx, `update instance_settings set settings=jsonb_set(settings,'{smtp}',$1::jsonb), updated_at=now() where id=true`, raw)
	}
	if err != nil {
		return fmt.Errorf("save instance SMTP settings: %w", err)
	}
	return nil
}
