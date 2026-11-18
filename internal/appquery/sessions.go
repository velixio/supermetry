package appquery

import (
	"context"
	"fmt"
	"time"

	"supermetry/internal/approllup"
)

// Health sources of a release or overview row.
const (
	// HealthSessions marks numbers computed from tracked session lifecycles
	// (app_session_daily and app_sessions).
	HealthSessions = "sessions"
	// HealthEvents marks the v1 computation from session pings in app_events,
	// used when no day of the range has session rollup data.
	HealthEvents = "events"
)

// sessionStats is the release-health v2 count set for one key (a release, or
// "" for the whole range). Abnormal sessions (ANR, hang, watchdog) are not
// crashes: crash-free sessions = 1 - crashed/sessions, crash-free users =
// 1 - crashed_users/users.
type sessionStats struct {
	Sessions, Errored, Crashed, Abnormal int64
	Users, CrashedUsers                  int64
}

func (s *sessionStats) crashFreeSessions() *float64 { return ratio(s.Crashed, s.Sessions) }

// crashFreeUsers is nil when no session carried an install (aggregates have no
// did), so callers can fall back.
func (s *sessionStats) crashFreeUsers() *float64 { return ratio(s.CrashedUsers, s.Users) }

// sessionStatsBy reads the session-based counts for the range. A day uses
// session data when app_session_daily has a row for that key on that day;
// otherwise the v1 ping computation fills in that day's sessions and crashed
// sessions. Keys without any rollup row are absent from the result: callers
// keep the v1 numbers (source "events"). perRelease groups by release (empty
// releases left out); otherwise the single key is "".
func (q *Querier) sessionStatsBy(ctx context.Context, w window, perRelease bool) (map[string]*sessionStats, error) {
	rel, relWhere := "''", ""
	if perRelease {
		rel, relWhere = "release", " and release <> ''"
	}
	rows, err := q.pool.Query(ctx, `
select `+rel+`, day, sum(sessions), sum(errored), sum(crashed), sum(abnormal)
from app_session_daily
where site_id = $1 and day >= $2 and day <= $3 and ($4 = '' or release = $4) and ($5 = '' or environment = $5) and ($6 = '' or platform = $6)`+relWhere+`
group by 1, day`, w.scope.SiteID, w.cutoffDay, w.toDay, w.f.Release, w.f.Environment, w.f.Platform)
	if err != nil {
		return nil, fmt.Errorf("read session rollups: %w", err)
	}
	defer rows.Close()
	out := map[string]*sessionStats{}
	have := map[string]map[string]bool{} // key -> local days with rollup data
	for rows.Next() {
		var key string
		var day time.Time
		var s sessionStats
		if err := rows.Scan(&key, &day, &s.Sessions, &s.Errored, &s.Crashed, &s.Abnormal); err != nil {
			return nil, err
		}
		acc := out[key]
		if acc == nil {
			acc = &sessionStats{}
			out[key] = acc
			have[key] = map[string]bool{}
		}
		acc.Sessions += s.Sessions
		acc.Errored += s.Errored
		acc.Crashed += s.Crashed
		acc.Abnormal += s.Abnormal
		have[key][day.Format(time.DateOnly)] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return out, nil
	}
	if err := q.fillV1Days(ctx, w, perRelease, out, have); err != nil {
		return nil, err
	}
	return out, q.sessionUsers(ctx, w, perRelease, out)
}

// fillV1Days adds the v1 sessions of the days a key has no rollup row for.
func (q *Querier) fillV1Days(ctx context.Context, w window, perRelease bool, out map[string]*sessionStats, have map[string]map[string]bool) error {
	missing := false
	for key := range out {
		for _, d := range w.days {
			if d >= w.cutoffDay.Format(time.DateOnly) && !have[key][d] {
				missing = true
			}
		}
	}
	if !missing {
		return nil
	}
	rel, relWhere := "''", ""
	if perRelease {
		rel, relWhere = "release", " and release <> ''"
	}
	rows, err := q.pool.Query(ctx, `
with ev as (
  select kind, level, occurrences, session_hash, exit_reason, `+rel+` as rel, width_bucket(ts, $7::timestamptz[]) as bucket
  from app_events where `+eventWhere+relWhere+`
), pings as (
  select distinct session_hash from ev where kind = 'session' and session_hash is not null
)
select e.rel, e.bucket, `+approllup.SessionsExpr("e.")+`, `+approllup.CrashedSessionsExpr("e.")+`
from ev e left join pings p on p.session_hash = e.session_hash
group by e.rel, e.bucket`, append(w.evArgs(), w.dayStarts)...)
	if err != nil {
		return fmt.Errorf("read v1 session days: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var bucket int
		var sessions, crashed int64
		if err := rows.Scan(&key, &bucket, &sessions, &crashed); err != nil {
			return err
		}
		acc := out[key]
		if acc == nil || bucket < 1 || bucket > len(w.days) || have[key][w.days[bucket-1]] {
			continue
		}
		acc.Sessions += sessions
		acc.Crashed += crashed
	}
	return rows.Err()
}

// sessionUsers counts distinct installs (and crashed installs) of the range
// from app_sessions: distinct counts cannot be summed from the daily rollup.
func (q *Querier) sessionUsers(ctx context.Context, w window, perRelease bool, out map[string]*sessionStats) error {
	rel, relWhere := "''", ""
	if perRelease {
		rel, relWhere = "release", " and release <> ''"
	}
	rows, err := q.pool.Query(ctx, `
select `+rel+`, count(distinct install_hash), count(distinct install_hash) filter (where status = 'crashed')
from app_sessions
where site_id = $1 and started >= $2 and started < $3 and ($4 = '' or release = $4) and ($5 = '' or environment = $5) and ($6 = '' or platform = $6)`+relWhere+`
group by 1`, w.scope.SiteID, w.start, w.end, w.f.Release, w.f.Environment, w.f.Platform)
	if err != nil {
		return fmt.Errorf("read session users: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var users, crashed int64
		if err := rows.Scan(&key, &users, &crashed); err != nil {
			return err
		}
		if acc := out[key]; acc != nil {
			acc.Users, acc.CrashedUsers = users, crashed
		}
	}
	return rows.Err()
}

// handledBy sums non-session event occurrences by the handled flag, per
// release or in total (key "").
func (q *Querier) handledBy(ctx context.Context, w window, perRelease bool) (map[string][2]int64, error) {
	rel, relWhere := "''", ""
	if perRelease {
		rel, relWhere = "release", " and release <> ''"
	}
	rows, err := q.pool.Query(ctx, `
select `+rel+`, coalesce(sum(occurrences) filter (where handled), 0), coalesce(sum(occurrences) filter (where not handled), 0)
from app_events where `+eventWhere+` and kind <> 'session'`+relWhere+` group by 1`, w.evArgs()...)
	if err != nil {
		return nil, fmt.Errorf("read handled split: %w", err)
	}
	defer rows.Close()
	out := map[string][2]int64{}
	for rows.Next() {
		var key string
		var v [2]int64
		if err := rows.Scan(&key, &v[0], &v[1]); err != nil {
			return nil, err
		}
		out[key] = v
	}
	return out, rows.Err()
}

// adoption is each release's share of the site's sessions started in the last
// 24 hours (environment and platform filters apply, the release filter and the
// range do not). Sessions are app_sessions rows plus Sentry aggregates; a site
// with neither falls back to v1 session pings. It is empty when the site had
// no sessions.
func (q *Querier) adoption(ctx context.Context, w window) (map[string]float64, error) {
	now := q.now()
	since := now.Add(-24 * time.Hour)
	if w.scope.RetentionDays > 0 {
		if c := now.AddDate(0, 0, -w.scope.RetentionDays); c.After(since) {
			since = c
		}
	}
	counts := map[string]int64{}
	run := func(query string) error {
		rows, err := q.pool.Query(ctx, query, w.scope.SiteID, since, now, w.f.Environment, w.f.Platform)
		if err != nil {
			return fmt.Errorf("read adoption: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var rel string
			var n int64
			if err := rows.Scan(&rel, &n); err != nil {
				return err
			}
			counts[rel] += n
		}
		return rows.Err()
	}
	if err := run(`select release, count(*) from app_sessions
where site_id = $1 and started >= $2 and started <= $3 and release <> '' and ($4 = '' or environment = $4) and ($5 = '' or platform = $5) group by release`); err != nil {
		return nil, err
	}
	if err := run(`select release, sum(occurrences)::bigint from app_events
where site_id = $1 and ts >= $2 and ts <= $3 and kind = 'session' and session_hash is null and release <> '' and ($4 = '' or environment = $4) and ($5 = '' or platform = $5) group by release`); err != nil {
		return nil, err
	}
	if len(counts) == 0 {
		if err := run(`select release, count(distinct session_hash) from app_events
where site_id = $1 and ts >= $2 and ts <= $3 and kind = 'session' and session_hash is not null and release <> '' and ($4 = '' or environment = $4) and ($5 = '' or platform = $5) group by release`); err != nil {
			return nil, err
		}
	}
	var total int64
	for _, n := range counts {
		total += n
	}
	out := map[string]float64{}
	if total == 0 {
		return out, nil
	}
	for rel, n := range counts {
		out[rel] = float64(n) / float64(total)
	}
	return out, nil
}
