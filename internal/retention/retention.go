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

	"supermetry/internal/db"
)

var eraseTables = []string{
	"site_bot_rule_daily",
	"site_drop_daily",
	"heatmap_cells",
	"events",
	"sessions",
	"profiles",
	"profile_tombstones",
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
	if err := deleteExpiredDropStats(ctx, pool, now); err != nil {
		return nil, err
	}
	if err := deleteExpiredPixelViews(ctx, pool, now); err != nil {
		return nil, err
	}
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
	for {
		tag, err := pool.Exec(ctx, `delete from pixel_views_daily
			where ctid in (
				select v.ctid from pixel_views_daily v join pixels p on p.id=v.pixel_id
				where p.site_id=$1 limit $2
			)`, siteID, batch)
		if err != nil {
			return fmt.Errorf("erase site %d from pixel_views_daily: %w", siteID, err)
		}
		if tag.RowsAffected() < int64(batch) {
			break
		}
		if err := wait(ctx, pause); err != nil {
			return err
		}
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

// deleteExpiredPixelViews applies each pixel project's retention class to its
// small, unpartitioned daily table, in the project's time zone.
func deleteExpiredPixelViews(ctx context.Context, pool *pgxpool.Pool, now time.Time) error {
	_, err := pool.Exec(ctx, `
		delete from pixel_views_daily v using pixels p, sites s
		where v.pixel_id = p.id and p.site_id = s.id
		  and v.day < (($1::timestamptz at time zone s.timezone)::date - s.retention_class - 1)`, now)
	if err != nil {
		return fmt.Errorf("delete expired pixel views: %w", err)
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

// deleteExpiredDropStats removes drop-count rows older than 90 days of each
// site's own local day. The local day is computed here, not in SQL: a database
// built without every tz alias (for example Asia/Calcutta) rejects zone names
// that Go accepts, and one such site must not stop retention for all of them.
// An unknown zone name falls back to UTC.
func deleteExpiredDropStats(ctx context.Context, pool *pgxpool.Pool, now time.Time) error {
	rows, err := pool.Query(ctx, "select id, timezone from sites")
	if err != nil {
		return fmt.Errorf("list sites for drop-stat retention: %w", err)
	}
	type siteZone struct {
		id       int32
		timezone string
	}
	var sites []siteZone
	for rows.Next() {
		var site siteZone
		if err := rows.Scan(&site.id, &site.timezone); err != nil {
			rows.Close()
			return fmt.Errorf("scan site for drop-stat retention: %w", err)
		}
		sites = append(sites, site)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("list sites for drop-stat retention: %w", err)
	}
	for _, site := range sites {
		location, err := time.LoadLocation(site.timezone)
		if err != nil {
			location = time.UTC
		}
		local := now.In(location)
		cutoff := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -90)
		for _, table := range []string{"site_bot_rule_daily", "site_drop_daily"} {
			if _, err := pool.Exec(ctx, "delete from "+table+" where site_id=$1 and day < $2", site.id, cutoff); err != nil {
				return fmt.Errorf("delete expired %s rows: %w", table, err)
			}
		}
	}
	return nil
}
