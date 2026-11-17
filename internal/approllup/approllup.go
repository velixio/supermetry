// Package approllup rebuilds the App Health daily and hourly rollups from the
// raw app_events table. Every run replaces whole buckets, so it is idempotent
// and a late event simply reopens its day.
package approllup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/scheduler"
	"supermetry/internal/sites"
)

// Every is how often the scheduler job reruns the recent days.
const Every = 10 * time.Minute

// lockClass namespaces the per-site advisory lock that serialises recomputes.
const lockClass = 0x41524c // "ARL"

// CrashPredicate is the SQL crash definition for a column prefix (such as "e."
// or ""): a crash event is kind crash or level fatal. Session pings are
// liveness markers and never count as events or crashes.
func CrashPredicate(prefix string) string {
	return "(" + prefix + "kind <> 'session' and (" + prefix + "kind = 'crash' or " + prefix + "level = 'fatal'))"
}

// SessionsExpr counts sessions for a column prefix: distinct session hashes
// of session rows (a retried ping counts once), plus the occurrences of
// session rows without a hash, which are Sentry aggregates standing for that
// many sessions.
func SessionsExpr(prefix string) string {
	return "(count(distinct " + prefix + "session_hash) filter (where " + prefix + "kind = 'session')" +
		" + coalesce(sum(" + prefix + "occurrences) filter (where " + prefix + "kind = 'session' and " + prefix + "session_hash is null), 0))"
}

// CrashedSessionsExpr counts crashed sessions: sessions with a ping that saw a
// crash event (alias p lists the ping hashes), session rows that report a
// crashed exit, and the occurrences of hash-less (aggregate) rows that do.
func CrashedSessionsExpr(prefix string) string {
	crashedRow := prefix + "kind = 'session' and " + prefix + "exit_reason = 'crashed'"
	return "(count(distinct " + prefix + "session_hash) filter (where (" + CrashPredicate(prefix) + " and p.session_hash is not null) or (" + crashedRow + "))" +
		" + coalesce(sum(" + prefix + "occurrences) filter (where " + crashedRow + " and " + prefix + "session_hash is null), 0))"
}

func location(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.UTC
	}
	return loc
}

// DayBounds returns the half-open UTC interval of a site-local calendar day.
// Only the year, month and day of day are used. Bounds are computed in Go so
// the database needs no time zone data for the site's zone.
func DayBounds(day time.Time, loc *time.Location) (start, end time.Time) {
	y, m, d := day.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, loc), time.Date(y, m, d+1, 0, 0, 0, 0, loc)
}

// RecomputeDay replaces the site's rollup rows for one site-local day in one
// transaction. The hourly table is keyed by UTC hour, and a site whose offset
// is not a whole number of hours has an hour that straddles local midnight, so
// every whole hour the day touches is recomputed from all events in that hour:
// both neighbouring days then write the same count.
func RecomputeDay(ctx context.Context, pool *pgxpool.Pool, siteID int32, day time.Time) error {
	var zone string
	if err := pool.QueryRow(ctx, `select timezone from sites where id = $1`, siteID).Scan(&zone); err != nil {
		return fmt.Errorf("load site time zone: %w", err)
	}
	start, end := DayBounds(day, location(zone))
	hourFrom := start.UTC().Truncate(time.Hour)
	hourTo := end.UTC().Truncate(time.Hour)
	if hourTo.Before(end.UTC()) {
		hourTo = hourTo.Add(time.Hour)
	}
	date := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock($1, $2)`, int32(lockClass), siteID); err != nil {
			return fmt.Errorf("lock app rollups: %w", err)
		}
		statements := []struct {
			name string
			sql  string
			args []any
		}{
			{"clear issue daily", `delete from app_issue_daily where site_id = $1 and day = $2`, []any{siteID, date}},
			{"insert issue daily", `
insert into app_issue_daily (site_id, issue_id, day, events, installs, crashes)
select site_id, issue_id, $2::date,
       coalesce(sum(occurrences), 0),
       count(distinct install_hash),
       coalesce(sum(occurrences) filter (where ` + CrashPredicate("") + `), 0)
from app_events
where site_id = $1 and ts >= $3 and ts < $4 and issue_id is not null and kind <> 'session'
group by site_id, issue_id`, []any{siteID, date, start, end}},
			{"clear release daily", `delete from app_release_daily where site_id = $1 and day = $2`, []any{siteID, date}},
			{"insert release daily", `
with pings as (
  select distinct session_hash from app_events
  where site_id = $1 and ts >= $3 and ts < $4 and kind = 'session' and session_hash is not null
)
insert into app_release_daily (site_id, day, release, platform, events, crashes, installs, crashed_installs, sessions, crashed_sessions)
select e.site_id, $2::date, e.release, e.platform,
       coalesce(sum(e.occurrences) filter (where e.kind <> 'session'), 0),
       coalesce(sum(e.occurrences) filter (where ` + CrashPredicate("e.") + `), 0),
       count(distinct e.install_hash),
       count(distinct e.install_hash) filter (where ` + CrashPredicate("e.") + `),
       ` + SessionsExpr("e.") + `,
       ` + CrashedSessionsExpr("e.") + `
from app_events e
left join pings p on p.session_hash = e.session_hash
where e.site_id = $1 and e.ts >= $3 and e.ts < $4
group by e.site_id, e.release, e.platform`, []any{siteID, date, start, end}},
			{"clear session daily", `delete from app_session_daily where site_id = $1 and day = $2`, []any{siteID, date}},
			{"insert session daily", `
with s as (
  select release, environment, platform,
         count(*) as sessions,
         count(*) filter (where status = 'errored') as errored,
         count(*) filter (where status = 'crashed') as crashed,
         count(*) filter (where status = 'abnormal') as abnormal,
         count(distinct install_hash) as users,
         count(distinct install_hash) filter (where status = 'crashed') as crashed_users,
         count(distinct install_hash) filter (where status = 'errored') as errored_users
  from app_sessions
  where site_id = $1 and started >= $3 and started < $4
  group by release, environment, platform
), a as (
  select release, environment, platform,
         sum(occurrences) as sessions,
         coalesce(sum(occurrences) filter (where exit_reason = 'errored'), 0) as errored,
         coalesce(sum(occurrences) filter (where exit_reason = 'crashed'), 0) as crashed,
         coalesce(sum(occurrences) filter (where exit_reason = 'abnormal'), 0) as abnormal
  from app_events
  where site_id = $1 and ts >= $3 and ts < $4 and kind = 'session' and session_hash is null
  group by release, environment, platform
)
insert into app_session_daily (site_id, day, release, environment, platform, sessions, errored, crashed, abnormal, users, crashed_users, errored_users)
select $1, $2::date, coalesce(s.release, a.release), coalesce(s.environment, a.environment), coalesce(s.platform, a.platform),
       coalesce(s.sessions, 0) + coalesce(a.sessions, 0),
       coalesce(s.errored, 0) + coalesce(a.errored, 0),
       coalesce(s.crashed, 0) + coalesce(a.crashed, 0),
       coalesce(s.abnormal, 0) + coalesce(a.abnormal, 0),
       coalesce(s.users, 0), coalesce(s.crashed_users, 0), coalesce(s.errored_users, 0)
from s full join a on a.release = s.release and a.environment = s.environment and a.platform = s.platform`, []any{siteID, date, start, end}},
			{"clear issue hourly", `delete from app_issue_hourly where site_id = $1 and hour >= $2 and hour < $3`, []any{siteID, hourFrom, hourTo}},
			{"insert issue hourly", `
insert into app_issue_hourly (site_id, issue_id, hour, events)
select site_id, issue_id, to_timestamp(floor(extract(epoch from ts) / 3600) * 3600), sum(occurrences)
from app_events
where site_id = $1 and ts >= $2 and ts < $3 and issue_id is not null and kind <> 'session'
group by site_id, issue_id, 3`, []any{siteID, hourFrom, hourTo}},
		}
		for _, s := range statements {
			if _, err := tx.Exec(ctx, s.sql, s.args...); err != nil {
				return fmt.Errorf("%s: %w", s.name, err)
			}
		}
		return nil
	})
}

// Target is one app project to refresh and how many past days to reopen.
type Target struct {
	SiteID       int32
	TZ           *time.Location
	LookbackDays int
}

// RecomputeRecent recomputes, for each target, the site-local today and the
// LookbackDays days before it, so late events reopen closed days. It keeps
// going after a failing day and returns the number of days done plus the
// joined errors.
func RecomputeRecent(ctx context.Context, pool *pgxpool.Pool, targets []Target, now time.Time) (int, error) {
	var errs []error
	done := 0
	for _, t := range targets {
		loc := t.TZ
		if loc == nil {
			var zone string
			if err := pool.QueryRow(ctx, `select timezone from sites where id = $1`, t.SiteID).Scan(&zone); err != nil {
				errs = append(errs, fmt.Errorf("site %d: %w", t.SiteID, err))
				continue
			}
			loc = location(zone)
		}
		today := now.In(loc)
		for back := 0; back <= max(t.LookbackDays, 0); back++ {
			if err := ctx.Err(); err != nil {
				return done, errors.Join(append(errs, err)...)
			}
			d := time.Date(today.Year(), today.Month(), today.Day()-back, 0, 0, 0, 0, time.UTC)
			if err := RecomputeDay(ctx, pool, t.SiteID, d); err != nil {
				errs = append(errs, fmt.Errorf("site %d day %s: %w", t.SiteID, d.Format(time.DateOnly), err))
				continue
			}
			done++
		}
	}
	return done, errors.Join(errs...)
}

// Job returns the scheduler job that refreshes every live app project.
func Job(pool *pgxpool.Pool, registry sites.Registry, now func() time.Time) scheduler.Job {
	return scheduler.Job{Name: "app-rollups", Every: Every, Run: func(ctx context.Context) error {
		var targets []Target
		for _, s := range registry.All() {
			if s.Kind != sites.KindApp || s.App == nil {
				continue
			}
			targets = append(targets, Target{SiteID: s.ID, TZ: s.TZ, LookbackDays: s.App.MaxReportAgeDays})
		}
		days, err := RecomputeRecent(ctx, pool, targets, now())
		if err != nil {
			slog.Error("app rollups", "days", days, "error", err)
		}
		return err
	}}
}

// RecomputeAll rebuilds every day, from the site's first event to today, of
// every live app project. The recompute-rollups command uses it.
func RecomputeAll(ctx context.Context, pool *pgxpool.Pool, now time.Time) (int, error) {
	rows, err := pool.Query(ctx, `
select s.id, s.timezone, (select min(ts) from app_events e where e.site_id = s.id)
from sites s where s.kind = 'app' and s.deleted_at is null`)
	if err != nil {
		return 0, fmt.Errorf("list app projects: %w", err)
	}
	type site struct {
		id    int32
		zone  string
		first *time.Time
	}
	var all []site
	for rows.Next() {
		var s site
		if err := rows.Scan(&s.id, &s.zone, &s.first); err != nil {
			rows.Close()
			return 0, err
		}
		all = append(all, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	var errs []error
	done := 0
	for _, s := range all {
		if s.first == nil {
			continue
		}
		loc := location(s.zone)
		first, today := s.first.In(loc), now.In(loc)
		from := time.Date(first.Year(), first.Month(), first.Day(), 0, 0, 0, 0, time.UTC)
		to := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)
		for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
			if err := ctx.Err(); err != nil {
				return done, errors.Join(append(errs, err)...)
			}
			if err := RecomputeDay(ctx, pool, s.id, d); err != nil {
				errs = append(errs, fmt.Errorf("site %d day %s: %w", s.id, d.Format(time.DateOnly), err))
				continue
			}
			done++
		}
	}
	return done, errors.Join(errs...)
}
