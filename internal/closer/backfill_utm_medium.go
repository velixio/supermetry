package closer

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DefaultUTMMediumBatch bounds the rows updated per transaction.
const DefaultUTMMediumBatch = 10000

// UTMMediumBackfillResult counts what BackfillUTMMedium changed.
type UTMMediumBackfillResult struct {
	Sessions, Custom, CustomEvent int64
	RollupDays                    int
}

// BackfillUTMMedium copies the derived medium_id into utm_medium_id for closed
// rows recorded before the raw utm_medium was stored, when the row was UTM
// tagged (utm_source_id <> 0), then rebuilds rollup dimension 17 for the
// affected days. It is a one-off operator task: idempotent, resumable, and
// bounded to batch rows per transaction. Raw events and hot sessions are not
// touched.
func BackfillUTMMedium(ctx context.Context, pool *pgxpool.Pool, batch int, logger *slog.Logger) (UTMMediumBackfillResult, error) {
	if batch <= 0 {
		batch = DefaultUTMMediumBatch
	}
	if logger == nil {
		logger = slog.Default()
	}
	var result UTMMediumBackfillResult
	steps := []struct {
		name  string
		total *int64
		sql   string
		scan  string
	}{
		{"sessions_closed", &result.Sessions,
			`update sessions_closed t set utm_medium_id = t.medium_id
			 where t.ctid = any(array(select c.ctid from sessions_closed c
			   where c.site_id=$1 and c.day=$2 and c.utm_source_id<>0 and c.utm_medium_id=0 and c.medium_id<>0 limit $3))`,
			`select distinct site_id, day from sessions_closed where utm_source_id<>0 and utm_medium_id=0 and medium_id<>0 order by day, site_id`},
		{"custom_closed", &result.Custom,
			`update custom_closed t set utm_medium_id = t.medium_id
			 where t.ctid = any(array(select c.ctid from custom_closed c
			   where c.site_id=$1 and c.day=$2 and c.utm_source_id<>0 and c.utm_medium_id=0 and c.medium_id<>0 limit $3))`,
			`select distinct site_id, day from custom_closed where utm_source_id<>0 and utm_medium_id=0 and medium_id<>0 order by day, site_id`},
		{"custom_closed event", &result.CustomEvent,
			`update custom_closed t set event_raw_utm_medium_id = t.event_utm_medium_id
			 where t.ctid = any(array(select c.ctid from custom_closed c
			   where c.site_id=$1 and c.day=$2 and c.event_raw_utm_medium_id is null
			     and c.event_utm_source_id<>0 and c.event_utm_medium_id is not null limit $3))`,
			`select distinct site_id, day from custom_closed where event_raw_utm_medium_id is null and event_utm_source_id<>0 and event_utm_medium_id is not null order by day, site_id`},
	}
	type siteDay struct {
		site int32
		day  time.Time
	}
	for _, step := range steps {
		rows, err := pool.Query(ctx, step.scan)
		if err != nil {
			return result, fmt.Errorf("scan %s: %w", step.name, err)
		}
		var units []siteDay
		for rows.Next() {
			var u siteDay
			if err := rows.Scan(&u.site, &u.day); err != nil {
				rows.Close()
				return result, err
			}
			units = append(units, u)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return result, err
		}
		for i, u := range units {
			for {
				tag, err := pool.Exec(ctx, step.sql, u.site, u.day, batch)
				if err != nil {
					return result, fmt.Errorf("backfill %s site %d day %s: %w", step.name, u.site, u.day.Format(time.DateOnly), err)
				}
				*step.total += tag.RowsAffected()
				if tag.RowsAffected() < int64(batch) {
					break
				}
			}
			logger.Info("utm medium backfill", "table", step.name, "site_day", i+1, "of", len(units), "rows_total", *step.total)
		}
	}

	// Rebuild dim 17 for every day holding tagged sessions, not only the days
	// changed in this run, so a run interrupted before this step is repaired.
	rows, err := pool.Query(ctx, `
select day, array_agg(distinct site_id order by site_id)::int[]
from sessions_closed where utm_source_id<>0 and utm_medium_id<>0
group by day order by day`)
	if err != nil {
		return result, fmt.Errorf("load days for rollup rebuild: %w", err)
	}
	type dayBatch struct {
		day   time.Time
		sites []int32
	}
	var days []dayBatch
	for rows.Next() {
		var d dayBatch
		if err := rows.Scan(&d.day, &d.sites); err != nil {
			rows.Close()
			return result, err
		}
		days = append(days, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return result, err
	}
	for i, d := range days {
		if err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
			if err := recomputeDayDimensions(ctx, tx, d.day, d.sites, []int16{17}); err != nil {
				return err
			}
			return bumpCloseVersions(ctx, tx, d.day, d.sites)
		}); err != nil {
			return result, fmt.Errorf("rebuild utm medium rollup for %s: %w", d.day.Format(time.DateOnly), err)
		}
		result.RollupDays++
		logger.Info("utm medium rollup rebuilt", "day", d.day.Format(time.DateOnly), "days_done", i+1, "of", len(days))
	}
	return result, nil
}
