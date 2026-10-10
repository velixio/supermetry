package synth

import (
	"time"
)

// A unit is one (site, site-local date): the generation and truth grain.

type visitorRec struct {
	vk      uint64
	ip      uint32
	screen  uint16
	ua      uint8
	country uint8
}

type sessionRec struct {
	visitor uint32
	ord     uint8
	channel uint8
}

type eventRec struct {
	off     uint32 // ms since the unit's local midnight
	sess    uint32 // index into unit.sessions
	path    uint16
	refPath uint16 // previous page, or landing for the session's first page
	name    uint8
	props   uint8
}

const (
	landing    = 0xFFFF
	homeIdx    = 0 // "/"
	pricingIdx = 2 // "/pricing"
)

type counts struct {
	events, pageviews, customs, sessions, visitors int64
}

type unit struct {
	site    *site
	siteIdx int
	day     int // dayNum of the local date
	dateIdx int
	date    string
	start   int64 // local midnight, unix ms
	end     int64 // next local midnight, unix ms

	visitors []visitorRec
	sessions []sessionRec
	events   []eventRec // sorted by off (stable)

	counts counts
	row    []byte
	done   chan struct{}
}

func (g *Generator) newUnit(s *site, siteIdx, dateIdx int) *unit {
	d := g.cfg.Start.AddDate(0, 0, dateIdx)
	y, m, dd := d.Date()
	return &unit{
		site: s, siteIdx: siteIdx, day: g.firstDay + dateIdx, dateIdx: dateIdx, date: d.Format("2006-01-02"),
		start: time.Date(y, m, dd, 0, 0, 0, 0, s.loc).UnixMilli(),
		end:   time.Date(y, m, dd+1, 0, 0, 0, 0, s.loc).UnixMilli(),
		done:  make(chan struct{}),
	}
}

// person derives the stable attributes of a visitor from its origin.
func (g *Generator) person(s *site, originDay int, idx int64) visitorRec {
	r := rng{key(g.seed, tagPerson, uint64(s.id), uint64(originDay), uint64(idx))}
	var c int
	if s.home >= 0 && r.chance(homeCountryPPM) {
		c = s.home
	} else {
		c = g.countryTab.pick(&r)
	}
	ua := g.uaTab.pick(&r)
	w := g.uaWidths[ua][g.uaScreen[ua].pick(&r)]
	return visitorRec{vk: g.vk(s, originDay, idx), ip: s.ip(originDay, idx), screen: uint16(w), ua: uint8(ua), country: uint8(c)}
}

// generate fills the unit and, when truth is wanted, its truth row.
func (g *Generator) generate(u *unit, truth bool) {
	s := u.site
	n := g.visitorsOn(s, u.day)
	rToday := g.returners(s, u.day)
	rTomorrow := g.returners(s, u.day+1)
	rYesterday := g.returners(s, u.day-1)
	dayLen := u.end - u.start

	u.visitors = make([]visitorRec, n)
	u.sessions = make([]sessionRec, 0, n*5/4)
	u.events = make([]eventRec, 0, int(float64(n)*eventsPerVisitor*1.05)+16)
	a := newAcc(g, s, truth)
	for i := int64(0); i < n; i++ {
		oDay, oIdx := u.day, i
		lo, hi := int64(0), dayLen
		switch {
		case i < rToday: // came yesterday too: same person, ip and ua
			oDay, oIdx = u.day-1, rYesterday+i
			lo = midnightGuard
		case i < rToday+rTomorrow: // comes back tomorrow
			hi = dayLen - midnightGuard
		}
		v := g.person(s, oDay, oIdx)
		u.visitors[i] = v
		r := rng{key(g.seed, tagVisitor, uint64(s.id), uint64(u.day), uint64(i))}
		a.beginVisitor(int32(i))
		k := g.sessTab.pick(&r) + 1
		t := g.sessionStart(&r, lo, hi)
		for ord := 0; ord < k && t < hi; ord++ {
			last := g.session(u, &r, uint32(i), uint8(ord), t, hi, a)
			t = last + minInterGap + g.interGap.sample(&r)
		}
		a.endVisitor(&v)
	}
	sortEvents(u.events)
	u.counts = counts{events: int64(len(u.events)), pageviews: a.pageviews, customs: a.customs,
		sessions: int64(len(u.sessions)), visitors: n}
	if truth {
		u.row = a.row(u)
	}
}

// sessionStart samples a start time from the site-local daily cycle.
func (g *Generator) sessionStart(r *rng, lo, hi int64) int64 {
	for try := 0; try < 32; try++ {
		t := int64(g.hourTab.pick(r))*hour + r.intn(hour)
		if t >= lo && t < hi {
			return t
		}
	}
	return lo + r.intn(hi-lo)
}

func (g *Generator) pageviews(r *rng) int {
	if r.chance(singlePageviewPPM) {
		return 1
	}
	n := 2
	for n < maxPageviews && r.chance(geomContinuePPM) {
		n++
	}
	return n
}

func (g *Generator) entryPath(r *rng, s *site) int {
	if r.chance(entryHomePPM) {
		return homeIdx
	}
	return s.pathZipf.pick(r)
}

func (g *Generator) nextPath(r *rng, s *site, cur int) int {
	if cur == homeIdx && r.chance(homeToPricingPPM) {
		return pricingIdx
	}
	return s.pathZipf.pick(r)
}

// session emits one session starting at t and returns its last event time.
// Events at or after hi (the end of the visitor's window) are dropped, which
// is how sessions are kept inside the site-local day.
func (g *Generator) session(u *unit, r *rng, vi uint32, ord uint8, t, hi int64, a *acc) int64 {
	s := u.site
	ch := g.channelTab.pick(r)
	si := uint32(len(u.sessions))
	u.sessions = append(u.sessions, sessionRec{visitor: vi, ord: ord, channel: uint8(ch)})
	a.beginSession(ch)
	npv := g.pageviews(r)
	cur := g.entryPath(r, s)
	ref := uint16(landing)
	first, last := t, t
	for p := 0; p < npv; p++ {
		if p > 0 {
			t += g.pageGap.sample(r)
			if t >= hi {
				break
			}
			ref = uint16(cur)
			cur = g.nextPath(r, s, cur)
		}
		u.events = append(u.events, eventRec{off: uint32(t), sess: si, path: uint16(cur), refPath: ref, name: evPageview})
		a.pageview(cur)
		last = t
		ppm, tab := int64(customOtherPPM), &g.customElse
		if cur == pricingIdx {
			ppm, tab = customPricingPPM, &g.customPric
		}
		if r.chance(ppm) {
			te := t + g.customGap.sample(r)
			if te >= hi {
				break
			}
			name := tab.pick(r)
			pi := g.propTabs[name].pick(r)
			u.events = append(u.events, eventRec{off: uint32(te), sess: si, path: uint16(cur), refPath: ref, name: uint8(name), props: uint8(pi)})
			a.custom(name, propCombos[name][pi].plan)
			t, last = te, te
		}
	}
	a.endSession(first, last)
	return last
}

// sortEvents is a stable LSD radix sort on off (< 2^28), two 14-bit passes.
// Stability keeps generation order as the tie-break, so it is deterministic.
func sortEvents(ev []eventRec) {
	if len(ev) < 2 {
		return
	}
	tmp := make([]eventRec, len(ev))
	src, dst := ev, tmp
	for shift := uint(0); shift < 28; shift += 14 {
		var cnt [1<<14 + 1]int
		for i := range src {
			cnt[(src[i].off>>shift)&(1<<14-1)+1]++
		}
		for i := 1; i < len(cnt); i++ {
			cnt[i] += cnt[i-1]
		}
		for i := range src {
			b := (src[i].off >> shift) & (1<<14 - 1)
			dst[cnt[b]] = src[i]
			cnt[b]++
		}
		src, dst = dst, src
	}
	// Two passes: the result is back in ev.
}
