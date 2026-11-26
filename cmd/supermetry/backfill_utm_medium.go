package main

import (
	"context"
	"errors"
	"fmt"

	"supermetry/internal/closer"
	"supermetry/internal/config"
	"supermetry/internal/db"
)

// backfillUTMMediumCommand is a one-off repair for closed data recorded before
// the raw utm_medium was stored (migration 0329). It is never run at startup.
func backfillUTMMediumCommand(ctx context.Context, args []string, getenv func(string) string) error {
	if len(args) > 0 {
		return errors.New("backfill-utm-medium takes no arguments")
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
	result, err := closer.BackfillUTMMedium(ctx, pool, closer.DefaultUTMMediumBatch, nil)
	if err != nil {
		return err
	}
	fmt.Printf("backfilled utm_medium: %d closed session row(s), %d custom row(s), %d custom event attribution row(s); rebuilt rollups for %d day(s)\n",
		result.Sessions, result.Custom, result.CustomEvent, result.RollupDays)
	return nil
}
