package main

import (
	"context"
	"errors"
	"fmt"

	"panel/internal/closer"
	"panel/internal/config"
	"panel/internal/db"
)

// recomputeRollupsCommand rebuilds every closed day's rollups from the
// closed-session tier. Run it once after upgrading to a release that adds a
// rollup counter (for example engaged_n for time on page). It is idempotent.
func recomputeRollupsCommand(ctx context.Context, args []string, getenv func(string) string) error {
	if len(args) > 0 {
		return errors.New("recompute-rollups takes no arguments")
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
	days, err := closer.RecomputeRollups(ctx, pool)
	if err != nil {
		return err
	}
	fmt.Printf("recomputed rollups for %d closed day(s)\n", days)
	return nil
}
