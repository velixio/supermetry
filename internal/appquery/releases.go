package appquery

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/apprelease"
)

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// ReleaseRow is one release of the releases table. FirstSeen and LastSeen span
// the whole retention window; the other numbers follow the range and filters.
// Events without a release are left out.
type ReleaseRow struct {
	Release           string    `json:"release"`
	FirstSeen         time.Time `json:"first_seen"`
	LastSeen          time.Time `json:"last_seen"`
	Installs          int64     `json:"installs"`
	CrashFreeUsers    *float64  `json:"crash_free_users"`
	CrashFreeSessions *float64  `json:"crash_free_sessions"`
	NewIssues         int64     `json:"new_issues"`
	RegressedIssues   int64     `json:"regressed_issues"`
}

// Releases lists the releases with events in the range, newest first
// (apprelease ordering). New issues are the site's issues whose first release
// is this one; regressed issues are regressed issues whose last release is this
// one.
func (q *Querier) Releases(ctx context.Context, scope Scope, f Filter) ([]ReleaseRow, error) {
	w, err := q.window(scope, f)
	if err != nil {
		return nil, err
	}
	groups, err := q.healthBy(ctx, w, " and release <> ''", "release")
	if err != nil {
		return nil, err
	}
	rows := make([]ReleaseRow, 0, len(groups))
	names := make([]string, 0, len(groups))
	byName := map[string]*ReleaseRow{}
	for _, g := range groups {
		rows = append(rows, ReleaseRow{Release: g.keys[0], Installs: g.Installs, CrashFreeUsers: g.crashFreeUsers(), CrashFreeSessions: g.crashFreeSessions()})
		names = append(names, g.keys[0])
	}
	for i := range rows {
		byName[rows[i].Release] = &rows[i]
	}
	if len(rows) == 0 {
		return rows, nil
	}
	seen, err := q.releaseSpans(ctx, scope, names)
	if err != nil {
		return nil, err
	}
	firstSeen := map[string]time.Time{}
	for name, span := range seen {
		firstSeen[name] = span[0]
		if r := byName[name]; r != nil {
			r.FirstSeen, r.LastSeen = span[0], span[1]
		}
	}
	counts, err := q.pool.Query(ctx, `
select release, count(*) filter (where is_first), count(*) filter (where is_last and status = 'regressed') from (
  select first_release as release, true as is_first, false as is_last, status from app_issues where site_id = $1 and merged_into is null and first_release = any($2)
  union all
  select last_release, false, true, status from app_issues where site_id = $1 and merged_into is null and last_release = any($2)
) t group by release`, scope.SiteID, names)
	if err != nil {
		return nil, fmt.Errorf("count release issues: %w", err)
	}
	defer counts.Close()
	for counts.Next() {
		var name string
		var newIssues, regressed int64
		if err := counts.Scan(&name, &newIssues, &regressed); err != nil {
			return nil, err
		}
		if r := byName[name]; r != nil {
			r.NewIssues, r.RegressedIssues = newIssues, regressed
		}
	}
	if err := counts.Err(); err != nil {
		return nil, err
	}
	order := apprelease.SortNewestFirst(names, firstSeen)
	sorted := make([]ReleaseRow, 0, len(rows))
	for _, name := range order {
		sorted = append(sorted, *byName[name])
	}
	return sorted, nil
}

// releaseSpans returns the first and last event time per release inside the
// retention window.
func (q *Querier) releaseSpans(ctx context.Context, scope Scope, names []string) (map[string][2]time.Time, error) {
	var cutoff time.Time
	if scope.RetentionDays > 0 {
		cutoff = q.now().AddDate(0, 0, -scope.RetentionDays)
	}
	rows, err := q.pool.Query(ctx, `select release, min(ts), max(ts) from app_events
where site_id = $1 and release = any($2) and ($3::timestamptz is null or ts >= $3) group by release`, scope.SiteID, names, nullTime(cutoff))
	if err != nil {
		return nil, fmt.Errorf("read release spans: %w", err)
	}
	defer rows.Close()
	out := map[string][2]time.Time{}
	for rows.Next() {
		var name string
		var span [2]time.Time
		if err := rows.Scan(&name, &span[0], &span[1]); err != nil {
			return nil, err
		}
		out[name] = span
	}
	return out, rows.Err()
}

func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}
