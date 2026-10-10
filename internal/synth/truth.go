package synth

import (
	"slices"
	"strconv"
)

// The generator's own truth accounting, filled while sessions are generated.
// It is checked against an independent recount of the stream in the tests.

const topN = 50

// dimCount counts visitors (deduplicated with a last-visitor marker) and
// pageviews per value index.
type dimCount struct {
	last []int32
	v    []int64
	pv   []int64
}

func newDim(n int) dimCount {
	d := dimCount{last: make([]int32, n), v: make([]int64, n), pv: make([]int64, n)}
	for i := range d.last {
		d.last[i] = -1
	}
	return d
}

func (d *dimCount) add(i int, visitor int32, pv int64) {
	if d.last[i] != visitor {
		d.last[i] = visitor
		d.v[i]++
	}
	d.pv[i] += pv
}

type acc struct {
	g     *Generator
	s     *site
	truth bool

	visits, pageviews, bounces, customs, durMs int64
	visitors                                   int64
	funnel                                     [3]int64
	plan                                       [3]int64

	path, entry, exit, source    dimCount
	country, browser, os, device dimCount
	vi                           int32
	vpv                          int64
	spv, sce                     int64
	sEntry, sExit, sStep, sCh    int
}

func newAcc(g *Generator, s *site, truth bool) *acc {
	a := &acc{g: g, s: s, truth: truth}
	if truth {
		n := len(s.paths)
		a.path, a.entry, a.exit = newDim(n), newDim(n), newDim(n)
		a.source = newDim(len(g.sources))
		a.country = newDim(len(countries))
		a.browser = newDim(len(g.browsers))
		a.os = newDim(len(g.oses))
		a.device = newDim(len(g.devices))
	}
	return a
}

func (a *acc) beginVisitor(vi int32) { a.vi, a.vpv = vi, 0 }

func (a *acc) endVisitor(v *visitorRec) {
	a.visitors++
	if !a.truth {
		return
	}
	ua := int(v.ua)
	a.country.add(int(v.country), a.vi, a.vpv)
	a.browser.add(a.g.uaBrowser[ua], a.vi, a.vpv)
	a.os.add(a.g.uaOS[ua], a.vi, a.vpv)
	a.device.add(a.g.uaDevice[ua], a.vi, a.vpv)
}

func (a *acc) beginSession(ch int) {
	a.spv, a.sce, a.sEntry, a.sExit, a.sStep, a.sCh = 0, 0, -1, -1, 0, ch
}

func (a *acc) pageview(p int) {
	a.pageviews++
	a.spv++
	if !a.truth {
		return
	}
	if a.sEntry < 0 {
		a.sEntry = p
	}
	a.sExit = p
	a.path.add(p, a.vi, 1)
	switch {
	case a.sStep == 0 && p == homeIdx:
		a.sStep = 1
	case a.sStep == 1 && p == pricingIdx:
		a.sStep = 2
	}
}

func (a *acc) custom(name, plan int) {
	a.customs++
	a.sce++
	if !a.truth || name != evSignup {
		return
	}
	a.plan[plan]++
	if a.sStep == 2 {
		a.sStep = 3
	}
}

func (a *acc) endSession(first, last int64) {
	a.visits++
	a.vpv += a.spv
	if !a.truth {
		return
	}
	if a.spv == 1 && a.sce == 0 {
		a.bounces++
	}
	a.durMs += last - first
	for i := 0; i < a.sStep; i++ {
		a.funnel[i]++
	}
	a.entry.add(a.sEntry, a.vi, a.spv)
	a.exit.add(a.sExit, a.vi, a.spv)
	a.source.add(a.g.channelSource[a.sCh], a.vi, a.spv)
}

type topEntry struct {
	value     string
	visitors  int64
	pageviews int64
}

// top returns the top values: visitors desc, pageviews desc, value asc.
func top(d *dimCount, label func(int) string) []topEntry {
	var out []topEntry
	for i, v := range d.v {
		if v > 0 {
			out = append(out, topEntry{label(i), v, d.pv[i]})
		}
	}
	slices.SortFunc(out, func(x, y topEntry) int {
		switch {
		case x.visitors != y.visitors:
			if x.visitors > y.visitors {
				return -1
			}
			return 1
		case x.pageviews != y.pageviews:
			if x.pageviews > y.pageviews {
				return -1
			}
			return 1
		case x.value < y.value:
			return -1
		case x.value > y.value:
			return 1
		}
		return 0
	})
	if len(out) > topN {
		out = out[:topN]
	}
	return out
}

func appendQuoted(b []byte, s string) []byte {
	if jsonSafe(s) {
		b = append(b, '"')
		b = append(b, s...)
		return append(b, '"')
	}
	return strconv.AppendQuote(b, s) // not reached: all labels are checked plain ASCII
}

func appendInt(b []byte, k string, v int64) []byte {
	b = append(b, ',', '"')
	b = append(b, k...)
	b = append(b, '"', ':')
	return strconv.AppendInt(b, v, 10)
}

// row serializes the unit's truth as one JSON object (one line).
func (a *acc) row(u *unit) []byte {
	g, s := a.g, a.s
	b := make([]byte, 0, 8192)
	b = append(b, `{"site":`...)
	b = strconv.AppendInt(b, int64(s.id), 10)
	b = append(b, `,"date":"`...)
	b = append(b, u.date...)
	b = append(b, '"')
	b = appendInt(b, "visitors", a.visitors)
	b = appendInt(b, "visits", a.visits)
	b = appendInt(b, "pageviews", a.pageviews)
	b = appendInt(b, "bounces", a.bounces)
	b = append(b, `,"duration_sum_s":`...)
	b = strconv.AppendInt(b, a.durMs/1000, 10)
	b = append(b, '.')
	ms := a.durMs % 1000
	b = append(b, byte('0'+ms/100), byte('0'+ms/10%10), byte('0'+ms%10))
	b = append(b, `,"top":{`...)
	pathLabel := func(i int) string { return s.paths[i] }
	dims := []struct {
		name  string
		d     *dimCount
		label func(int) string
	}{
		{"path", &a.path, pathLabel},
		{"country", &a.country, func(i int) string { return countries[i].code }},
		{"source", &a.source, func(i int) string { return g.sources[i] }},
		{"browser", &a.browser, func(i int) string { return g.browsers[i] }},
		{"os", &a.os, func(i int) string { return g.oses[i] }},
		{"device", &a.device, func(i int) string { return g.devices[i] }},
		{"entry", &a.entry, pathLabel},
		{"exit", &a.exit, pathLabel},
	}
	for di, d := range dims {
		if di > 0 {
			b = append(b, ',')
		}
		b = append(b, '"')
		b = append(b, d.name...)
		b = append(b, `":[`...)
		for i, e := range top(d.d, d.label) {
			if i > 0 {
				b = append(b, ',')
			}
			b = append(b, `{"value":`...)
			b = appendQuoted(b, e.value)
			b = appendInt(b, "visitors", e.visitors)
			b = appendInt(b, "pageviews", e.pageviews)
			b = append(b, '}')
		}
		b = append(b, ']')
	}
	b = append(b, `},"funnel_f":[`...)
	for i, f := range a.funnel {
		if i > 0 {
			b = append(b, ',')
		}
		b = strconv.AppendInt(b, f, 10)
	}
	b = append(b, `],"signup_plan":{`...)
	for i, p := range plans {
		if i > 0 {
			b = append(b, ',')
		}
		b = appendQuoted(b, p)
		b = append(b, ':')
		b = strconv.AppendInt(b, a.plan[i], 10)
	}
	return append(b, '}', '}')
}
