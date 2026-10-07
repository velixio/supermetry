package store

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"

	"panel/bench/proto/internal/core"
)

// dayWindow returns a UTC range that contains local date d in every time
// zone (UTC-12 .. UTC+14), for partition pruning; local_date does the exact cut.
func dayWindow(d core.Day) (time.Time, time.Time) {
	t := d.Time()
	return t.Add(-14 * time.Hour), t.Add(36 * time.Hour)
}

// closeDictSQL adds every string of the day that the closed tables reference.
// Only missing values are inserted, so identity values are not burnt daily.
const closeDictSQL = `
INSERT INTO dict (value)
SELECT x.v FROM (
  SELECT unnest(CASE WHEN name = 'pageview' THEN ARRAY[path] ELSE ARRAY[name, path] END) AS v
    FROM events WHERE local_date = $1 AND ts >= $2 AND ts < $3 AND site_id = ANY($4)
  UNION
  SELECT unnest(ARRAY[source, country, browser, os, device,
                      coalesce(utm_source, ''), coalesce(utm_medium, ''), coalesce(utm_campaign, '')])
    FROM sessions WHERE local_date = $1 AND start_ts >= $2 AND start_ts < $3 AND site_id = ANY($4)
) x
WHERE NOT EXISTS (SELECT 1 FROM dict t WHERE t.value = x.v)
ON CONFLICT (value) DO NOTHING`

// closeSessionsSQL builds sessions_closed for the day from the hot tables:
// one pass over the day's events gives each session's ordered seq with the
// first-in-session / first-in-visitor-day bits, one pass over the sessions
// gives vd_n, the flags and the visitor-day's dims.
const closeSessionsSQL = `
INSERT INTO sessions_closed
WITH ev AS (
  SELECT e.session_id, e.ts, e.name = 'pageview' AS pv, dp.id AS pid,
         row_number() OVER (PARTITION BY e.session_id, e.name, e.path ORDER BY e.ts) AS rs,
         row_number() OVER (PARTITION BY e.site_id, e.visitor_id, e.name, e.path ORDER BY e.ts) AS rv
  FROM events e
  JOIN dict dp ON dp.value = CASE WHEN e.name = 'pageview' THEN e.path ELSE e.name END
  WHERE e.local_date = $1 AND e.ts >= $2 AND e.ts < $3 AND e.site_id = ANY($4)
), sq AS (
  SELECT session_id,
         array_agg(CASE WHEN pv THEN (pid << 2) | CASE WHEN rs = 1 THEN 2 ELSE 0 END | CASE WHEN rv = 1 THEN 1 ELSE 0 END
                        ELSE -pid END ORDER BY ts) AS seq
  FROM ev GROUP BY session_id
), s AS (
  SELECT s.site_id, s.session_id, s.visitor_id, s.start_ts, s.source, s.entry_path, s.exit_path,
         s.utm_source, s.utm_medium, s.utm_campaign, s.pageviews, s.custom_events, s.duration_ms,
         first_value(s.country) OVER vdo AS country, first_value(s.browser) OVER vdo AS browser,
         first_value(s.os) OVER vdo AS os, first_value(s.device) OVER vdo AS device,
         count(*) OVER vd AS vd_n,
         (CASE WHEN row_number() OVER vdo = 1 THEN 1 ELSE 0 END
        | CASE WHEN row_number() OVER (PARTITION BY s.site_id, s.visitor_id, s.source ORDER BY s.start_ts, s.session_id) = 1 THEN 2 ELSE 0 END
        | CASE WHEN row_number() OVER (PARTITION BY s.site_id, s.visitor_id, s.entry_path ORDER BY s.start_ts, s.session_id) = 1 THEN 4 ELSE 0 END
        | CASE WHEN row_number() OVER (PARTITION BY s.site_id, s.visitor_id, s.exit_path ORDER BY s.start_ts, s.session_id) = 1 THEN 8 ELSE 0 END) AS flags
  FROM sessions s
  WHERE s.local_date = $1 AND s.start_ts >= $2 AND s.start_ts < $3 AND s.site_id = ANY($4)
  WINDOW vd AS (PARTITION BY s.site_id, s.visitor_id),
         vdo AS (PARTITION BY s.site_id, s.visitor_id ORDER BY s.start_ts, s.session_id)
)
SELECT s.visitor_id, s.session_id, s.site_id, $1, ds.id, dc.id, db.id, dos.id, ddv.id, den.id, dex.id, du1.id, du2.id, du3.id,
       s.pageviews, s.custom_events, s.duration_ms, s.vd_n, s.flags, sq.seq
FROM s JOIN sq USING (session_id)
JOIN dict ds ON ds.value = s.source
JOIN dict dc ON dc.value = s.country
JOIN dict db ON db.value = s.browser
JOIN dict dos ON dos.value = s.os
JOIN dict ddv ON ddv.value = s.device
JOIN dict den ON den.value = s.entry_path
JOIN dict dex ON dex.value = s.exit_path
JOIN dict du1 ON du1.value = coalesce(s.utm_source, '')
JOIN dict du2 ON du2.value = coalesce(s.utm_medium, '')
JOIN dict du3 ON du3.value = coalesce(s.utm_campaign, '')
ORDER BY s.site_id, dc.id, s.visitor_id, s.start_ts`

// The rollups are derived from sessions_closed: thanks to the first-flags no
// distinct-visitor aggregation is needed (visitors = rows with the flag).
const rollupTotalsSQL = `
INSERT INTO rollup_day (site_id, day, visitors, visits, pageviews, bounces, duration_ms)
SELECT site_id, day, count(*) FILTER (WHERE flags & 1 <> 0), count(*), sum(pageviews),
       count(*) FILTER (WHERE pageviews = 1 AND custom = 0), sum(duration_ms)
FROM sessions_closed WHERE day = $1 AND site_id = ANY($2)
GROUP BY site_id, day`

// Session-level dimensions: unpivot each session into one row per dimension.
// Country/browser/os/device are visitor-day attributes (flag 1).
const rollupSessionDimsSQL = `
INSERT INTO rollup_day_dim (site_id, dim, day, value, visitors, visits, pageviews, bounces, duration_ms)
SELECT r.site_id, r.dim, $1, d.value, r.v, r.n, r.pv, r.b, r.dur
FROM (SELECT s.site_id, x.dim, x.id, count(*) FILTER (WHERE s.flags & x.f <> 0) AS v, count(*) AS n,
             sum(s.pageviews) AS pv, count(*) FILTER (WHERE s.pageviews = 1 AND s.custom = 0) AS b,
             sum(s.duration_ms) AS dur
      FROM sessions_closed s
      CROSS JOIN LATERAL (VALUES (2::int2, s.source, 2), (3::int2, s.country, 1), (4::int2, s.browser, 1),
                                 (5::int2, s.os, 1), (6::int2, s.device, 1), (7::int2, s.entry, 4),
                                 (8::int2, s.exit, 8)) AS x (dim, id, f)
      WHERE s.day = $1 AND s.site_id = ANY($2) AND NOT (x.dim IN (7, 8) AND s.pageviews = 0)
      GROUP BY 1, 2, 3) r
JOIN dict d ON d.id = r.id`

// Pages: one seq element per event; visits/bounces/duration are those of
// the sessions viewing the page (bit 2 marks one element per session).
const rollupPathSQL = `
INSERT INTO rollup_day_dim (site_id, dim, day, value, visitors, visits, pageviews, bounces, duration_ms)
SELECT r.site_id, 1, $1, d.value, r.v, r.n, r.pv, r.b, r.dur
FROM (SELECT site_id, x >> 2 AS id, count(*) FILTER (WHERE x & 1 <> 0) AS v, count(*) FILTER (WHERE x & 2 <> 0) AS n,
             count(*) AS pv, count(*) FILTER (WHERE x & 2 <> 0 AND bounce) AS b,
             coalesce(sum(dur) FILTER (WHERE x & 2 <> 0), 0) AS dur
      FROM (SELECT site_id, unnest(seq) AS x, pageviews = 1 AND custom = 0 AS bounce, duration_ms AS dur
            FROM sessions_closed WHERE day = $1 AND site_id = ANY($2)) u
      WHERE x > 0
      GROUP BY 1, 2) r
JOIN dict d ON d.id = r.id`

// customSQL copies the day's custom events with their session's dims.
const customSQL = `
INSERT INTO custom_closed
SELECT c.visitor_id, c.site_id, c.day, dn.id, dp.id, c.source, c.country, c.browser, c.os, c.device, c.entry, c.exit,
       c.utm_source, c.utm_medium, c.utm_campaign, e.props
FROM events e
JOIN sessions_closed c ON c.session_id = e.session_id AND c.day = $1 AND c.site_id = ANY($4)
JOIN dict dn ON dn.value = e.name
JOIN dict dp ON dp.value = e.path
WHERE e.local_date = $1 AND e.ts >= $2 AND e.ts < $3 AND e.site_id = ANY($4) AND e.name <> 'pageview'
ORDER BY c.site_id, e.ts`

const cubeSQL = `
INSERT INTO cube_day
SELECT site_id, day, source, country, browser, os, device,
       count(*) FILTER (WHERE flags & 1 <> 0), count(*) FILTER (WHERE flags & 2 <> 0), count(*), sum(pageviews),
       count(*) FILTER (WHERE pageviews = 1 AND custom = 0), sum(duration_ms)
FROM sessions_closed WHERE day = $1 AND site_id = ANY($2)
GROUP BY 1, 2, 3, 4, 5, 6, 7
ORDER BY 1, 4, 3`

// EnsureClosedMonth creates the monthly partitions of the closed-day tables.
func (s *Store) EnsureClosedMonth(ctx context.Context, d core.Day) error {
	t := d.Time()
	from := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	f, to, suffix := from.Format("2006-01-02"), from.AddDate(0, 1, 0).Format("2006-01-02"), from.Format("2006_01")
	for _, t := range []string{"sessions_closed", "cube_day", "custom_closed"} {
		if err := s.ensurePartition(ctx, t, t+"_p"+suffix, f, to, ""); err != nil {
			return err
		}
	}
	return nil
}

// RecomputeDay closes local date d for the given sites in one transaction
// (idempotent): it rebuilds sessions_closed from the hot tables, then derives
// rollup_day, rollup_day_dim and cube_day from it.
func (s *Store) RecomputeDay(ctx context.Context, d core.Day, sites []int32) error {
	if len(sites) == 0 {
		return nil
	}
	if err := s.EnsureClosedMonth(ctx, d); err != nil {
		return err
	}
	lo, hi := dayWindow(d)
	// The dictionary insert commits on its own: it is idempotent, and inside
	// the long day transaction concurrent recomputes would wait on (or
	// deadlock over) each other's new values.
	if _, err := s.Pool.Exec(ctx, closeDictSQL, d.Time(), lo, hi, sites); err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		// Set-based batch joins only. A nested loop (chosen on stale stats)
		// is quadratic here, and so is a merge join on site_id alone with
		// session_id as a join filter (seen on a month-boundary day: > 4 min
		// instead of 7 s).
		for _, q := range []string{`SET LOCAL enable_nestloop = off`, `SET LOCAL enable_mergejoin = off`,
			`SET LOCAL work_mem = '128MB'`} {
			if _, err := tx.Exec(ctx, q); err != nil {
				return err
			}
		}
		for _, t := range []string{"rollup_day", "rollup_day_dim", "sessions_closed", "cube_day", "custom_closed"} {
			if _, err := tx.Exec(ctx, `DELETE FROM `+t+` WHERE day = $1 AND site_id = ANY($2)`, d.Time(), sites); err != nil {
				return err
			}
		}
		for _, q := range []string{closeSessionsSQL, customSQL} {
			if _, err := tx.Exec(ctx, q, d.Time(), lo, hi, sites); err != nil {
				return err
			}
		}
		for _, q := range []string{rollupTotalsSQL, rollupSessionDimsSQL, rollupPathSQL, cubeSQL} {
			if _, err := tx.Exec(ctx, q, d.Time(), sites); err != nil {
				return err
			}
		}
		buckets := make([]core.SaltKey, len(sites))
		for i, site := range sites {
			buckets[i] = core.SaltKey{Site: site, Day: d}
		}
		return refreshDayBlocksTx(ctx, tx, buckets)
	})
}

var hotSessionPart = regexp.MustCompile(`^sessions_(d(\d{8})|p(\d{4})_(\d{2}))$`)

// DropCompactedSessions drops hot sessions partitions whose rows all belong
// to local days on or before through (retention by DROP PARTITION). A UTC day
// U holds local dates U-1 .. U+1, so its partition can go once U+1 <= through.
// The caller guarantees those days are closed for every site.
func (s *Store) DropCompactedSessions(ctx context.Context, through core.Day) ([]string, error) {
	rows, err := s.Pool.Query(ctx, `SELECT c.relname FROM pg_inherits i JOIN pg_class c ON c.oid = i.inhrelid
		WHERE i.inhparent = 'sessions'::regclass`)
	if err != nil {
		return nil, err
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	var dropped []string
	for _, n := range names {
		m := hotSessionPart.FindStringSubmatch(n)
		if m == nil {
			continue
		}
		var last time.Time // last UTC day covered by the partition
		if m[2] != "" {
			last, err = time.Parse("20060102", m[2])
		} else {
			last, err = time.Parse("2006_01", m[3]+"_"+m[4])
			last = last.AddDate(0, 1, -1)
		}
		if err != nil {
			return dropped, err
		}
		if core.Day(last.Unix()/86400)+1 > through {
			continue
		}
		if _, err := s.Pool.Exec(ctx, `DROP TABLE `+pgx.Identifier{n}.Sanitize()); err != nil {
			return dropped, fmt.Errorf("drop %s: %w", n, err)
		}
		s.mu.Lock()
		delete(s.parts, n)
		s.mu.Unlock()
		dropped = append(dropped, n)
	}
	return dropped, nil
}

// ReplaceRollups atomically replaces the rollups of the given (site, day)
// buckets with rows (used by rollup-stream, which builds no closed tables).
func (s *Store) ReplaceRollups(ctx context.Context, buckets []core.SaltKey, rows []core.RollupRow) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		sites := make([]int32, len(buckets))
		days := make([]time.Time, len(buckets))
		for i, b := range buckets {
			sites[i], days[i] = b.Site, b.Day.Time()
		}
		for _, t := range []string{"rollup_day", "rollup_day_dim"} {
			if _, err := tx.Exec(ctx, `DELETE FROM `+t+` r USING unnest($1::int[], $2::date[]) AS b (site_id, day)
			                            WHERE r.site_id = b.site_id AND r.day = b.day`, sites, days); err != nil {
				return err
			}
		}
		var tot, dim [][]any
		for _, r := range rows {
			if r.Dim == 0 {
				tot = append(tot, []any{r.Site, r.Day.Time(), int32(r.Visitors), int32(r.Visits), int32(r.Pageviews), int32(r.Bounces), r.DurationMs})
			} else {
				dim = append(dim, []any{r.Site, int16(r.Dim), r.Day.Time(), r.Value, int32(r.Visitors), int32(r.Visits), int32(r.Pageviews), int32(r.Bounces), r.DurationMs})
			}
		}
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"rollup_day"},
			[]string{"site_id", "day", "visitors", "visits", "pageviews", "bounces", "duration_ms"}, pgx.CopyFromRows(tot)); err != nil {
			return err
		}
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"rollup_day_dim"},
			[]string{"site_id", "dim", "day", "value", "visitors", "visits", "pageviews", "bounces", "duration_ms"}, pgx.CopyFromRows(dim)); err != nil {
			return err
		}
		return refreshDayBlocksTx(ctx, tx, buckets)
	})
}

// RecomputeMonth replaces the monthly rollups of the given sites for the
// month starting at first (a date on the 1st) by summing the daily rows,
// and marks the month complete. Call it only once every day of the month
// is closed and rolled up for those sites.
func (s *Store) RecomputeMonth(ctx context.Context, first core.Day, sites []int32) error {
	t := first.Time()
	if t.Day() != 1 {
		return fmt.Errorf("RecomputeMonth: %s is not the 1st", first)
	}
	last := t.AddDate(0, 1, -1)
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		for _, q := range []string{`DELETE FROM rollup_month_dim WHERE month = $1 AND site_id = ANY($2)`,
			`DELETE FROM rollup_month_done WHERE month = $1 AND site_id = ANY($2)`} {
			if _, err := tx.Exec(ctx, q, t, sites); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO rollup_month_dim (site_id, dim, month, value, visitors, visits, pageviews, bounces, duration_ms)
SELECT site_id, dim, $1, value, sum(visitors), sum(visits), sum(pageviews), sum(bounces), sum(duration_ms)
FROM rollup_day_dim WHERE day BETWEEN $1 AND $2 AND site_id = ANY($3)
GROUP BY site_id, dim, value`, t, last, sites); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO rollup_month_done (site_id, month) SELECT unnest($2::int[]), $1`, t, sites)
		return err
	})
}
