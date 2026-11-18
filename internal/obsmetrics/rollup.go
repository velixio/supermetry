package obsmetrics

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/db"
	"supermetry/internal/scheduler"
	"supermetry/internal/sites"
	"supermetry/internal/spanrollup"
)

const (
	// RollupEvery is how often the rollup job runs.
	RollupEvery = 5 * time.Minute
	// maxDirty bounds the in-memory set of late (site, time) marks; beyond it
	// the next run recomputes the last 24 hours of every active site.
	maxDirty = 10_000
	// lateAfter is how old a written point must be to count as late: the
	// current and previous local hours are recomputed on every run anyway.
	lateAfter = 2 * time.Hour
	// dirtyGrain groups late marks; every zone offset is a multiple of it, so
	// one grain never straddles two local hours.
	dirtyGrain = 15 * time.Minute
	// activeWindow is how far back a point makes a site worth recomputing.
	activeWindow = 2*time.Hour + 15*time.Minute
	// catchUp is the lookback of the first run after a start and of an
	// overflow.
	catchUp = 24 * time.Hour

	rollupLockClass = 0x4d5248 // "MRH"
)

// rollupSQL replaces one site-local hour. $1 site, $2 the wall hour, $3/$4 the
// UTC bounds. Histogram buckets are summed element-wise per series in the
// same statement, so no row is updated after its insert.
const rollupSQL = `
with pts as (
    select series_id,
           count(*)                                                   as samples,
           sum(value)                                                 as value_sum,
           min(coalesce(value, min))                                  as value_min,
           max(coalesce(value, max))                                  as value_max,
           (array_agg(value order by ts desc) filter (where value is not null))[1] as value_last,
           sum(count)                                                 as count,
           sum(sum)                                                   as sum
    from metric_points
    where site_id = $1 and ts >= $3 and ts < $4
    group by series_id
), buckets as (
    select series_id, array_agg(total order by i) as bucket_counts
    from (
        select p.series_id, u.i, sum(u.c)::bigint as total
        from metric_points p, unnest(p.bucket_counts) with ordinality as u(c, i)
        where p.site_id = $1 and p.ts >= $3 and p.ts < $4 and p.bucket_counts is not null
        group by p.series_id, u.i
    ) t
    group by series_id
)
insert into metric_hourly (site_id, series_id, hour, samples, value_sum, value_min, value_max, value_last, count, sum, bucket_counts)
select $1, pts.series_id, $2, pts.samples, pts.value_sum, pts.value_min, pts.value_max, pts.value_last, pts.count, pts.sum, buckets.bucket_counts
from pts left join buckets using (series_id)`

// RecomputeHour replaces the site's metric_hourly rows for one site-local hour
// (a wall-clock timestamp, see spanrollup.Wall) in one transaction: delete,
// then insert from the raw points. Running it again changes nothing, and it
// picks up late points. A local hour that does not exist (a spring-forward
// gap) just clears.
func RecomputeHour(ctx context.Context, pool *pgxpool.Pool, siteID int32, loc *time.Location, hour time.Time) error {
	hour = spanrollup.Wall(hour)
	if loc == nil {
		loc = time.UTC
	}
	bounds := spanrollup.LocalHourBounds(hour, loc)
	if err := db.EnsureMonthlyPartitions(ctx, pool, "metric_hourly", hour, hour.Add(time.Hour)); err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock($1, $2)`, int32(rollupLockClass), siteID); err != nil {
			return fmt.Errorf("lock metric rollups: %w", err)
		}
		if _, err := tx.Exec(ctx, `delete from metric_hourly where site_id = $1 and hour = $2`, siteID, hour); err != nil {
			return fmt.Errorf("clear metric hour: %w", err)
		}
		if len(bounds) == 0 {
			return nil
		}
		lo, hi := bounds[0][0], bounds[len(bounds)-1][1]
		if _, err := tx.Exec(ctx, rollupSQL, siteID, hour, lo, hi); err != nil {
			return fmt.Errorf("rebuild metric hour: %w", err)
		}
		return nil
	})
}

// RollupSQL exposes the rollup statement for EXPLAIN in tests and tooling.
func RollupSQL() string { return rollupSQL }

// Rollup recomputes metric_hourly. Late points written by the Writer are noted
// in memory (NoteLate) and rebuilt on the next run.
type Rollup struct {
	pool *pgxpool.Pool
	now  func() time.Time

	mu       sync.Mutex
	dirty    map[int32]map[time.Time]struct{} // site -> UTC time grain
	size     int
	overflow bool
	booted   bool
}

// NewRollup returns a Rollup reading raw points from pool.
func NewRollup(pool *pgxpool.Pool, now func() time.Time) *Rollup {
	if now == nil {
		now = time.Now
	}
	return &Rollup{pool: pool, now: now, dirty: map[int32]map[time.Time]struct{}{}}
}

// NoteLate is the Writer's OnWritten hook: points older than two hours were
// written into an hour that may already be rolled up, so it is marked dirty.
func (r *Rollup) NoteLate(batch []Point) {
	cutoff := r.now().Add(-lateAfter)
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range batch {
		p := &batch[i]
		if !p.TS.Before(cutoff) {
			continue
		}
		grain := p.TS.UTC().Truncate(dirtyGrain)
		set := r.dirty[p.SiteID]
		if set == nil {
			set = map[time.Time]struct{}{}
			r.dirty[p.SiteID] = set
		}
		if _, ok := set[grain]; ok {
			continue
		}
		if r.size >= maxDirty {
			r.overflow = true
			r.dirty = map[int32]map[time.Time]struct{}{}
			r.size = 0
			return
		}
		set[grain] = struct{}{}
		r.size++
	}
}

// takeDirty returns and clears the pending marks and the catch-up flag.
func (r *Rollup) takeDirty() (map[int32]map[time.Time]struct{}, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, catch := r.dirty, r.overflow || !r.booted
	r.dirty, r.size, r.overflow, r.booted = map[int32]map[time.Time]struct{}{}, 0, false, true
	return d, catch
}

// Job returns the scheduler job "obs-metric-rollup": every run recomputes the
// current and previous local hour of every app site with recent points, plus
// the hours of late points, plus the last 24 hours on the first run and after
// an overflow of the late set.
func (r *Rollup) Job(registry sites.Registry) scheduler.Job {
	return scheduler.Job{Name: "obs-metric-rollup", Every: RollupEvery, Run: func(ctx context.Context) error {
		return r.Run(ctx, registry)
	}}
}

// Run does one pass of the job.
func (r *Rollup) Run(ctx context.Context, registry sites.Registry) error {
	now := r.now()
	dirty, catch := r.takeDirty()
	since := now.Add(-activeWindow)
	if catch {
		since = now.Add(-catchUp - 2*time.Hour)
	}
	active := map[int32]bool{}
	rows, err := r.pool.Query(ctx, `select distinct site_id from metric_points where ts >= $1`, since)
	if err != nil {
		r.restore(dirty)
		return fmt.Errorf("find active metric sites: %w", err)
	}
	for rows.Next() {
		var id int32
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			r.restore(dirty)
			return err
		}
		active[id] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		r.restore(dirty)
		return err
	}
	var errs []error
	for _, s := range registry.All() {
		if s.Kind != sites.KindApp {
			continue
		}
		if !active[s.ID] && len(dirty[s.ID]) == 0 {
			continue
		}
		loc := s.TZ
		if loc == nil {
			loc = time.UTC
		}
		hours := map[time.Time]struct{}{}
		cur := spanrollup.Wall(now.In(loc))
		hours[cur] = struct{}{}
		hours[cur.Add(-time.Hour)] = struct{}{}
		if catch && active[s.ID] {
			for h := cur.Add(-catchUp); h.Before(cur); h = h.Add(time.Hour) {
				hours[h] = struct{}{}
			}
		}
		for grain := range dirty[s.ID] {
			hours[spanrollup.Wall(grain.In(loc))] = struct{}{}
		}
		ordered := make([]time.Time, 0, len(hours))
		for h := range hours {
			ordered = append(ordered, h)
		}
		sort.Slice(ordered, func(i, j int) bool { return ordered[i].Before(ordered[j]) })
		for _, h := range ordered {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := RecomputeHour(ctx, r.pool, s.ID, loc, h); err != nil {
				slog.Error("metric rollup", "site", s.ID, "hour", h.Format("2006-01-02T15"), "error", err)
				errs = append(errs, err)
				break
			}
		}
	}
	return errors.Join(errs...)
}

// restore puts marks back after a failed pass so they are not lost.
func (r *Rollup) restore(d map[int32]map[time.Time]struct{}) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for site, set := range d {
		for g := range set {
			if r.dirty[site] == nil {
				r.dirty[site] = map[time.Time]struct{}{}
			}
			if _, ok := r.dirty[site][g]; !ok {
				r.dirty[site][g] = struct{}{}
				r.size++
			}
		}
	}
}
