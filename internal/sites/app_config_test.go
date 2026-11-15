package sites_test

import (
	"testing"
	"time"

	"supermetry/internal/sites"
)

func TestKeyValid(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	current, grace, other := [32]byte{1}, [32]byte{2}, [32]byte{3}
	cfg := &sites.AppConfig{Keys: []sites.AppKey{
		{Hash: current},
		{Hash: grace, ExpiresAt: now.Add(time.Hour)},
	}}
	if !cfg.KeyValid(current, now) {
		t.Error("current key rejected")
	}
	if !cfg.KeyValid(grace, now) {
		t.Error("grace key rejected before expiry")
	}
	if cfg.KeyValid(grace, now.Add(time.Hour)) || cfg.KeyValid(grace, now.Add(2*time.Hour)) {
		t.Error("grace key accepted at or after expiry")
	}
	if cfg.KeyValid(other, now) {
		t.Error("unknown key accepted")
	}
	var none *sites.AppConfig
	if none.KeyValid(current, now) {
		t.Error("nil config accepted a key")
	}
}
