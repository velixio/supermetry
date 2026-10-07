// Package ref is a deliberately naive in-memory evaluator of the CONTRACT's
// truth semantics plus Panel's filter semantics. It shares no session or SQL
// code with the prototype and serves only as a test oracle.
package ref

import (
	"sort"
	"strconv"
	"time"

	"panel/bench/proto/internal/core"
)

type event struct {
	rec core.Record
	idx int
}

// Session is a reference session.
type Session struct {
	Site      int32
	Day       core.Day
	Visitor   string
	Events    []event
	Pageviews int64
	Custom    int64
	Entry     string
	Exit      string
	Source    string
	Dims      map[string]string // session filter keys -> value
	DurMs     int64
}

func (s *Session) bounce() bool { return s.Pageviews == 1 && s.Custom == 0 }

// Ref holds the reference sessions.
type Ref struct {
	Sessions []*Session
}

// Build sessionizes records (sorted by ts) per CONTRACT: visitor = (site,
// local day, ip, ua); a gap > 30 min starts a new session.
func Build(recs []core.Record, sites []core.Site) *Ref {
	loc := map[int32]core.Site{}
	for _, s := range sites {
		loc[s.ID] = s
	}
	open := map[string]*Session{}
	r := &Ref{}
	for i, rec := range recs {
		st := loc[rec.Site]
		day := core.DayOf(rec.TS, st.Loc)
		vkey := strconv.Itoa(int(rec.Site)) + "|" + day.String() + "|" + rec.IP + "|" + rec.UA
		s := open[vkey]
		if s != nil && rec.TS.Sub(s.Events[len(s.Events)-1].rec.TS) > 30*time.Minute {
			s = nil
		}
		if s == nil {
			s = &Session{Site: rec.Site, Day: day, Visitor: vkey,
				Source: core.Source(rec.Ref, rec.UTMSource, st.Host)}
			s.Dims = map[string]string{
				"source": s.Source, "country": rec.Country, "browser": rec.Browser, "os": rec.OS,
				"device": rec.Device, "utm_source": rec.UTMSource, "utm_medium": rec.UTMMedium,
				"utm_campaign": rec.UTMCampaign,
			}
			open[vkey] = s
			r.Sessions = append(r.Sessions, s)
		}
		s.Events = append(s.Events, event{rec, i})
	}
	for _, s := range r.Sessions {
		for _, e := range s.Events {
			if e.rec.Name == "pageview" {
				s.Pageviews++
				if s.Entry == "" {
					s.Entry = e.rec.Path
				}
				s.Exit = e.rec.Path
			} else {
				s.Custom++
			}
		}
		s.DurMs = s.Events[len(s.Events)-1].rec.TS.Sub(s.Events[0].rec.TS).Milliseconds()
		s.Dims["entry"], s.Dims["exit"] = s.Entry, s.Exit
	}
	return r
}

// Query selects sessions of a site over local dates [From, To] with filters.
type Query struct {
	Site     int32
	From, To core.Day
	Filters  []core.Filter
}

func (q Query) pathFilters() []string {
	var p []string
	for _, f := range q.Filters {
		if f.Key == "path" {
			p = append(p, f.Value)
		}
	}
	return p
}

// pageMatch: with path filters, an event matches if it is a pageview of every filtered path.
func pageMatch(e event, paths []string) bool {
	if len(paths) == 0 {
		return true
	}
	if e.rec.Name != "pageview" {
		return false
	}
	for _, p := range paths {
		if e.rec.Path != p {
			return false
		}
	}
	return true
}

func (r *Ref) matched(q Query) []*Session {
	paths := q.pathFilters()
	var out []*Session
	for _, s := range r.Sessions {
		if s.Site != q.Site || s.Day < q.From || s.Day > q.To {
			continue
		}
		ok := true
		for _, f := range q.Filters {
			if f.Key != "path" && s.Dims[f.Key] != f.Value {
				ok = false
			}
		}
		if !ok {
			continue
		}
		if len(paths) > 0 {
			hit := false
			for _, e := range s.Events {
				if pageMatch(e, paths) {
					hit = true
				}
			}
			if !hit {
				continue
			}
		}
		out = append(out, s)
	}
	return out
}

func metricsOf(ss []*Session, paths []string) core.Metrics {
	var m core.Metrics
	vis := map[string]bool{}
	for _, s := range ss {
		vis[s.Visitor] = true
		m.Visits++
		if s.bounce() {
			m.Bounces++
		}
		m.DurationMs += s.DurMs
		if len(paths) == 0 {
			m.Pageviews += s.Pageviews
		} else {
			for _, e := range s.Events {
				if pageMatch(e, paths) {
					m.Pageviews++
				}
			}
		}
	}
	m.Visitors = int64(len(vis))
	return m
}

// Overview returns filtered totals.
func (r *Ref) Overview(q Query) core.Metrics { return metricsOf(r.matched(q), q.pathFilters()) }

// Timeseries returns filtered totals per local day.
func (r *Ref) Timeseries(q Query) map[core.Day]core.Metrics {
	by := map[core.Day][]*Session{}
	for _, s := range r.matched(q) {
		by[s.Day] = append(by[s.Day], s)
	}
	out := map[core.Day]core.Metrics{}
	for d, ss := range by {
		out[d] = metricsOf(ss, q.pathFilters())
	}
	return out
}

// Breakdown returns metrics per value of dim. For DimPath, visitors/visits/
// pageviews count matching pageviews per path; bounces and duration are
// those of the distinct sessions that viewed the path (rollup definition).
func (r *Ref) Breakdown(q Query, dim core.Dim) map[string]core.Metrics {
	ss := r.matched(q)
	paths := q.pathFilters()
	out := map[string]core.Metrics{}
	if dim != core.DimPath {
		by := map[string][]*Session{}
		for _, s := range ss {
			v := s.Dims[dim.String()]
			by[v] = append(by[v], s)
		}
		for v, g := range by {
			out[v] = metricsOf(g, paths)
		}
		return out
	}
	type acc struct {
		vis  map[string]bool
		sess map[int]bool
		m    core.Metrics
	}
	accs := map[string]*acc{}
	for si, s := range ss {
		for _, e := range s.Events {
			if e.rec.Name != "pageview" || !pageMatch(e, paths) {
				continue
			}
			a := accs[e.rec.Path]
			if a == nil {
				a = &acc{vis: map[string]bool{}, sess: map[int]bool{}}
				accs[e.rec.Path] = a
			}
			a.m.Pageviews++
			a.vis[s.Visitor] = true
			if !a.sess[si] {
				a.sess[si] = true
				a.m.Visits++
				a.m.DurationMs += s.DurMs
				if s.bounce() {
					a.m.Bounces++
				}
			}
		}
	}
	for p, a := range accs {
		a.m.Visitors = int64(len(a.vis))
		out[p] = a.m
	}
	return out
}

// Funnel counts sessions reaching each step of core.FunnelSteps, in order.
func (r *Ref) Funnel(q Query) []int64 {
	out := make([]int64, len(core.FunnelSteps))
	for _, s := range r.matched(q) {
		step := 0
		for _, e := range s.Events {
			if step < len(core.FunnelSteps) {
				fs := core.FunnelSteps[step]
				if e.rec.Name == fs.Name && (fs.Path == "" || e.rec.Path == fs.Path) {
					step++
				}
			}
		}
		for i := 0; i < step; i++ {
			out[i]++
		}
	}
	return out
}

// PropRow is a prop breakdown row.
type PropRow struct{ Events, Visitors int64 }

// Props breaks custom event `name` down by props[key].
func (r *Ref) Props(q Query, name, key string) map[string]PropRow {
	type acc struct {
		n   int64
		vis map[string]bool
	}
	accs := map[string]*acc{}
	paths := q.pathFilters()
	for _, s := range r.matched(q) {
		for _, e := range s.Events {
			if e.rec.Name != name || (len(paths) > 0 && !pathEq(e, paths)) {
				continue
			}
			props, _ := core.NormalizeProps(e.rec.Props)
			v, ok := decodeProp(props, key)
			if !ok {
				continue
			}
			a := accs[v]
			if a == nil {
				a = &acc{vis: map[string]bool{}}
				accs[v] = a
			}
			a.n++
			a.vis[s.Visitor] = true
		}
	}
	out := map[string]PropRow{}
	for v, a := range accs {
		out[v] = PropRow{a.n, int64(len(a.vis))}
	}
	return out
}

func pathEq(e event, paths []string) bool {
	for _, p := range paths {
		if e.rec.Path != p {
			return false
		}
	}
	return true
}

// Days returns the sorted (site, day) pairs present.
func (r *Ref) Days() [][2]int32 {
	seen := map[[2]int32]bool{}
	for _, s := range r.Sessions {
		seen[[2]int32{s.Site, int32(s.Day)}] = true
	}
	var out [][2]int32
	for k := range seen {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i][0] != out[j][0] {
			return out[i][0] < out[j][0]
		}
		return out[i][1] < out[j][1]
	})
	return out
}
