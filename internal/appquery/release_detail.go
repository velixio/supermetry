package appquery

import (
	"context"
	"fmt"
	"time"
)

// maxReleaseIssues bounds the new and regressed issue lists of a release.
const maxReleaseIssues = 50

// RolloutPoint is one day of a release's rollout. Adoption is the release's
// sessions divided by all sessions of the site that day (same environment and
// platform filter).
type RolloutPoint struct {
	Day               string   `json:"day"`
	Sessions          int64    `json:"sessions"`
	Adoption          *float64 `json:"adoption"`
	CrashFreeSessions *float64 `json:"crash_free_sessions"`
	CrashFreeUsers    *float64 `json:"crash_free_users"`
}

// ReleaseDetail is everything the release page shows. Created, Status and Ref
// come from the release API row; a release known only from events has no row
// (Created is nil, Status "open").
type ReleaseDetail struct {
	Release         string         `json:"release"`
	FirstSeen       *time.Time     `json:"first_seen"`
	LastSeen        *time.Time     `json:"last_seen"`
	Created         *time.Time     `json:"created"`
	Status          string         `json:"status"`
	Ref             string         `json:"ref"`
	Health          ReleaseRow     `json:"health"`
	Rollout         []RolloutPoint `json:"rollout"`
	Deploys         []Deploy       `json:"deploys"`
	NewIssues       []TopIssue     `json:"new_issues"`
	RegressedIssues []TopIssue     `json:"regressed_issues"`
	CommitCount     int            `json:"commit_count"`
	HealthSource    string         `json:"health_source"`
}

// ReleaseDetail reads one release for the site. ErrNotFound means it has no
// release row visible to the site and no data (events or rollups) either.
func (q *Querier) ReleaseDetail(ctx context.Context, scope Scope, f Filter, release string) (ReleaseDetail, error) {
	f.Release = release
	w, err := q.window(scope, f)
	if err != nil {
		return ReleaseDetail{}, err
	}
	out := ReleaseDetail{Release: release, Status: "open", Rollout: []RolloutPoint{}, Deploys: []Deploy{}, NewIssues: []TopIssue{}, RegressedIssues: []TopIssue{}}

	var created *time.Time
	var rowID int64
	var status, ref string
	hasRow := true
	if err := q.pool.QueryRow(ctx, `
select id, date_created, status, ref from app_releases
where org_id = $1 and version = $2 and (cardinality(site_ids) = 0 or $3 = any(site_ids))`,
		scope.OrgID, release, scope.SiteID).Scan(&rowID, &created, &status, &ref); err != nil {
		if !isNoRows(err) {
			return out, fmt.Errorf("read release row: %w", err)
		}
		hasRow = false
	}
	if hasRow {
		out.Created, out.Status, out.Ref = created, status, ref
		if err := q.pool.QueryRow(ctx, `select count(*) from app_release_commits where release_id = $1`, rowID).Scan(&out.CommitCount); err != nil {
			return out, fmt.Errorf("count release commits: %w", err)
		}
	}

	spans, err := q.releaseSpans(ctx, scope, []string{release})
	if err != nil {
		return out, err
	}
	if span, ok := spans[release]; ok {
		first, last := span[0], span[1]
		out.FirstSeen, out.LastSeen = &first, &last
	}
	if !hasRow && out.FirstSeen == nil {
		var data bool
		if err := q.pool.QueryRow(ctx, `select exists(select 1 from app_session_daily where site_id = $1 and release = $2 and day >= $3)
or exists(select 1 from app_release_daily where site_id = $1 and release = $2 and day >= $3)`,
			scope.SiteID, release, w.cutoffDay).Scan(&data); err != nil {
			return out, fmt.Errorf("check release data: %w", err)
		}
		if !data {
			return ReleaseDetail{}, ErrNotFound
		}
	}

	if out.Health, err = q.releaseHealth(ctx, w, scope, release); err != nil {
		return out, err
	}
	if out.Rollout, out.HealthSource, err = q.rollout(ctx, w, release); err != nil {
		return out, err
	}
	if out.HealthSource == "" {
		out.HealthSource = out.Health.HealthSource
	}
	if out.Deploys, err = q.Deploys(ctx, scope, f, release); err != nil {
		return out, err
	}
	if out.NewIssues, err = q.releaseIssues(ctx, scope.SiteID, release, false, maxReleaseIssues); err != nil {
		return out, err
	}
	if out.RegressedIssues, err = q.releaseIssues(ctx, scope.SiteID, release, true, maxReleaseIssues); err != nil {
		return out, err
	}
	var newCount, regressedCount int64
	if err := q.pool.QueryRow(ctx, `
select count(*) filter (where first_release = $2), count(*) filter (where last_release = $2 and status = 'regressed')
from app_issues where site_id = $1 and merged_into is null and (first_release = $2 or last_release = $2)`,
		scope.SiteID, release).Scan(&newCount, &regressedCount); err != nil {
		return out, fmt.Errorf("count release issues: %w", err)
	}
	out.Health.NewIssues, out.Health.RegressedIssues = newCount, regressedCount
	return out, nil
}

// releaseHealth is the release's row of the releases table; a release without
// events in the range (rollup rows only, or nothing) gets the session numbers
// alone.
func (q *Querier) releaseHealth(ctx context.Context, w window, scope Scope, release string) (ReleaseRow, error) {
	rows, err := q.Releases(ctx, scope, w.f)
	if err != nil {
		return ReleaseRow{}, err
	}
	for _, r := range rows {
		if r.Release == release {
			return r, nil
		}
	}
	row := ReleaseRow{Release: release, HealthSource: HealthEvents}
	stats, err := q.sessionStatsBy(ctx, w, true)
	if err != nil {
		return row, err
	}
	if st := stats[release]; st != nil {
		applySessionStats(&row.Sessions, &row.Users, &row.AbnormalSessions, &row.ErroredSessions, &row.CrashFreeSessions, &row.CrashFreeUsers, &row.HealthSource, st)
	}
	return row, nil
}

// rollout reads the per-day adoption and crash-free series in two statements:
// app_session_daily for the days the site has session rollups, and
// app_release_daily (v1 pings) for the other days. The source is "sessions"
// when any day came from the session rollup, else "events" when any day came
// from v1, else empty.
func (q *Querier) rollout(ctx context.Context, w window, release string) ([]RolloutPoint, string, error) {
	rows, err := q.pool.Query(ctx, `
select day,
  coalesce(sum(sessions) filter (where release = $6), 0)::bigint,
  sum(sessions)::bigint,
  coalesce(sum(crashed) filter (where release = $6), 0)::bigint,
  coalesce(sum(users) filter (where release = $6), 0)::bigint,
  coalesce(sum(crashed_users) filter (where release = $6), 0)::bigint
from app_session_daily
where site_id = $1 and day >= $2 and day <= $3 and day >= $4 and ($5 = '' or environment = $5) and ($7 = '' or platform = $7)
group by day order by day`, w.scope.SiteID, w.fromDay, w.toDay, w.cutoffDay, w.f.Environment, release, w.f.Platform)
	if err != nil {
		return nil, "", fmt.Errorf("read rollout: %w", err)
	}
	defer rows.Close()
	points := []RolloutPoint{}
	rollupDays := map[string]bool{}
	for rows.Next() {
		var day time.Time
		var sessions, total, crashed, users, crashedUsers int64
		if err := rows.Scan(&day, &sessions, &total, &crashed, &users, &crashedUsers); err != nil {
			return nil, "", err
		}
		key := day.Format(time.DateOnly)
		rollupDays[key] = true
		if sessions == 0 {
			continue
		}
		p := RolloutPoint{Day: key, Sessions: sessions, CrashFreeSessions: ratio(crashed, sessions), CrashFreeUsers: ratio(crashedUsers, users)}
		if total > 0 {
			a := float64(sessions) / float64(total)
			p.Adoption = &a
		}
		points = append(points, p)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	source := ""
	if len(points) > 0 {
		source = HealthSessions
	}
	v1, err := q.pool.Query(ctx, `
select day,
  coalesce(sum(sessions) filter (where release = $5), 0)::bigint,
  sum(sessions)::bigint,
  coalesce(sum(crashed_sessions) filter (where release = $5), 0)::bigint
from app_release_daily
where site_id = $1 and day >= $2 and day <= $3 and day >= $4 and ($6 = '' or platform = $6)
group by day order by day`, w.scope.SiteID, w.fromDay, w.toDay, w.cutoffDay, release, w.f.Platform)
	if err != nil {
		return nil, "", fmt.Errorf("read v1 rollout: %w", err)
	}
	defer v1.Close()
	added := false
	for v1.Next() {
		var day time.Time
		var sessions, total, crashed int64
		if err := v1.Scan(&day, &sessions, &total, &crashed); err != nil {
			return nil, "", err
		}
		key := day.Format(time.DateOnly)
		if rollupDays[key] || sessions == 0 {
			continue
		}
		p := RolloutPoint{Day: key, Sessions: sessions, CrashFreeSessions: ratio(crashed, sessions)}
		if total > 0 {
			a := float64(sessions) / float64(total)
			p.Adoption = &a
		}
		points = append(points, p)
		added = true
	}
	if err := v1.Err(); err != nil {
		return nil, "", err
	}
	if added {
		if source == "" {
			source = HealthEvents
		}
		sortPoints(points)
	}
	return points, source, nil
}

func sortPoints(p []RolloutPoint) {
	for i := 1; i < len(p); i++ {
		for j := i; j > 0 && p[j].Day < p[j-1].Day; j-- {
			p[j], p[j-1] = p[j-1], p[j]
		}
	}
}

// releaseIssues lists the site's issues that first appeared in the release
// (or, with regressed, regressed issues last seen in it), most events first.
func (q *Querier) releaseIssues(ctx context.Context, siteID int32, release string, regressed bool, limit int) ([]TopIssue, error) {
	col, extra := "first_release", ""
	if regressed {
		col, extra = "last_release", " and status = 'regressed'"
	}
	rows, err := q.pool.Query(ctx, `
select id, title, event_count from app_issues
where site_id = $1 and merged_into is null and `+col+` = $2`+extra+`
order by event_count desc, id limit $3`, siteID, release, limit)
	if err != nil {
		return nil, fmt.Errorf("read release issues: %w", err)
	}
	defer rows.Close()
	out := []TopIssue{}
	for rows.Next() {
		var t TopIssue
		if err := rows.Scan(&t.ID, &t.Title, &t.Events); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
