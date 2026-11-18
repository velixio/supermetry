package spanrollup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/scheduler"
	"supermetry/internal/sites"
)

const (
	// Every is how often the job looks for hours to roll.
	Every = time.Minute
	// closeSlack is how long after its end a local hour is rolled.
	closeSlack = 5 * time.Minute
	// settleAfter is when a rolled hour gets one last rebuild: the writer only
	// reports spans older than the previous local hour as late, so spans
	// landing in the just-closed hour after the first roll are caught here.
	settleAfter = 65 * time.Minute
	// maxBackfill bounds the raw retention the job looks back over.
	maxBackfill = 15 * 24 * time.Hour
	// maxHoursPerSite bounds the work one run does for one site.
	maxHoursPerSite = 72
	// discoverEvery limits the raw scan for sites that have no rollup state.
	discoverEvery = 10 * time.Minute
)

// Job returns the scheduler job that rolls closed local hours of every app
// project: hours that ended at least five minutes ago and have no done state
// within the raw retention, every dirty hour, and one settling pass per hour.
func Job(pool *pgxpool.Pool, registry sites.Registry, now func() time.Time) scheduler.Job {
	r := &runner{pool: pool, now: now, discovered: map[int32]time.Time{}}
	return scheduler.Job{Name: "span-rollups", Every: Every, Run: func(ctx context.Context) error {
		var errs []error
		for _, s := range registry.All() {
			if s.Kind != sites.KindApp || s.App == nil {
				continue
			}
			if err := r.site(ctx, s.ID, s.TZ); err != nil {
				slog.Error("span rollups", "site", s.ID, "error", err)
				errs = append(errs, err)
			}
		}
		return errors.Join(errs...)
	}}
}

type runner struct {
	pool *pgxpool.Pool
	now  func() time.Time

	mu         sync.Mutex
	discovered map[int32]time.Time // last raw scan for sites without state
}

// site rolls what is due for one site, oldest hour first, and stops at the
// first failure so later hours never get ahead of a failed one.
func (r *runner) site(ctx context.Context, siteID int32, loc *time.Location) error {
	if loc == nil {
		loc = time.UTC
	}
	due, err := r.due(ctx, siteID, loc, r.now())
	if err != nil {
		return err
	}
	for i, h := range due {
		if i >= maxHoursPerSite {
			break
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := RecomputeHour(ctx, r.pool, siteID, loc, h); err != nil {
			return fmt.Errorf("hour %s: %w", h.Format("2006-01-02T15"), err)
		}
	}
	return nil
}

// due lists local hours to roll, ascending.
func (r *runner) due(ctx context.Context, siteID int32, loc *time.Location, now time.Time) ([]time.Time, error) {
	localNow := Wall(now.In(loc))
	rows, err := r.pool.Query(ctx, `
select local_hour, status, updated_at from span_hour_state
where site_id = $1 and (status = 'dirty' or local_hour >= $2)`, siteID, localNow.Add(-6*time.Hour))
	if err != nil {
		return nil, fmt.Errorf("read span hour state: %w", err)
	}
	set := map[time.Time]struct{}{}
	for rows.Next() {
		var h, updated time.Time
		var status string
		if err := rows.Scan(&h, &status, &updated); err != nil {
			rows.Close()
			return nil, err
		}
		h = Wall(h)
		end, ok := hourEnd(h, loc)
		switch {
		case status == "dirty":
			set[h] = struct{}{}
		case ok && !now.Before(end.Add(settleAfter)) && updated.Before(end.Add(settleAfter)):
			set[h] = struct{}{}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// New closed hours after the newest rolled hour.
	var last *time.Time
	if err := r.pool.QueryRow(ctx, `select max(local_hour) from span_hour_state where site_id = $1 and status = 'done'`, siteID).Scan(&last); err != nil {
		return nil, fmt.Errorf("read newest span hour: %w", err)
	}
	var first time.Time
	if last != nil {
		first = Wall(*last).Add(time.Hour)
		if floor := Wall(now.Add(-maxBackfill).In(loc)); first.Before(floor) {
			first = floor
		}
	} else {
		// Nothing rolled yet: find the oldest raw span, at most every
		// discoverEvery so a site without spans costs one cheap scan.
		r.mu.Lock()
		at, seen := r.discovered[siteID]
		if seen && now.Sub(at) < discoverEvery {
			r.mu.Unlock()
			return sorted(set), nil
		}
		r.discovered[siteID] = now
		r.mu.Unlock()
		var oldest *time.Time
		if err := r.pool.QueryRow(ctx, `select min(start_ts) from spans where site_id = $1 and start_ts >= $2`,
			siteID, now.Add(-maxBackfill)).Scan(&oldest); err != nil {
			return nil, fmt.Errorf("find oldest span: %w", err)
		}
		if oldest == nil {
			return sorted(set), nil
		}
		first = Wall(oldest.In(loc))
	}
	for h := first; len(set) < maxHoursPerSite*2; h = h.Add(time.Hour) {
		end, ok := hourEnd(h, loc)
		if ok && now.Before(end.Add(closeSlack)) {
			break
		}
		if !ok && h.After(localNow) {
			break
		}
		if ok {
			set[h] = struct{}{}
		}
	}
	return sorted(set), nil
}

func sorted(set map[time.Time]struct{}) []time.Time {
	out := make([]time.Time, 0, len(set))
	for h := range set {
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out
}
