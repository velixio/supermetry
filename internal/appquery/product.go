package appquery

import (
	"context"
	"fmt"
	"time"

	"supermetry/internal/approllup"
	"supermetry/internal/dict"
)

// The product reads answer unique-install questions (DAU/WAU/MAU, adoption
// splits) from app_active_day, one row per install per site-local day, with
// the open day computed live from events by the same SELECT the rollup runs.
// Counts of distinct installs are never summed across days.

// DayCount is a count on one site-local day.
type DayCount struct {
	Day   time.Time `json:"day"`
	Count int64     `json:"count"`
}

// ActivePoint is one day of the active-install series. WAU and MAU are the
// distinct installs active in the 7 and 30 local days ending that day.
type ActivePoint struct {
	Day time.Time `json:"day"`
	DAU int64     `json:"dau"`
	WAU int64     `json:"wau"`
	MAU int64     `json:"mau"`
}

// ActiveUsers is the active-install answer for a range. Distinct counts the
// installs active at least once in the range; Stickiness is the mean DAU over
// the elapsed days of the range divided by Distinct (null when nobody was
// active). NewInstalls counts, per day, installs whose first active day inside
// the retention window is that day.
type ActiveUsers struct {
	Series      []ActivePoint `json:"series"`
	Distinct    int64         `json:"distinct"`
	Stickiness  *float64      `json:"stickiness"`
	NewInstalls []DayCount    `json:"new_installs"`
}

// AdoptionRow is the number of installs on one value (release, platform, ...)
// and their share of the active installs. The Day of a per-day row is set;
// the range totals of AdoptionTotals leave it zero. An install that reports
// two values (two releases on two days of a range) counts once in each, so
// the shares of a range total can add up to more than 1.
type AdoptionRow struct {
	Day      time.Time `json:"day"`
	Value    string    `json:"value"`
	Installs int64     `json:"installs"`
	Share    float64   `json:"share"`
}

// Engagement summarises product sessions of a range.
type Engagement struct {
	Sessions             int64    `json:"sessions"`
	SessionsPerUser      *float64 `json:"sessions_per_user"`
	MeanSessionSeconds   *float64 `json:"mean_session_seconds"`
	MedianSessionSeconds *float64 `json:"median_session_seconds"`
	ScreensPerSession    *float64 `json:"screens_per_session"`
}

// adoptionColumns maps an adoption dimension to its SQL value expression
// over the active CTE (alias a, dictionary join o for the OS name).
var adoptionColumns = map[string]string{
	"release":  "a.release",
	"platform": "a.platform",
	"country":  "a.country",
	"os":       "coalesce(o.value, '')",
	"device":   "case a.device when 1 then 'mobile' when 2 then 'tablet' else 'desktop' end",
}

// activeSource returns the "with active as (...)" prefix and its arguments
// ($1 site, $2 today, $3-$4 live bounds, $5-$6 days read, $7 release and $8
// platform filters). lookback extends the read before the range start (WAU and
// MAU need 29 earlier days).
func (q *Querier) activeSource(w window, lookback int) (string, []any) {
	loc := w.scope.loc()
	now := q.now().In(loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	liveStart, liveEnd := w.start, w.start
	if !today.Before(w.fromDay) && !today.After(w.toDay) {
		liveStart, liveEnd = approllup.DayBounds(today, loc)
	}
	from := w.fromDay.AddDate(0, 0, -lookback)
	if !w.cutoff.IsZero() {
		c := w.cutoff.In(loc)
		if day := time.Date(c.Year(), c.Month(), c.Day(), 0, 0, 0, 0, time.UTC); day.After(from) {
			from = day
		}
	}
	sql := `with active as (
  select * from (
    select day, visitor_id, release, platform, country, os_id, device, events, screens, sessions, engagement_ms
    from app_active_day where site_id = $1 and day >= $5 and day <= $6 and day < $2
    union all
    select $2::date, a.* from (` + approllup.ActiveDaySelect + `) a
  ) u where ($7 = '' or release = $7) and ($8 = '' or platform = $8)
)`
	return sql, []any{w.scope.SiteID, today, liveStart, liveEnd, from, w.toDay, w.f.Release, w.f.Platform}
}

func (q *Querier) elapsedDays(w window) int {
	loc := w.scope.loc()
	now := q.now().In(loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	to := w.toDay
	if today.Before(to) {
		to = today
	}
	n := int(to.Sub(w.fromDay).Hours()/24) + 1
	return max(n, 0)
}

// ActiveUsers returns the DAU/WAU/MAU series, the distinct installs of the
// range, stickiness and new installs per day.
func (q *Querier) ActiveUsers(ctx context.Context, scope Scope, f Filter) (ActiveUsers, error) {
	w, err := q.window(scope, f)
	if err != nil {
		return ActiveUsers{}, err
	}
	prefix, args := q.activeSource(w, 29)
	out := ActiveUsers{Series: make([]ActivePoint, 0, len(w.days)), NewInstalls: make([]DayCount, 0, len(w.days))}

	// A window count of installs is a sum of +1/-1 steps: each active day
	// covers the days [day, day+size) up to the install's next active day, so
	// an install is counted once however often it was active inside the window.
	windowed := func(name string, size int) string {
		return fmt.Sprintf(`%[1]s_ranges as (
  select day as s, least(day + %[2]d, coalesce(lead(day) over (partition by visitor_id order by day), day + %[2]d)) as e from active
), %[1]s_steps as (
  select d, sum(x) as x from (select s as d, 1 as x from %[1]s_ranges union all select e, -1 from %[1]s_ranges) t group by d
)`, name, size)
	}
	rows, err := q.pool.Query(ctx, prefix+`, `+windowed("w", 7)+`, `+windowed("m", 30)+`, dau as (
  select day, count(*) as n from active where day >= $9 and day <= $6 group by day
)
select g.day::date,
       coalesce((select n from dau where dau.day = g.day), 0),
       coalesce((select sum(x) from w_steps where d <= g.day), 0),
       coalesce((select sum(x) from m_steps where d <= g.day), 0)
from (select $9::date + i as day from generate_series(0, $6::date - $9::date) i) g
order by 1`, append(args, w.fromDay)...)
	if err != nil {
		return ActiveUsers{}, fmt.Errorf("read active users series: %w", err)
	}
	defer rows.Close()
	var dauSum int64
	for rows.Next() {
		var p ActivePoint
		if err := rows.Scan(&p.Day, &p.DAU, &p.WAU, &p.MAU); err != nil {
			return ActiveUsers{}, err
		}
		dauSum += p.DAU
		out.Series = append(out.Series, p)
	}
	if err := rows.Err(); err != nil {
		return ActiveUsers{}, err
	}
	rows.Close()

	if err := q.pool.QueryRow(ctx, prefix+` select count(distinct visitor_id) from active where day >= $9 and day <= $6`,
		append(args, w.fromDay)...).Scan(&out.Distinct); err != nil {
		return ActiveUsers{}, fmt.Errorf("read distinct installs: %w", err)
	}
	if out.Distinct > 0 {
		if n := q.elapsedDays(w); n > 0 {
			v := float64(dauSum) / float64(n) / float64(out.Distinct)
			out.Stickiness = &v
		}
	}

	newRows, err := q.pool.Query(ctx, prefix+`
select g.day::date, coalesce(n.c, 0) from (select $9::date + i as day from generate_series(0, $6::date - $9::date) i) g
left join (
  select a.day, count(*) as c from active a
  where a.day >= $9 and a.day <= $6
    and not exists (select 1 from app_active_day p where p.site_id = $1 and p.visitor_id = a.visitor_id and p.day < a.day)
  group by a.day
) n on n.day = g.day
order by 1`, append(args, w.fromDay)...)
	if err != nil {
		return ActiveUsers{}, fmt.Errorf("read new installs: %w", err)
	}
	defer newRows.Close()
	for newRows.Next() {
		var c DayCount
		if err := newRows.Scan(&c.Day, &c.Count); err != nil {
			return ActiveUsers{}, err
		}
		out.NewInstalls = append(out.NewInstalls, c)
	}
	return out, newRows.Err()
}

// Adoption returns, per day of the range, the installs and share of each value
// of by (release, platform, country, os or device). The share divides by the
// distinct installs active that day.
func (q *Querier) Adoption(ctx context.Context, scope Scope, f Filter, by string) ([]AdoptionRow, error) {
	return q.adoption(ctx, scope, f, by, false)
}

// AdoptionTotals is Adoption over the whole range: installs are distinct
// across the range and the share divides by the distinct installs of the range.
func (q *Querier) AdoptionTotals(ctx context.Context, scope Scope, f Filter, by string) ([]AdoptionRow, error) {
	return q.adoption(ctx, scope, f, by, true)
}

func (q *Querier) adoption(ctx context.Context, scope Scope, f Filter, by string, total bool) ([]AdoptionRow, error) {
	expr, ok := adoptionColumns[by]
	if !ok {
		return nil, invalid("by must be one of release, platform, country, os, device")
	}
	w, err := q.window(scope, f)
	if err != nil {
		return nil, err
	}
	prefix, args := q.activeSource(w, 0)
	args = append(args, int16(dict.OS))
	dayExpr, group, denom := "a.day", "a.day, 2", "(select count(*) from active t where t.day = a.day)"
	if total {
		dayExpr, group = "null::date", "2"
		denom = "(select count(distinct t.visitor_id) from active t)"
	}
	rows, err := q.pool.Query(ctx, prefix+`
select `+dayExpr+` as day, `+expr+` as value, count(distinct a.visitor_id) as installs,
       count(distinct a.visitor_id)::float8 / nullif(`+denom+`, 0)
from active a left join dict o on o.kind = $9 and o.id = a.os_id
where a.day >= $5 and a.day <= $6
group by `+group+`
order by 1 nulls first, 3 desc, 2`, args...)
	if err != nil {
		return nil, fmt.Errorf("read adoption: %w", err)
	}
	defer rows.Close()
	out := []AdoptionRow{}
	for rows.Next() {
		var r AdoptionRow
		var day *time.Time
		var share *float64
		if err := rows.Scan(&day, &r.Value, &r.Installs, &share); err != nil {
			return nil, err
		}
		if day != nil {
			r.Day = *day
		}
		if share != nil {
			r.Share = *share
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Engagement summarises the product sessions that started in the range.
// Sessions come from the web sessions table, so the open day is included. The
// platform filter applies; the release filter does not (sessions carry no
// release).
func (q *Querier) Engagement(ctx context.Context, scope Scope, f Filter) (Engagement, error) {
	w, err := q.window(scope, f)
	if err != nil {
		return Engagement{}, err
	}
	var out Engagement
	var perUser, mean, median, screens *float64
	err = q.pool.QueryRow(ctx, `
select count(*), count(*)::float8 / nullif(count(distinct s.visitor_id), 0),
       avg(extract(epoch from s.last - s.start)),
       percentile_cont(0.5) within group (order by extract(epoch from s.last - s.start)),
       sum(s.pageviews)::float8 / nullif(count(*), 0)
from sessions s left join dict h on h.kind = $4 and h.id = s.hostname_id
where s.site_id = $1 and s.start >= $2 and s.start < $3 and ($5 = '' or h.value = $5)`,
		w.scope.SiteID, w.start, w.end, int16(dict.Hostname), f.Platform).Scan(&out.Sessions, &perUser, &mean, &median, &screens)
	if err != nil {
		return Engagement{}, fmt.Errorf("read engagement: %w", err)
	}
	out.SessionsPerUser, out.MeanSessionSeconds, out.MedianSessionSeconds, out.ScreensPerSession = perUser, mean, median, screens
	return out, nil
}
