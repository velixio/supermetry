package main

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/config"
	"supermetry/internal/sites"
)

// validateTrackingStartup runs before any listener or collection writer starts.
// Query durable configuration directly: a failed/stale registry refresh must not
// silently classify an existing identified installation as a fresh installation.
func validateTrackingStartup(ctx context.Context, pool *pgxpool.Pool, mode config.TrackingMode, compatibilitySites []sites.Site) error {
	if err := mode.ValidateUpgrade(false); err != nil {
		return err
	}
	if mode.Explicit {
		return nil
	}
	var enabled bool
	if err := pool.QueryRow(ctx, `select exists(select 1 from sites where deleted_at is null and settings->'profiles_enabled'='true'::jsonb)`).Scan(&enabled); err != nil {
		return fmt.Errorf("inspect tracking upgrade configuration: %w", err)
	}
	for _, site := range compatibilitySites {
		enabled = enabled || site.ProfilesEnabled
	}
	return mode.ValidateUpgrade(enabled)
}
