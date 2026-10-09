// Package minutes persists closed-minute realtime activity aggregates.
package minutes

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/scheduler"
	"supermetry/internal/session"
)

const (
	defaultChunkSize = 6 * time.Hour
	defaultMaxChunks = 24
	lateEventWindow  = 5 * time.Minute
	// catchUpClampAfter is how far behind the job must be before it checks
	// the oldest raw event; normal runs skip that query.
	catchUpClampAfter = time.Hour
)

// Store reads and rebuilds persisted per-minute activity.
type Store struct {
	pool      *pgxpool.Pool
	chunkSize time.Duration
	maxChunks int
}

// NewStore returns a minute aggregate store backed by pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{
		pool:      pool,
		chunkSize: defaultChunkSize,
		maxChunks: defaultMaxChunks,
	}
}

// Minutes returns persisted visitor counts in [from, to), ordered by minute.
func (s *Store) Minutes(ctx context.Context, siteID int32, from, to time.Time) ([]session.MinuteVisitors, error) {
	rows, err := s.pool.Query(ctx, `
SELECT minute, visitors
FROM visitors_per_minute
WHERE site_id = $1 AND minute >= $2 AND minute < $3
ORDER BY minute`, siteID, from, to)
	if err != nil {
		return nil, fmt.Errorf("query visitors per minute: %w", err)
	}
	defer rows.Close()

	minutes := make([]session.MinuteVisitors, 0)
	for rows.Next() {
		var minute session.MinuteVisitors
		if err := rows.Scan(&minute.Minute, &minute.Visitors); err != nil {
			return nil, fmt.Errorf("scan visitors per minute: %w", err)
		}
		minute.Minute = minute.Minute.UTC()
		minutes = append(minutes, minute)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate visitors per minute: %w", err)
	}
	return minutes, nil
}

// Recompute replaces aggregates in [from, to) from raw pageview and custom
// events. The statement also removes aggregates whose source events vanished.
func (s *Store) Recompute(ctx context.Context, from, to time.Time) error {
	if !from.Before(to) {
		return nil
	}
	_, err := s.pool.Exec(ctx, `
WITH aggregate AS MATERIALIZED (
    SELECT site_id,
           date_trunc('minute', ts, 'UTC') AS minute,
           count(DISTINCT visitor_id)::integer AS visitors,
           count(*) FILTER (WHERE kind = 0)::integer AS pageviews,
           count(*) FILTER (WHERE kind = 1)::integer AS events
    FROM events
    WHERE ts >= $1 AND ts < $2 AND kind IN (0, 1)
    GROUP BY site_id, date_trunc('minute', ts, 'UTC')
), upserted AS (
    INSERT INTO visitors_per_minute(site_id, minute, visitors, pageviews, events)
    SELECT site_id, minute, visitors, pageviews, events
    FROM aggregate
    ON CONFLICT (site_id, minute) DO UPDATE SET
        visitors = EXCLUDED.visitors,
        pageviews = EXCLUDED.pageviews,
        events = EXCLUDED.events
    RETURNING site_id, minute
)
DELETE FROM visitors_per_minute AS stored
WHERE stored.minute >= $1 AND stored.minute < $2
  AND NOT EXISTS (
      SELECT 1 FROM aggregate
      WHERE aggregate.site_id = stored.site_id
        AND aggregate.minute = stored.minute
  )`, from, to)
	if err != nil {
		return fmt.Errorf("recompute visitors per minute: %w", err)
	}
	return nil
}

// Job returns the bounded periodic closed-minute rollup job.
func (s *Store) Job(now func() time.Time) scheduler.Job {
	return scheduler.Job{
		Name:  "minute-rollup",
		Every: time.Minute,
		Run: func(ctx context.Context) error {
			return s.runJob(ctx, now().UTC().Truncate(time.Minute))
		},
	}
}

func (s *Store) runJob(ctx context.Context, to time.Time) error {
	from, hasStored, err := s.startMinute(ctx, to)
	if err != nil {
		return err
	}
	if !hasStored {
		oldest, ok, err := s.nextEventMinute(ctx, time.Time{}, to)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		from = oldest
	}

	if hasStored && to.Sub(from) > catchUpClampAfter {
		// A long catch-up may start before the oldest surviving raw event.
		// Recomputing that range would delete stored minutes whose raw events
		// expired, so begin at the oldest raw event instead.
		oldest, ok, err := s.nextEventMinute(ctx, time.Time{}, to)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		if oldest.After(from) {
			from = oldest
		}
		hasStored = false
	}

	for chunks := 0; chunks < s.maxChunks && from.Before(to); chunks++ {
		// The first range for an existing table must never be skipped: it
		// rebuilds the late-event window and deletes stale stored rows.
		if chunks > 0 || !hasStored {
			next, ok, err := s.nextEventMinute(ctx, from, to)
			if err != nil {
				return err
			}
			if !ok {
				return nil
			}
			if next.After(from) {
				from = next
			}
		}

		chunkTo := from.Add(s.chunkSize)
		if chunkTo.After(to) {
			chunkTo = to
		}
		if err := s.Recompute(ctx, from, chunkTo); err != nil {
			return err
		}
		from = chunkTo
		hasStored = false
	}
	return nil
}

func (s *Store) startMinute(ctx context.Context, to time.Time) (time.Time, bool, error) {
	var newest *time.Time
	if err := s.pool.QueryRow(ctx, `
SELECT max(minute) FROM visitors_per_minute WHERE minute < $1`, to).Scan(&newest); err != nil {
		return time.Time{}, false, fmt.Errorf("find newest visitors-per-minute row: %w", err)
	}
	if newest == nil {
		return time.Time{}, false, nil
	}
	return newest.UTC().Add(-lateEventWindow), true, nil
}

func (s *Store) nextEventMinute(ctx context.Context, from, to time.Time) (time.Time, bool, error) {
	var oldest *time.Time
	var err error
	if from.IsZero() {
		err = s.pool.QueryRow(ctx, `
SELECT min(ts) FROM events WHERE ts < $1 AND kind IN (0, 1)`, to).Scan(&oldest)
	} else {
		err = s.pool.QueryRow(ctx, `
SELECT min(ts) FROM events WHERE ts >= $1 AND ts < $2 AND kind IN (0, 1)`, from, to).Scan(&oldest)
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("find next raw event: %w", err)
	}
	if oldest == nil {
		return time.Time{}, false, nil
	}
	return oldest.UTC().Truncate(time.Minute), true, nil
}
