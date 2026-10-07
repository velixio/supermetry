package query

import (
	"fmt"
	"strconv"
	"strings"

	"panel/bench/proto/internal/core"
)

// Filter keys. Session dimensions exist on both sessions and events (copied
// at ingest); entry/exit only on sessions; path is a page (event) filter.
var (
	sessionKeys = map[string]string{"source": "source", "country": "country", "browser": "browser", "os": "os",
		"device": "device", "utm_source": "utm_source", "utm_medium": "utm_medium", "utm_campaign": "utm_campaign"}
	sessionOnlyKeys = map[string]string{"entry": "entry_path", "exit": "exit_path"}
	pageKeys        = map[string]bool{"path": true, "page": true}
	closedOnlyCols  = map[string]string{"entry": "entry", "exit": "exit"}
)

// FilterKeys lists the supported filter keys.
func FilterKeys() []string {
	k := []string{"path"}
	for s := range sessionKeys {
		k = append(k, s)
	}
	return append(k, "entry", "exit")
}

type sqlb struct {
	args                   []any
	site, lo, hi           string
	sess, sessOnly, evSess []string // conditions: s.*, s.* (entry/exit), e.* (copied session dims)
	closedOnly             []string // entry/exit on sessions_closed (c.*)
	pages                  []string // placeholders of path values
	from, to               core.Day
	err                    error
}

func newSQL(pl plan, from, to core.Day, filters []core.Filter) *sqlb {
	b := &sqlb{from: from, to: to}
	lo, _ := from.Bounds(pl.site.Loc)
	_, hi := to.Bounds(pl.site.Loc)
	b.site, b.lo, b.hi = b.arg(pl.site.ID), b.arg(lo), b.arg(hi)
	for _, f := range filters {
		switch {
		case sessionKeys[f.Key] != "":
			cond := " IS NULL" // utm_* are stored as NULL when absent
			if f.Value != "" || !strings.HasPrefix(f.Key, "utm_") {
				cond = " = " + b.arg(f.Value)
			}
			b.sess = append(b.sess, "s."+sessionKeys[f.Key]+cond)
			b.evSess = append(b.evSess, "e."+sessionKeys[f.Key]+cond)
		case sessionOnlyKeys[f.Key] != "":
			v := b.arg(f.Value)
			b.sessOnly = append(b.sessOnly, "s."+sessionOnlyKeys[f.Key]+" = "+v)
			b.closedOnly = append(b.closedOnly, "c."+closedOnlyCols[f.Key]+" = (SELECT id FROM dict WHERE value = "+v+")")
		case pageKeys[f.Key]:
			b.pages = append(b.pages, b.arg(f.Value))
		default:
			b.err = fmt.Errorf("unknown filter %q (have %s)", f.Key, strings.Join(FilterKeys(), ", "))
		}
	}
	return b
}

func (b *sqlb) arg(v any) string {
	b.args = append(b.args, v)
	return "$" + strconv.Itoa(len(b.args))
}

func and(conds ...[]string) string {
	var all []string
	for _, c := range conds {
		all = append(all, c...)
	}
	if len(all) == 0 {
		return ""
	}
	return " AND " + strings.Join(all, " AND ")
}

func (b *sqlb) sessRange() string {
	return "s.site_id = " + b.site + " AND s.start_ts >= " + b.lo + " AND s.start_ts < " + b.hi
}

func (b *sqlb) evRange() string {
	return "e.site_id = " + b.site + " AND e.ts >= " + b.lo + " AND e.ts < " + b.hi
}

func (b *sqlb) pageConds() []string {
	var c []string
	for _, p := range b.pages {
		c = append(c, "e.path = "+p)
	}
	return c
}

// entryExitSemi restricts events to sessions matching entry/exit filters,
// looked up in the hot sessions and, for closed days whose hot rows may have
// been dropped, in sessions_closed.
func (b *sqlb) entryExitSemi() []string {
	if len(b.sessOnly) == 0 {
		return nil
	}
	from, to := b.arg(b.from.Time()), b.arg(b.to.Time())
	return []string{"e.session_id IN (SELECT s.session_id FROM sessions s WHERE " + b.sessRange() + and(b.sessOnly) +
		" UNION SELECT c.session_id FROM sessions_closed c WHERE c.site_id = " + b.site + " AND c.day BETWEEN " + from +
		"::date AND " + to + "::date" + and(b.closedOnly) + ")"}
}

// sessions returns "sessions s [JOIN m] WHERE ..." for the sessions matching
// all filters, and the pageviews expression to sum. With a page filter a
// session matches if it has a pageview of the page, and only those
// pageviews are counted.
func (b *sqlb) sessions() (string, string, error) {
	if b.err != nil {
		return "", "", b.err
	}
	if len(b.pages) == 0 {
		return "sessions s WHERE " + b.sessRange() + and(b.sess, b.sessOnly), "s.pageviews", nil
	}
	m := "(SELECT e.session_id, count(*) AS pv FROM events e WHERE " + b.evRange() +
		" AND e.name = 'pageview'" + and(b.pageConds(), b.evSess) + " GROUP BY e.session_id) m"
	return "sessions s JOIN " + m + " ON m.session_id = s.session_id WHERE " + b.sessRange() + and(b.sess, b.sessOnly), "m.pv", nil
}

// pageviews returns "events e WHERE ..." for pageviews matching all filters.
func (b *sqlb) pageviews() (string, error) {
	if b.err != nil {
		return "", b.err
	}
	return "events e WHERE " + b.evRange() + " AND e.name = 'pageview'" + and(b.pageConds(), b.evSess, b.entryExitSemi()), nil
}

// eventWhere returns conditions for events of any name matching all filters.
func (b *sqlb) eventWhere() (string, error) {
	if b.err != nil {
		return "", b.err
	}
	return b.evRange() + and(b.pageConds(), b.evSess, b.entryExitSemi()), nil
}
