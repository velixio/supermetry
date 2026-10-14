package opencounters

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Cleanup removes counters idle for 365 days and daily rows older than 400.
// These tables are small and unpartitioned, so DELETE is used.
func Cleanup(ctx context.Context, pool *pgxpool.Pool, today time.Time) (int64, error) {
	day := today.UTC().Format(time.DateOnly)
	tag, err := pool.Exec(ctx, `delete from open_counters where last_seen_day < $1::date - 365`, day)
	if err != nil {
		return 0, fmt.Errorf("delete idle open counters: %w", err)
	}
	if _, err := pool.Exec(ctx, `delete from open_counter_views_daily where day < $1::date - 400`, day); err != nil {
		return 0, fmt.Errorf("delete old open counter views: %w", err)
	}
	return tag.RowsAffected(), nil
}
