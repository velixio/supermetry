package sitesvc

import (
	"context"
	"fmt"
	"time"
)

// loadHighlightFigures fills the extra facts the status line needs, with one
// grouped query per fact for all projects on the page (never per project). The
// top source reads closed days only, because the open day has no rollup.
func (s *Overview) loadHighlightFigures(ctx context.Context, sites []OverviewSite, values map[int32]overviewFigures, now time.Time, period OverviewPeriod) error {
	days := 1
	switch period {
	case Overview7Days:
		days = 7
	case Overview30Days:
		days = 30
	}
	ids := make([]int32, 0, len(sites))
	appIDs := make([]int32, 0)
	curFrom := make([]time.Time, 0, len(sites))
	prevFrom := make([]time.Time, 0, len(sites))
	today := make([]time.Time, 0, len(sites))
	for _, site := range sites {
		location, err := time.LoadLocation(site.Timezone)
		if err != nil {
			location = time.UTC
		}
		local := now.In(location)
		day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
		ids = append(ids, site.ID)
		today = append(today, day)
		curFrom = append(curFrom, day.AddDate(0, 0, -(days-1)))
		prevFrom = append(prevFrom, day.AddDate(0, 0, -(2*days-1)))
		if site.Kind == highlightKindApp {
			appIDs = append(appIDs, site.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	// Top referrer source: closed days of the window (the last closed day for 24h).
	sourceFrom := make([]time.Time, len(ids))
	sourceTo := make([]time.Time, len(ids))
	for index := range ids {
		sourceTo[index] = today[index].AddDate(0, 0, -1)
		sourceFrom[index] = today[index].AddDate(0, 0, -days)
	}
	rows, err := s.store.Pool.Query(ctx, topSourceSQL, ids, sourceFrom, sourceTo)
	if err != nil {
		return fmt.Errorf("query top sources: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var siteID int32
		var name string
		var visitors, total int64
		if err := rows.Scan(&siteID, &name, &visitors, &total); err != nil {
			return err
		}
		figure := values[siteID]
		figure.topSource, figure.topSourceVisitors, figure.sourceTotal = name, visitors, total
		values[siteID] = figure
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(appIDs) == 0 {
		return nil
	}
	// App projects: issues first seen in the window and crash-free users.
	startAt := make([]time.Time, 0, len(appIDs))
	for _, id := range appIDs {
		startAt = append(startAt, values[id].points[0].Start)
	}
	issueRows, err := s.store.Pool.Query(ctx, newIssuesSQL, appIDs, startAt, now)
	if err != nil {
		return fmt.Errorf("query new issues: %w", err)
	}
	defer issueRows.Close()
	for issueRows.Next() {
		var siteID int32
		var count int64
		if err := issueRows.Scan(&siteID, &count); err != nil {
			return err
		}
		figure := values[siteID]
		figure.newIssues = count
		values[siteID] = figure
	}
	if err := issueRows.Err(); err != nil {
		return err
	}
	appCur, appPrev, appToday := make([]time.Time, 0, len(appIDs)), make([]time.Time, 0, len(appIDs)), make([]time.Time, 0, len(appIDs))
	position := make(map[int32]int, len(ids))
	for index, id := range ids {
		position[id] = index
	}
	for _, id := range appIDs {
		index := position[id]
		appCur, appPrev, appToday = append(appCur, curFrom[index]), append(appPrev, prevFrom[index]), append(appToday, today[index])
	}
	crashRows, err := s.store.Pool.Query(ctx, crashFreeSQL, appIDs, appCur, appPrev, appToday)
	if err != nil {
		return fmt.Errorf("query crash-free users: %w", err)
	}
	defer crashRows.Close()
	for crashRows.Next() {
		var siteID int32
		var users, crashed, prevUsers, prevCrashed int64
		if err := crashRows.Scan(&siteID, &users, &crashed, &prevUsers, &prevCrashed); err != nil {
			return err
		}
		figure := values[siteID]
		if users > 0 && prevUsers > 0 {
			cur := 1 - float64(crashed)/float64(users)
			prev := 1 - float64(prevCrashed)/float64(prevUsers)
			figure.crashFree, figure.crashFreePrevious = &cur, &prev
		}
		values[siteID] = figure
	}
	return crashRows.Err()
}

const topSourceSQL = `
with w as (select * from unnest($1::int[], $2::date[], $3::date[]) as w(site_id, d_from, d_to)),
tot as (
  select w.site_id, sum(r.visitors)::bigint as total
  from w join rollup_day r on r.site_id=w.site_id and r.day between w.d_from and w.d_to
  group by w.site_id
),
src as (
  select w.site_id, r.value_id, sum(r.visitors)::bigint as visitors
  from w join rollup_day_dim r on r.site_id=w.site_id and r.dim=2 and r.value_id<>0 and r.day between w.d_from and w.d_to
  group by w.site_id, r.value_id
),
ranked as (
  select site_id, value_id, visitors, row_number() over (partition by site_id order by visitors desc, value_id) as rn from src
)
select k.site_id, d.value, k.visitors, t.total
from ranked k join tot t using (site_id) join dict d on d.kind=2 and d.id=k.value_id
where k.rn=1 and d.value<>'Other'`

const newIssuesSQL = `
select i.site_id, count(*)::bigint
from unnest($1::int[], $2::timestamptz[]) as w(site_id, since)
join app_issues i on i.site_id=w.site_id and i.first_seen >= w.since and i.first_seen <= $3::timestamptz
group by i.site_id`

const crashFreeSQL = `
select w.site_id,
       coalesce(sum(a.users) filter (where a.day >= w.cur_from),0)::bigint,
       coalesce(sum(a.crashed_users) filter (where a.day >= w.cur_from),0)::bigint,
       coalesce(sum(a.users) filter (where a.day < w.cur_from),0)::bigint,
       coalesce(sum(a.crashed_users) filter (where a.day < w.cur_from),0)::bigint
from unnest($1::int[], $2::date[], $3::date[], $4::date[]) as w(site_id, cur_from, prev_from, d_to)
join app_session_daily a on a.site_id=w.site_id and a.day between w.prev_from and w.d_to
group by w.site_id`
