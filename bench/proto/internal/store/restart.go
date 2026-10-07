package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"panel/bench/proto/internal/core"
)

// LoadOpenSessions rebuilds every open session from durable events, reconciles
// the sessions table to that event-derived state, and returns the snapshots to
// restore into memory before the server accepts traffic. Events accepted only
// into the crashed process's memory are intentionally absent from both states.
func (s *Store) LoadOpenSessions(ctx context.Context, now time.Time, timeout time.Duration) ([]core.Session, error) {
	var newest time.Time
	if err := s.Pool.QueryRow(ctx, `SELECT coalesce(max(ts), $1::timestamptz) FROM events`, now).Scan(&newest); err != nil {
		return nil, fmt.Errorf("find durable event watermark: %w", err)
	}
	reference := now
	if newest.After(reference) {
		reference = newest
	}
	cutoff := reference.Add(-timeout)

	// The event half of candidate_sessions is pruned by the BRIN timestamp
	// index. Recent session snapshots are candidates too because they may be
	// ahead of the event log at the instant of the crash. The second pass reads
	// every durable event for each candidate: a continuously active session may
	// have started more than one timeout ago.
	rows, err := s.Pool.Query(ctx, `
WITH candidate_sessions AS MATERIALIZED (
  SELECT DISTINCT site_id, session_id
  FROM events
  WHERE ts >= $1
  UNION
  SELECT site_id, session_id
  FROM sessions
  WHERE start_ts + duration_ms * interval '1 millisecond' >= $1
)
SELECT e.site_id, e.local_date, e.ts, e.visitor_id, e.session_id, e.name, e.path,
       e.source, e.country, e.browser, e.os, e.device,
       coalesce(e.utm_source, ''), coalesce(e.utm_medium, ''), coalesce(e.utm_campaign, '')
FROM events e
JOIN candidate_sessions r USING (site_id, session_id)
ORDER BY e.site_id, e.session_id, e.ts, e.tableoid, e.ctid`, cutoff)
	if err != nil {
		return nil, fmt.Errorf("load durable open-session events: %w", err)
	}
	defer rows.Close()

	type sessionKey struct {
		site int32
		id   int64
	}
	var (
		out     []core.Session
		rebuilt []core.Session
		group   []core.Event
		key     sessionKey
		hasKey  bool
	)
	flush := func() error {
		if len(group) == 0 {
			return nil
		}
		session, err := core.RebuildSession(group)
		if err != nil {
			return err
		}
		rebuilt = append(rebuilt, session)
		if session.Last >= cutoff.UnixMicro() {
			out = append(out, session)
		}
		group = group[:0]
		return nil
	}
	for rows.Next() {
		var ev core.Event
		var day, ts time.Time
		if err := rows.Scan(&ev.SiteID, &day, &ts, &ev.VisitorID, &ev.SessionID, &ev.Name, &ev.Path,
			&ev.Source, &ev.Country, &ev.Browser, &ev.OS, &ev.Device,
			&ev.UTMSource, &ev.UTMMedium, &ev.UTMCampaign); err != nil {
			return nil, err
		}
		ev.Day = core.Day(day.Unix() / 86400)
		ev.TS = ts.UnixMicro()
		next := sessionKey{site: ev.SiteID, id: ev.SessionID}
		if hasKey && next != key {
			if err := flush(); err != nil {
				return nil, err
			}
		}
		key, hasKey = next, true
		group = append(group, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := flush(); err != nil {
		return nil, err
	}
	if err := s.reconcileOpenSessions(ctx, cutoff, rebuilt); err != nil {
		return nil, err
	}
	return out, nil
}

// reconcileOpenSessions replaces recent persisted snapshots with the state
// derived from durable events. In particular, it removes a snapshot whose
// only events were still buffered when the process died.
func (s *Store) reconcileOpenSessions(ctx context.Context, cutoff time.Time, sessions []core.Session) error {
	if err := s.EnsureSessionDays(ctx, distinctDays(len(sessions), func(i int) int64 { return sessions[i].Start })...); err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `CREATE TEMP TABLE IF NOT EXISTS sessions_stage (LIKE sessions) ON COMMIT DELETE ROWS`); err != nil {
			return err
		}
		if len(sessions) > 0 {
			if _, err := tx.CopyFrom(ctx, pgx.Identifier{"sessions_stage"}, sessionCols,
				&sessionSource{ss: sessions, row: make([]any, 0, len(sessionCols))}); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `
DELETE FROM sessions s
WHERE s.start_ts + s.duration_ms * interval '1 millisecond' >= $1
  AND NOT EXISTS (
    SELECT 1 FROM sessions_stage n
    WHERE n.site_id = s.site_id AND n.start_ts = s.start_ts AND n.session_id = s.session_id
  )`, cutoff); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
INSERT INTO sessions SELECT * FROM sessions_stage
ON CONFLICT (site_id, start_ts, session_id) DO UPDATE SET
  local_date = EXCLUDED.local_date, visitor_id = EXCLUDED.visitor_id,
  duration_ms = EXCLUDED.duration_ms, pageviews = EXCLUDED.pageviews,
  custom_events = EXCLUDED.custom_events, entry_path = EXCLUDED.entry_path,
  exit_path = EXCLUDED.exit_path, source = EXCLUDED.source,
  country = EXCLUDED.country, browser = EXCLUDED.browser, os = EXCLUDED.os,
  device = EXCLUDED.device, utm_source = EXCLUDED.utm_source,
  utm_medium = EXCLUDED.utm_medium, utm_campaign = EXCLUDED.utm_campaign`)
		return err
	})
}
