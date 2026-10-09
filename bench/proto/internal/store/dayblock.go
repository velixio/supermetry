package store

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"

	"supermetry/bench/proto/internal/core"
)

type dayBlockKey struct {
	site  int32
	start core.Day
}

// RefreshDayBlocks idempotently rebuilds every materialized ancestor of the
// supplied completed site-days. It is exported for rollup-stream and focused
// correctness tests; normal close-day maintenance calls the transaction helper.
func (s *Store) RefreshDayBlocks(ctx context.Context, days []core.SaltKey) error {
	if len(days) == 0 {
		return nil
	}
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		return refreshDayBlocksTx(ctx, tx, days)
	})
}

// RebuildDayBlocks replaces all block state for sites from the complete
// closed-day span [from, to]. Callers must pass the whole retained span for
// those sites; dates with no dimension rows are valid completed zero days.
func (s *Store) RebuildDayBlocks(ctx context.Context, from, to core.Day, sites []int32) error {
	if to < from {
		return errors.New("RebuildDayBlocks: to < from")
	}
	if len(sites) == 0 {
		return nil
	}
	days := make([]core.SaltKey, 0, (int(to-from)+1)*len(sites))
	for d := from; d <= to; d++ {
		for _, site := range sites {
			days = append(days, core.SaltKey{Site: site, Day: d})
		}
	}
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(7469002)`); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM rollup_block_dim WHERE site_id = ANY($1)`, sites); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM rollup_block_done WHERE site_id = ANY($1)`, sites); err != nil {
			return err
		}
		return refreshDayBlocksLocked(ctx, tx, days)
	})
}

func refreshDayBlocksTx(ctx context.Context, tx pgx.Tx, days []core.SaltKey) error {
	// All close-day transactions take this lock only after their expensive raw
	// aggregation. It prevents overlapping ancestor rewrites from racing while
	// preserving parallel work on the hot/closed tables.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(7469002)`); err != nil {
		return err
	}
	return refreshDayBlocksLocked(ctx, tx, days)
}

func refreshDayBlocksLocked(ctx context.Context, tx pgx.Tx, days []core.SaltKey) error {
	affected := make([]map[dayBlockKey]struct{}, int(core.MaxDayBlockLevel)+1)
	for i := range affected {
		affected[i] = map[dayBlockKey]struct{}{}
	}
	for _, d := range days {
		for level := uint8(0); level <= core.MaxDayBlockLevel; level++ {
			b := core.ContainingDayBlock(d.Day, level)
			affected[level][dayBlockKey{site: d.Site, start: b.Start}] = struct{}{}
		}
	}

	for level, set := range affected {
		keys := make([]dayBlockKey, 0, len(set))
		for k := range set {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			if keys[i].site != keys[j].site {
				return keys[i].site < keys[j].site
			}
			return keys[i].start < keys[j].start
		})
		sites := make([]int32, len(keys))
		startTimes := make([]time.Time, len(keys))
		for i, k := range keys {
			sites[i], startTimes[i] = k.site, k.start.Time()
		}
		if level > 0 {
			if _, err := tx.Exec(ctx, `DELETE FROM rollup_block_dim b USING unnest($1::int[], $2::date[]) AS w(site_id, block_start)
WHERE b.site_id = w.site_id AND b.level = $3 AND b.block_start = w.block_start`, sites, startTimes, int16(level)); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM rollup_block_done b USING unnest($1::int[], $2::date[]) AS w(site_id, block_start)
WHERE b.site_id = w.site_id AND b.level = $3 AND b.block_start = w.block_start`, sites, startTimes, int16(level)); err != nil {
			return err
		}
		if level == 0 {
			if _, err := tx.Exec(ctx, `INSERT INTO rollup_block_done (site_id, level, block_start)
SELECT site_id, 0, block_start FROM unnest($1::int[], $2::date[]) AS w(site_id, block_start)`, sites, startTimes); err != nil {
				return err
			}
			continue
		}

		half := int32(1) << (level - 1)
		if level == 1 {
			if _, err := tx.Exec(ctx, `WITH wanted AS (
  SELECT site_id, block_start FROM unnest($1::int[], $2::date[]) AS w(site_id, block_start)
), ready AS (
  SELECT w.* FROM wanted w
  JOIN rollup_block_done l ON l.site_id = w.site_id AND l.level = 0 AND l.block_start = w.block_start
  JOIN rollup_block_done r ON r.site_id = w.site_id AND r.level = 0 AND r.block_start = w.block_start + 1
)
INSERT INTO rollup_block_dim (site_id, dim, level, block_start, value, visitors, visits, pageviews, bounces, duration_ms)
SELECT r.site_id, d.dim, 1, r.block_start, d.value, sum(d.visitors), sum(d.visits), sum(d.pageviews), sum(d.bounces), sum(d.duration_ms)
FROM ready r JOIN rollup_day_dim d ON d.site_id = r.site_id AND d.day >= r.block_start AND d.day < r.block_start + 2
GROUP BY r.site_id, d.dim, r.block_start, d.value`, sites, startTimes); err != nil {
				return err
			}
		} else {
			if _, err := tx.Exec(ctx, `WITH wanted AS (
  SELECT site_id, block_start FROM unnest($1::int[], $2::date[]) AS w(site_id, block_start)
), ready AS (
  SELECT w.* FROM wanted w
  JOIN rollup_block_done l ON l.site_id = w.site_id AND l.level = $3 - 1 AND l.block_start = w.block_start
  JOIN rollup_block_done r ON r.site_id = w.site_id AND r.level = $3 - 1 AND r.block_start = w.block_start + $4::int
)
INSERT INTO rollup_block_dim (site_id, dim, level, block_start, value, visitors, visits, pageviews, bounces, duration_ms)
SELECT r.site_id, d.dim, $3, r.block_start, d.value, sum(d.visitors), sum(d.visits), sum(d.pageviews), sum(d.bounces), sum(d.duration_ms)
FROM ready r JOIN rollup_block_dim d ON d.site_id = r.site_id AND d.level = $3 - 1
 AND (d.block_start = r.block_start OR d.block_start = r.block_start + $4::int)
GROUP BY r.site_id, d.dim, r.block_start, d.value`, sites, startTimes, int16(level), half); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `WITH wanted AS (
  SELECT site_id, block_start FROM unnest($1::int[], $2::date[]) AS w(site_id, block_start)
)
INSERT INTO rollup_block_done (site_id, level, block_start)
SELECT w.site_id, $3, w.block_start FROM wanted w
JOIN rollup_block_done l ON l.site_id = w.site_id AND l.level = $3 - 1 AND l.block_start = w.block_start
JOIN rollup_block_done r ON r.site_id = w.site_id AND r.level = $3 - 1 AND r.block_start = w.block_start + $4::int`, sites, startTimes, int16(level), half); err != nil {
			return err
		}
	}
	return nil
}
