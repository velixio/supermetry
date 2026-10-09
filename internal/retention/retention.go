// Package retention expires hot-tier partitions and erases removed sites.
package retention

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"panel/internal/db"
)

var eraseTables = []string{
	"heatmap_cells",
	"events",
	"sessions",
	"sessions_closed",
	"verified_page_sessions",
	"verified_page_day",
	"custom_closed",
	"cube_day",
	"visitor_day",
	"rollup_day",
	"rollup_day_dim",
	"rollup_month_dim",
	"rollup_month_done",
	"rollup_block_dim",
	"rollup_block_done",
	"day_close",
	"dirty_day",
}

// Run drops expired hot-tier leaf partitions. A retention class keeps its
// configured number of days plus one day of closing slack.
func Run(ctx context.Context, pool *pgxpool.Pool, now time.Time) ([]string, error) {
	var expired []db.Partition
	for _, table := range []string{"events", "sessions"} {
		partitions, err := db.ListPartitions(ctx, pool, table)
		if err != nil {
			return nil, fmt.Errorf("list %s retention partitions: %w", table, err)
		}
		for _, partition := range partitions {
			if partition.Class == db.RetForever {
				continue
			}
			cutoff := now.AddDate(0, 0, -int(partition.Class)-1)
			if partition.To.Before(cutoff) {
				expired = append(expired, partition)
			}
		}
	}
	heatmapPartitions, err := db.ListMonthlyPartitions(ctx, pool, "heatmap_cells")
	if err != nil {
		return nil, fmt.Errorf("list heatmap retention partitions: %w", err)
	}
	utcNow := now.UTC()
	heatmapCutoff := time.Date(utcNow.Year(), utcNow.Month(), utcNow.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -90)
	for _, partition := range heatmapPartitions {
		if !partition.To.After(heatmapCutoff) {
			expired = append(expired, partition)
		}
	}
	replayPartitions, err := db.ListMonthlyPartitions(ctx, pool, "replay_sessions")
	if err != nil {
		return nil, fmt.Errorf("list replay retention partitions: %w", err)
	}
	currentMonth := time.Date(utcNow.Year(), utcNow.Month(), 1, 0, 0, 0, 0, time.UTC)
	for _, partition := range replayPartitions {
		if partition.To.After(currentMonth) {
			continue
		}
		var empty bool
		query := fmt.Sprintf("select not exists(select 1 from %s limit 1)", pgx.Identifier{partition.Name}.Sanitize())
		if err := pool.QueryRow(ctx, query).Scan(&empty); err != nil {
			return nil, fmt.Errorf("check replay retention partition %s: %w", partition.Name, err)
		}
		if empty {
			expired = append(expired, partition)
		}
	}
	sort.Slice(expired, func(i, j int) bool { return expired[i].Name < expired[j].Name })

	dropped := make([]string, 0, len(expired))
	for _, partition := range expired {
		parent := partitionParent(partition.Name)
		if _, err := pool.Exec(ctx, fmt.Sprintf(
			"ALTER TABLE %s DETACH PARTITION %s CONCURRENTLY",
			pgx.Identifier{parent}.Sanitize(),
			pgx.Identifier{partition.Name}.Sanitize(),
		)); err != nil {
			return dropped, fmt.Errorf("detach expired partition %s: %w", partition.Name, err)
		}
		if _, err := pool.Exec(ctx, "DROP TABLE "+pgx.Identifier{partition.Name}.Sanitize()); err != nil {
			return dropped, fmt.Errorf("drop expired partition %s: %w", partition.Name, err)
		}
		dropped = append(dropped, partition.Name)
	}
	return dropped, nil
}

// Erase removes a site's analytics rows in bounded batches across every tier.
func Erase(ctx context.Context, pool *pgxpool.Pool, siteID int32, batch int, pause time.Duration) error {
	if batch <= 0 {
		return fmt.Errorf("erase batch must be positive")
	}
	for _, table := range eraseTables {
		identifier := pgx.Identifier{table}.Sanitize()
		query := fmt.Sprintf(`DELETE FROM %s
WHERE (tableoid, ctid) IN (
  SELECT tableoid, ctid FROM %s WHERE site_id = $1 LIMIT $2
)`, identifier, identifier)
		for {
			tag, err := pool.Exec(ctx, query, siteID, batch)
			if err != nil {
				return fmt.Errorf("erase site %d from %s: %w", siteID, table, err)
			}
			if tag.RowsAffected() < int64(batch) {
				break
			}
			if err := wait(ctx, pause); err != nil {
				return err
			}
		}
	}
	return nil
}

func partitionParent(name string) string {
	return name[:strings.LastIndexByte(name, '_')]
}

func wait(ctx context.Context, pause time.Duration) error {
	if pause <= 0 {
		return nil
	}
	timer := time.NewTimer(pause)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
