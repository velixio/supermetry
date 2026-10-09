package closer

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/scheduler"
)

const rollupBackfillInterval = 5 * time.Minute

// RollupBackfillProgress is the durable progress of the automatic historical
// breakdown update. A day means one closed site-local day.
type RollupBackfillProgress struct {
	Running   bool
	DaysDone  int
	TotalDays int
}

// RollupBackfill rebuilds closed days after an upgrade changed the rollup
// layout. A pure dimension addition rebuilds only the new dimensions; any other
// change runs the full per-day recompute. Work is resumable via rollup_state.
type RollupBackfill struct {
	pool   *pgxpool.Pool
	layout RollupLayout
	pause  func(context.Context) error
	notify func(int32)
}

// NewRollupBackfill constructs a resumable backfill towards layout. pause runs
// between site-days; nil disables throttling and is intended for tests.
func NewRollupBackfill(pool *pgxpool.Pool, layout RollupLayout, pause func(context.Context) error, notify ...func(int32)) *RollupBackfill {
	backfill := &RollupBackfill{pool: pool, layout: NewRollupLayout(layout.Dimensions, layout.Counters), pause: pause}
	if len(notify) > 0 {
		backfill.notify = notify[0]
	}
	return backfill
}

// Job returns the scheduler definition that starts the upgrade work without
// delaying process readiness and retries transient failures.
func (b *RollupBackfill) Job() scheduler.Job {
	return scheduler.Job{
		Name:       "rollup-backfill",
		Every:      rollupBackfillInterval,
		RunOnStart: true,
		Run: func(ctx context.Context) error {
			_, err := b.Run(ctx)
			return err
		},
	}
}

// Run initializes or resumes the backfill and processes site-days oldest
// first. Cancellation (shutdown) returns nil and leaves a resumable cursor.
func (b *RollupBackfill) Run(ctx context.Context) (RollupBackfillProgress, error) {
	progress, plan, err := b.initialize(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return progress, nil
		}
		return progress, err
	}
	for progress.Running {
		item, ok, err := b.next(ctx)
		if err == nil && !ok {
			finished, finishErr := b.finish(ctx)
			if finishErr == nil {
				return finished, nil
			}
			err = finishErr
		}
		if err == nil {
			var advanced RollupBackfillProgress
			if advanced, err = b.recompute(ctx, item, plan); err == nil {
				progress = advanced
			}
		}
		if err == nil && b.pause != nil {
			if pauseErr := b.pause(ctx); pauseErr != nil {
				err = fmt.Errorf("pause rollup backfill: %w", pauseErr)
			}
		}
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, context.Canceled) {
				return progress, nil
			}
			return progress, err
		}
	}
	return progress, nil
}

type rollupBackfillItem struct {
	day    time.Time
	siteID int32
}

type rollupBackfillPlan struct {
	mode       rollupBackfillMode
	dimensions []int16
}

func (b *RollupBackfill) initialize(ctx context.Context) (RollupBackfillProgress, rollupBackfillPlan, error) {
	var progress RollupBackfillProgress
	var plan rollupBackfillPlan
	version := b.layout.Version()
	err := pgx.BeginFunc(ctx, b.pool, func(tx pgx.Tx) error {
		var storedVersion, pendingVersion, mode *string
		var storedDimensions []int16
		var storedCounters []string
		var finishedAt *time.Time
		err := tx.QueryRow(ctx, `
			select layout_version, dimension_set, counter_set, pending_layout_version,
			       backfill_mode, days_done, total_days, finished_at
			from rollup_state where singleton for update`).Scan(
			&storedVersion, &storedDimensions, &storedCounters, &pendingVersion,
			&mode, &progress.DaysDone, &progress.TotalDays, &finishedAt)
		exists := err == nil
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var stored *RollupLayout
		if exists && storedVersion != nil {
			layout := NewRollupLayout(storedDimensions, storedCounters)
			stored = &layout
		}
		if exists && pendingVersion != nil && *pendingVersion == version && finishedAt == nil {
			plan.mode = rollupBackfillMode(*mode)
			if plan.mode == rollupBackfillDimensions && stored != nil {
				plan.dimensions = dimensionDifference(b.layout.Dimensions, stored.Dimensions)
			}
			progress.Running = true
			return nil
		}
		if exists && pendingVersion == nil && storedVersion != nil && *storedVersion == version {
			return nil
		}

		plan.mode, plan.dimensions = planRollupBackfill(stored, b.layout)
		var throughDay *time.Time
		var throughSite *int32
		if plan.mode != rollupBackfillNone {
			if err := tx.QueryRow(ctx, `
				select day, site_id from day_close
				where status='done' and (exists (
				  select 1 from sessions_closed
				  where sessions_closed.site_id=day_close.site_id and sessions_closed.day=day_close.day
				) or exists (select 1 from verified_page_sessions verified where verified.site_id=day_close.site_id and verified.day=day_close.day))
				order by day desc, site_id desc limit 1`).Scan(&throughDay, &throughSite); err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
		}
		if throughDay == nil {
			// Fresh install, nothing closed yet, or nothing to change: the
			// current layout describes every closed day.
			progress = RollupBackfillProgress{}
			_, err = tx.Exec(ctx, `
				insert into rollup_state(singleton, layout_version, dimension_set, counter_set,
				  days_done, total_days, started_at, finished_at)
				values (true, $1, $2, $3, 0, 0, now(), now())
				on conflict (singleton) do update set
				  layout_version=excluded.layout_version, dimension_set=excluded.dimension_set,
				  counter_set=excluded.counter_set, pending_layout_version=null,
				  pending_dimension_set=null, pending_counter_set=null, backfill_mode=null,
				  last_processed_site_id=null, last_processed_day=null,
				  through_site_id=null, through_day=null, days_done=0, total_days=0,
				  started_at=excluded.started_at, finished_at=excluded.finished_at`,
				version, b.layout.Dimensions, b.layout.Counters)
			return err
		}

		if err := tx.QueryRow(ctx, `
			select count(*) from day_close
			where status='done' and (day,site_id) <= ($1::date,$2::int)
			  and (exists (select 1 from sessions_closed where sessions_closed.site_id=day_close.site_id and sessions_closed.day=day_close.day) or exists (select 1 from verified_page_sessions verified where verified.site_id=day_close.site_id and verified.day=day_close.day))`,
			*throughDay, *throughSite).Scan(&progress.TotalDays); err != nil {
			return err
		}
		progress.DaysDone = 0
		var oldVersion *string
		oldDimensions, oldCounters := []int16{}, []string{}
		if stored != nil {
			oldVersion, oldDimensions, oldCounters = storedVersion, stored.Dimensions, stored.Counters
		}
		_, err = tx.Exec(ctx, `
			insert into rollup_state(
			  singleton, layout_version, dimension_set, counter_set,
			  pending_layout_version, pending_dimension_set, pending_counter_set, backfill_mode,
			  through_site_id, through_day, days_done, total_days, started_at, finished_at)
			values (true, $1, $2, $3, $4, $5, $6, $7, $8, $9, 0, $10, now(), null)
			on conflict (singleton) do update set
			  pending_layout_version=excluded.pending_layout_version,
			  pending_dimension_set=excluded.pending_dimension_set,
			  pending_counter_set=excluded.pending_counter_set,
			  backfill_mode=excluded.backfill_mode,
			  last_processed_site_id=null, last_processed_day=null,
			  through_site_id=excluded.through_site_id, through_day=excluded.through_day,
			  days_done=0, total_days=excluded.total_days, started_at=now(), finished_at=null`,
			oldVersion, oldDimensions, oldCounters, version, b.layout.Dimensions, b.layout.Counters,
			string(plan.mode), *throughSite, *throughDay, progress.TotalDays)
		if err == nil {
			progress.Running = true
		}
		return err
	})
	if err != nil {
		return RollupBackfillProgress{}, rollupBackfillPlan{}, fmt.Errorf("initialize rollup backfill: %w", err)
	}
	return progress, plan, nil
}

func (b *RollupBackfill) next(ctx context.Context) (rollupBackfillItem, bool, error) {
	var item rollupBackfillItem
	err := b.pool.QueryRow(ctx, `
		select closed.day, closed.site_id
		from day_close closed cross join rollup_state state
		where state.singleton and closed.status='done'
		  and (closed.day,closed.site_id) <= (state.through_day,state.through_site_id)
		  and (state.last_processed_day is null or
		       (closed.day,closed.site_id) > (state.last_processed_day,state.last_processed_site_id))
		  and (exists (select 1 from sessions_closed sessions
		              where sessions.site_id=closed.site_id and sessions.day=closed.day) or exists (select 1 from verified_page_sessions verified where verified.site_id=closed.site_id and verified.day=closed.day))
		order by closed.day, closed.site_id limit 1`).Scan(&item.day, &item.siteID)
	if errors.Is(err, pgx.ErrNoRows) {
		return rollupBackfillItem{}, false, nil
	}
	if err != nil {
		return rollupBackfillItem{}, false, fmt.Errorf("load next rollup backfill day: %w", err)
	}
	return item, true, nil
}

func (b *RollupBackfill) recompute(ctx context.Context, item rollupBackfillItem, plan rollupBackfillPlan) (RollupBackfillProgress, error) {
	progress := RollupBackfillProgress{Running: true}
	err := pgx.BeginFunc(ctx, b.pool, func(tx pgx.Tx) error {
		siteIDs := []int32{item.siteID}
		if plan.mode == rollupBackfillFull {
			if err := recomputeDayRollups(ctx, tx, item.day, siteIDs); err != nil {
				return err
			}
		} else if err := recomputeDayDimensions(ctx, tx, item.day, siteIDs, plan.dimensions); err != nil {
			return err
		}
		if err := bumpCloseVersions(ctx, tx, item.day, siteIDs); err != nil {
			return err
		}
		// days_done is capped: a day re-closed behind the cursor during the
		// run may add one more item than counted at the start.
		return tx.QueryRow(ctx, `
			update rollup_state set last_processed_day=$1, last_processed_site_id=$2,
			  days_done=least(days_done+1, total_days)
			where singleton
			returning days_done, total_days`, item.day, item.siteID).Scan(&progress.DaysDone, &progress.TotalDays)
	})
	if err != nil {
		return progress, fmt.Errorf("backfill rollups for site %d on %s: %w", item.siteID, item.day.Format(time.DateOnly), err)
	}
	if b.notify != nil {
		b.notify(item.siteID)
	}
	return progress, nil
}

// recomputeDayDimensions replaces only the given dimensions of one closed day
// and refreshes the dependent block and month rollups.
func recomputeDayDimensions(ctx context.Context, tx pgx.Tx, day time.Time, siteIDs []int32, dimensions []int16) error {
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock($1)`, rollupRefreshLockID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `delete from rollup_day_dim where site_id=any($1) and day=$2 and dim=any($3::int2[])`, siteIDs, day, dimensions); err != nil {
		return err
	}
	if hasDimension(dimensions, 1) {
		if _, err := tx.Exec(ctx, rollupPathSQL, day, siteIDs); err != nil {
			return fmt.Errorf("build selected path rollup: %w", err)
		}
	}
	if sessionDimensions := withoutDimension(dimensions, 1); len(sessionDimensions) > 0 {
		if _, err := tx.Exec(ctx, rollupSelectedSessionDimensionsSQL, day, siteIDs, sessionDimensions); err != nil {
			return fmt.Errorf("build selected session rollups: %w", err)
		}
	}
	if err := refreshDayBlocks(ctx, tx, siteIDs, day); err != nil {
		return fmt.Errorf("refresh day blocks: %w", err)
	}
	if err := refreshMonth(ctx, tx, siteIDs, day); err != nil {
		return fmt.Errorf("refresh month: %w", err)
	}
	return nil
}

// finish records the pending layout as current once the cursor is exhausted.
func (b *RollupBackfill) finish(ctx context.Context) (RollupBackfillProgress, error) {
	var progress RollupBackfillProgress
	err := b.pool.QueryRow(ctx, `
		update rollup_state set
		  layout_version=pending_layout_version, dimension_set=pending_dimension_set,
		  counter_set=pending_counter_set, pending_layout_version=null,
		  pending_dimension_set=null, pending_counter_set=null, backfill_mode=null,
		  last_processed_site_id=null, last_processed_day=null,
		  through_site_id=null, through_day=null,
		  days_done=total_days, finished_at=now()
		where singleton and pending_layout_version=$1
		returning days_done, total_days`, b.layout.Version()).Scan(&progress.DaysDone, &progress.TotalDays)
	if err != nil {
		return progress, fmt.Errorf("finish rollup backfill: %w", err)
	}
	return progress, nil
}

// Progress loads the persisted backfill state for health reporting.
func (b *RollupBackfill) Progress(ctx context.Context) (RollupBackfillProgress, error) {
	var progress RollupBackfillProgress
	var finishedAt *time.Time
	err := b.pool.QueryRow(ctx, `select days_done,total_days,finished_at from rollup_state where singleton`).Scan(
		&progress.DaysDone, &progress.TotalDays, &finishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return progress, nil
	}
	if err != nil {
		return progress, err
	}
	progress.Running = finishedAt == nil
	return progress, nil
}

func dimensionDifference(current, stored []int16) []int16 {
	result := make([]int16, 0, len(current))
	for _, dimension := range current {
		if !slices.Contains(stored, dimension) {
			result = append(result, dimension)
		}
	}
	return result
}

func hasDimension(dimensions []int16, wanted int16) bool {
	return slices.Contains(dimensions, wanted)
}

func withoutDimension(dimensions []int16, excluded int16) []int16 {
	result := make([]int16, 0, len(dimensions))
	for _, dimension := range dimensions {
		if dimension != excluded {
			result = append(result, dimension)
		}
	}
	return result
}
