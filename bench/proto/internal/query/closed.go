package query

// The closed tier: filtered queries over closed site-local days read the
// integer-encoded tables sessions_closed and cube_day (store/schema.go,
// migration 2) instead of the hot sessions/events tables.
//
// Counts stay exact. Visitors are counted without a distinct-visitor
// aggregation whenever that is exact:
//   - cube_day answers overview, timeseries, sources, countries, browsers,
//     os and devices when the filters only use visitor-level dims and source:
//     every visitor-day is in exactly one cell (v_vd), or in exactly one cell
//     per source it arrived from (v_src);
//   - otherwise sessions_closed with a "first" flag: a visitor-day has exactly
//     one session flagged first (with its source / entry / exit, or first to
//     view a path), so count(*) FILTER (flag) is the distinct count as long as
//     the filters select whole visitor-days plus at most that one
//     session-level dimension;
//   - for every other combination ("split"): sessions of single-session
//     visitor-days (vd_n = 1) are counted directly and only the others are
//     de-duplicated by visitor_id.
// Visitor IDs rotate daily, so every number is additive across days: a
// range is cut into chunks of days that run concurrently (Engine.Fanout) and
// are summed.

import (
	"context"
	"fmt"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/sync/errgroup"

	"supermetry/bench/proto/internal/core"
)

// debugSQL (SUPERMETRY_DEBUG_SQL=1) prints each closed-tier statement's time.
var debugSQL = os.Getenv("SUPERMETRY_DEBUG_SQL") != ""

// cpanel is a closed-tier panel.
type cpanel int

const (
	cpDay cpanel = iota // timeseries; the overview is its sum
	cpSource
	cpCountry
	cpBrowser
	cpOS
	cpDevice
	cpEntry
	cpExit
	cpPath
	nPanels
)

var dimPanel = map[core.Dim]cpanel{core.DimSource: cpSource, core.DimCountry: cpCountry, core.DimBrowser: cpBrowser,
	core.DimOS: cpOS, core.DimDevice: cpDevice, core.DimEntry: cpEntry, core.DimExit: cpExit, core.DimPath: cpPath}

// key column of a panel in sessions_closed / cube_day.
var panelCol = [nPanels]string{cpDay: "(day - DATE '1970-01-01')", cpSource: "source", cpCountry: "country",
	cpBrowser: "browser", cpOS: "os", cpDevice: "device",
	cpEntry: "CASE WHEN pageviews > 0 THEN entry END", cpExit: "CASE WHEN pageviews > 0 THEN exit END"}

// session-level dimension a panel breaks down by ("" = visitor-level).
var panelOwn = [nPanels]string{cpSource: "source", cpEntry: "entry", cpExit: "exit", cpPath: "path"}

// visitor flag of a session-level dimension ("" = first session of the visitor-day).
var dimFlag = map[string]string{"": "flags & 1 <> 0", "source": "flags & 2 <> 0", "entry": "flags & 4 <> 0", "exit": "flags & 8 <> 0"}

var closedCols = map[string]string{"source": "source", "country": "country", "browser": "browser", "os": "os",
	"device": "device", "entry": "entry", "exit": "exit", "utm_source": "utm_source", "utm_medium": "utm_medium",
	"utm_campaign": "utm_campaign"}

var visitorLevel = map[string]bool{"country": true, "browser": true, "os": true, "device": true}

// cfilters are filters translated to the closed tables.
type cfilters struct {
	where  []string // conditions on sessions_closed columns
	custom []string // the same conditions on custom_closed (path = the event's page)
	cubeOK bool     // only visitor-level dims and source: cube_day can answer
	sess   []string // distinct session-level dims filtered (incl. "path")
	path   int64    // path id when a path filter is set, else -1
	never  bool     // matches nothing (unknown value, two different paths)
}

func (e *Engine) closedFilters(ctx context.Context, fs []core.Filter) (*cfilters, error) {
	cf := &cfilters{cubeOK: true, path: -1}
	var vals []string
	for _, f := range fs {
		vals = append(vals, f.Value)
	}
	ids, err := e.dictIDs(ctx, vals)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, f := range fs {
		id, ok := ids[f.Value]
		if !ok {
			cf.never = true
			id = -1
		}
		switch {
		case pageKeys[f.Key]:
			if cf.path >= 0 && cf.path != int64(id) {
				cf.never = true
			}
			cf.path = int64(id)
			cf.cubeOK = false
		case closedCols[f.Key] != "":
			cf.where = append(cf.where, closedCols[f.Key]+" = "+strconv.Itoa(int(id)))
			if !visitorLevel[f.Key] && f.Key != "source" {
				cf.cubeOK = false
			}
		default:
			return nil, fmt.Errorf("unknown filter %q (have %s)", f.Key, strings.Join(FilterKeys(), ", "))
		}
		dim := f.Key
		if pageKeys[dim] {
			dim = "path"
		}
		if !visitorLevel[dim] && !seen[dim] {
			seen[dim] = true
			cf.sess = append(cf.sess, dim)
		}
	}
	cf.custom = slices.Clone(cf.where)
	if cf.path >= 0 {
		cf.where = append(cf.where, fmt.Sprintf("seq && ARRAY[%d, %d]", cf.path*4+2, cf.path*4+3))
		cf.custom = append(cf.custom, fmt.Sprintf("path = %d", cf.path))
	}
	if cf.never {
		cf.where = append(cf.where, "false")
		cf.custom = append(cf.custom, "false")
	}
	return cf, nil
}

// pvExpr is the pageviews of a matching session: with a path filter only the
// pageviews of that path count.
func (cf *cfilters) pvExpr() string {
	if cf.path < 0 {
		return "pageviews"
	}
	p := cf.path * 4
	return fmt.Sprintf("(cardinality(array_positions(seq, %d)) + cardinality(array_positions(seq, %d)) + cardinality(array_positions(seq, %d)))", p, p+2, p+3)
}

// flag returns the exact visitor flag for panel p, or "" if the panel needs
// the split de-duplication.
func (cf *cfilters) flag(p cpanel) string {
	own := panelOwn[p]
	var d string
	switch len(cf.sess) {
	case 0:
		d = own
	case 1:
		if own != "" && own != cf.sess[0] {
			return ""
		}
		d = cf.sess[0]
	default:
		return ""
	}
	if d == "path" {
		if cf.path < 0 {
			return "x & 1 <> 0" // pages panel, unnested seq element
		}
		return fmt.Sprintf("%d = ANY (seq)", cf.path*4+3)
	}
	return dimFlag[d] // "" for utm_*
}

// cquery is one SQL statement run per chunk of days; it fills some panels.
type cquery struct {
	sql    string
	panels []cpanel // GROUPING set i (in order) -> panel
	vcols  []int    // per panel: which v column holds its visitors
	split  bool     // visitors already computed in column v0
	gIndex bool     // g is the index into panels (not a GROUPING() bitmask)
	scale  int64    // results are multiplied by this (approximate sampling mode; 0 = 1)
}

// result rows: g, k, v0..v4, n, pv, b, d
const nV = 5

type crow struct {
	k int64
	v [nV]int64
	m core.Metrics // Visits, Pageviews, Bounces, DurationMs
}

func gsets(panels []cpanel, extra string) (cols, groupBy, gexpr string) {
	var cs, sets []string
	for i := range panels {
		c := "c" + strconv.Itoa(i)
		cs = append(cs, c)
		sets = append(sets, "("+c+extra+")")
	}
	if len(panels) == 1 {
		return cs[0], cs[0] + extra, "0"
	}
	return "coalesce(" + strings.Join(cs, ", ") + ")", "GROUPING SETS (" + strings.Join(sets, ", ") + ")",
		"GROUPING(" + strings.Join(cs, ", ") + ")"
}

func keySelect(panels []cpanel, key func(cpanel) string) string {
	var s []string
	for i, p := range panels {
		s = append(s, key(p)+" AS c"+strconv.Itoa(i))
	}
	return strings.Join(s, ", ")
}

// cubeQuery: panels from cube_day (one pass, GROUPING SETS).
func cubeQuery(panels []cpanel, cf *cfilters) cquery {
	k, gb, g := gsets(panels, "")
	q := cquery{panels: panels}
	for _, p := range panels {
		if p == cpSource || len(cf.sess) > 0 { // len(cf.sess) > 0 means a source filter here
			q.vcols = append(q.vcols, 1)
		} else {
			q.vcols = append(q.vcols, 0)
		}
	}
	q.sql = `SELECT ` + g + `, ` + k + `::int8, sum(v_vd)::int8, sum(v_src)::int8, 0::int8, 0::int8, 0::int8,
  sum(visits)::int8, sum(pageviews)::int8, sum(bounces)::int8, sum(duration_ms)::int8
FROM (SELECT ` + keySelect(panels, func(p cpanel) string { return panelCol[p] }) + `, v_vd, v_src, visits, pageviews, bounces, duration_ms
      FROM cube_day WHERE site_id = $1 AND day BETWEEN $2 AND $3` + and(cf.where) + `) s
GROUP BY ` + gb
	return q
}

// sessQuery: session panels from sessions_closed with exact flags (one pass).
func sessQuery(panels []cpanel, cf *cfilters) cquery {
	var flags []string
	q := cquery{panels: panels}
	for _, p := range panels {
		f := cf.flag(p)
		i := indexOf(flags, f)
		if i < 0 {
			flags = append(flags, f)
			i = len(flags) - 1
		}
		q.vcols = append(q.vcols, i)
	}
	var vs []string
	for i := range nV {
		if i < len(flags) {
			vs = append(vs, "count(*) FILTER (WHERE "+flags[i]+")")
		} else {
			vs = append(vs, "0::int8")
		}
	}
	k, gb, g := gsets(panels, "")
	q.sql = `SELECT ` + g + `, ` + k + `::int8, ` + strings.Join(vs, ", ") + `,
  count(*), sum(pv)::int8, count(*) FILTER (WHERE bounce), sum(duration_ms)::int8
FROM (SELECT ` + keySelect(panels, cf.sessKey) + `, flags, seq, ` + cf.pvExpr() + ` AS pv,
             pageviews = 1 AND custom = 0 AS bounce, duration_ms
      FROM sessions_closed WHERE site_id = $1 AND day BETWEEN $2 AND $3` + and(cf.where) + `) s
GROUP BY ` + gb
	return q
}

// sessKey is the key column of a session panel; the pages panel under a path
// filter is the filtered path itself.
func (cf *cfilters) sessKey(p cpanel) string {
	if p == cpPath {
		return strconv.FormatInt(cf.path, 10)
	}
	return panelCol[p]
}

// sessSplitQuery: session panels with the split de-duplication.
func sessSplitQuery(panels []cpanel, cf *cfilters) cquery {
	q := cquery{panels: panels, split: true, vcols: make([]int, len(panels))}
	k, gb, g := gsets(panels, ", vk")
	q.sql = `SELECT g, k, sum(CASE WHEN vk IS NULL THEN n ELSE 1 END)::int8, 0::int8, 0::int8, 0::int8, 0::int8,
  sum(n)::int8, sum(pv)::int8, sum(b)::int8, sum(d)::int8
FROM (SELECT ` + g + ` AS g, ` + k + `::int8 AS k, vk, count(*) AS n, sum(pv) AS pv, count(*) FILTER (WHERE bounce) AS b,
             sum(duration_ms) AS d
      FROM (SELECT ` + keySelect(panels, cf.sessKey) + `, CASE WHEN vd_n > 1 THEN visitor_id END AS vk,
                   ` + cf.pvExpr() + ` AS pv, pageviews = 1 AND custom = 0 AS bounce, duration_ms
            FROM sessions_closed WHERE site_id = $1 AND day BETWEEN $2 AND $3` + and(cf.where) + `) s
      GROUP BY ` + gb + `) t
GROUP BY g, k`
	return q
}

// peeQuery: pages, entry and exit in one pass over the unnested seq, keyed
// by path id (no path filter). Each session's entry (exit) is attributed to
// the first-in-session element of its entry (exit) path, so one hash table
// and one heap scan serve the three panels. flagged: the three visitor
// flags are exact (no session-level filter); otherwise split de-duplication.
func peeQuery(cf *cfilters, flagged bool) cquery {
	q := cquery{panels: []cpanel{cpPath, cpEntry, cpExit}, vcols: []int{0, 0, 0}, gIndex: true, split: !flagged}
	inner := `SELECT unnest(seq) AS x, entry, exit, flags, vd_n, visitor_id, pageviews, pageviews = 1 AND custom = 0 AS bounce,
                   duration_ms AS dur
            FROM sessions_closed WHERE site_id = $1 AND day BETWEEN $2 AND $3` + and(cf.where)
	const isE, isX = `x & 2 <> 0 AND x >> 2 = entry`, `x & 2 <> 0 AND x >> 2 = exit`
	unpivot := `
CROSS JOIN LATERAL (VALUES (0, a.vp, a.np, a.pvp, a.bp, a.dp), (1, a.ve, a.ne, a.pve, a.be, a.de),
                           (2, a.vx, a.nx, a.pvx, a.bx, a.dx)) p (g, v, n, pv, b, d)
WHERE p.n > 0`
	if flagged {
		q.sql = `SELECT p.g, a.k, p.v, 0::int8, 0::int8, 0::int8, 0::int8, p.n, p.pv, p.b, p.d
FROM (SELECT x >> 2 AS k,
             count(*) FILTER (WHERE x & 1 <> 0) AS vp, count(*) FILTER (WHERE x & 2 <> 0) AS np, count(*) AS pvp,
             count(*) FILTER (WHERE x & 2 <> 0 AND bounce) AS bp, coalesce(sum(dur) FILTER (WHERE x & 2 <> 0), 0)::int8 AS dp,
             count(*) FILTER (WHERE ` + isE + ` AND flags & 4 <> 0) AS ve, count(*) FILTER (WHERE ` + isE + `) AS ne,
             coalesce(sum(pageviews) FILTER (WHERE ` + isE + `), 0)::int8 AS pve, count(*) FILTER (WHERE ` + isE + ` AND bounce) AS be,
             coalesce(sum(dur) FILTER (WHERE ` + isE + `), 0)::int8 AS de,
             count(*) FILTER (WHERE ` + isX + ` AND flags & 8 <> 0) AS vx, count(*) FILTER (WHERE ` + isX + `) AS nx,
             coalesce(sum(pageviews) FILTER (WHERE ` + isX + `), 0)::int8 AS pvx, count(*) FILTER (WHERE ` + isX + ` AND bounce) AS bx,
             coalesce(sum(dur) FILTER (WHERE ` + isX + `), 0)::int8 AS dx
      FROM (` + inner + `) u WHERE x > 0 GROUP BY 1) a` + unpivot
		return q
	}
	vis := func(f string) string {
		return `sum(CASE WHEN vk IS NULL THEN ` + f + ` ELSE (` + f + ` > 0)::int END)::int8`
	}
	q.sql = `SELECT p.g, a.k, p.v, 0::int8, 0::int8, 0::int8, 0::int8, p.n, p.pv, p.b, p.d
FROM (SELECT k, ` + vis("fp") + ` AS vp, sum(fp)::int8 AS np, sum(n)::int8 AS pvp, sum(bp)::int8 AS bp, sum(dp)::int8 AS dp,
             ` + vis("fe") + ` AS ve, sum(fe)::int8 AS ne, sum(pve)::int8 AS pve, sum(be)::int8 AS be, sum(de)::int8 AS de,
             ` + vis("fx") + ` AS vx, sum(fx)::int8 AS nx, sum(pvx)::int8 AS pvx, sum(bx)::int8 AS bx, sum(dx)::int8 AS dx
      FROM (SELECT x >> 2 AS k, CASE WHEN vd_n > 1 THEN visitor_id END AS vk, count(*) AS n,
                   count(*) FILTER (WHERE x & 2 <> 0) AS fp, count(*) FILTER (WHERE x & 2 <> 0 AND bounce) AS bp,
                   coalesce(sum(dur) FILTER (WHERE x & 2 <> 0), 0) AS dp,
                   count(*) FILTER (WHERE ` + isE + `) AS fe, coalesce(sum(pageviews) FILTER (WHERE ` + isE + `), 0) AS pve,
                   count(*) FILTER (WHERE ` + isE + ` AND bounce) AS be, coalesce(sum(dur) FILTER (WHERE ` + isE + `), 0) AS de,
                   count(*) FILTER (WHERE ` + isX + `) AS fx, coalesce(sum(pageviews) FILTER (WHERE ` + isX + `), 0) AS pvx,
                   count(*) FILTER (WHERE ` + isX + ` AND bounce) AS bx, coalesce(sum(dur) FILTER (WHERE ` + isX + `), 0) AS dx
            FROM (` + inner + `) u WHERE x > 0 GROUP BY 1, 2) t
      GROUP BY k) a` + unpivot
	return q
}

// pagesQuery: the pages panel from the unnested seq (no path filter).
func pagesQuery(cf *cfilters) cquery {
	q := cquery{panels: []cpanel{cpPath}, vcols: []int{0}}
	inner := `SELECT unnest(seq) AS x, vd_n, visitor_id, pageviews = 1 AND custom = 0 AS bounce, duration_ms AS dur
            FROM sessions_closed WHERE site_id = $1 AND day BETWEEN $2 AND $3` + and(cf.where)
	if f := cf.flag(cpPath); f != "" {
		q.sql = `SELECT 0, x >> 2, count(*) FILTER (WHERE ` + f + `), 0::int8, 0::int8, 0::int8, 0::int8,
  count(*) FILTER (WHERE x & 2 <> 0), count(*), count(*) FILTER (WHERE x & 2 <> 0 AND bounce),
  coalesce(sum(dur) FILTER (WHERE x & 2 <> 0), 0)::int8
FROM (` + inner + `) u WHERE x > 0 GROUP BY 2`
		return q
	}
	q.split = true
	q.sql = `SELECT 0, k, sum(CASE WHEN vk IS NULL THEN f ELSE 1 END)::int8, 0::int8, 0::int8, 0::int8, 0::int8,
  sum(f)::int8, sum(n)::int8, sum(b)::int8, sum(d)::int8
FROM (SELECT x >> 2 AS k, CASE WHEN vd_n > 1 THEN visitor_id END AS vk, count(*) FILTER (WHERE x & 2 <> 0) AS f,
             count(*) AS n, count(*) FILTER (WHERE x & 2 <> 0 AND bounce) AS b,
             coalesce(sum(dur) FILTER (WHERE x & 2 <> 0), 0) AS d
      FROM (` + inner + `) u WHERE x > 0 GROUP BY 1, 2) t
GROUP BY 2`
	return q
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}

// sampled returns cf restricted to a 1/n sample of visitor-days: visitor IDs
// are uniform hashes, and sessions_closed is sorted by visitor_id within each
// (day, site, country) and BRIN-indexed on it, so the lowest 1/n of the ID
// space is a contiguous slice of every block that the BRIN can skip to.
// Opt-in approximation (Engine.Sample); counts are scaled.
func (cf *cfilters) sampled(n int) *cfilters {
	c := *cf
	// visitor_id < MinInt64 + 2^64/n selects the lowest 1/n of the ID space.
	bound := int64(math.MinInt64) + int64(uint64(1<<63)/uint64(n)*2)
	c.where = append(slices.Clone(cf.where), fmt.Sprintf("visitor_id < %d", bound))
	return &c
}

// plan the statements answering the panels under cf. With sample > 1 the
// cube statement stays exact and every sessions_closed statement reads the
// 1/sample slice (scaled back up).
func closedQueries(panels []cpanel, cf *cfilters, grouping bool, sample int) []cquery {
	if sample <= 1 {
		return closedQueriesAll(panels, cf, grouping, cubeAll)
	}
	out := closedQueriesAll(panels, cf, grouping, cubeOnly)
	for _, q := range closedQueriesAll(panels, cf.sampled(sample), grouping, cubeNone) {
		q.scale = int64(sample)
		out = append(out, q)
	}
	return out
}

const (
	cubeAll  = iota // every statement
	cubeOnly        // only the cube statement
	cubeNone        // everything but the cube statement
)

func closedQueriesAll(panels []cpanel, cf *cfilters, grouping bool, cubeMode int) []cquery {
	var cube, flagged, split []cpanel
	var out []cquery
	if cubeMode == cubeOnly {
		var cube []cpanel
		for _, p := range panels {
			if cf.cubeOK && p <= cpDevice {
				cube = append(cube, p)
			}
		}
		if len(cube) == 0 {
			return nil
		}
		if !grouping {
			for _, p := range cube {
				out = append(out, cubeQuery([]cpanel{p}, cf))
			}
			return out
		}
		return []cquery{cubeQuery(cube, cf)}
	}
	if cubeMode == cubeNone {
		panels = slices.DeleteFunc(slices.Clone(panels), func(p cpanel) bool { return cf.cubeOK && p <= cpDevice })
	}
	// Pages + entry + exit share one pass when their visitor strategy agrees.
	if grouping && cf.path < 0 && slices.Contains(panels, cpPath) && slices.Contains(panels, cpEntry) && slices.Contains(panels, cpExit) {
		fp, fe, fx := cf.flag(cpPath) != "", cf.flag(cpEntry) != "", cf.flag(cpExit) != ""
		if fp == fe && fe == fx && (!fp || len(cf.sess) == 0) {
			out = append(out, peeQuery(cf, fp))
			panels = slices.DeleteFunc(slices.Clone(panels), func(p cpanel) bool { return p == cpPath || p == cpEntry || p == cpExit })
		}
	}
	for _, p := range panels {
		switch {
		case cf.cubeOK && p <= cpDevice:
			cube = append(cube, p)
		case p == cpPath && cf.path < 0:
			out = append(out, pagesQuery(cf))
		case cf.flag(p) != "":
			flagged = append(flagged, p)
		default:
			split = append(split, p)
		}
	}
	add := func(ps []cpanel, mk func([]cpanel, *cfilters) cquery) {
		if len(ps) == 0 {
			return
		}
		if grouping {
			out = append(out, mk(ps, cf))
			return
		}
		for _, p := range ps {
			out = append(out, mk([]cpanel{p}, cf))
		}
	}
	add(cube, cubeQuery)
	add(flagged, sessQuery)
	add(split, sessSplitQuery)
	return out
}

// chunks cuts [from, to] into at most n contiguous day ranges.
func chunks(from, to core.Day, n int) [][2]core.Day {
	days := int(to-from) + 1
	n = max(1, min(n, days))
	var out [][2]core.Day
	for i := range n {
		a := from + core.Day(days*i/n)
		b := from + core.Day(days*(i+1)/n) - 1
		out = append(out, [2]core.Day{a, b})
	}
	return out
}

// closedResult holds merged panel rows keyed by dict id (or day).
type closedResult [nPanels]map[int64]*core.Metrics

// runClosed runs the panels over closed days [from, to], fanned out by day chunks.
func (e *Engine) runClosed(ctx context.Context, site int32, from, to core.Day, panels []cpanel, cf *cfilters) (*closedResult, error) {
	qs := closedQueries(panels, cf, !e.NoGrouping, e.Sample)
	var res closedResult
	for _, p := range panels {
		res[p] = map[int64]*core.Metrics{}
	}
	var mu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	if e.MaxConcurrent > 0 {
		g.SetLimit(e.MaxConcurrent)
	}
	fan := e.Fanout
	if fan <= 0 {
		fan = 1
	}
	for _, ch := range chunks(from, to, fan) {
		for _, q := range qs {
			g.Go(func() error {
				t0 := time.Now()
				if debugSQL {
					defer func() {
						fmt.Fprintf(os.Stderr, "closed %s..%s panels %v: %s\n", ch[0], ch[1], q.panels, time.Since(t0).Round(time.Millisecond))
					}()
				}
				rows, err := e.Pool.Query(gctx, q.sql, site, ch[0].Time(), ch[1].Time())
				if err != nil {
					return fmt.Errorf("closed query: %w\n%s", err, q.sql)
				}
				var local []struct {
					g int
					r crow
				}
				for rows.Next() {
					var gi int
					var k *int64
					var r crow
					if err := rows.Scan(&gi, &k, &r.v[0], &r.v[1], &r.v[2], &r.v[3], &r.v[4],
						&r.m.Visits, &r.m.Pageviews, &r.m.Bounces, &r.m.DurationMs); err != nil {
						rows.Close()
						return err
					}
					if k == nil { // entry/exit of sessions without pageviews
						continue
					}
					r.k = *k
					local = append(local, struct {
						g int
						r crow
					}{gi, r})
				}
				if err := rows.Err(); err != nil {
					return err
				}
				mu.Lock()
				defer mu.Unlock()
				n := len(q.panels)
				for _, x := range local {
					i := 0
					if q.gIndex {
						i = x.g
					} else if n > 1 { // GROUPING() bit (n-1-i) is 0 for the grouped column
						for j := range n {
							if x.g&(1<<(n-1-j)) == 0 {
								i = j
								break
							}
						}
					}
					p := q.panels[i]
					m := res[p][x.r.k]
					if m == nil {
						m = &core.Metrics{}
						res[p][x.r.k] = m
					}
					mm := x.r.m
					mm.Visitors = x.r.v[q.vcols[i]]
					if q.scale > 1 {
						mm.Visitors, mm.Visits, mm.Pageviews = mm.Visitors*q.scale, mm.Visits*q.scale, mm.Pageviews*q.scale
						mm.Bounces, mm.DurationMs = mm.Bounces*q.scale, mm.DurationMs*q.scale
					}
					m.Add(mm)
				}
				return nil
			})
		}
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return &res, nil
}

// --- dictionary cache ------------------------------------------------------

type dictCache struct {
	mu   sync.RWMutex
	byID map[int32]string
	byV  map[string]int32
}

func (d *dictCache) init() {
	if d.byID == nil {
		d.byID, d.byV = map[int32]string{}, map[string]int32{}
	}
}

// dictIDs resolves values to dict ids; unknown values are absent.
func (e *Engine) dictIDs(ctx context.Context, vals []string) (map[string]int32, error) {
	out := map[string]int32{}
	var miss []string
	e.dict.mu.RLock()
	for _, v := range vals {
		if id, ok := e.dict.byV[v]; ok {
			out[v] = id
		} else {
			miss = append(miss, v)
		}
	}
	e.dict.mu.RUnlock()
	if len(miss) == 0 {
		return out, nil
	}
	rows, err := e.Pool.Query(ctx, `SELECT id, value FROM dict WHERE value = ANY($1)`, miss)
	if err != nil {
		return nil, err
	}
	return out, e.cacheDict(rows, func(id int32, v string) { out[v] = id })
}

// dictNames resolves ids to values.
func (e *Engine) dictNames(ctx context.Context, ids []int32) (map[int32]string, error) {
	out := make(map[int32]string, len(ids))
	var miss []int32
	e.dict.mu.RLock()
	for _, id := range ids {
		if v, ok := e.dict.byID[id]; ok {
			out[id] = v
		} else {
			miss = append(miss, id)
		}
	}
	e.dict.mu.RUnlock()
	if len(miss) == 0 {
		return out, nil
	}
	rows, err := e.Pool.Query(ctx, `SELECT id, value FROM dict WHERE id = ANY($1)`, miss)
	if err != nil {
		return nil, err
	}
	return out, e.cacheDict(rows, func(id int32, v string) { out[id] = v })
}

func (e *Engine) cacheDict(rows pgx.Rows, each func(int32, string)) error {
	defer rows.Close()
	e.dict.mu.Lock()
	defer e.dict.mu.Unlock()
	e.dict.init()
	for rows.Next() {
		var id int32
		var v string
		if err := rows.Scan(&id, &v); err != nil {
			return err
		}
		e.dict.byID[id], e.dict.byV[v] = v, id
		each(id, v)
	}
	return rows.Err()
}

// closedThrough is the last local day of site that has closed-tier data and
// is closed (before today), cached briefly.
func (e *Engine) closedThrough(ctx context.Context, site int32, today core.Day) (core.Day, error) {
	e.ctMu.Lock()
	if c, ok := e.ct[site]; ok && time.Since(c.at) < 10*time.Second {
		e.ctMu.Unlock()
		return min(c.day, today-1), nil
	}
	e.ctMu.Unlock()
	var t *time.Time
	if err := e.Pool.QueryRow(ctx, `SELECT max(day) FROM cube_day WHERE site_id = $1`, site).Scan(&t); err != nil {
		return 0, err
	}
	d := core.Day(-1 << 30)
	if t != nil {
		d = core.Day(t.Unix() / 86400)
	}
	e.ctMu.Lock()
	if e.ct == nil {
		e.ct = map[int32]ctEntry{}
	}
	e.ct[site] = ctEntry{day: d, at: time.Now()}
	e.ctMu.Unlock()
	return min(d, today-1), nil
}

type ctEntry struct {
	day core.Day
	at  time.Time
}

// rowsOf converts a merged panel to sorted DimRows with values decoded.
func (e *Engine) rowsOf(ctx context.Context, m map[int64]*core.Metrics) ([]core.DimRow, error) {
	ids := make([]int32, 0, len(m))
	for k := range m {
		ids = append(ids, int32(k))
	}
	names, err := e.dictNames(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]core.DimRow, 0, len(m))
	for k, v := range m {
		out = append(out, core.DimRow{Value: names[int32(k)], Metrics: *v})
	}
	sortRows(out)
	return out, nil
}

// closedFunnel counts funnel F sessions over closed days from seq:
// array_position finds the first matching element after the previous step.
func (e *Engine) closedFunnel(ctx context.Context, site int32, from, to core.Day, cf *cfilters) ([3]int64, error) {
	var out [3]int64
	st := core.FunnelSteps
	ids, err := e.dictIDs(ctx, []string{st[0].Path, st[1].Path, st[2].Name})
	if err != nil {
		return out, err
	}
	p1, ok1 := ids[st[0].Path]
	p2, ok2 := ids[st[1].Path]
	n3, ok3 := ids[st[2].Name]
	if !ok1 || cf.never {
		return out, nil
	}
	if !ok2 {
		p2 = -1 // never matches
	}
	if !ok3 {
		n3 = 1 << 30
	}
	a, b := int64(p1)*4, int64(p2)*4
	// Step 1: the first view of p1 is always flagged first-in-session (bit 2).
	// Step 2: any view of p2 after it (bits 0, 2, 3; bit 1 implies bit 2).
	q := fmt.Sprintf(`SELECT count(*), count(i2), count(*) FILTER (WHERE i2 IS NOT NULL AND array_position(seq, %d, CASE WHEN i2 IS NOT NULL THEN i2 + 1 ELSE 1 END) IS NOT NULL)
FROM (SELECT seq, least(array_position(seq, %d, i1 + 1), array_position(seq, %d, i1 + 1), array_position(seq, %d, i1 + 1)) AS i2
      FROM (SELECT seq, least(array_position(seq, %d), array_position(seq, %d)) AS i1
            FROM sessions_closed WHERE site_id = $1 AND day BETWEEN $2 AND $3 AND seq && ARRAY[%d, %d]%s) s1) s2`,
		-int64(n3), b, b+2, b+3, a+2, a+3, a+2, a+3, and(cf.where))
	var mu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	fan := max(e.Fanout, 1)
	for _, ch := range chunks(from, to, fan) {
		g.Go(func() error {
			var c [3]int64
			if err := e.Pool.QueryRow(gctx, q, site, ch[0].Time(), ch[1].Time()).Scan(&c[0], &c[1], &c[2]); err != nil {
				return err
			}
			mu.Lock()
			for i := range c {
				out[i] += c[i]
			}
			mu.Unlock()
			return nil
		})
	}
	return out, g.Wait()
}

// --- dashboard --------------------------------------------------------------

// Dashboard is every panel of the dashboard, computed together.
type Dashboard struct {
	Overview   Overview                 `json:"overview"`
	Timeseries []Point                  `json:"timeseries"`
	Breakdowns map[string][]core.DimRow `json:"breakdowns"`
}

var dashDims = []core.Dim{core.DimPath, core.DimSource, core.DimCountry, core.DimBrowser, core.DimOS, core.DimDevice,
	core.DimEntry, core.DimExit}

// Dashboard computes overview, timeseries and the 8 breakdowns in as few
// scans as possible: closed days in one fan-out (one cube pass, one
// sessions_closed pass, one seq pass per chunk), open days from the hot
// tables, rollups for unfiltered closed days.
func (e *Engine) Dashboard(ctx context.Context, p Params) (*Dashboard, error) {
	pl, err := e.plan(ctx, p)
	if err != nil {
		return nil, err
	}
	limit := p.Limit
	if limit == 0 {
		limit = 50
	}
	out := &Dashboard{Breakdowns: map[string][]core.DimRow{}}
	byDay := map[core.Day]core.Metrics{}
	merged := map[core.Dim]map[string]core.Metrics{}
	for _, d := range dashDims {
		merged[d] = map[string]core.Metrics{}
	}
	addRows := func(d core.Dim, rs []core.DimRow) {
		for _, r := range rs {
			m := merged[d][r.Value]
			m.Add(r.Metrics)
			merged[d][r.Value] = m
		}
	}
	var mu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	if pl.hasClosed() {
		g.Go(func() error {
			cf, err := e.closedFilters(gctx, p.Filters)
			if err != nil {
				return err
			}
			panels := []cpanel{cpDay, cpSource, cpCountry, cpBrowser, cpOS, cpDevice, cpEntry, cpExit, cpPath}
			res, err := e.runClosed(gctx, p.Site, pl.closedFrom, pl.closedTo, panels, cf)
			if err != nil {
				return err
			}
			days := res[cpDay]
			conv := map[core.Dim][]core.DimRow{}
			for d, cp := range dimPanel {
				if conv[d], err = e.rowsOf(gctx, res[cp]); err != nil {
					return err
				}
			}
			mu.Lock()
			defer mu.Unlock()
			for k, m := range days {
				x := byDay[core.Day(k)]
				x.Add(*m)
				byDay[core.Day(k)] = x
			}
			for d, rs := range conv {
				addRows(d, rs)
			}
			return nil
		})
	}
	// Rollup and hot parts reuse the per-panel queries on their sub-ranges.
	other := func(sub Params, mode string) {
		sub.Mode, sub.Limit = mode, -1
		g.Go(func() error {
			ts, err := e.Timeseries(gctx, sub)
			if err != nil {
				return err
			}
			mu.Lock()
			for _, pt := range ts {
				x := byDay[pt.Day]
				x.Add(pt.Metrics)
				byDay[pt.Day] = x
			}
			mu.Unlock()
			return nil
		})
		for _, d := range dashDims {
			g.Go(func() error {
				rs, err := e.Breakdown(gctx, d, sub)
				if err != nil {
					return err
				}
				mu.Lock()
				addRows(d, rs)
				mu.Unlock()
				return nil
			})
		}
	}
	if pl.hasRoll() {
		other(Params{Site: p.Site, From: pl.rollFrom, To: pl.rollTo}, ModeRollup)
	}
	if pl.hasRaw() {
		other(Params{Site: p.Site, From: pl.rawFrom, To: pl.rawTo, Filters: p.Filters}, ModeHot)
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	var total core.Metrics
	for d := p.From; d <= p.To; d++ {
		m := byDay[d]
		total.Add(m)
		out.Timeseries = append(out.Timeseries, Point{Day: d, Overview: overviewOf(m)})
	}
	out.Overview = overviewOf(total)
	for _, d := range dashDims {
		rs := make([]core.DimRow, 0, len(merged[d]))
		for v, m := range merged[d] {
			rs = append(rs, core.DimRow{Value: v, Metrics: m})
		}
		sortRows(rs)
		if limit > 0 && len(rs) > limit {
			rs = rs[:limit]
		}
		out.Breakdowns[d.String()] = rs
	}
	return out, nil
}

// closedProps breaks custom event name down by props[key] over closed days
// from custom_closed: events and distinct visitors per value (additive across
// day chunks because visitor IDs rotate daily).
func (e *Engine) closedProps(ctx context.Context, site int32, from, to core.Day, cf *cfilters, name, key string) (map[string]*PropRow, error) {
	out := map[string]*PropRow{}
	ids, err := e.dictIDs(ctx, []string{name})
	if err != nil {
		return nil, err
	}
	nid, ok := ids[name]
	if !ok || cf.never {
		return out, nil
	}
	q := `SELECT value, sum(n)::int8, count(*) FROM (
  SELECT props ->> $4 AS value, visitor_id, count(*) AS n FROM custom_closed
  WHERE site_id = $1 AND day BETWEEN $2 AND $3 AND name = ` + strconv.Itoa(int(nid)) + ` AND props ? $4` + and(cf.custom) + `
  GROUP BY 1, 2) t GROUP BY 1`
	var mu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	for _, ch := range chunks(from, to, max(e.Fanout, 1)) {
		g.Go(func() error {
			rows, err := e.Pool.Query(gctx, q, site, ch[0].Time(), ch[1].Time(), key)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var r PropRow
				if err := rows.Scan(&r.Value, &r.Events, &r.Visitors); err != nil {
					return err
				}
				mu.Lock()
				if x := out[r.Value]; x != nil {
					x.Events += r.Events
					x.Visitors += r.Visitors
				} else {
					out[r.Value] = &r
				}
				mu.Unlock()
			}
			return rows.Err()
		})
	}
	return out, g.Wait()
}
