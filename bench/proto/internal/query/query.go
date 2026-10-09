// Package query implements the dashboard query set: unfiltered panels from
// rollups (any range), filtered panels, funnels and prop breakdowns from the
// raw tier (D6).
package query

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/bench/proto/internal/core"
)

// Engine runs queries.
type Engine struct {
	Pool *pgxpool.Pool
	Site func(int32) *core.Site
	Now  func() time.Time
	// Fanout cuts closed-tier ranges into this many day chunks that run
	// concurrently (0 or 1 = one statement per panel group).
	Fanout int
	// MaxConcurrent bounds the closed-tier statements in flight per call (0 = no bound).
	MaxConcurrent int
	// NoGrouping runs one statement per panel instead of GROUPING SETS (for measurements).
	NoGrouping bool
	// NoMonthly uses pure dyadic blocks instead of the measured faster default,
	// which combines monthly rows with dyadic edge blocks.
	NoMonthly bool
	// Sample > 1 is the opt-in APPROXIMATE mode: sessions_closed statements
	// read a 1/Sample slice of visitor-days and scale the counts back up (the
	// cube stays exact). 0 or 1 = exact (the default).
	Sample int

	dict    dictCache
	ctMu    sync.Mutex
	ct      map[int32]ctEntry
	blockMu sync.Mutex
	blocks  map[int32]blockEntry
}

// Mode selects the storage tier.
const (
	ModeAuto   = "auto"   // rollups for closed days (+ hot for today) unless filtered
	ModeRollup = "rollup" // force rollups (filters not allowed)
	ModeRaw    = "raw"    // raw tier: closed-day tables for closed days, hot tables for the rest
	ModeHot    = "hot"    // hot tables (events, sessions) only, as before the closed tier
)

// Params of a query. From/To are inclusive site-local dates.
type Params struct {
	Site     int32         `json:"site"`
	From     core.Day      `json:"from"`
	To       core.Day      `json:"to"`
	Filters  []core.Filter `json:"filters,omitempty"`
	Limit    int           `json:"limit"`
	Mode     string        `json:"mode"`
	PropName string        `json:"prop_event,omitempty"` // props query: event name
	PropKey  string        `json:"prop_key,omitempty"`   // props query: prop key
}

// Overview result.
type Overview struct {
	core.Metrics
	BounceRate   float64 `json:"bounce_rate"`
	AvgDurationS float64 `json:"avg_duration_s"`
}

func overviewOf(m core.Metrics) Overview {
	return Overview{Metrics: m, BounceRate: m.BounceRate(), AvgDurationS: m.AvgDurationS()}
}

// Point is a timeseries point.
type Point struct {
	Day core.Day `json:"day"`
	Overview
}

// FunnelStep result.
type FunnelStep struct {
	core.FunnelStep
	Sessions int64 `json:"sessions"`
}

// PropRow is a prop breakdown row.
type PropRow struct {
	Value    string `json:"value"`
	Events   int64  `json:"events"`
	Visitors int64  `json:"visitors"`
}

// Names lists every query name.
var Names = []string{"overview", "timeseries", "pages", "sources", "countries", "browsers", "os",
	"devices", "entry", "exit", "funnel", "props"}

// Run dispatches by name.
func (e *Engine) Run(ctx context.Context, name string, p Params) (any, error) {
	switch name {
	case "overview":
		return e.Overview(ctx, p)
	case "timeseries":
		return e.Timeseries(ctx, p)
	case "funnel":
		return e.Funnel(ctx, p)
	case "props":
		if p.PropName == "" {
			p.PropName, p.PropKey = "signup", "plan"
		}
		return e.Props(ctx, p)
	}
	d, err := core.ParseDim(name)
	if err != nil {
		return nil, fmt.Errorf("unknown query %q (have %s)", name, strings.Join(Names, ", "))
	}
	return e.Breakdown(ctx, d, p)
}

// --- range planning -------------------------------------------------------

type plan struct {
	site                 *core.Site
	rollFrom, rollTo     core.Day // rollup part; empty if rollFrom > rollTo
	closedFrom, closedTo core.Day // closed-tier part
	rawFrom, rawTo       core.Day // hot (raw) part; empty if rawFrom > rawTo
}

func (pl plan) hasRoll() bool   { return pl.rollFrom <= pl.rollTo }
func (pl plan) hasClosed() bool { return pl.closedFrom <= pl.closedTo }
func (pl plan) hasRaw() bool    { return pl.rawFrom <= pl.rawTo }

func (e *Engine) today(site *core.Site) core.Day {
	now := time.Now
	if e.Now != nil {
		now = e.Now
	}
	return core.DayOf(now(), site.Loc)
}

func (e *Engine) plan(ctx context.Context, p Params) (plan, error) {
	site := e.Site(p.Site)
	if site == nil {
		return plan{}, fmt.Errorf("unknown site %d", p.Site)
	}
	if p.To < p.From {
		return plan{}, errors.New("to < from")
	}
	pl := plan{site: site, rollFrom: 1, rollTo: 0, closedFrom: 1, closedTo: 0, rawFrom: 1, rawTo: 0}
	mode := p.Mode
	if mode == "" {
		mode = ModeAuto
	}
	switch {
	case mode == ModeHot:
		pl.rawFrom, pl.rawTo = p.From, p.To
	case mode == ModeRaw || (mode == ModeAuto && len(p.Filters) > 0):
		through, err := e.closedThrough(ctx, site.ID, e.today(site))
		if err != nil {
			return plan{}, err
		}
		pl.closedFrom, pl.closedTo = p.From, min(p.To, through)
		pl.rawFrom, pl.rawTo = max(p.From, through+1), p.To
	case mode == ModeRollup:
		if len(p.Filters) > 0 {
			return plan{}, errors.New("rollups cannot answer filtered queries")
		}
		pl.rollFrom, pl.rollTo = p.From, p.To
	case mode == ModeAuto:
		today := e.today(site) // open bucket: read raw
		pl.rollFrom, pl.rollTo = p.From, min(p.To, today-1)
		pl.rawFrom, pl.rawTo = max(p.From, today), p.To
	default:
		return plan{}, fmt.Errorf("unknown mode %q", mode)
	}
	return pl, nil
}

// --- rollup queries --------------------------------------------------------

func scanMetrics(row pgx.Row, extra ...any) (core.Metrics, error) {
	var m core.Metrics
	dst := append(extra, &m.Visitors, &m.Visits, &m.Pageviews, &m.Bounces, &m.DurationMs)
	return m, row.Scan(dst...)
}

const sumCols = `coalesce(sum(visitors),0)::int8, coalesce(sum(visits),0)::int8, coalesce(sum(pageviews),0)::int8,
                 coalesce(sum(bounces),0)::int8, coalesce(sum(duration_ms),0)::int8`

// --- public queries -------------------------------------------------------

// Overview: visitors, visits, pageviews, bounce rate, avg duration.
func (e *Engine) Overview(ctx context.Context, p Params) (Overview, error) {
	pl, err := e.plan(ctx, p)
	if err != nil {
		return Overview{}, err
	}
	var total core.Metrics
	if pl.hasClosed() {
		res, err := e.closedPanels(ctx, p, pl, cpDay)
		if err != nil {
			return Overview{}, err
		}
		for _, m := range res[cpDay] {
			total.Add(*m)
		}
	}
	if pl.hasRoll() {
		m, err := scanMetrics(e.Pool.QueryRow(ctx, `SELECT `+sumCols+` FROM rollup_day
			WHERE site_id = $1 AND day BETWEEN $2 AND $3`, p.Site, pl.rollFrom.Time(), pl.rollTo.Time()))
		if err != nil {
			return Overview{}, err
		}
		total.Add(m)
	}
	if pl.hasRaw() {
		b := newSQL(pl, pl.rawFrom, pl.rawTo, p.Filters)
		from, pv, err := b.sessions()
		if err != nil {
			return Overview{}, err
		}
		q := `SELECT count(*), coalesce(sum(v),0)::int8, coalesce(sum(pv),0)::int8, coalesce(sum(b),0)::int8, coalesce(sum(d),0)::int8
FROM (SELECT s.visitor_id, count(*) AS v, sum(` + pv + `) AS pv,
             count(*) FILTER (WHERE s.pageviews = 1 AND s.custom_events = 0) AS b, sum(s.duration_ms) AS d
      FROM ` + from + ` GROUP BY s.visitor_id) t`
		m, err := scanMetrics(e.Pool.QueryRow(ctx, q, b.args...))
		if err != nil {
			return Overview{}, err
		}
		total.Add(m)
	}
	return overviewOf(total), nil
}

// Timeseries: one point per site-local day (zero-filled).
func (e *Engine) Timeseries(ctx context.Context, p Params) ([]Point, error) {
	pl, err := e.plan(ctx, p)
	if err != nil {
		return nil, err
	}
	by := map[core.Day]core.Metrics{}
	if pl.hasClosed() {
		res, err := e.closedPanels(ctx, p, pl, cpDay)
		if err != nil {
			return nil, err
		}
		for k, m := range res[cpDay] {
			by[core.Day(k)] = *m
		}
	}
	collect := func(rows pgx.Rows) error {
		defer rows.Close()
		for rows.Next() {
			var d time.Time
			m, err := scanMetrics(rows, &d)
			if err != nil {
				return err
			}
			x := by[core.Day(d.Unix()/86400)]
			x.Add(m)
			by[core.Day(d.Unix()/86400)] = x
		}
		return rows.Err()
	}
	if pl.hasRoll() {
		rows, err := e.Pool.Query(ctx, `SELECT day, visitors, visits, pageviews, bounces, duration_ms FROM rollup_day
			WHERE site_id = $1 AND day BETWEEN $2 AND $3`, p.Site, pl.rollFrom.Time(), pl.rollTo.Time())
		if err != nil {
			return nil, err
		}
		if err := collect(rows); err != nil {
			return nil, err
		}
	}
	if pl.hasRaw() {
		b := newSQL(pl, pl.rawFrom, pl.rawTo, p.Filters)
		from, pv, err := b.sessions()
		if err != nil {
			return nil, err
		}
		q := `SELECT local_date, count(*), sum(v)::int8, sum(pv)::int8, sum(b)::int8, sum(d)::int8
FROM (SELECT s.local_date, s.visitor_id, count(*) AS v, sum(` + pv + `) AS pv,
             count(*) FILTER (WHERE s.pageviews = 1 AND s.custom_events = 0) AS b, sum(s.duration_ms) AS d
      FROM ` + from + ` GROUP BY s.local_date, s.visitor_id) t GROUP BY local_date`
		rows, err := e.Pool.Query(ctx, q, b.args...)
		if err != nil {
			return nil, err
		}
		if err := collect(rows); err != nil {
			return nil, err
		}
	}
	out := make([]Point, 0, p.To-p.From+1)
	for d := p.From; d <= p.To; d++ {
		out = append(out, Point{Day: d, Overview: overviewOf(by[d])})
	}
	return out, nil
}

var sessionDimCol = map[core.Dim]string{
	core.DimSource: "source", core.DimCountry: "country", core.DimBrowser: "browser", core.DimOS: "os",
	core.DimDevice: "device", core.DimEntry: "entry_path", core.DimExit: "exit_path",
}

// Breakdown: top values of a dimension, by visitors.
func (e *Engine) Breakdown(ctx context.Context, d core.Dim, p Params) ([]core.DimRow, error) {
	pl, err := e.plan(ctx, p)
	if err != nil {
		return nil, err
	}
	limit := p.Limit
	if limit == 0 {
		limit = 50
	}
	merged := map[string]core.Metrics{}
	parts := 0
	var rows []core.DimRow
	add := func(rs []core.DimRow) {
		parts++
		rows = rs
		for _, r := range rs {
			m := merged[r.Value]
			m.Add(r.Metrics)
			merged[r.Value] = m
		}
	}
	// When several tiers contribute, fetch everything and cut after merging.
	partLimit := limit
	if (pl.hasRoll() || pl.hasClosed()) && pl.hasRaw() {
		partLimit = -1
	}
	if pl.hasClosed() {
		cp := dimPanel[d]
		res, err := e.closedPanels(ctx, p, pl, cp)
		if err != nil {
			return nil, err
		}
		rs, err := e.rowsOf(ctx, res[cp])
		if err != nil {
			return nil, err
		}
		if partLimit > 0 && len(rs) > partLimit {
			rs = rs[:partLimit]
		}
		add(rs)
	}
	if pl.hasRoll() {
		from, subtracted, args, err := e.rollupRange(ctx, p.Site, d, pl.rollFrom, pl.rollTo)
		if err != nil {
			return nil, err
		}
		having := ""
		if subtracted { // values seen only on excluded days sum to zero
			having = " HAVING sum(visits) <> 0 OR sum(pageviews) <> 0"
		}
		q := `SELECT value, ` + sumCols + ` FROM (` + from + `) u
			GROUP BY value` + having + ` ORDER BY 2 DESC, 4 DESC, value COLLATE "C"` + limitSQL(partLimit)
		rs, err := scanRows(e.Pool.Query(ctx, q, args...))
		if err != nil {
			return nil, err
		}
		add(rs)
	}
	if pl.hasRaw() {
		b := newSQL(pl, pl.rawFrom, pl.rawTo, p.Filters)
		var q string
		if d == core.DimPath {
			from, err := b.pageviews()
			if err != nil {
				return nil, err
			}
			q = `SELECT path, count(*), sum(visits)::int8, sum(pv)::int8, 0::int8, 0::int8
FROM (SELECT path, visitor_id, count(*) AS visits, sum(pv) AS pv
      FROM (SELECT e.path, e.visitor_id, e.session_id, count(*) AS pv FROM ` + from + `
            GROUP BY e.path, e.visitor_id, e.session_id) x
      GROUP BY path, visitor_id) y
GROUP BY path ORDER BY 2 DESC, 4 DESC, path COLLATE "C"` + limitSQL(partLimit)
		} else {
			from, pv, err := b.sessions()
			if err != nil {
				return nil, err
			}
			col := "s." + sessionDimCol[d]
			extra := ""
			if d == core.DimEntry || d == core.DimExit {
				extra = " AND s.pageviews > 0"
			}
			q = `SELECT value, count(*), sum(v)::int8, sum(pv)::int8, sum(b)::int8, sum(dur)::int8
FROM (SELECT ` + col + ` AS value, s.visitor_id, count(*) AS v, sum(` + pv + `) AS pv,
             count(*) FILTER (WHERE s.pageviews = 1 AND s.custom_events = 0) AS b, sum(s.duration_ms) AS dur
      FROM ` + from + extra + ` GROUP BY 1, s.visitor_id) t
GROUP BY value ORDER BY 2 DESC, 4 DESC, value COLLATE "C"` + limitSQL(partLimit)
		}
		rs, err := scanRows(e.Pool.Query(ctx, q, b.args...))
		if err != nil {
			return nil, err
		}
		add(rs)
	}
	if parts <= 1 {
		if rows == nil {
			rows = []core.DimRow{}
		}
		return rows, nil
	}
	out := make([]core.DimRow, 0, len(merged))
	for v, m := range merged {
		out = append(out, core.DimRow{Value: v, Metrics: m})
	}
	sortRows(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func sortRows(rs []core.DimRow) {
	sort.Slice(rs, func(i, j int) bool {
		if rs[i].Visitors != rs[j].Visitors {
			return rs[i].Visitors > rs[j].Visitors
		}
		if rs[i].Pageviews != rs[j].Pageviews {
			return rs[i].Pageviews > rs[j].Pageviews
		}
		return rs[i].Value < rs[j].Value
	})
}

func limitSQL(n int) string {
	if n <= 0 {
		return ""
	}
	return " LIMIT " + strconv.Itoa(n)
}

func scanRows(rows pgx.Rows, err error) ([]core.DimRow, error) {
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (core.DimRow, error) {
		var d core.DimRow
		err := r.Scan(&d.Value, &d.Visitors, &d.Visits, &d.Pageviews, &d.Bounces, &d.DurationMs)
		return d, err
	})
}

// Funnel F (core.FunnelSteps), sessions reaching each step in order. Raw tier.
func (e *Engine) Funnel(ctx context.Context, p Params) ([]FunnelStep, error) {
	mode := ModeRaw
	if p.Mode == ModeHot {
		mode = ModeHot
	}
	pl, err := e.plan(ctx, Params{Site: p.Site, From: p.From, To: p.To, Mode: mode})
	if err != nil {
		return nil, err
	}
	var c [3]int64
	if pl.hasClosed() {
		cf, err := e.closedFilters(ctx, p.Filters)
		if err != nil {
			return nil, err
		}
		if c, err = e.closedFunnel(ctx, p.Site, pl.closedFrom, pl.closedTo, cf); err != nil {
			return nil, err
		}
	}
	if pl.hasRaw() {
		h, err := e.hotFunnel(ctx, pl, p.Filters)
		if err != nil {
			return nil, err
		}
		for i := range c {
			c[i] += h[i]
		}
	}
	st := core.FunnelSteps
	out := make([]FunnelStep, len(st))
	for i := range st {
		out[i] = FunnelStep{FunnelStep: st[i], Sessions: c[i]}
	}
	return out, nil
}

// hotFunnel is the funnel over the hot events of pl's raw range.
func (e *Engine) hotFunnel(ctx context.Context, pl plan, filters []core.Filter) ([3]int64, error) {
	var c [3]int64
	b := newSQL(pl, pl.rawFrom, pl.rawTo, filters)
	where, err := b.eventWhere()
	if err != nil {
		return c, err
	}
	st := core.FunnelSteps
	s1n, s1p := b.arg(st[0].Name), b.arg(st[0].Path)
	s2n, s2p := b.arg(st[1].Name), b.arg(st[1].Path)
	s3n := b.arg(st[2].Name)
	q := `WITH ev AS MATERIALIZED (
  SELECT e.session_id, e.ts,
         CASE WHEN e.name = ` + s1n + ` AND e.path = ` + s1p + ` THEN 1
              WHEN e.name = ` + s2n + ` AND e.path = ` + s2p + ` THEN 2 ELSE 3 END AS step
  FROM events e WHERE ` + where + `
    AND ((e.name = ` + s1n + ` AND e.path IN (` + s1p + `, ` + s2p + `)) OR e.name = ` + s3n + `)),
f1 AS (SELECT session_id, min(ts) AS t FROM ev WHERE step = 1 GROUP BY session_id),
f2 AS (SELECT ev.session_id, min(ev.ts) AS t FROM ev JOIN f1 USING (session_id)
       WHERE ev.step = 2 AND ev.ts > f1.t GROUP BY ev.session_id),
f3 AS (SELECT DISTINCT ev.session_id FROM ev JOIN f2 USING (session_id) WHERE ev.step = 3 AND ev.ts > f2.t)
SELECT (SELECT count(*) FROM f1), (SELECT count(*) FROM f2), (SELECT count(*) FROM f3)`
	err = e.Pool.QueryRow(ctx, q, b.args...).Scan(&c[0], &c[1], &c[2])
	return c, err
}

// Props: breakdown of a custom event's prop values (F4): custom_closed for
// closed days, the hot events for the rest.
func (e *Engine) Props(ctx context.Context, p Params) ([]PropRow, error) {
	mode := ModeRaw
	if p.Mode == ModeHot {
		mode = ModeHot
	}
	pl, err := e.plan(ctx, Params{Site: p.Site, From: p.From, To: p.To, Mode: mode})
	if err != nil {
		return nil, err
	}
	limit := p.Limit
	if limit == 0 {
		limit = 50
	}
	merged := map[string]*PropRow{}
	if pl.hasClosed() {
		cf, err := e.closedFilters(ctx, p.Filters)
		if err != nil {
			return nil, err
		}
		if merged, err = e.closedProps(ctx, p.Site, pl.closedFrom, pl.closedTo, cf, p.PropName, p.PropKey); err != nil {
			return nil, err
		}
	}
	if pl.hasRaw() {
		b := newSQL(pl, pl.rawFrom, pl.rawTo, p.Filters)
		where, err := b.eventWhere()
		if err != nil {
			return nil, err
		}
		name, key := b.arg(p.PropName), b.arg(p.PropKey)
		q := `SELECT value, sum(n)::int8, count(*) FROM (
  SELECT e.props ->> ` + key + ` AS value, e.visitor_id, count(*) AS n
  FROM events e WHERE ` + where + ` AND e.name <> 'pageview' AND e.name = ` + name + ` AND e.props ? ` + key + `
  GROUP BY 1, 2) t
GROUP BY value`
		rows, err := e.Pool.Query(ctx, q, b.args...)
		if err != nil {
			return nil, err
		}
		rs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (PropRow, error) {
			var pr PropRow
			err := r.Scan(&pr.Value, &pr.Events, &pr.Visitors)
			return pr, err
		})
		if err != nil {
			return nil, err
		}
		for _, r := range rs {
			if x := merged[r.Value]; x != nil {
				x.Events += r.Events
				x.Visitors += r.Visitors
			} else {
				merged[r.Value] = &r
			}
		}
	}
	out := make([]PropRow, 0, len(merged))
	for _, r := range merged {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Events != out[j].Events {
			return out[i].Events > out[j].Events
		}
		return out[i].Value < out[j].Value
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// closedPanels runs closed-tier panels for p's closed range.
func (e *Engine) closedPanels(ctx context.Context, p Params, pl plan, panels ...cpanel) (*closedResult, error) {
	cf, err := e.closedFilters(ctx, p.Filters)
	if err != nil {
		return nil, err
	}
	return e.runClosed(ctx, p.Site, pl.closedFrom, pl.closedTo, panels, cf)
}

// rollupRange returns a UNION ALL of the rollup rows of dim d over [from, to]
// (and whether some rows are subtracted). Unless Engine.NoMonthly, every
// month of the range with complete monthly rows is read from rollup_month_dim:
// a month inside the range as is, a month that the range covers for more
// than half of its days as the monthly row minus the daily rows of the
// excluded days (exact: every metric adds up across days, visitors included).
// Everything else is read from the daily rows.
func (e *Engine) rollupRange(ctx context.Context, site int32, d core.Dim, from, to core.Day) (string, bool, []any, error) {
	args := []any{site, int16(d)}
	arg := func(v any) string { args = append(args, v); return "$" + strconv.Itoa(len(args)) }
	const cols = `value, visitors::int8 AS visitors, visits::int8 AS visits, pageviews::int8 AS pageviews, bounces::int8 AS bounces, duration_ms`
	const neg = `value, -visitors::int8 AS visitors, -visits::int8 AS visits, -pageviews::int8 AS pageviews,
		-bounces::int8 AS bounces, -duration_ms AS duration_ms`
	blockDone, err := e.completedDayBlocks(ctx, site)
	if err != nil {
		return "", false, nil, err
	}
	var parts []string
	blocks := func(a, b core.Day, c string) {
		if a > b {
			return
		}
		selected := availableDayBlocks(a, b, blockDone)
		var daily, starts []time.Time
		var levels []int16
		for _, block := range selected {
			if block.Level == 0 {
				daily = append(daily, block.Start.Time())
			} else {
				levels = append(levels, int16(block.Level))
				starts = append(starts, block.Start.Time())
			}
		}
		if len(daily) > 0 {
			parts = append(parts, `SELECT `+c+` FROM rollup_day_dim WHERE site_id = $1 AND dim = $2 AND day = ANY(`+arg(daily)+`)`)
		}
		if len(starts) > 0 {
			la, sa := arg(levels), arg(starts)
			parts = append(parts, `SELECT `+c+` FROM rollup_block_dim r
JOIN unnest(`+la+`::int2[], `+sa+`::date[]) AS w(level, block_start)
  ON r.level = w.level AND r.block_start = w.block_start
WHERE r.site_id = $1 AND r.dim = $2`)
		}
	}
	monthOf := func(x core.Day) core.Day {
		t := x.Time()
		return core.Day(time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC).Unix() / 86400)
	}
	nextMonth := func(m core.Day) core.Day { return core.Day(m.Time().AddDate(0, 1, 0).Unix() / 86400) }
	done := map[core.Day]bool{}
	if e.NoMonthly {
		blocks(from, to, cols)
		if len(parts) == 0 {
			parts = append(parts, `SELECT `+cols+` FROM rollup_day_dim WHERE false`)
		}
		return strings.Join(parts, " UNION ALL "), false, args, nil
	}
	if !e.NoMonthly {
		var cand []time.Time
		for m := monthOf(from); m <= to; m = nextMonth(m) {
			cand = append(cand, m.Time())
		}
		rows, err := e.Pool.Query(ctx, `SELECT month FROM rollup_month_done WHERE site_id = $1 AND month = ANY($2)`, site, cand)
		if err != nil {
			return "", false, nil, err
		}
		ms, err := pgx.CollectRows(rows, pgx.RowTo[time.Time])
		if err != nil {
			return "", false, nil, err
		}
		for _, m := range ms {
			done[core.Day(m.Unix()/86400)] = true
		}
	}
	var monthly []time.Time
	subtracted := false
	for m := monthOf(from); m <= to; m = nextMonth(m) {
		end := nextMonth(m) - 1
		a, b := max(m, from), min(end, to)
		inside, outside := int(b-a)+1, int(end-m)+1-(int(b-a)+1)
		if !done[m] || outside >= inside {
			blocks(a, b, cols)
			continue
		}
		monthly = append(monthly, m.Time())
		if outside > 0 {
			subtracted = true
			blocks(m, a-1, neg)
			blocks(b+1, end, neg)
		}
	}
	if len(monthly) > 0 {
		parts = append(parts, `SELECT `+cols+` FROM rollup_month_dim WHERE site_id = $1 AND dim = $2 AND month = ANY(`+arg(monthly)+`)`)
	}
	if len(parts) == 0 {
		parts = append(parts, `SELECT `+cols+` FROM rollup_day_dim WHERE false`)
	}
	return strings.Join(parts, " UNION ALL "), subtracted, args, nil
}
