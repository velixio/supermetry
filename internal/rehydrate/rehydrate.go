// Package rehydrate rebuilds the live-session cache from durable state after
// a process restart.
package rehydrate

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"panel/internal/session"
)

// Run restores sessions active within the idle window before the later of
// now and the newest durable event. Durable events rebuild the session shape;
// a newer sessions row supplies counts or activity not yet reflected there.
func Run(ctx context.Context, pool *pgxpool.Pool, store session.Store, now time.Time) (restored int, err error) {
	var newest time.Time
	if err := pool.QueryRow(ctx, `SELECT coalesce(max(ts), $1::timestamptz) FROM events`, now).Scan(&newest); err != nil {
		return 0, fmt.Errorf("find durable event watermark: %w", err)
	}
	reference := now
	if newest.After(reference) {
		reference = newest
	}
	cutoff := reference.Add(-session.IdleTimeout)

	rows, err := pool.Query(ctx, `
WITH candidates AS MATERIALIZED (
    SELECT site_id, retention_class, session_id
    FROM events
    WHERE ts >= $1
    GROUP BY site_id, retention_class, session_id
    UNION
    SELECT site_id, retention_class, session_id
    FROM sessions
    WHERE last >= $1
), event_state AS (
    SELECT
        e.site_id,
        e.retention_class,
        e.session_id,
        (array_agg(e.visitor_id ORDER BY e.ts, e.tableoid, e.ctid))[1] AS visitor_id,
        min(e.ts) AS start,
        max(e.ts) AS event_last,
        (array_agg(coalesce(e.local_day, e.ts::date) ORDER BY e.ts, e.tableoid, e.ctid))[1] AS local_day,
        coalesce((array_agg(e.path_id ORDER BY e.ts, e.tableoid, e.ctid)
            FILTER (WHERE e.kind = 0))[1], 0) AS entry_path,
        coalesce((array_agg(e.path_id ORDER BY e.ts DESC, e.tableoid DESC, e.ctid DESC)
            FILTER (WHERE e.kind = 0))[1], 0) AS exit_path,
        count(*) FILTER (WHERE e.kind = 0) AS pageviews,
        count(*) FILTER (WHERE e.kind = 1) AS events,
        coalesce(sum(e.engagement_ms), 0) AS engagement_ms,
        (array_agg(e.source_id ORDER BY e.ts, e.tableoid, e.ctid))[1] AS source_id,
        (array_agg(e.medium_id ORDER BY e.ts, e.tableoid, e.ctid))[1] AS medium_id,
        (array_agg(e.campaign_id ORDER BY e.ts, e.tableoid, e.ctid))[1] AS campaign_id,
        (array_agg(e.country ORDER BY e.ts, e.tableoid, e.ctid))[1] AS country,
        (array_agg(e.region_id ORDER BY e.ts, e.tableoid, e.ctid))[1] AS region_id,
        (array_agg(e.city_id ORDER BY e.ts, e.tableoid, e.ctid))[1] AS city_id,
        (array_agg(e.browser_id ORDER BY e.ts, e.tableoid, e.ctid))[1] AS browser_id,
        (array_agg(e.os_id ORDER BY e.ts, e.tableoid, e.ctid))[1] AS os_id,
        (array_agg(e.device ORDER BY e.ts, e.tableoid, e.ctid))[1] AS device
    FROM events AS e
    JOIN candidates AS c USING (site_id, retention_class, session_id)
    GROUP BY e.site_id, e.retention_class, e.session_id
)
SELECT
    e.site_id,
    e.retention_class,
    e.session_id,
    e.visitor_id,
    e.start,
    greatest(e.event_last, s.last),
    e.local_day,
    e.entry_path,
    e.exit_path,
    greatest(e.pageviews, coalesce(s.pageviews, 0))::integer,
    greatest(e.events, coalesce(s.events, 0))::integer,
    greatest(e.engagement_ms, coalesce(s.engagement_ms, 0))::integer,
    e.source_id,
    e.medium_id,
    e.campaign_id,
    e.country,
    e.region_id,
    e.city_id,
    e.browser_id,
    e.os_id,
    e.device
FROM event_state AS e
LEFT JOIN LATERAL (
    SELECT last, pageviews, events, engagement_ms
    FROM sessions
    WHERE site_id = e.site_id
      AND retention_class = e.retention_class
      AND session_id = e.session_id
    ORDER BY last DESC
    LIMIT 1
) AS s ON true
WHERE greatest(e.event_last, s.last) >= $1
ORDER BY e.site_id, e.session_id`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("load open sessions: %w", err)
	}
	defer rows.Close()

	snapshots := make([]session.Snapshot, 0)
	for rows.Next() {
		var snapshot session.Snapshot
		if err := rows.Scan(
			&snapshot.SiteID,
			&snapshot.Retention,
			&snapshot.SessionID,
			&snapshot.VisitorID,
			&snapshot.Start,
			&snapshot.Last,
			&snapshot.LocalDay,
			&snapshot.Entry,
			&snapshot.Exit,
			&snapshot.Pageviews,
			&snapshot.Events,
			&snapshot.EngagementMS,
			&snapshot.Source,
			&snapshot.Medium,
			&snapshot.Campaign,
			&snapshot.Country,
			&snapshot.Region,
			&snapshot.City,
			&snapshot.Browser,
			&snapshot.OS,
			&snapshot.Device,
		); err != nil {
			return 0, fmt.Errorf("scan open session: %w", err)
		}
		snapshot.Bounce = snapshot.Pageviews == 1 && snapshot.Events == 0
		snapshots = append(snapshots, snapshot)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate open sessions: %w", err)
	}

	store.Restore(snapshots)
	return len(snapshots), nil
}
