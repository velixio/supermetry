// Package spanrollup builds the hourly span rollup (span_hourly) from the raw
// spans table. Rows are keyed by the site-local wall-clock hour. Every run
// replaces a whole hour, so it is idempotent; a late span marks its hour dirty
// and the job rebuilds it.
package spanrollup

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// lockClass namespaces the per-site advisory lock that serialises recomputes
// and dirty marks.
const lockClass = 0x535052 // "SPR"

// errorPredicate is the SQL error definition over the spans columns.
const errorPredicate = `(status not in ('', 'ok', 'cancelled') or coalesce(http_status, 0) >= 500 or (not is_segment and op = 'http.client' and coalesce(http_status, 0) >= 400))`

// rangeFilter restricts spans to one local hour: $1 site, $2/$3 the UTC bounds,
// $4/$5 an excluded stretch inside them. One plain range lets Postgres prune
// partitions and use the BRIN index.
const rangeFilter = `site_id = $1 and start_ts >= $2 and start_ts < $3 and (start_ts < $4 or start_ts >= $5)`

// rollupInsert wraps a per-bucket aggregate (columns is_segment, name, op,
// group_hash, release, environment, platform, bucket, c, e, s) into span_hourly
// rows with the histogram as two parallel arrays. $6 is the local hour. SQL
// never computes a bucket: it is stored on the span.
func rollupInsert(perBucket string) string {
	return `
insert into span_hourly (site_id, local_hour, is_segment, name, op, group_hash, release, environment, platform,
                         count, errors, sum_us, buckets, bucket_counts)
select $1, $6::timestamp, is_segment, name, op, group_hash, release, environment, platform,
       sum(c), sum(e), sum(s), array_agg(bucket order by bucket), array_agg(c order by bucket)
from (` + perBucket + `) b
group by is_segment, name, op, group_hash, release, environment, platform`
}

// recomputeStatements rebuild one local hour: segment (transaction) rows, child
// span group rows, and app start rows. Each reads the hour once with a hash
// aggregate, so nothing large is materialised (a hour of millions of spans
// stays within work_mem).
var recomputeStatements = []string{
	rollupInsert(`
    select true as is_segment, name, op, 0::bigint as group_hash, release, environment, platform, bucket,
           count(*) as c, count(*) filter (where ` + errorPredicate + `) as e, sum(duration_us) as s
    from spans where ` + rangeFilter + ` and is_segment
    group by name, op, release, environment, platform, bucket`),
	rollupInsert(`
    select false as is_segment, ''::text as name, op, group_hash, release, environment, platform, bucket,
           count(*) as c, count(*) filter (where ` + errorPredicate + `) as e, sum(duration_us) as s
    from spans where ` + rangeFilter + ` and not is_segment
    group by op, group_hash, release, environment, platform, bucket`),
	rollupInsert(`
    select false as is_segment, name, 'app.start.' || app_start_type as op, 0::bigint as group_hash,
           release, environment, platform, app_start_bucket as bucket,
           count(*) as c, 0::bigint as e, sum(app_start_ms::bigint * 1000) as s
    from spans where ` + rangeFilter + ` and is_segment and app_start_type is not null and app_start_bucket is not null
    group by name, app_start_type, release, environment, platform, app_start_bucket`),
}

// RecomputeStatements exposes the rollup reads for EXPLAIN in tests and tooling.
func RecomputeStatements() []string { return append([]string(nil), recomputeStatements...) }

// RecomputeArgs returns the arguments of RecomputeSQL for a local hour, and
// false when the hour does not exist (spring-forward gap). One range covers the
// hour's intervals ($2, $3); the stretch between two intervals (never present
// in practice, the repeated DST hour is contiguous) is excluded by ($4, $5),
// an empty range otherwise. One plain range lets Postgres prune partitions and
// use the BRIN index.
func RecomputeArgs(siteID int32, hour time.Time, loc *time.Location) ([]any, bool) {
	hour = Wall(hour)
	b := LocalHourBounds(hour, loc)
	if len(b) == 0 {
		return nil, false
	}
	lo, hi := b[0][0], b[len(b)-1][1]
	gapLo, gapHi := lo, lo
	if len(b) == 2 && b[0][1].Before(b[1][0]) {
		gapLo, gapHi = b[0][1], b[1][0]
	}
	return []any{siteID, lo, hi, gapLo, gapHi, hour}, true
}

// RecomputeHour replaces the site's span_hourly rows for one site-local hour
// in one transaction and records the hour as done.
func RecomputeHour(ctx context.Context, pool *pgxpool.Pool, siteID int32, loc *time.Location, hour time.Time) error {
	hour = Wall(hour)
	args, exists := RecomputeArgs(siteID, hour, loc)
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock($1, $2)`, int32(lockClass), siteID); err != nil {
			return fmt.Errorf("lock span rollups: %w", err)
		}
		if _, err := tx.Exec(ctx, `delete from span_hourly where site_id = $1 and local_hour = $2`, siteID, hour); err != nil {
			return fmt.Errorf("clear span hour: %w", err)
		}
		if exists {
			for _, stmt := range recomputeStatements {
				if _, err := tx.Exec(ctx, stmt, args...); err != nil {
					return fmt.Errorf("rebuild span hour: %w", err)
				}
			}
		}
		if _, err := tx.Exec(ctx, `
insert into span_hour_state (site_id, local_hour, status, updated_at) values ($1, $2, 'done', now())
on conflict (site_id, local_hour) do update set status = 'done', updated_at = now()`, siteID, hour); err != nil {
			return fmt.Errorf("record span hour: %w", err)
		}
		return nil
	})
}

// MarkDirty marks already-rolled hours for a rebuild. It takes the recompute
// lock, so a mark can never be overwritten by a rebuild that started before
// the late span was committed. Hours with no state row are left alone: the job
// rolls them anyway.
func MarkDirty(ctx context.Context, pool *pgxpool.Pool, siteID int32, hours []time.Time) error {
	if len(hours) == 0 {
		return nil
	}
	walls := make([]time.Time, len(hours))
	for i, h := range hours {
		walls[i] = Wall(h)
	}
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock($1, $2)`, int32(lockClass), siteID); err != nil {
			return fmt.Errorf("lock span rollups: %w", err)
		}
		_, err := tx.Exec(ctx, `
update span_hour_state set status = 'dirty', updated_at = now()
where site_id = $1 and local_hour = any($2::timestamp[]) and status = 'done'`, siteID, walls)
		if err != nil {
			return fmt.Errorf("mark span hours dirty: %w", err)
		}
		return nil
	})
}

// OpenHours returns the local hours in [from, to) (hour steps) that are not
// rolled up (no state or dirty): queries read those from raw spans and the
// others from span_hourly.
func OpenHours(ctx context.Context, pool *pgxpool.Pool, siteID int32, from, to time.Time) ([]time.Time, error) {
	from, to = Wall(from), Wall(to)
	rows, err := pool.Query(ctx, `
select local_hour from span_hour_state
where site_id = $1 and status = 'done' and local_hour >= $2 and local_hour < $3`, siteID, from, to)
	if err != nil {
		return nil, fmt.Errorf("read span hour state: %w", err)
	}
	defer rows.Close()
	done := map[time.Time]struct{}{}
	for rows.Next() {
		var h time.Time
		if err := rows.Scan(&h); err != nil {
			return nil, err
		}
		done[Wall(h)] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var open []time.Time
	for h := from; h.Before(to); h = h.Add(time.Hour) {
		if _, ok := done[h]; !ok {
			open = append(open, h)
		}
	}
	return open, nil
}
