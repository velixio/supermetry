// Package appquery serves the App Health read models: the overview, the issue
// list and detail, the compatibility matrix and the releases table, plus the
// issue status change.
//
// Distinct counts cannot be summed across days, so period-level crash-free
// rates always come from raw app_events, which is partition-pruned by ts and
// bounded by retention. Daily rollups serve the per-day series and sparklines.
// Every read keeps ts >= now - the site's retention: app_events partitions are
// dropped by the longest retention of any app, so a site with a shorter
// retention must not see the older rows that still exist.
package appquery

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/approllup"
)

// MaxRangeDays is the longest from-to span a read accepts.
const MaxRangeDays = 366

var (
	// ErrRange reports an unusable from/to pair.
	ErrRange = errors.New("from and to must be dates in ascending order at most 366 days apart")
	// ErrInvalid reports a bad parameter; the error text says which.
	ErrInvalid = errors.New("invalid parameter")
	// ErrNotFound reports an issue that does not exist for the site.
	ErrNotFound = errors.New("not found")
)

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// Scope identifies the site a read runs for.
type Scope struct {
	SiteID        int32
	OrgID         int64          // owning organisation (deploys are org-level)
	TZ            *time.Location // nil means UTC
	RetentionDays int            // 0 keeps everything
}

func (s Scope) loc() *time.Location {
	if s.TZ == nil {
		return time.UTC
	}
	return s.TZ
}

// Filter is the range and the optional dimension filters every read takes.
// From and To are inclusive calendar dates in the site's time zone (only their
// year, month and day are used).
type Filter struct {
	From, To                       time.Time
	Release, Environment, Platform string
}

// Querier runs the reads.
type Querier struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

// New returns a Querier; a nil now uses the wall clock.
func New(pool *pgxpool.Pool, now func() time.Time) *Querier {
	if now == nil {
		now = time.Now
	}
	return &Querier{pool: pool, now: now}
}

// window is a resolved Filter: the UTC interval to read from raw events, with
// the start clamped to the retention cutoff, and the local days of the range.
type window struct {
	start, end time.Time
	cutoff     time.Time // zero when retention is unlimited
	dayStarts  []time.Time
	days       []string // YYYY-MM-DD, parallel to dayStarts
	fromDay    time.Time
	toDay      time.Time
	cutoffDay  time.Time // first rollup day still readable
	f          Filter
	scope      Scope
}

func (q *Querier) window(scope Scope, f Filter) (window, error) {
	loc := scope.loc()
	date := func(t time.Time) time.Time {
		y, m, d := t.Date()
		return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	}
	from, to := date(f.From), date(f.To)
	if f.From.IsZero() || f.To.IsZero() || from.After(to) || to.Sub(from) > (MaxRangeDays-1)*24*time.Hour {
		return window{}, ErrRange
	}
	w := window{f: f, scope: scope, fromDay: from, toDay: to}
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		w.dayStarts = append(w.dayStarts, time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, loc))
		w.days = append(w.days, d.Format(time.DateOnly))
	}
	w.start = w.dayStarts[0]
	w.end = time.Date(to.Year(), to.Month(), to.Day()+1, 0, 0, 0, 0, loc)
	w.cutoffDay = from
	if scope.RetentionDays > 0 {
		w.cutoff = q.now().AddDate(0, 0, -scope.RetentionDays)
		if w.cutoff.After(w.start) {
			w.start = w.cutoff
		}
		if c := date(w.cutoff.In(loc)); c.After(w.cutoffDay) {
			w.cutoffDay = c
		}
	}
	return w, nil
}

// eventWhere is the filter shared by every raw read; evArgs are its $1..$6.
const eventWhere = `site_id = $1 and ts >= $2 and ts < $3 and ($4 = '' or release = $4) and ($5 = '' or environment = $5) and ($6 = '' or platform = $6)`

func (w window) evArgs() []any {
	return []any{w.scope.SiteID, w.start, w.end, w.f.Release, w.f.Environment, w.f.Platform}
}

// ratio is 1 - part/whole, or nil when there is nothing to divide by.
func ratio(part, whole int64) *float64 {
	if whole <= 0 {
		return nil
	}
	v := 1 - float64(part)/float64(whole)
	return &v
}

// health is the set of counts the crash-free formulas need.
type health struct {
	Events, Crashes, Installs, CrashedInstalls, Sessions, CrashedSessions int64
}

func (h health) crashFreeUsers() *float64    { return ratio(h.CrashedInstalls, h.Installs) }
func (h health) crashFreeSessions() *float64 { return ratio(h.CrashedSessions, h.Sessions) }

type healthRow struct {
	keys []string
	health
}

// healthBy computes the counts per group of columns (none means one total
// row). Definitions: events and crashes sum occurrences and leave out session
// pings; a crash is kind crash or level fatal; installs count distinct
// install hashes with any event; sessions count distinct session hashes with a
// session ping plus the occurrences of hash-less session rows (Sentry
// aggregates); a crashed session is one with a crash event, or a session row
// with exit_reason crashed.
// cols must be fixed column names, never user input.
func (q *Querier) healthBy(ctx context.Context, w window, extraWhere string, cols ...string) ([]healthRow, error) {
	qualified := make([]string, len(cols))
	for i, c := range cols {
		qualified[i] = "e." + c
	}
	selectKeys, groupBy, withCols := "", "", ""
	if len(cols) > 0 {
		selectKeys = strings.Join(qualified, ", ") + ", "
		groupBy = "group by " + strings.Join(qualified, ", ")
		withCols = ", " + strings.Join(cols, ", ")
	}
	crash := approllup.CrashPredicate("e.")
	query := `
with ev as (
  select kind, level, occurrences, install_hash, session_hash, exit_reason` + withCols + ` from app_events where ` + eventWhere + extraWhere + `
), pings as (
  select distinct session_hash from ev where kind = 'session' and session_hash is not null
)
select ` + selectKeys + `
  coalesce(sum(e.occurrences) filter (where e.kind <> 'session'), 0),
  coalesce(sum(e.occurrences) filter (where ` + crash + `), 0),
  count(distinct e.install_hash),
  count(distinct e.install_hash) filter (where ` + crash + `),
  ` + approllup.SessionsExpr("e.") + `,
  ` + approllup.CrashedSessionsExpr("e.") + `
from ev e left join pings p on p.session_hash = e.session_hash ` + groupBy
	rows, err := q.pool.Query(ctx, query, w.evArgs()...)
	if err != nil {
		return nil, fmt.Errorf("read app health: %w", err)
	}
	defer rows.Close()
	var out []healthRow
	for rows.Next() {
		r := healthRow{keys: make([]string, len(cols))}
		dest := make([]any, 0, len(cols)+6)
		for i := range r.keys {
			dest = append(dest, &r.keys[i])
		}
		dest = append(dest, &r.Events, &r.Crashes, &r.Installs, &r.CrashedInstalls, &r.Sessions, &r.CrashedSessions)
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SeriesPoint is one day of the overview series.
type SeriesPoint struct {
	Day            string   `json:"day"`
	Events         int64    `json:"events"`
	Crashes        int64    `json:"crashes"`
	CrashFreeUsers *float64 `json:"crash_free_users"`
}

// Overview is the app dashboard's headline numbers.
type Overview struct {
	Events            int64         `json:"events"`
	Crashes           int64         `json:"crashes"`
	NewIssues         int64         `json:"new_issues"`
	RegressedIssues   int64         `json:"regressed_issues"`
	CrashFreeUsers    *float64      `json:"crash_free_users"`
	CrashFreeSessions *float64      `json:"crash_free_sessions"`
	Series            []SeriesPoint `json:"series"`
}

// Overview returns the totals and the daily series for a range. New and
// regressed issues are issue-level facts: they follow the release filter (the
// release an issue first or last appeared in) but ignore environment and
// platform.
func (q *Querier) Overview(ctx context.Context, scope Scope, f Filter) (Overview, error) {
	w, err := q.window(scope, f)
	if err != nil {
		return Overview{}, err
	}
	out := Overview{Series: []SeriesPoint{}}
	totals, err := q.healthBy(ctx, w, "")
	if err != nil {
		return out, err
	}
	if len(totals) == 1 {
		t := totals[0]
		out.Events, out.Crashes = t.Events, t.Crashes
		out.CrashFreeUsers, out.CrashFreeSessions = t.crashFreeUsers(), t.crashFreeSessions()
	}
	if err := q.pool.QueryRow(ctx, `
select count(*) filter (where first_seen >= $2 and first_seen < $3 and ($4 = '' or first_release = $4)),
       count(*) filter (where status = 'regressed' and last_seen >= $2 and last_seen < $3 and ($4 = '' or last_release = $4))
from app_issues where site_id = $1 and (last_seen >= $2 or first_seen >= $2)`,
		scope.SiteID, w.start, w.end, f.Release).Scan(&out.NewIssues, &out.RegressedIssues); err != nil {
		return out, fmt.Errorf("count issues: %w", err)
	}
	if f.Environment == "" {
		out.Series, err = q.seriesFromRollups(ctx, w)
	} else {
		out.Series, err = q.seriesFromEvents(ctx, w)
	}
	return out, err
}

// seriesFromRollups reads app_release_daily. Its per-day crash_free_users sums
// installs and crashed installs over releases and platforms, so it is only
// approximate on a day when one install ran in more than one release or
// platform (it counts once per group). Period totals do not have this problem:
// they are computed from raw events.
func (q *Querier) seriesFromRollups(ctx context.Context, w window) ([]SeriesPoint, error) {
	rows, err := q.pool.Query(ctx, `
select day, sum(events), sum(crashes), sum(installs), sum(crashed_installs)
from app_release_daily
where site_id = $1 and day >= $2 and day <= $3 and day >= $4 and ($5 = '' or release = $5) and ($6 = '' or platform = $6)
group by day`, w.scope.SiteID, w.fromDay, w.toDay, w.cutoffDay, w.f.Release, w.f.Platform)
	if err != nil {
		return nil, fmt.Errorf("read release rollups: %w", err)
	}
	defer rows.Close()
	byDay := map[string]health{}
	for rows.Next() {
		var day time.Time
		var h health
		if err := rows.Scan(&day, &h.Events, &h.Crashes, &h.Installs, &h.CrashedInstalls); err != nil {
			return nil, err
		}
		byDay[day.Format(time.DateOnly)] = h
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	series := make([]SeriesPoint, 0, len(w.days))
	for _, day := range w.days {
		h := byDay[day]
		series = append(series, SeriesPoint{Day: day, Events: h.Events, Crashes: h.Crashes, CrashFreeUsers: h.crashFreeUsers()})
	}
	return series, nil
}

// seriesFromEvents buckets raw events into the range's local days. It serves
// the environment filter, which the rollups do not carry. Day boundaries come
// from Go, so no database time zone data is needed.
func (q *Querier) seriesFromEvents(ctx context.Context, w window) ([]SeriesPoint, error) {
	rows, err := q.pool.Query(ctx, `
select width_bucket(ts, $7::timestamptz[]) as bucket,
  coalesce(sum(occurrences) filter (where kind <> 'session'), 0),
  coalesce(sum(occurrences) filter (where `+approllup.CrashPredicate("")+`), 0),
  count(distinct install_hash),
  count(distinct install_hash) filter (where `+approllup.CrashPredicate("")+`)
from app_events where `+eventWhere+`
group by bucket`, append(w.evArgs(), w.dayStarts)...)
	if err != nil {
		return nil, fmt.Errorf("read event series: %w", err)
	}
	defer rows.Close()
	series := make([]SeriesPoint, len(w.days))
	for i, day := range w.days {
		series[i].Day = day
	}
	for rows.Next() {
		var bucket int
		var h health
		if err := rows.Scan(&bucket, &h.Events, &h.Crashes, &h.Installs, &h.CrashedInstalls); err != nil {
			return nil, err
		}
		if bucket >= 1 && bucket <= len(series) {
			series[bucket-1] = SeriesPoint{Day: w.days[bucket-1], Events: h.Events, Crashes: h.Crashes, CrashFreeUsers: h.crashFreeUsers()}
		}
	}
	return series, rows.Err()
}
