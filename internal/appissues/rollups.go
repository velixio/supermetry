package appissues

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/approllup"
)

// rollupWindow is where the rollups can be rebuilt from events: from the
// first whole site-local day (and the first UTC hour) of the month of the
// site's oldest stored event. Older rollup rows outlive the events (retention drops event
// partitions first) and cannot be attributed to an issue again, so they are
// moved, never recomputed.
type rollupWindow struct {
	zone    string
	dayFrom time.Time // first site-local day, as a UTC midnight date
	dayTs   time.Time // start of dayFrom in the site's zone
	hourTs  time.Time // first whole UTC hour
}

func (s *Service) window(ctx context.Context, tx pgx.Tx, siteID int32) (rollupWindow, error) {
	var zone string
	var oldest *time.Time
	if err := tx.QueryRow(ctx, `select timezone, (select min(ts) from app_events where site_id = $1) from sites where id = $1`, siteID).Scan(&zone, &oldest); err != nil {
		return rollupWindow{}, fmt.Errorf("read rollup window: %w", err)
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		loc, zone = time.UTC, "UTC"
	}
	w := rollupWindow{zone: zone}
	if oldest == nil {
		far := time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)
		w.dayFrom, w.dayTs, w.hourTs = far, far, far
		return w, nil
	}
	// Retention drops whole monthly partitions, so every event of the oldest
	// stored event's month still exists: that month is fully covered.
	ou := oldest.UTC()
	month := time.Date(ou.Year(), ou.Month(), 1, 0, 0, 0, 0, time.UTC)
	start, end := approllup.DayBounds(month.In(loc), loc)
	if start.Before(month) {
		start = end
	}
	y, m, d := start.In(loc).Date()
	w.dayFrom = time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	w.dayTs = start
	w.hourTs = month
	return w, nil
}

// rebuildRollups replaces the issues' daily and hourly rows inside the window
// with sums recomputed from their events.
func (s *Service) rebuildRollups(ctx context.Context, tx pgx.Tx, siteID int32, ids []int64) error {
	w, err := s.window(ctx, tx, siteID)
	if err != nil {
		return err
	}
	return rebuild(ctx, tx, siteID, ids, w)
}

func rebuild(ctx context.Context, tx pgx.Tx, siteID int32, ids []int64, w rollupWindow) error {
	stmts := []struct {
		name string
		sql  string
		args []any
	}{
		{"clear issue daily", `delete from app_issue_daily where site_id = $1 and issue_id = any($2) and day >= $3`, []any{siteID, ids, w.dayFrom}},
		{"rebuild issue daily", `
insert into app_issue_daily (site_id, issue_id, day, events, installs, crashes)
select site_id, issue_id, (ts at time zone $4)::date,
       coalesce(sum(occurrences), 0), count(distinct install_hash),
       coalesce(sum(occurrences) filter (where ` + approllup.CrashPredicate("") + `), 0)
from app_events
where site_id = $1 and issue_id = any($2) and ts >= $3 and kind <> 'session'
group by site_id, issue_id, 3`, []any{siteID, ids, w.dayTs, w.zone}},
		{"clear issue hourly", `delete from app_issue_hourly where site_id = $1 and issue_id = any($2) and hour >= $3`, []any{siteID, ids, w.hourTs}},
		{"rebuild issue hourly", `
insert into app_issue_hourly (site_id, issue_id, hour, events)
select site_id, issue_id, to_timestamp(floor(extract(epoch from ts) / 3600) * 3600), sum(occurrences)
from app_events
where site_id = $1 and issue_id = any($2) and ts >= $3 and kind <> 'session'
group by site_id, issue_id, 3`, []any{siteID, ids, w.hourTs}},
	}
	for _, st := range stmts {
		if _, err := tx.Exec(ctx, st.sql, st.args...); err != nil {
			return fmt.Errorf("%s: %w", st.name, err)
		}
	}
	return nil
}

// foldRollups moves the sources' rollup rows into the target. Rows older than
// the window are added into the target's (upsert-add); rows inside it are
// recomputed from the events, which already moved.
func (s *Service) foldRollups(ctx context.Context, tx pgx.Tx, siteID int32, target int64, sources []int64) error {
	w, err := s.window(ctx, tx, siteID)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
insert into app_issue_daily (site_id, issue_id, day, events, installs, crashes)
select site_id, $3, day, sum(events), sum(installs), sum(crashes)
from app_issue_daily where site_id = $1 and issue_id = any($2) and day < $4
group by site_id, day
on conflict (site_id, issue_id, day) do update set
  events = app_issue_daily.events + excluded.events,
  installs = app_issue_daily.installs + excluded.installs,
  crashes = app_issue_daily.crashes + excluded.crashes`, siteID, sources, target, w.dayFrom); err != nil {
		return fmt.Errorf("fold issue daily: %w", err)
	}
	if _, err := tx.Exec(ctx, `
insert into app_issue_hourly (site_id, issue_id, hour, events)
select site_id, $3, hour, sum(events)
from app_issue_hourly where site_id = $1 and issue_id = any($2) and hour < $4
group by site_id, hour
on conflict (site_id, issue_id, hour) do update set events = app_issue_hourly.events + excluded.events`, siteID, sources, target, w.hourTs); err != nil {
		return fmt.Errorf("fold issue hourly: %w", err)
	}
	if _, err := tx.Exec(ctx, `delete from app_issue_daily where site_id = $1 and issue_id = any($2)`, siteID, sources); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `delete from app_issue_hourly where site_id = $1 and issue_id = any($2)`, siteID, sources); err != nil {
		return err
	}
	return rebuild(ctx, tx, siteID, []int64{target}, w)
}
