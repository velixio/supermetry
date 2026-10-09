// Package closer compacts completed site-local days into the closed tier and
// exact additive rollups.
package closer

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/db"
	"supermetry/internal/dict"
	"supermetry/internal/session"
	"supermetry/internal/sites"
)

const closeDelay = 45 * time.Minute

// Stats describes the rows compacted by one CloseDay call.
type Stats struct {
	Sessions int
	Custom   int
	Duration time.Duration
}

// Due returns each configured site's most recently ended local day once the
// local clock has passed midnight plus the session-idle grace period.
func Due(now time.Time, reg sites.Registry, closed func(site int32, day time.Time) bool) map[time.Time][]int32 {
	due := make(map[time.Time][]int32)
	for _, site := range reg.All() {
		location := site.TZ
		if location == nil {
			location = time.UTC
		}
		localNow := now.In(location)
		midnight := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, location)
		if localNow.Before(midnight.Add(closeDelay)) {
			continue
		}
		previous := midnight.AddDate(0, 0, -1)
		day := time.Date(previous.Year(), previous.Month(), previous.Day(), 0, 0, 0, 0, time.UTC)
		if closed != nil && closed(site.ID, day) {
			continue
		}
		due[day] = append(due[day], site.ID)
	}
	for day := range due {
		sort.Slice(due[day], func(i, j int) bool { return due[day][i] < due[day][j] })
	}
	return due
}

// CloseDay idempotently replaces the closed rows and rollups for siteIDs on
// day, then records a new completed close version for every site.
func CloseDay(ctx context.Context, pool *pgxpool.Pool, siteIDs []int32, day time.Time) (Stats, error) {
	started := time.Now()
	siteIDs = uniqueSites(siteIDs)
	if len(siteIDs) == 0 {
		return Stats{}, nil
	}
	day = date(day)
	windowStart, windowEnd := dayWindow(day)
	if err := db.EnsurePartitions(ctx, pool, day, day.AddDate(0, 0, 1)); err != nil {
		return Stats{}, fmt.Errorf("closer: ensure partitions: %w", err)
	}

	var stats Stats
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock($1)`, rollupRefreshLockID); err != nil {
			return fmt.Errorf("lock rollup refresh: %w", err)
		}
		for _, setting := range []string{
			`set local enable_nestloop = off`,
			`set local enable_mergejoin = off`,
			`set local work_mem = '128MB'`,
		} {
			if _, err := tx.Exec(ctx, setting); err != nil {
				return err
			}
		}
		if err := refreshVerifiedPages(ctx, tx, day, windowStart, windowEnd, siteIDs); err != nil {
			return err
		}
		for _, table := range []string{
			"rollup_day", "rollup_day_dim", "visitor_day", "cube_day", "custom_closed", "sessions_closed",
		} {
			if _, err := tx.Exec(ctx, `delete from `+table+` where day = $1 and site_id = any($2)`, day, siteIDs); err != nil {
				return fmt.Errorf("delete %s: %w", table, err)
			}
		}
		for _, dimension := range []struct {
			column string
			kind   dict.Kind
		}{
			{column: "browser_ver_id", kind: dict.BrowserVersion},
			{column: "os_ver_id", kind: dict.OSVersion},
			{column: "language_id", kind: dict.Language},
			{column: "hostname_id", kind: dict.Hostname},
		} {
			if _, err := tx.Exec(ctx, foldDimensionSQL(dimension.column), day, windowStart, windowEnd, siteIDs, dimension.kind); err != nil {
				return fmt.Errorf("fold %s: %w", dimension.column, err)
			}
		}

		tag, err := tx.Exec(ctx, closeSessionsSQL, day, windowStart, windowEnd, siteIDs)
		if err != nil {
			return fmt.Errorf("build sessions_closed: %w", err)
		}
		stats.Sessions = int(tag.RowsAffected())
		tag, err = tx.Exec(ctx, closeCustomSQL, day, windowStart, windowEnd, siteIDs)
		if err != nil {
			return fmt.Errorf("build custom_closed: %w", err)
		}
		stats.Custom = int(tag.RowsAffected())

		derived := []struct {
			name string
			sql  string
		}{
			{name: "cube_day", sql: cubeDaySQL},
			{name: "visitor_day", sql: visitorDaySQL},
			{name: "rollup_day", sql: rollupDaySQL},
			{name: "session rollups", sql: rollupSessionDimensionsSQL},
			{name: "path rollups", sql: rollupPathSQL},
		}
		for _, table := range derived {
			if _, err := tx.Exec(ctx, table.sql, day, siteIDs); err != nil {
				return fmt.Errorf("build %s: %w", table.name, err)
			}
		}
		if err := refreshDayBlocks(ctx, tx, siteIDs, day); err != nil {
			return fmt.Errorf("refresh day blocks: %w", err)
		}

		elapsedMS := time.Since(started).Milliseconds()
		if _, err := tx.Exec(ctx, markClosedSQL, day, siteIDs, elapsedMS); err != nil {
			return fmt.Errorf("mark day closed: %w", err)
		}
		if err := refreshMonth(ctx, tx, siteIDs, day); err != nil {
			return fmt.Errorf("refresh month: %w", err)
		}
		if _, err := tx.Exec(ctx, `delete from dirty_day where day = $1 and site_id = any($2)`, day, siteIDs); err != nil {
			return fmt.Errorf("clear dirty day: %w", err)
		}
		return nil
	})
	stats.Duration = time.Since(started)
	if err != nil {
		return Stats{}, err
	}
	return stats, nil
}

// foldDimensionSQL is called only with the fixed column names above.
func foldDimensionSQL(column string) string {
	return `
with ins as (
  insert into dict (kind, value) values ($5, 'Other')
  on conflict (kind, value) do nothing
  returning id
), other as (
  select id from ins
  union all
  select id from dict where kind=$5 and value='Other'
  limit 1
), ranked as (
  select site_id, value_id,
         row_number() over (partition by site_id order by sessions desc, value_id) as rank
  from (
    select site_id, ` + column + ` as value_id, count(*) as sessions
    from sessions
    where local_day=$1 and start >= $2 and start < $3 and site_id=any($4)
      and ` + column + ` <> 0 and ` + column + ` <> (select id from other)
    group by site_id, ` + column + `
  ) counts
)
update sessions s set ` + column + `=(select id from other)
from ranked r
where s.local_day=$1 and s.start >= $2 and s.start < $3
  and s.site_id=r.site_id and s.` + column + `=r.value_id and r.rank > 200`
}

const markClosedSQL = `
insert into day_close (site_id, day, status, version, closed_at, rows, duration_ms)
select wanted.site_id, $1, 'done', 1, clock_timestamp(),
       (select count(*) from sessions_closed s where s.site_id = wanted.site_id and s.day = $1)
       + (select count(*) from custom_closed c where c.site_id = wanted.site_id and c.day = $1),
       $3
from unnest($2::int[]) as wanted(site_id)
on conflict (site_id, day) do update set
  status = 'done',
  version = day_close.version + 1,
  closed_at = excluded.closed_at,
  rows = excluded.rows,
  duration_ms = excluded.duration_ms`

func uniqueSites(siteIDs []int32) []int32 {
	seen := make(map[int32]struct{}, len(siteIDs))
	result := make([]int32, 0, len(siteIDs))
	for _, siteID := range siteIDs {
		if _, ok := seen[siteID]; ok {
			continue
		}
		seen[siteID] = struct{}{}
		result = append(result, siteID)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func date(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// dayWindow bounds every UTC instant that can belong to a session which
// starts on day in an IANA time zone from UTC-12 through UTC+14. The idle
// timeout admits a session that crosses the latest possible local midnight.
func dayWindow(day time.Time) (time.Time, time.Time) {
	return day.Add(-14 * time.Hour), day.Add(36*time.Hour + session.IdleTimeout)
}
